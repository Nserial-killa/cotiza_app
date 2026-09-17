package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
// previamente compilada. No interpreta fórmulas ni tipos complejos.
type CotizadorRuntimeHandler struct {
	DB *pgxpool.Pool
}

type guardarValoresRuntimeRequest struct {
	Version int                        `json:"version"`
	Valores map[string]json.RawMessage `json:"valores"`
}

type elementoRuntime struct {
	Tipo             string
	CatalogoID       string
	TipoListaPrecios string
}

type contextoRuntime struct {
	CotizacionID string
	Version      int
	CompiladoID  string
	Estructura   map[string]any
	Elementos    map[string]elementoRuntime
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
	valores, err := h.leerValores(ctx, h.DB, cotizacionID, runtime.Version)
	if err != nil {
		log.Printf("cotizador runtime: error leyendo valores de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar los valores de la cotización."})
		return
	}
	resolverCamposCalculados(indexarElementosCompletoRuntime(runtime.Estructura), valores)
	incluirValoresCajaValor(runtime.Estructura, valores)
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
	if req.Version <= 0 || req.Valores == nil {
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
	for elementoID, valor := range req.Valores {
		elementoID = strings.TrimSpace(elementoID)
		elemento, existe := runtime.Elementos[elementoID]
		if !existe {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El elemento %s no pertenece a la estructura compilada de esta cotización.", elementoID)})
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
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar el guardado."})
		return
	}
	defer tx.Rollback(ctx)
	for elementoID, valor := range req.Valores {
		_, err = tx.Exec(ctx, `
			INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, valor)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (cotizacion_id, version, elemento_id) DO UPDATE SET valor=EXCLUDED.valor`,
			cotizacionID, req.Version, strings.TrimSpace(elementoID), string(valor))
		if err != nil {
			break
		}
	}
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	comentario := fmt.Sprintf("Se actualizaron %d valor(es) del cotizador.", len(req.Valores))
	if err == nil {
		err = insertarHistorial(ctx, tx, cotizacionID, &req.Version, "valores_actualizados", nil, nil, comentario, usuarioID)
	}
	if err == nil {
		err = h.actualizarTotalesCotizacionVersion(ctx, tx, runtime.Estructura, cotizacionID, req.Version)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("cotizador runtime: error guardando valores de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los valores de la cotización."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "cotizacion_id": cotizacionID, "version": req.Version, "valores_guardados": len(req.Valores)})
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
		indexarElementosRuntimeRecursivo(elementos, resultado)
	}
	return resultado
}

// indexarElementosRuntimeRecursivo baja también a "hijos": desde la Ronda 1
// del Diseñador un CONTENEDOR anida sus componentes ahí en vez de dejarlos
// en el array plano de la sección (ver anidarHijosCompilado en compilador.go).
func indexarElementosRuntimeRecursivo(elementos []any, resultado map[string]elementoRuntime) {
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		id := strings.TrimSpace(fmt.Sprint(elemento["elemento_id"]))
		if id != "" {
			tipoListaPrecios := ""
			if cfg, ok := elemento["configuracion"].(map[string]any); ok {
				tipoListaPrecios = strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["tipo_lista_precios"])))
			}
			resultado[id] = elementoRuntime{
				Tipo:             strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))),
				CatalogoID:       strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"])),
				TipoListaPrecios: tipoListaPrecios,
			}
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			indexarElementosRuntimeRecursivo(hijos, resultado)
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

