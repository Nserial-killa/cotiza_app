package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

// CotizadorRuntimeHandler sirve y persiste la ejecución de una estructura
// previamente compilada, incluidos sus cálculos y valores por opción.
type CotizadorRuntimeHandler struct {
	DB *pgxpool.Pool
}

type guardarValoresRuntimeRequest struct {
	Version          int                         `json:"version"`
	Valores          map[string]json.RawMessage  `json:"valores"`
	ValoresPorOpcion []guardarValorOpcionRuntime `json:"valores_por_opcion"`
}

type guardarValorOpcionRuntime struct {
	ElementoID string          `json:"elemento_id"`
	OpcionID   string          `json:"opcion_id"`
	Valor      json.RawMessage `json:"valor"`
}

type valorPendienteRuntime struct {
	ElementoID string
	OpcionID   string
	Valor      json.RawMessage
}

type elementoRuntime struct {
	Tipo             string
	CatalogoID       string
	TipoListaPrecios string
	PadreOpcionesID  string

	// Solo para TABLA (Ronda 4): columnas válidas de esta tabla (columna_id
	// -> true) y si se permite agregar/quitar filas al guardar.
	ColumnasTabla         map[string]bool
	PermitirAgregarFilas  bool
	PermitirEliminarFilas bool
}

type contextoRuntime struct {
	CotizacionID  string
	CalculadoraID string
	Version       int
	CompiladoID   string
	Estructura    map[string]any
	Elementos     map[string]elementoRuntime
}

type errorRuntime struct {
	status  int
	mensaje string
}

func (e *errorRuntime) Error() string { return e.mensaje }

// Obtener devuelve la estructura fijada para la cotización y los valores de
// su versión. La primera apertura fija el compilado activo si aún no existía.
func (h *CotizadorRuntimeHandler) Obtener(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "cotizacion_id"))
	version, err := versionOpcional(r.URL.Query().Get("version"))
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runtime, err := h.cargarContexto(ctx, cotizacionID, version, true)
	if err != nil {
		h.responderError(w, "obteniendo runtime", cotizacionID, err)
		return
	}
	if err := h.incluirOpcionesCatalogo(ctx, runtime.Estructura); err != nil {
		log.Printf("cotizador runtime: error cargando opciones de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar las opciones de catálogo."})
		return
	}
	if err := h.asegurarOpcionesPropuesta(ctx, &runtime); err != nil {
		log.Printf("cotizador runtime: error preparando opciones de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible preparar las opciones de propuesta."})
		return
	}
	valores, err := h.leerValores(ctx, h.DB, cotizacionID, runtime.Version)
	if err != nil {
		log.Printf("cotizador runtime: error leyendo valores de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar los valores de la cotización."})
		return
	}
	resolverCamposCalculados(indexarElementosCompletoRuntime(runtime.Estructura), valores)
	resolverCamposCalculadosPorOpcion(indexarElementosCompletoRuntime(runtime.Estructura), runtime.Elementos, valores)
	incluirValoresCajaValor(runtime.Estructura, valores)

	reglas, err := reglasCotizadorParaEvaluar(ctx, h.DB, runtime.CalculadoraID)
	if err != nil {
		log.Printf("cotizador runtime: error cargando reglas de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar las reglas del cotizador."})
		return
	}
	incluirEstadoReglas(runtime.Estructura, evaluarEstadoCamposRegla(valores, reglas))

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "estructura": runtime.Estructura, "valores": valores, "version": runtime.Version})
}

// GuardarValores valida cada elemento contra el JSON compilado fijado y hace
// upsert atómico de los valores de la versión indicada.
func (h *CotizadorRuntimeHandler) GuardarValores(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "cotizacion_id"))
	var req guardarValoresRuntimeRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Version <= 0 || (req.Valores == nil && req.ValoresPorOpcion == nil) {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar version y valores."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	runtime, err := h.cargarContexto(ctx, cotizacionID, req.Version, true)
	if err != nil {
		h.responderError(w, "guardando runtime", cotizacionID, err)
		return
	}
	pendientes := make([]valorPendienteRuntime, 0, len(req.Valores)+len(req.ValoresPorOpcion))
	for elementoID, valor := range req.Valores {
		pendientes = append(pendientes, valorPendienteRuntime{ElementoID: strings.TrimSpace(elementoID), Valor: valor})
	}
	for _, valor := range req.ValoresPorOpcion {
		pendientes = append(pendientes, valorPendienteRuntime{
			ElementoID: strings.TrimSpace(valor.ElementoID),
			OpcionID:   strings.TrimSpace(valor.OpcionID),
			Valor:      valor.Valor,
		})
	}
	vistos := make(map[string]bool, len(pendientes))
	for _, pendiente := range pendientes {
		elementoID := pendiente.ElementoID
		valor := pendiente.Valor
		elemento, existe := runtime.Elementos[elementoID]
		if !existe {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El elemento %s no pertenece a la estructura compilada de esta cotización.", elementoID)})
			return
		}
		clave := elementoID + "\x00" + pendiente.OpcionID
		if vistos[clave] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor de %s está repetido para la misma opción.", elementoID)})
			return
		}
		vistos[clave] = true
		if elemento.PadreOpcionesID != "" {
			if pendiente.OpcionID == "" {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El elemento %s pertenece a Opciones de Propuesta y debe indicar opcion_id.", elementoID)})
				return
			}
			var opcionValida bool
			if err := h.DB.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM cotizacion_opciones
					WHERE opcion_id=$1 AND cotizacion_id=$2 AND numero_version=$3 AND elemento_padre_id=$4
				)`, pendiente.OpcionID, cotizacionID, req.Version, elemento.PadreOpcionesID).Scan(&opcionValida); err != nil {
				log.Printf("cotizador runtime: error validando opción %s: %v", pendiente.OpcionID, err)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la opción de propuesta."})
				return
			}
			if !opcionValida {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("La opción %s no pertenece al componente padre de %s.", pendiente.OpcionID, elementoID)})
				return
			}
		} else if pendiente.OpcionID != "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El elemento %s no pertenece a Opciones de Propuesta y no admite opcion_id.", elementoID)})
			return
		}
		if !json.Valid(valor) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor del elemento %s no es JSON válido.", elementoID)})
			return
		}
		if elemento.Tipo == "CAMPO_CATALOGO" {
			var valorSistema string
			if err := json.Unmarshal(valor, &valorSistema); err != nil || strings.TrimSpace(valorSistema) == "" {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor de %s debe ser una opción activa del catálogo %s.", elementoID, elemento.CatalogoID)})
				return
			}
			var permitido bool
			if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalogo_valores WHERE catalogo_id=$1 AND valor_sistema=$2 AND activo=true)`, elemento.CatalogoID, valorSistema).Scan(&permitido); err != nil {
				log.Printf("cotizador runtime: error validando catálogo %s: %v", elemento.CatalogoID, err)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el valor de catálogo."})
				return
			}
			if !permitido {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor %s no es una opción activa del catálogo %s para el elemento %s.", valorSistema, elemento.CatalogoID, elementoID)})
				return
			}
		}
		if elemento.Tipo == "LISTA_PRECIOS" {
			var valorParsed map[string]any
			if err := json.Unmarshal(valor, &valorParsed); err != nil {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor de %s debe ser un objeto (item_id/cantidad, o filas).", elementoID)})
				return
			}
			itemIDs, err := itemIDsDesdeValorListaPrecios(elemento.TipoListaPrecios, valorParsed)
			if err != nil {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("%s: %s", elementoID, err.Error())})
				return
			}
			for _, itemID := range itemIDs {
				var perteneceYActivo bool
				if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lista_precios_items WHERE item_id::text=$1 AND elemento_id=$2 AND activo=true)`, itemID, elementoID).Scan(&perteneceYActivo); err != nil {
					log.Printf("cotizador runtime: error validando ítem %s de %s: %v", itemID, elementoID, err)
					escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los ítems de la lista de precios."})
					return
				}
				if !perteneceYActivo {
					escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El ítem %s no existe, está inactivo o no pertenece a %s.", itemID, elementoID)})
					return
				}
			}
		}
		if elemento.Tipo == "TABLA" {
			var valorParsed map[string]any
			if err := json.Unmarshal(valor, &valorParsed); err != nil {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor de %s debe ser un objeto {\"filas\":[...]}.", elementoID)})
				return
			}
			filas, err := filasDesdeValorTabla(valorParsed)
			if err != nil {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("%s: %s", elementoID, err.Error())})
				return
			}
			for _, fila := range filas {
				for columnaID := range fila {
					if !elemento.ColumnasTabla[columnaID] {
						escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("La columna %s no pertenece a la tabla %s.", columnaID, elementoID)})
						return
					}
				}
			}
			var previoRaw []byte
			errPrevio := h.DB.QueryRow(ctx, `
				SELECT valor FROM cotizacion_valores
				WHERE cotizacion_id=$1 AND version=$2 AND elemento_id=$3
				  AND opcion_id IS NOT DISTINCT FROM NULLIF($4, '')`, cotizacionID, req.Version, elementoID, pendiente.OpcionID).Scan(&previoRaw)
			previoFilas := 0
			if errPrevio == nil {
				var previoParsed map[string]any
				if err := json.Unmarshal(previoRaw, &previoParsed); err == nil {
					if pf, ok := previoParsed["filas"].([]any); ok {
						previoFilas = len(pf)
					}
				}
			} else if !errors.Is(errPrevio, pgx.ErrNoRows) {
				log.Printf("cotizador runtime: error leyendo filas previas de %s: %v", elementoID, errPrevio)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar las filas de la tabla."})
				return
			}
			if len(filas) > previoFilas && !elemento.PermitirAgregarFilas {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("La tabla %s no permite agregar filas.", elementoID)})
				return
			}
			if len(filas) < previoFilas && !elemento.PermitirEliminarFilas {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("La tabla %s no permite eliminar filas.", elementoID)})
				return
			}
		}
	}

	// --- Reglas de Cotizador (migración 0024). Primero VALIDACIÓN
	// (BLOQUEAR_GUARDADO/CAMPO_REQUERIDO): si algo dispara, se rechaza el
	// guardado COMPLETO acá, antes de abrir la transacción — nada se
	// persiste. Recién si pasa, se evalúan las reglas de VISIBILIDAD/
	// ACCIÓN y se corrigen los valores a guardar (oculto o forzar_cero ->
	// 0/null) ANTES de persistir, sin importar qué mandó el frontend para
	// ese campo — sección 6.1 del documento: "un valor oculto no puede
	// seguir sumándose silenciosamente". Los campos de Opciones de
	// Propuesta quedan fuera de esta ronda (ver reglas_evaluacion.go).
	reglas, err := reglasCotizadorParaEvaluar(ctx, h.DB, runtime.CalculadoraID)
	if err != nil {
		log.Printf("cotizador runtime: error cargando reglas de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar las reglas del cotizador."})
		return
	}
	if len(reglas) > 0 {
		valoresActuales, err := h.leerValores(ctx, h.DB, cotizacionID, req.Version)
		if err != nil {
			log.Printf("cotizador runtime: error leyendo valores actuales de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar las reglas del cotizador."})
			return
		}
		valoresPropuestos := make(map[string]any, len(valoresActuales))
		for id, valor := range valoresActuales {
			valoresPropuestos[id] = valor
		}
		indicePendientePorElemento := make(map[string]int, len(pendientes))
		for i, pendiente := range pendientes {
			if pendiente.OpcionID != "" {
				continue
			}
			indicePendientePorElemento[pendiente.ElementoID] = i
			var decodificado any
			if err := json.Unmarshal(pendiente.Valor, &decodificado); err == nil {
				valoresPropuestos[pendiente.ElementoID] = decodificado
			}
		}

		if erroresValidacion := evaluarValidacionReglas(valoresPropuestos, reglas); len(erroresValidacion) > 0 {
			mensajes := make([]string, 0, len(erroresValidacion))
			for _, e := range erroresValidacion {
				mensajes = append(mensajes, e.Mensaje)
			}
			escribirJSON(w, http.StatusBadRequest, map[string]any{
				"ok": false, "error": strings.Join(mensajes, " "), "errores_regla": erroresValidacion,
			})
			return
		}

		estadoCampos := evaluarEstadoCamposRegla(valoresPropuestos, reglas)
		if len(estadoCampos) > 0 {
			elementosCompleto := indexarElementosCompletoRuntime(runtime.Estructura)
			for campoID, estado := range estadoCampos {
				if estado.Visible && !estado.ForzarCero {
					continue
				}
				elementoObjetivo, existe := runtime.Elementos[campoID]
				if !existe || elementoObjetivo.PadreOpcionesID != "" {
					continue
				}
				valorForzado := valorForzadoPorRegla(elementosCompleto[campoID])
				if indice, tocado := indicePendientePorElemento[campoID]; tocado {
					pendientes[indice].Valor = valorForzado
				} else {
					indicePendientePorElemento[campoID] = len(pendientes)
					pendientes = append(pendientes, valorPendienteRuntime{ElementoID: campoID, Valor: valorForzado})
				}
			}
		}
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar el guardado."})
		return
	}
	defer tx.Rollback(ctx)
	for _, pendiente := range pendientes {
		if pendiente.OpcionID == "" {
			_, err = tx.Exec(ctx, `
				INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, opcion_id, valor)
				VALUES ($1, $2, $3, NULL, $4)
				ON CONFLICT (cotizacion_id, version, elemento_id) WHERE opcion_id IS NULL
				DO UPDATE SET valor=EXCLUDED.valor`,
				cotizacionID, req.Version, pendiente.ElementoID, string(pendiente.Valor))
		} else {
			_, err = tx.Exec(ctx, `
				INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, opcion_id, valor)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (cotizacion_id, version, elemento_id, opcion_id) WHERE opcion_id IS NOT NULL
				DO UPDATE SET valor=EXCLUDED.valor`,
				cotizacionID, req.Version, pendiente.ElementoID, pendiente.OpcionID, string(pendiente.Valor))
		}
		if err != nil {
			break
		}
	}
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	comentario := fmt.Sprintf("Se actualizaron %d valor(es) del cotizador.", len(pendientes))
	if err == nil {
		err = insertarHistorial(ctx, tx, cotizacionID, &req.Version, "valores_actualizados", nil, nil, comentario, usuarioID)
	}
	if err == nil {
		err = h.actualizarTotalesCotizacionVersion(ctx, tx, runtime.Estructura, runtime.Elementos, cotizacionID, req.Version)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("cotizador runtime: error guardando valores de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los valores de la cotización."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "cotizacion_id": cotizacionID, "version": req.Version, "valores_guardados": len(pendientes)})
}

func (h *CotizadorRuntimeHandler) cargarContexto(ctx context.Context, cotizacionID string, versionSolicitada int, fijar bool) (contextoRuntime, error) {
	resultado := contextoRuntime{CotizacionID: cotizacionID, Elementos: make(map[string]elementoRuntime)}
	if cotizacionID == "" {
		return resultado, &errorRuntime{status: http.StatusBadRequest, mensaje: "Debe indicar cotizacion_id."}
	}
	var calculadoraID string
	var versionActual int
	var compiladoID *string
	err := h.DB.QueryRow(ctx, `SELECT calculadora_id, version_actual, compilado_id_usado::text FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&calculadoraID, &versionActual, &compiladoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return resultado, &errorRuntime{status: http.StatusNotFound, mensaje: "La cotización indicada no existe."}
	}
	if err != nil {
		return resultado, err
	}
	resultado.CalculadoraID = calculadoraID
	resultado.Version = versionSolicitada
	if resultado.Version == 0 {
		resultado.Version = versionActual
	}
	var versionExiste bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2)`, cotizacionID, resultado.Version).Scan(&versionExiste); err != nil {
		return resultado, err
	}
	if !versionExiste {
		return resultado, &errorRuntime{status: http.StatusNotFound, mensaje: "La versión indicada de la cotización no existe."}
	}
	if compiladoID == nil || strings.TrimSpace(*compiladoID) == "" {
		var activo string
		err := h.DB.QueryRow(ctx, `SELECT compilado_id::text FROM cotizadores_compilados WHERE calculadora_id=$1 AND estado='ACTIVA'`, calculadoraID).Scan(&activo)
		if errors.Is(err, pgx.ErrNoRows) {
			return resultado, &errorRuntime{status: http.StatusConflict, mensaje: "El cotizador no tiene una versión compilada activa."}
		}
		if err != nil {
			return resultado, err
		}
		compiladoID = &activo
		if fijar {
			if _, err := h.DB.Exec(ctx, `UPDATE cotizaciones SET compilado_id_usado=$2::uuid WHERE cotizacion_id=$1 AND compilado_id_usado IS NULL`, cotizacionID, activo); err != nil {
				return resultado, err
			}
		}
	}
	resultado.CompiladoID = *compiladoID
	var estructuraJSON []byte
	err = h.DB.QueryRow(ctx, `SELECT configuracion FROM cotizadores_compilados WHERE compilado_id=$1::uuid AND calculadora_id=$2`, resultado.CompiladoID, calculadoraID).Scan(&estructuraJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return resultado, &errorRuntime{status: http.StatusConflict, mensaje: "La versión compilada fijada ya no está disponible para este cotizador."}
	}
	if err != nil {
		return resultado, err
	}
	if err := json.Unmarshal(estructuraJSON, &resultado.Estructura); err != nil {
		return resultado, fmt.Errorf("estructura compilada inválida: %w", err)
	}
	resultado.Elementos = indexarElementosRuntime(resultado.Estructura)
	return resultado, nil
}

func indexarElementosRuntime(estructura map[string]any) map[string]elementoRuntime {
	resultado := make(map[string]elementoRuntime)
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		indexarElementosRuntimeRecursivo(elementos, resultado, "")
	}
	return resultado
}

// indexarElementosRuntimeRecursivo baja también a "hijos": desde la Ronda 1
// del Diseñador un CONTENEDOR anida sus componentes ahí en vez de dejarlos
// en el array plano de la sección (ver anidarHijosCompilado en compilador.go).
func indexarElementosRuntimeRecursivo(elementos []any, resultado map[string]elementoRuntime, padreOpcionesID string) {
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		id := strings.TrimSpace(fmt.Sprint(elemento["elemento_id"]))
		if id != "" {
			tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
			tipoListaPrecios := ""
			var columnasTabla map[string]bool
			permitirAgregar, permitirEliminar := true, true
			if cfg, ok := elemento["configuracion"].(map[string]any); ok {
				tipoListaPrecios = strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["tipo_lista_precios"])))
				if tipo == "TABLA" {
					permitirAgregar = boolDesdeConfiguracion(cfg, "permitir_agregar_filas", true)
					permitirEliminar = boolDesdeConfiguracion(cfg, "permitir_eliminar_filas", true)
					columnasRaw, _ := cfg["columnas"].([]any)
					columnasTabla = make(map[string]bool, len(columnasRaw))
					for _, colRaw := range columnasRaw {
						col, _ := colRaw.(map[string]any)
						columnaID := strings.TrimSpace(fmt.Sprint(col["columna_id"]))
						if columnaID != "" {
							columnasTabla[columnaID] = true
						}
					}
				}
			}
			resultado[id] = elementoRuntime{
				Tipo:                  tipo,
				CatalogoID:            strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"])),
				TipoListaPrecios:      tipoListaPrecios,
				ColumnasTabla:         columnasTabla,
				PermitirAgregarFilas:  permitirAgregar,
				PermitirEliminarFilas: permitirEliminar,
				PadreOpcionesID:       padreOpcionesID,
			}
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			nuevoPadreOpcionesID := padreOpcionesID
			if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) == "OPCIONES_PROPUESTA" {
				nuevoPadreOpcionesID = id
			}
			indexarElementosRuntimeRecursivo(hijos, resultado, nuevoPadreOpcionesID)
		}
	}
}

func (h *CotizadorRuntimeHandler) incluirOpcionesCatalogo(ctx context.Context, estructura map[string]any) error {
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		if err := h.incluirOpcionesCatalogoRecursivo(ctx, elementos); err != nil {
			return err
		}
	}
	return nil
}

func (h *CotizadorRuntimeHandler) incluirOpcionesCatalogoRecursivo(ctx context.Context, elementos []any) error {
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) == "CAMPO_CATALOGO" {
			catalogoID := strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"]))
			rows, err := h.DB.Query(ctx, `
				SELECT valor_id, COALESCE(clave, ''), texto_visible, valor_sistema, COALESCE(orden, 0)
				FROM catalogo_valores WHERE catalogo_id=$1 AND activo=true
				ORDER BY COALESCE(orden, 0), texto_visible`, catalogoID)
			if err != nil {
				return err
			}
			opciones := make([]map[string]any, 0)
			for rows.Next() {
				var valorID, clave, textoVisible, valorSistema string
				var orden int
				if err := rows.Scan(&valorID, &clave, &textoVisible, &valorSistema, &orden); err != nil {
					rows.Close()
					return err
				}
				opciones = append(opciones, map[string]any{"valor_id": valorID, "clave": clave, "texto_visible": textoVisible, "valor_sistema": valorSistema, "orden": orden})
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			elemento["opciones"] = opciones
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			if err := h.incluirOpcionesCatalogoRecursivo(ctx, hijos); err != nil {
				return err
			}
		}
	}
	return nil
}

// incluirValoresCajaValor resuelve, para cada CAJA_VALOR de la estructura
// (incluidos los anidados dentro de un CONTENEDOR), el valor real guardado
// de su campo_fuente_id en esta misma cotización/versión; si no hay valor
// guardado todavía, usa configuracion.valor_por_defecto. El resultado queda
// en "valor_resuelto", junto a prefijo/sufijo, para que el frontend solo
// tenga que concatenar sin volver a consultar nada.
func incluirValoresCajaValor(estructura map[string]any, valores map[string]any) {
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		incluirValoresCajaValorRecursivo(elementos, valores)
	}
}

func incluirValoresCajaValorRecursivo(elementos []any, valores map[string]any) {
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) == "CAJA_VALOR" {
			cfg, _ := elemento["configuracion"].(map[string]any)
			var resuelto any
			if cfg != nil {
				if fuenteRaw, existe := cfg["campo_fuente_id"]; existe && fuenteRaw != nil {
					fuenteID := strings.TrimSpace(fmt.Sprint(fuenteRaw))
					if fuenteID != "" {
						if valor, ok := valores[fuenteID]; ok {
							resuelto = valor
						}
					}
				}
				if resuelto == nil {
					resuelto = cfg["valor_por_defecto"]
				}
			}
			elemento["valor_resuelto"] = resuelto
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			incluirValoresCajaValorRecursivo(hijos, valores)
		}
	}
}

// incluirEstadoReglas deja, en cada elemento alcanzado por al menos una
// regla_cotizador activa (evaluarEstadoCamposRegla), su estado resultante
// bajo "estado_regla" — así el Motor de Ejecución sabe qué mostrar sin
// tener que reevaluar nada del lado del cliente. Un elemento que ninguna
// regla toca se queda sin esta clave: el frontend debe tratar su ausencia
// como el default (visible, habilitado, sin mínimo, no requerido).
func incluirEstadoReglas(estructura map[string]any, estados map[string]estadoCampoRegla) {
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		incluirEstadoReglasRecursivo(elementos, estados)
	}
}

func incluirEstadoReglasRecursivo(elementos []any, estados map[string]estadoCampoRegla) {
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		id := strings.TrimSpace(fmt.Sprint(elemento["elemento_id"]))
		if estado, ok := estados[id]; ok {
			elemento["estado_regla"] = estado
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			incluirEstadoReglasRecursivo(hijos, estados)
		}
	}
}

// valorForzadoPorRegla decide el valor "seguro" a persistir cuando una
// regla_cotizador deja un campo oculto o forzado a cero (sección 6.1 del
// documento: "un valor oculto no puede seguir sumándose silenciosamente").
// Un Campo numérico (Número/Moneda/Porcentaje) va a "0" — así un Campo
// Calculado que lo sume ya lo ve en cero, no un operando sin resolver
// (numeroDesdeValor("0") = (0, true)). Cualquier otro tipo (texto, Campo
// Catálogo, Lista de Precios, Tabla, o un Campo Calculado objetivo cuyo
// valor de todas formas se recalcula solo) va a null: no hay una
// "selección en cero" equivalente para esos tipos, y null es justamente lo
// que ya tratan como "sin resolver" valorCalculoCatalogo/valorListaPrecios/
// valorTotalTabla.
func valorForzadoPorRegla(elemento map[string]any) json.RawMessage {
	if elemento != nil && strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) == "CAMPO" {
		cfg, _ := elemento["configuracion"].(map[string]any)
		tipoCampo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["tipo_campo"])))
		if tipoCampo == "NUMERO" || tipoCampo == "MONEDA" || tipoCampo == "PORCENTAJE" {
			return json.RawMessage(`"0"`)
		}
	}
	return json.RawMessage(`null`)
}

// itemIDsDesdeValorListaPrecios extrae los item_id que trae el valor
// guardado de una Lista de Precios, validando su forma según el modo
// (UNICA: {"item_id":...,"cantidad":...}; MULTIPLE: {"filas":[...]}). No
// toca la base — GuardarValores valida existencia/pertenencia/estado aparte.
func itemIDsDesdeValorListaPrecios(tipoLista string, valorParsed map[string]any) ([]string, error) {
	if strings.ToUpper(strings.TrimSpace(tipoLista)) == "MULTIPLE" {
		filasRaw, _ := valorParsed["filas"].([]any)
		if len(filasRaw) == 0 {
			return nil, errors.New("debe indicar al menos una fila (item_id y cantidad)")
		}
		ids := make([]string, 0, len(filasRaw))
		for _, filaRaw := range filasRaw {
			fila, ok := filaRaw.(map[string]any)
			if !ok {
				return nil, errors.New("cada fila debe ser un objeto con item_id y cantidad")
			}
			id := strings.TrimSpace(fmt.Sprint(fila["item_id"]))
			if id == "" {
				return nil, errors.New("cada fila debe indicar item_id")
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	id := strings.TrimSpace(fmt.Sprint(valorParsed["item_id"]))
	if id == "" {
		return nil, errors.New("debe indicar item_id")
	}
	return []string{id}, nil
}

// filasDesdeValorTabla extrae las filas del valor guardado de una TABLA
// ({"filas":[{columna_id: valor, ...}, ...]}), validando su forma. No toca
// la base — GuardarValores valida pertenencia de columnas y límites de
// filas aparte.
func filasDesdeValorTabla(valorParsed map[string]any) ([]map[string]any, error) {
	filasRaw, _ := valorParsed["filas"].([]any)
	filas := make([]map[string]any, 0, len(filasRaw))
	for _, filaRaw := range filasRaw {
		fila, ok := filaRaw.(map[string]any)
		if !ok {
			return nil, errors.New(`cada fila debe ser un objeto {"columna_id": valor, ...}`)
		}
		filas = append(filas, fila)
	}
	return filas, nil
}

// consultadorRuntime lo satisfacen tanto *pgxpool.Pool como pgx.Tx: leer
// valores necesita funcionar contra la conexión suelta (GET normal) y contra
// la misma transacción que los acaba de escribir (POST, para que el
// recálculo de totales vea los valores recién guardados sin esperar a que
// el commit termine).
type consultadorRuntime interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// indexarElementosCompletoRuntime arma un índice elemento_id -> el mapa
// completo del elemento (no solo tipo/catalogo_id, como indexarElementosRuntime)
// porque resolverCamposCalculados necesita leer su "configuracion"
// (operacion, decimales, operandos) para calcular su valor.
func indexarElementosCompletoRuntime(estructura map[string]any) map[string]map[string]any {
	resultado := make(map[string]map[string]any)
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		indexarElementosCompletoRecursivo(elementos, resultado)
	}
	return resultado
}

func indexarElementosCompletoRecursivo(elementos []any, resultado map[string]map[string]any) {
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		id := strings.TrimSpace(fmt.Sprint(elemento["elemento_id"]))
		if id != "" {
			resultado[id] = elemento
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			indexarElementosCompletoRecursivo(hijos, resultado)
		}
	}
}

// numeroDesdeValor interpreta un valor guardado en cotizacion_valores (o ya
// resuelto de otro Campo Calculado) como float64. Los valores de CAMPO
// llegan como string (así los manda el Motor de Ejecución); un Campo
// Calculado que ya se resolvió llega directo como float64.
func numeroDesdeValor(valor any) (float64, bool) {
	switch v := valor.(type) {
	case float64:
		return v, true
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return 0, false
		}
		numero, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, false
		}
		return numero, true
	default:
		return 0, false
	}
}

// precioPorItemDesdeConfiguracion lee configuracion["items"] (embebido por
// incluirItemsListaPrecios en compilador.go) y arma un índice item_id ->
// precio, para no volver a tocar la base al resolver una Lista de Precios.
func precioPorItemDesdeConfiguracion(cfg map[string]any) map[string]float64 {
	resultado := make(map[string]float64)
	if cfg == nil {
		return resultado
	}
	items, _ := cfg["items"].([]any)
	for _, itemRaw := range items {
		item, _ := itemRaw.(map[string]any)
		id := strings.TrimSpace(fmt.Sprint(item["item_id"]))
		precio, ok := numeroDesdeValor(item["precio"])
		if id != "" && ok {
			resultado[id] = precio
		}
	}
	return resultado
}

// resolverFormulaConfigurada comparte el mismo resolutor de operandos entre
// los modos Simple/Avanzada y entre valores globales/por opción. La fórmula
// avanzada solicita únicamente los tokens de la rama elegida de SI.
func resolverFormulaConfigurada(cfg map[string]any, resolver func(string) (float64, bool), condicion func(string) (any, bool)) (float64, error) {
	decimales, ok := enteroDesdeConfiguracion(cfg, "decimales")
	if !ok {
		decimales = 2
	}
	if cfg["tipo_formula"] == "AVANZADA" {
		texto, _ := cfg["formula_texto"].(string)
		formula, err := compilarFormulaAvanzada(texto)
		if err != nil {
			return 0, err
		}
		// El compilado fija nombre interno -> ID. No se consulta ni se
		// vuelve a enlazar la fórmula contra nombres editados posteriormente.
		mapa := map[string]string{}
		switch tokens := cfg["tokens_operandos"].(type) {
		case map[string]string:
			mapa = tokens
		case map[string]any:
			for nombre, valor := range tokens {
				mapa[nombre], _ = valor.(string)
			}
		}
		resultado, err := formula.evaluarConResolver(func(nombre string, esCondicion bool) (any, bool) {
			id := mapa[nombre]
			if id == "" {
				return nil, false
			}
			if esCondicion {
				return condicion(id)
			}
			return resolver(id)
		})
		if err != nil {
			return 0, err
		}
		resultado = redondear(resultado, decimales)
		if math.IsInf(resultado, 0) || math.IsNaN(resultado) {
			return 0, fmt.Errorf("el resultado de la fórmula excede el rango numérico permitido")
		}
		return resultado, nil
	}
	operandos := operandosDesdeConfiguracion(cfg)
	valoresOperandos := make([]float64, 0, len(operandos))
	for _, id := range operandos {
		valor, ok := resolver(id)
		if !ok {
			return 0, fmt.Errorf("el operando %s no tiene un valor resuelto", id)
		}
		valoresOperandos = append(valoresOperandos, valor)
	}
	return calcularOperacion(valoresOperandos, strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["operacion"]))), decimales)
}

func resolverCondicionFormulaRuntime(elemento map[string]any, valor any, id string, resolver func(string) (float64, bool)) (any, bool) {
	if elemento == nil {
		return nil, false
	}
	switch elemento["tipo"] {
	case "CAMPO", "CAMPO_CATALOGO":
		// Una condición usa la selección original (Sí/No, true/false),
		// mientras que la aritmética del catálogo usa valor_calculo.
		return valor, true
	default:
		return resolver(id)
	}
}

// resolverCamposCalculados calcula el valor de cada CAMPO_CALCULADO, cada
// LISTA_PRECIOS y cada TABLA de la estructura y lo deja en "valor_resuelto"
// de ese elemento, mismo patrón que incluirValoresCajaValor — así el Motor
// de Ejecución muestra los tres de la misma forma. Un Campo Calculado puede
// depender de otro Campo Calculado, de una Lista de Precios (Ronda 3, tarea
// 3) o de una Tabla (Ronda 4, tarea 4); por eso los tres pasan por el mismo
// "resolver" recursivo: ni una Lista de Precios ni una Tabla dependen de
// nada, así que la recursión se corta sola ahí sin lógica extra. Devuelve
// además un mapa elemento_id ->
// valor resuelto (float64) para que actualizarTotalesCotizacionVersion no
// tenga que volver a recorrer la estructura. Un operando sin valor guardado
// todavía, un ciclo (no debería pasar, GuardarElemento ya lo rechaza al
// guardar) o una división entre cero dejan el campo sin resolver (nil) en
// vez de inventar un número — más "controlado" es no calcular que calcular mal.
func resolverCamposCalculados(elementosPorID map[string]map[string]any, valores map[string]any) map[string]float64 {
	resueltos := make(map[string]float64)
	enProceso := make(map[string]bool)

	var resolver func(id string) (float64, bool)
	resolver = func(id string) (float64, bool) {
		if v, ok := resueltos[id]; ok {
			return v, true
		}
		elemento, existe := elementosPorID[id]
		if !existe {
			return 0, false
		}
		tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))

		if tipo == "LISTA_PRECIOS" {
			cfg, _ := elemento["configuracion"].(map[string]any)
			valorGuardado, _ := valores[id].(map[string]any)
			resultado, ok := valorListaPrecios(fmt.Sprint(cfg["tipo_lista_precios"]), valorGuardado, precioPorItemDesdeConfiguracion(cfg))
			if ok {
				resueltos[id] = resultado
			}
			return resultado, ok
		}

		if tipo == "TABLA" {
			cfg, _ := elemento["configuracion"].(map[string]any)
			valorGuardado, _ := valores[id].(map[string]any)
			resultado, ok := valorTotalTabla(cfg, valorGuardado)
			if ok {
				resueltos[id] = resultado
			}
			return resultado, ok
		}

		if tipo == "CAMPO_CATALOGO" {
			cfg, _ := elemento["configuracion"].(map[string]any)
			resultado, ok := valorCalculoCatalogo(cfg, valores[id])
			if ok {
				resueltos[id] = resultado
			}
			return resultado, ok
		}

		if tipo != "CAMPO_CALCULADO" {
			return numeroDesdeValor(valores[id])
		}
		if enProceso[id] {
			return 0, false
		}
		enProceso[id] = true
		defer delete(enProceso, id)

		cfg, _ := elemento["configuracion"].(map[string]any)
		if cfg == nil {
			return 0, false
		}
		resultado, err := resolverFormulaConfigurada(cfg, resolver, func(opID string) (any, bool) {
			return resolverCondicionFormulaRuntime(elementosPorID[opID], valores[opID], opID, resolver)
		})
		if err != nil {
			return 0, false
		}
		resueltos[id] = resultado
		return resultado, true
	}

	for id, elemento := range elementosPorID {
		tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
		if tipo != "CAMPO_CALCULADO" && tipo != "LISTA_PRECIOS" && tipo != "TABLA" {
			continue
		}
		if valor, ok := resolver(id); ok {
			elemento["valor_resuelto"] = valor
		} else {
			elemento["valor_resuelto"] = nil
		}
	}
	return resueltos
}

// resolverCamposCalculadosPorOpcion aplica el mismo cálculo de la ruta
// tradicional, pero conserva un resultado por opcion_id para los componentes
// que viven dentro de OPCIONES_PROPUESTA. Un operando externo al componente
// sigue usando su valor global; un operando de otra colección de opciones se
// considera ambiguo y no se resuelve.
func resolverCamposCalculadosPorOpcion(elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any) {
	for padreID, padre := range elementosPorID {
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(padre["tipo"]))) != "OPCIONES_PROPUESTA" {
			continue
		}
		opcionesIDs := make([]string, 0)
		switch opciones := padre["opciones"].(type) {
		case []cotizacionOpcion:
			for _, opcion := range opciones {
				opcionesIDs = append(opcionesIDs, opcion.OpcionID)
			}
		case []any:
			for _, opcionRaw := range opciones {
				opcion, _ := opcionRaw.(map[string]any)
				opcionesIDs = append(opcionesIDs, strings.TrimSpace(fmt.Sprint(opcion["opcion_id"])))
			}
		}
		for _, opcionID := range opcionesIDs {
			if opcionID == "" {
				continue
			}
			resueltos := make(map[string]float64)
			enProceso := make(map[string]bool)
			var resolver func(string) (float64, bool)
			resolver = func(id string) (float64, bool) {
				if valor, ok := resueltos[id]; ok {
					return valor, true
				}
				elemento, existe := elementosPorID[id]
				if !existe {
					return 0, false
				}
				meta := metadatos[id]
				if meta.PadreOpcionesID != "" && meta.PadreOpcionesID != padreID {
					return 0, false
				}
				valorGuardado := valores[id]
				if meta.PadreOpcionesID == padreID {
					porOpcion, _ := valorGuardado.(map[string]any)
					valorGuardado = porOpcion[opcionID]
				}
				tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
				if tipo == "LISTA_PRECIOS" {
					cfg, _ := elemento["configuracion"].(map[string]any)
					valorLista, _ := valorGuardado.(map[string]any)
					resultado, ok := valorListaPrecios(fmt.Sprint(cfg["tipo_lista_precios"]), valorLista, precioPorItemDesdeConfiguracion(cfg))
					if ok {
						resueltos[id] = resultado
					}
					return resultado, ok
				}
				if tipo == "TABLA" {
					cfg, _ := elemento["configuracion"].(map[string]any)
					valorTabla, _ := valorGuardado.(map[string]any)
					resultado, ok := valorTotalTabla(cfg, valorTabla)
					if ok {
						resueltos[id] = resultado
					}
					return resultado, ok
				}
				if tipo == "CAMPO_CATALOGO" {
					cfg, _ := elemento["configuracion"].(map[string]any)
					resultado, ok := valorCalculoCatalogo(cfg, valorGuardado)
					if ok {
						resueltos[id] = resultado
					}
					return resultado, ok
				}
				if tipo != "CAMPO_CALCULADO" {
					return numeroDesdeValor(valorGuardado)
				}
				if enProceso[id] {
					return 0, false
				}
				enProceso[id] = true
				defer delete(enProceso, id)
				cfg, _ := elemento["configuracion"].(map[string]any)
				resultado, err := resolverFormulaConfigurada(cfg, resolver, func(opID string) (any, bool) {
					metaOp := metadatos[opID]
					if metaOp.PadreOpcionesID != "" && metaOp.PadreOpcionesID != padreID {
						return nil, false
					}
					valor := valores[opID]
					if metaOp.PadreOpcionesID == padreID {
						porOpcion, _ := valor.(map[string]any)
						valor = porOpcion[opcionID]
					}
					return resolverCondicionFormulaRuntime(elementosPorID[opID], valor, opID, resolver)
				})
				if err != nil {
					return 0, false
				}
				resueltos[id] = resultado
				return resultado, true
			}

			for id, meta := range metadatos {
				if meta.PadreOpcionesID != padreID || (meta.Tipo != "CAMPO_CALCULADO" && meta.Tipo != "LISTA_PRECIOS" && meta.Tipo != "TABLA") {
					continue
				}
				elemento := elementosPorID[id]
				porOpcion, _ := elemento["valores_resueltos_por_opcion"].(map[string]any)
				if porOpcion == nil {
					porOpcion = make(map[string]any)
				}
				if valor, ok := resolver(id); ok {
					porOpcion[opcionID] = valor
				} else {
					porOpcion[opcionID] = nil
				}
				elemento["valores_resueltos_por_opcion"] = porOpcion
			}
		}
	}
}

// columnaPorFuncionCampo mapea cada rol de "Función del campo" (Ronda 2,
// migración 0018) a su columna en cotizacion_versiones. NORMAL no mapea a
// nada — no actualiza totales.
var columnaPorFuncionCampo = map[string]string{
	"TOTAL_PRECIO_OFERTA":    "total_precio",
	"TOTAL_COSTO_INTERNO":    "total_costo",
	"TOTAL_GANANCIA_INTERNA": "total_ganancia",
	"MARGEN_TOTAL":           "margen_total",
	"MONEDA_OFERTA":          "moneda",
	"TIPO_CAMBIO":            "tipo_cambio",
	"SUBTOTAL_OFERTA":        "subtotal",
	"DESCUENTO_OFERTA":       "descuento",
	"IMPUESTOS_OFERTA":       "impuestos",
}

// columnasFuncionCampoTexto son las columnas de columnaPorFuncionCampo que
// son TEXT (moneda) en vez de NUMERIC — el resto se convierte con
// numeroDesdeValor antes de guardar.
var columnasFuncionCampoTexto = map[string]bool{"moneda": true}

// valorColumnaFuncionCampo convierte el valor crudo de un elemento con
// funcion_campo al tipo que espera su columna (texto para moneda, número
// para el resto) — compartido entre el destino cotizacion_versiones y el
// destino cotizacion_opciones (migración 0026). Un valor sin resolver
// (operando faltante, ciclo, texto no numérico) da ok=false: no es un error
// de la petición, simplemente esa columna no se actualiza esta vez.
func valorColumnaFuncionCampo(columna string, valorCrudo any) (any, bool) {
	if valorCrudo == nil {
		return nil, false
	}
	if columnasFuncionCampoTexto[columna] {
		texto := strings.TrimSpace(fmt.Sprint(valorCrudo))
		if texto == "" {
			return nil, false
		}
		return texto, true
	}
	return numeroDesdeValor(valorCrudo)
}

// actualizarTotalesCotizacionVersion recorre la estructura buscando
// elementos con funcion_campo distinto de NORMAL y actualiza la columna
// correspondiente — en la misma transacción que GuardarValores usa para los
// valores, así todo queda atómico.
//
// Un elemento con funcion_campo puede vivir en dos lugares distintos:
//
//   - Global (metadatos[id].PadreOpcionesID == ""): un único valor por
//     versión, va a la columna de cotizacion_versiones de siempre.
//   - Anidado bajo Opciones de Propuesta: su valor guardado es un mapa
//     opcion_id -> valor (mismo que resolverCamposCalculadosPorOpcion ya usa
//     para la vista del Motor de Ejecución), y cada opción tiene SU PROPIO
//     total — no cabe una sola cifra en cotizacion_versiones (que es una
//     fila por versión, no por escenario). Antes de la migración 0026 este
//     caso no tenía ningún manejo: el valor llegaba como mapa donde se
//     esperaba un escalar y actualizarColumnaCotizacionVersion simplemente
//     no encontraba nada que guardar — ni tomaba la primera opción, ni
//     sumaba, ni daba error, quedaba en silencio. Ahora se resuelve y
//     persiste por opción, en las columnas homónimas de cotizacion_opciones.
//
// padre["opciones"] no llega poblado acá (a diferencia de Obtener, que sí
// llama asegurarOpcionesPropuesta antes) — se completa de una vez, de solo
// lectura, con la misma consulta que usa esa función.
func (h *CotizadorRuntimeHandler) actualizarTotalesCotizacionVersion(ctx context.Context, tx pgx.Tx, estructura map[string]any, metadatos map[string]elementoRuntime, cotizacionID string, version int) error {
	valores, err := h.leerValores(ctx, tx, cotizacionID, version)
	if err != nil {
		return err
	}
	elementosPorID := indexarElementosCompletoRuntime(estructura)
	resolverCamposCalculados(elementosPorID, valores)

	for padreID, padre := range elementosPorID {
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(padre["tipo"]))) != "OPCIONES_PROPUESTA" {
			continue
		}
		opciones, err := listarOpcionesPropuesta(ctx, tx, cotizacionID, version, padreID)
		if err != nil {
			return err
		}
		padre["opciones"] = opciones
	}
	resolverCamposCalculadosPorOpcion(elementosPorID, metadatos, valores)

	for id, elemento := range elementosPorID {
		funcion := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["funcion_campo"])))
		if funcion == "" || funcion == "NORMAL" {
			continue
		}
		columna, ok := columnaPorFuncionCampo[funcion]
		if !ok {
			continue
		}
		tipoElemento := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
		esResuelto := tipoElemento == "CAMPO_CALCULADO" || tipoElemento == "LISTA_PRECIOS" || tipoElemento == "TABLA"

		if metadatos[id].PadreOpcionesID == "" {
			var valorCrudo any
			if esResuelto {
				valorCrudo = elemento["valor_resuelto"]
			} else {
				valorCrudo = valores[id]
			}
			valor, ok := valorColumnaFuncionCampo(columna, valorCrudo)
			if !ok {
				continue
			}
			if err := actualizarColumnaCotizacionVersion(ctx, tx, cotizacionID, version, columna, valor); err != nil {
				return err
			}
			continue
		}

		var porOpcion map[string]any
		if esResuelto {
			porOpcion, _ = elemento["valores_resueltos_por_opcion"].(map[string]any)
		} else {
			porOpcion, _ = valores[id].(map[string]any)
		}
		for opcionID, valorCrudo := range porOpcion {
			valor, ok := valorColumnaFuncionCampo(columna, valorCrudo)
			if !ok {
				continue
			}
			if err := actualizarColumnaCotizacionOpcion(ctx, tx, opcionID, columna, valor); err != nil {
				return err
			}
		}
	}
	return nil
}

// actualizarColumnaCotizacionVersion hace el UPDATE de una sola columna de
// cotizacion_versiones. Va con un switch de columnas literales (no con el
// nombre de columna interpolado en el SQL) a propósito: "columna" nunca debe
// construir la sentencia dinámicamente, aunque hoy solo llegue desde el mapa
// fijo columnaPorFuncionCampo.
func actualizarColumnaCotizacionVersion(ctx context.Context, tx pgx.Tx, cotizacionID string, version int, columna string, valor any) error {
	var err error
	switch columna {
	case "total_precio":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET total_precio=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "total_costo":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET total_costo=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "total_ganancia":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET total_ganancia=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "margen_total":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET margen_total=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "moneda":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET moneda=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "tipo_cambio":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET tipo_cambio=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "subtotal":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET subtotal=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "descuento":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET descuento=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	case "impuestos":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET impuestos=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, valor)
	}
	return err
}

// actualizarColumnaCotizacionOpcion es el equivalente de
// actualizarColumnaCotizacionVersion para un elemento con funcion_campo
// anidado bajo Opciones de Propuesta (migración 0026) — mismo criterio de
// switch con columnas literales, nunca interpoladas.
func actualizarColumnaCotizacionOpcion(ctx context.Context, tx pgx.Tx, opcionID string, columna string, valor any) error {
	var err error
	switch columna {
	case "total_precio":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET total_precio=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "total_costo":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET total_costo=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "total_ganancia":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET total_ganancia=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "margen_total":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET margen_total=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "moneda":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET moneda=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "tipo_cambio":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET tipo_cambio=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "subtotal":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET subtotal=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "descuento":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET descuento=$2 WHERE opcion_id=$1`, opcionID, valor)
	case "impuestos":
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET impuestos=$2 WHERE opcion_id=$1`, opcionID, valor)
	}
	return err
}

func (h *CotizadorRuntimeHandler) leerValores(ctx context.Context, q consultadorRuntime, cotizacionID string, version int) (map[string]any, error) {
	rows, err := q.Query(ctx, `SELECT elemento_id, opcion_id, valor FROM cotizacion_valores WHERE cotizacion_id=$1 AND version=$2`, cotizacionID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	valores := make(map[string]any)
	for rows.Next() {
		var elementoID string
		var opcionID *string
		var raw []byte
		if err := rows.Scan(&elementoID, &opcionID, &raw); err != nil {
			return nil, err
		}
		var valor any
		if err := json.Unmarshal(raw, &valor); err != nil {
			return nil, err
		}
		if opcionID == nil {
			valores[elementoID] = valor
			continue
		}
		porOpcion, _ := valores[elementoID].(map[string]any)
		if porOpcion == nil {
			porOpcion = make(map[string]any)
		}
		porOpcion[*opcionID] = valor
		valores[elementoID] = porOpcion
	}
	return valores, rows.Err()
}

func versionOpcional(texto string) (int, error) {
	texto = strings.TrimSpace(texto)
	if texto == "" {
		return 0, nil
	}
	version, err := strconv.Atoi(texto)
	if err != nil || version <= 0 {
		return 0, errors.New("version debe ser un número entero positivo")
	}
	return version, nil
}

func (h *CotizadorRuntimeHandler) responderError(w http.ResponseWriter, operacion, cotizacionID string, err error) {
	var conocido *errorRuntime
	if errors.As(err, &conocido) {
		escribirJSON(w, conocido.status, map[string]any{"ok": false, "error": conocido.mensaje})
		return
	}
	log.Printf("cotizador runtime: error %s de %s: %v", operacion, cotizacionID, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible procesar el runtime del cotizador."})
}