// resolverCamposCalculados calcula el valor de cada CAMPO_CALCULADO y cada
// LISTA_PRECIOS de la estructura y lo deja en "valor_resuelto" de ese
// elemento, mismo patrón que incluirValoresCajaValor — así el Motor de
// Ejecución muestra ambos de la misma forma (Ronda 4). Un Campo Calculado
// puede depender de otro Campo Calculado o de una Lista de Precios (Ronda 3,
// tarea 3); por eso ambos pasan por el mismo "resolver" recursivo: una Lista
// de Precios nunca tiene ciclos (no depende de nada), así que la recursión
// se corta sola ahí sin lógica extra. Devuelve además un mapa elemento_id ->
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
		operandos := operandosDesdeConfiguracion(cfg)
		valoresOperandos := make([]float64, 0, len(operandos))
		for _, opID := range operandos {
			valorOp, ok := resolver(opID)
			if !ok {
				return 0, false
			}
			valoresOperandos = append(valoresOperandos, valorOp)
		}
		operacion := strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["operacion"])))
		decimales, ok := enteroDesdeConfiguracion(cfg, "decimales")
		if !ok {
			decimales = 2
		}
		resultado, err := calcularOperacion(valoresOperandos, operacion, decimales)
		if err != nil {
			return 0, false
		}
		resueltos[id] = resultado
		return resultado, true
	}

	for id, elemento := range elementosPorID {
		tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
		if tipo != "CAMPO_CALCULADO" && tipo != "LISTA_PRECIOS" {
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

// actualizarTotalesCotizacionVersion recorre la estructura buscando
// elementos con funcion_campo distinto de NORMAL, resuelve su valor (el ya
// calculado de resolverCamposCalculados para CAMPO_CALCULADO, o el valor
// crudo recién guardado para CAMPO/CAMPO_CATALOGO) y actualiza la columna
// correspondiente de cotizacion_versiones — en la misma transacción que
// GuardarValores usa para los valores, así ambos quedan atómicos. Un valor
// sin resolver (operando faltante, ciclo, texto no numérico) simplemente no
// actualiza esa columna esta vez; no es un error de la petición.
func (h *CotizadorRuntimeHandler) actualizarTotalesCotizacionVersion(ctx context.Context, tx pgx.Tx, estructura map[string]any, cotizacionID string, version int) error {
	valores, err := h.leerValores(ctx, tx, cotizacionID, version)
	if err != nil {
		return err
	}
	elementosPorID := indexarElementosCompletoRuntime(estructura)
	resolverCamposCalculados(elementosPorID, valores)

	for id, elemento := range elementosPorID {
		funcion := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["funcion_campo"])))
		if funcion == "" || funcion == "NORMAL" {
			continue
		}
		columna, ok := columnaPorFuncionCampo[funcion]
		if !ok {
			continue
		}
		var valorCrudo any
		tipoElemento := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
		if tipoElemento == "CAMPO_CALCULADO" || tipoElemento == "LISTA_PRECIOS" {
			valorCrudo = elemento["valor_resuelto"]
		} else {
			valorCrudo = valores[id]
		}
		if valorCrudo == nil {
			continue
		}
		if columnasFuncionCampoTexto[columna] {
			texto := strings.TrimSpace(fmt.Sprint(valorCrudo))
			if texto == "" {
				continue
			}
			if err := actualizarColumnaCotizacionVersion(ctx, tx, cotizacionID, version, columna, texto); err != nil {
				return err
			}
			continue
		}
		numero, ok := numeroDesdeValor(valorCrudo)
		if !ok {
			continue
		}
		if err := actualizarColumnaCotizacionVersion(ctx, tx, cotizacionID, version, columna, numero); err != nil {
			return err
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

func (h *CotizadorRuntimeHandler) leerValores(ctx context.Context, q consultadorRuntime, cotizacionID string, version int) (map[string]any, error) {
	rows, err := q.Query(ctx, `SELECT elemento_id, valor FROM cotizacion_valores WHERE cotizacion_id=$1 AND version=$2`, cotizacionID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	valores := make(map[string]any)
	for rows.Next() {
		var elementoID string
		var raw []byte
		if err := rows.Scan(&elementoID, &raw); err != nil {
			return nil, err
		}
		var valor any
		if err := json.Unmarshal(raw, &valor); err != nil {
			return nil, err
		}
		valores[elementoID] = valor
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
