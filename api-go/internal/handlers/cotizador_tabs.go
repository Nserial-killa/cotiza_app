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
)

// CotizadorTabsHandler administra la estructura simple del diseñador.
type CotizadorTabsHandler struct {
	DB *pgxpool.Pool
}

type tabCotizador struct {
	TabID                string  `json:"tab_id"`
	CalculadoraID        string  `json:"calculadora_id"`
	CalculadoraOrigenID  string  `json:"calculadora_origen_id"`
	Nombre               string  `json:"nombre"`
	NombreTab            string  `json:"nombre_tab"`
	Descripcion          *string `json:"descripcion"`
	Alcance              string  `json:"alcance"`
	Orden                int     `json:"orden"`
	Activo               bool    `json:"activo"`
	EsPropia             bool    `json:"es_propia"`
	SoloLectura          bool    `json:"solo_lectura"`
	ElementoAsociacionID *string `json:"elemento_asociacion_id,omitempty"`
}

type elementoTabCotizador struct {
	ElementoID        string             `json:"elemento_id"`
	TabID             string             `json:"tab_id"`
	Tipo              string             `json:"tipo"`
	TipoElemento      string             `json:"tipo_elemento"`
	Etiqueta          string             `json:"etiqueta"`
	CatalogoID        *string            `json:"catalogo_id"`
	ComponentePadreID *string            `json:"componente_padre_id"`
	CampoFuenteID     *string            `json:"campo_fuente_id"`
	FuncionCampo      string             `json:"funcion_campo"`
	ColumnasAncho     int                `json:"columnas_ancho"`
	Orden             int                `json:"orden"`
	Requerido         bool               `json:"requerido"`
	Configuracion     map[string]any     `json:"configuracion"`
	ConfigJSON        map[string]any     `json:"config_json"`
	Activo            bool               `json:"activo"`
	Items             []listaPreciosItem `json:"items,omitempty"`
	Columnas          []tablaColumna     `json:"columnas,omitempty"`
}

type guardarTabCotizadorRequest struct {
	TabID         string         `json:"tab_id"`
	CalculadoraID string         `json:"calculadora_id"`
	CotizadorID   string         `json:"cotizador_id"`
	Nombre        string         `json:"nombre"`
	NombreTab     string         `json:"nombre_tab"`
	Descripcion   string         `json:"descripcion"`
	Alcance       string         `json:"alcance"`
	Orden         enteroFlexible `json:"orden"`
	Activo        bool           `json:"activo"`
}

type guardarElementoTabRequest struct {
	ElementoID        string          `json:"elemento_id"`
	TabID             string          `json:"tab_id"`
	CalculadoraID     string          `json:"calculadora_id"`
	CotizadorID       string          `json:"cotizador_id"`
	Tipo              string          `json:"tipo"`
	TipoElemento      string          `json:"tipo_elemento"`
	Etiqueta          string          `json:"etiqueta"`
	CatalogoID        *string         `json:"catalogo_id"`
	ComponentePadreID *string         `json:"componente_padre_id"`
	CampoFuenteID     *string         `json:"campo_fuente_id"`
	FuncionCampo      string          `json:"funcion_campo"`
	ColumnasAncho     enteroFlexible  `json:"columnas_ancho"`
	Orden             enteroFlexible  `json:"orden"`
	Requerido         bool            `json:"requerido"`
	Configuracion     json.RawMessage `json:"configuracion"`
	ConfigJSON        json.RawMessage `json:"config_json"`
	Activo            bool            `json:"activo"`
}

// tiposElementoSimple es el conjunto de tipos que GuardarElemento acepta.
// Ronda 1 del Diseñador (migración 0017) agregó TITULO, CONTENEDOR y
// CAJA_VALOR a los 4 tipos simples del Sprint 2; Ronda 2 (migración 0018)
// agregó CAMPO_CALCULADO; Ronda 3 (migración 0019) agregó LISTA_PRECIOS;
// Ronda 4 (migración 0020) agrega TABLA y Ronda 5 (migración 0021)
// OPCIONES_PROPUESTA y Ronda 6 (migración 0022) SECCIONES_ADICIONALES.
var tiposElementoSimple = map[string]bool{
	"CAMPO": true, "CAMPO_CATALOGO": true, "LEYENDA": true, "TEXTO_INFORMATIVO": true,
	"TITULO": true, "CONTENEDOR": true, "CAJA_VALOR": true, "CAMPO_CALCULADO": true,
	"LISTA_PRECIOS": true, "TABLA": true, "OPCIONES_PROPUESTA": true,
	"SECCIONES_ADICIONALES": true,
}

// tiposConFuncionCampo son los únicos tipos donde "Función del campo" tiene
// sentido: alimentan un valor propio (numérico o de catálogo) que puede
// mapearse a un total de cotizacion_versiones. Una Lista de Precios es, en
// los hechos, otra fuente numérica (Ronda 3) — igual que un Campo numérico o
// un Campo Calculado. TABLA queda fuera a propósito: puede ser operando de
// un Campo Calculado (Ronda 4, tarea 4), pero no se pidió que alimente
// funcion_campo directamente. TITULO/CONTENEDOR/CAJA_VALOR/LEYENDA/
// TEXTO_INFORMATIVO no tienen un valor propio que exportar así.
var tiposConFuncionCampo = map[string]bool{
	"CAMPO": true, "CAMPO_CATALOGO": true, "CAMPO_CALCULADO": true, "LISTA_PRECIOS": true,
}

// tiposOperandoCalculadoValidos son los tipos que un Campo Calculado puede
// usar como operando (Ronda 2 + Ronda 3 + Ronda 4 + catálogos con
// valor_calculo, migración 0023): un Campo numérico se valida aparte por
// tipo_campo, y un CAMPO_CATALOGO se valida aparte por el tipo_calculo de
// su catálogo (SIN_VALOR queda afuera — ver el bloque de validación de
// operandos más abajo), así que acá solo van los tipos que no necesitan
// ningún chequeo extra: son, en los hechos, otra fuente numérica cuyo
// total/valor ya viene resuelto. Ver resolverCamposCalculados en
// cotizador_runtime.go.
var tiposOperandoCalculadoValidos = map[string]bool{
	"CAMPO_CALCULADO": true, "LISTA_PRECIOS": true, "TABLA": true,
}

// funcionesCampoValidas son los 9 roles de "Función del campo" (Ronda 2) más
// NORMAL (sin función especial, el caso común, único que puede repetirse).
var funcionesCampoValidas = map[string]bool{
	"NORMAL": true, "MONEDA_OFERTA": true, "TIPO_CAMBIO": true, "SUBTOTAL_OFERTA": true,
	"DESCUENTO_OFERTA": true, "IMPUESTOS_OFERTA": true, "TOTAL_PRECIO_OFERTA": true,
	"TOTAL_COSTO_INTERNO": true, "TOTAL_GANANCIA_INTERNA": true, "MARGEN_TOTAL": true,
}

func (h *CotizadorTabsHandler) ListarTabs(w http.ResponseWriter, r *http.Request) {
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := h.DB.Query(ctx, `
		SELECT t.tab_id, t.calculadora_id, t.nombre, t.descripcion, t.alcance,
		       t.orden, t.activo, true AS es_propia, NULL::text AS elemento_asociacion_id
		FROM tabs_cotizador t
		WHERE t.calculadora_id = $1
		UNION ALL
		SELECT t.tab_id, t.calculadora_id, t.nombre, t.descripcion, t.alcance,
		       t.orden, t.activo, false AS es_propia, a.elemento_id
		FROM tabs_cotizador_asociaciones a
		JOIN tabs_cotizador t ON t.tab_id = a.tab_id
		WHERE a.calculadora_id = $1 AND t.calculadora_id <> $1
		ORDER BY es_propia DESC, orden, nombre, tab_id`, calculadoraID)
	if err != nil {
		log.Printf("cotizador tabs: error listando %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las secciones."})
		return
	}
	defer rows.Close()
	tabs := make([]tabCotizador, 0)
	for rows.Next() {
		var tab tabCotizador
		if err := rows.Scan(&tab.TabID, &tab.CalculadoraID, &tab.Nombre, &tab.Descripcion, &tab.Alcance, &tab.Orden, &tab.Activo, &tab.EsPropia, &tab.ElementoAsociacionID); err != nil {
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer las secciones."})
			return
		}
		tab.NombreTab = tab.Nombre
		tab.CalculadoraOrigenID = tab.CalculadoraID
		tab.SoloLectura = !tab.EsPropia
		tabs = append(tabs, tab)
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "tabs": tabs, "data": tabs})
}

func (h *CotizadorTabsHandler) GuardarTab(w http.ResponseWriter, r *http.Request) {
	var req guardarTabCotizadorRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.TabID = strings.ToUpper(strings.TrimSpace(req.TabID))
	req.CalculadoraID = strings.ToUpper(strings.TrimSpace(req.CalculadoraID))
	if req.CalculadoraID == "" {
		req.CalculadoraID = strings.ToUpper(strings.TrimSpace(req.CotizadorID))
	}
	req.Nombre = strings.TrimSpace(req.Nombre)
	if req.Nombre == "" {
		req.Nombre = strings.TrimSpace(req.NombreTab)
	}
	req.Alcance = strings.ToUpper(strings.TrimSpace(req.Alcance))
	if req.Alcance == "" {
		req.Alcance = "PROPIO"
	}
	if req.TabID == "" || req.CalculadoraID == "" || req.Nombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar tab_id, calculadora_id y nombre."})
		return
	}
	if req.Alcance != "PROPIO" && req.Alcance != "REUTILIZABLE" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "alcance debe ser PROPIO o REUTILIZABLE."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar el guardado de la sección."})
		return
	}
	defer tx.Rollback(ctx)
	var calculadoraActual, alcanceActual string
	err = tx.QueryRow(ctx, `SELECT calculadora_id, alcance FROM tabs_cotizador WHERE tab_id=$1 FOR UPDATE`, req.TabID).Scan(&calculadoraActual, &alcanceActual)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("cotizador tabs: error validando %s: %v", req.TabID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la sección."})
		return
	}
	if err == nil && calculadoraActual != req.CalculadoraID {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "La sección pertenece a otro cotizador y es de solo lectura desde este diseñador."})
		return
	}
	if err == nil && alcanceActual == "REUTILIZABLE" && req.Alcance == "PROPIO" {
		var asociaciones int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM tabs_cotizador_asociaciones WHERE tab_id=$1`, req.TabID).Scan(&asociaciones); err != nil {
			log.Printf("cotizador tabs: error consultando asociaciones de %s: %v", req.TabID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar las asociaciones de la sección."})
			return
		}
		if asociaciones > 0 {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "La sección está asociada a otro cotizador. Desasóciela antes de cambiar su alcance a PROPIO."})
			return
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, descripcion, alcance, orden, activo)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tab_id) DO UPDATE SET
			calculadora_id = EXCLUDED.calculadora_id, nombre = EXCLUDED.nombre,
			descripcion = EXCLUDED.descripcion,
			alcance = EXCLUDED.alcance, orden = EXCLUDED.orden, activo = EXCLUDED.activo`,
		req.TabID, req.CalculadoraID, req.Nombre, req.Descripcion, req.Alcance, int(req.Orden), req.Activo)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("cotizador tabs: error guardando %s: %v", req.TabID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar la sección."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Sección guardada.", "tab_id": req.TabID})
}

func (h *CotizadorTabsHandler) ListarElementos(w http.ResponseWriter, r *http.Request) {
	tabID := strings.TrimSpace(r.URL.Query().Get("tab_id"))
	if tabID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar tab_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := h.DB.Query(ctx, `
		SELECT elemento_id, tab_id, tipo, COALESCE(etiqueta, ''), catalogo_id,
		       componente_padre_id, campo_fuente_id, funcion_campo,
		       columnas_ancho, orden, requerido, configuracion, activo
		FROM elementos_tab_cotizador WHERE tab_id = $1 ORDER BY orden, elemento_id`, tabID)
	if err != nil {
		log.Printf("cotizador elementos: error listando %s: %v", tabID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los elementos."})
		return
	}
	defer rows.Close()
	elementos := make([]elementoTabCotizador, 0)
	idsListaPrecios := make([]string, 0)
	idsTablas := make([]string, 0)
	for rows.Next() {
		var el elementoTabCotizador
		if err := rows.Scan(&el.ElementoID, &el.TabID, &el.Tipo, &el.Etiqueta, &el.CatalogoID, &el.ComponentePadreID, &el.CampoFuenteID, &el.FuncionCampo, &el.ColumnasAncho, &el.Orden, &el.Requerido, &el.Configuracion, &el.Activo); err != nil {
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer los elementos."})
			return
		}
		el.TipoElemento = el.Tipo
		el.ConfigJSON = el.Configuracion
		if el.Tipo == "LISTA_PRECIOS" {
			idsListaPrecios = append(idsListaPrecios, el.ElementoID)
		}
		if el.Tipo == "TABLA" {
			idsTablas = append(idsTablas, el.ElementoID)
		}
		elementos = append(elementos, el)
	}
	if err := rows.Err(); err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer los elementos."})
		return
	}
	if len(idsListaPrecios) > 0 {
		// El Diseñador ve TODOS los ítems, activos e inactivos — a
		// diferencia de compilador.go/runtime, que solo exponen los
		// activos. costo_interno/margen_porcentaje sí quedan detrás de
		// puede_ver_price acá (mismo mecanismo que cotizaciones.go/
		// dashboard.go): el Diseñador nunca tuvo restricción de rol, pero
		// estos dos campos son precio interno igual que en cualquier otra
		// pantalla — el resto del elemento (incluido el resto del ítem)
		// sigue visible para cualquier sesión.
		puedeVerPrice, err := (&CotizacionesHandler{DB: h.DB}).sesionPuedeVerPrice(ctx, r)
		if err != nil {
			log.Printf("cotizador elementos: error validando permiso de precio: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
			return
		}
		itemsRows, err := h.DB.Query(ctx, `
			SELECT item_id::text, elemento_id, codigo, nombre, descripcion, precio, moneda,
			       unidad_cobro, costo_interno, margen_porcentaje, orden, activo
			FROM lista_precios_items WHERE elemento_id = ANY($1) ORDER BY elemento_id, orden, codigo`, idsListaPrecios)
		if err != nil {
			log.Printf("cotizador elementos: error listando ítems de lista de precios: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los ítems de las listas de precios."})
			return
		}
		itemsPorElemento := make(map[string][]listaPreciosItem)
		for itemsRows.Next() {
			var item listaPreciosItem
			if err := itemsRows.Scan(&item.ItemID, &item.ElementoID, &item.Codigo, &item.Nombre, &item.Descripcion, &item.Precio, &item.Moneda, &item.UnidadCobro, &item.CostoInterno, &item.MargenPorcentaje, &item.Orden, &item.Activo); err != nil {
				itemsRows.Close()
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer los ítems de las listas de precios."})
				return
			}
			item.OcultarPrecioInterno = !puedeVerPrice
			itemsPorElemento[item.ElementoID] = append(itemsPorElemento[item.ElementoID], item)
		}
		if err := itemsRows.Err(); err != nil {
			itemsRows.Close()
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer los ítems de las listas de precios."})
			return
		}
		itemsRows.Close()
		for i := range elementos {
			if elementos[i].Tipo == "LISTA_PRECIOS" {
				elementos[i].Items = itemsPorElemento[elementos[i].ElementoID]
			}
		}
	}
	if len(idsTablas) > 0 {
		columnasRows, err := h.DB.Query(ctx, `
			SELECT columna_id::text, elemento_id, origen, campo_existente_id, tipo_dato, etiqueta, orden
			FROM tabla_columnas WHERE elemento_id = ANY($1) ORDER BY elemento_id, orden`, idsTablas)
		if err != nil {
			log.Printf("cotizador elementos: error listando columnas de tabla: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las columnas de las tablas."})
			return
		}
		columnasPorElemento := make(map[string][]tablaColumna)
		for columnasRows.Next() {
			var col tablaColumna
			if err := columnasRows.Scan(&col.ColumnaID, &col.ElementoID, &col.Origen, &col.CampoExistenteID, &col.TipoDato, &col.Etiqueta, &col.Orden); err != nil {
				columnasRows.Close()
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer las columnas de las tablas."})
				return
			}
			columnasPorElemento[col.ElementoID] = append(columnasPorElemento[col.ElementoID], col)
		}
		if err := columnasRows.Err(); err != nil {
			columnasRows.Close()
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer las columnas de las tablas."})
			return
		}
		columnasRows.Close()
		for i := range elementos {
			if elementos[i].Tipo == "TABLA" {
				elementos[i].Columnas = columnasPorElemento[elementos[i].ElementoID]
			}
		}
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "elementos": elementos, "data": elementos})
}

func (h *CotizadorTabsHandler) GuardarElemento(w http.ResponseWriter, r *http.Request) {
	var req guardarElementoTabRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.ElementoID = strings.ToUpper(strings.TrimSpace(req.ElementoID))
	req.TabID = strings.ToUpper(strings.TrimSpace(req.TabID))
	req.Tipo = strings.ToUpper(strings.TrimSpace(req.Tipo))
	if req.Tipo == "" {
		req.Tipo = strings.ToUpper(strings.TrimSpace(req.TipoElemento))
	}
	req.Etiqueta = strings.TrimSpace(req.Etiqueta)
	if req.ElementoID == "" || req.TabID == "" || req.Etiqueta == "" || !tiposElementoSimple[req.Tipo] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar elemento_id, tab_id, etiqueta y un tipo simple válido."})
		return
	}
	catalogoID := ""
	if req.CatalogoID != nil {
		catalogoID = strings.TrimSpace(*req.CatalogoID)
	}
	if req.Tipo == "CAMPO_CATALOGO" && catalogoID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "catalogo_id es obligatorio para CAMPO_CATALOGO."})
		return
	}
	if req.Tipo != "CAMPO_CATALOGO" && catalogoID != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "catalogo_id debe venir vacío para este tipo de elemento."})
		return
	}
	configuracion, err := normalizarConfiguracionElemento(req.Configuracion, req.ConfigJSON)
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	columnas := int(req.ColumnasAncho)
	if columnas == 0 {
		columnas = 1
	}
	if columnas < 1 || columnas > 4 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "columnas_ancho debe estar entre 1 y 4."})
		return
	}

	padreID := ""
	if req.ComponentePadreID != nil {
		padreID = strings.ToUpper(strings.TrimSpace(*req.ComponentePadreID))
	}
	fuenteID := ""
	if req.CampoFuenteID != nil {
		fuenteID = strings.ToUpper(strings.TrimSpace(*req.CampoFuenteID))
	}
	esComponentePadre := req.Tipo == "CONTENEDOR" || req.Tipo == "OPCIONES_PROPUESTA"
	if esComponentePadre && padreID != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un Contenedor u Opciones de Propuesta no puede tener componente_padre_id."})
		return
	}
	if esComponentePadre && fuenteID != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un Contenedor u Opciones de Propuesta no puede tener campo_fuente_id."})
		return
	}
	if req.Tipo != "CAJA_VALOR" && fuenteID != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "campo_fuente_id solo aplica a componentes de tipo CAJA_VALOR."})
		return
	}
	if req.Tipo == "CONTENEDOR" {
		columnasContenedor, ok := enteroDesdeConfiguracion(configuracion, "columnas")
		if !ok || (columnasContenedor != 2 && columnasContenedor != 3) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El Contenedor debe indicar columnas: 2 o 3."})
			return
		}
	}
	if req.Tipo == "OPCIONES_PROPUESTA" {
		cantidadInicial, ok := enteroDesdeConfiguracion(configuracion, "cantidad_inicial")
		if !ok || cantidadInicial <= 0 {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "cantidad_inicial debe ser un entero positivo."})
			return
		}
		configuracion["cantidad_inicial"] = cantidadInicial

		nombres, err := normalizarNombresSugeridos(configuracion["nombres_sugeridos"])
		if err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		configuracion["nombres_sugeridos"] = strings.Join(nombres, ", ")

		vistas := []struct {
			campo      string
			porDefecto string
			permitidas map[string]bool
		}{
			{"vista_editar", "PESTANAS", map[string]bool{"PESTANAS": true, "ACORDEON": true}},
			{"vista_resumen", "CAJAS", map[string]bool{"CAJAS": true, "TABLA_COMPARATIVA": true, "NINGUNA": true}},
			{"vista_oferta", "TABLA_COMPARATIVA", map[string]bool{"TABLA_COMPARATIVA": true, "CAJAS": true, "SOLO_SELECCIONADA": true}},
		}
		for _, vista := range vistas {
			valor := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion[vista.campo])))
			if valor == "" || valor == "<NIL>" {
				valor = vista.porDefecto
			}
			if !vista.permitidas[valor] {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": vista.campo + " no tiene un valor válido."})
				return
			}
			configuracion[vista.campo] = valor
		}

		campoPrincipalID := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["campo_principal_id"])))
		if campoPrincipalID == "<NIL>" {
			campoPrincipalID = ""
		}
		configuracion["campo_principal_id"] = campoPrincipalID
		configuracion["permitir_duplicar"] = boolDesdeConfiguracion(configuracion, "permitir_duplicar", true)
		configuracion["permitir_eliminar"] = boolDesdeConfiguracion(configuracion, "permitir_eliminar", true)
		configuracion["permitir_renombrar"] = boolDesdeConfiguracion(configuracion, "permitir_renombrar", true)
		configuracion["permitir_recomendado"] = boolDesdeConfiguracion(configuracion, "permitir_recomendado", true)
		configuracion["visible_calculadora"] = boolDesdeConfiguracion(configuracion, "visible_calculadora", true)
		configuracion["visible_oferta"] = boolDesdeConfiguracion(configuracion, "visible_oferta", true)
	}
	if req.Tipo == "SECCIONES_ADICIONALES" {
		presentacion := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["presentacion"])))
		if presentacion == "" || presentacion == "<NIL>" {
			presentacion = "CHECKS"
		}
		if presentacion != "CHECKS" && presentacion != "LISTA_MULTIPLE" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "presentacion debe ser CHECKS o LISTA_MULTIPLE."})
			return
		}
		columnas, ok := enteroDesdeConfiguracion(configuracion, "columnas")
		if !ok {
			columnas = 2
		}
		if columnas != 1 && columnas != 2 && columnas != 4 && columnas != 6 {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "columnas debe ser 1, 2, 4 o 6 para Secciones Adicionales."})
			return
		}
		configuracion["presentacion"] = presentacion
		configuracion["columnas"] = columnas
		configuracion["visible_calculadora"] = boolDesdeConfiguracion(configuracion, "visible_calculadora", true)
		configuracion["visible_oferta"] = boolDesdeConfiguracion(configuracion, "visible_oferta", false)
	}

	req.FuncionCampo = strings.ToUpper(strings.TrimSpace(req.FuncionCampo))
	if req.FuncionCampo == "" {
		req.FuncionCampo = "NORMAL"
	}
	if !funcionesCampoValidas[req.FuncionCampo] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "funcion_campo no es un valor válido."})
		return
	}
	if req.FuncionCampo != "NORMAL" && !tiposConFuncionCampo[req.Tipo] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "funcion_campo solo aplica a Campo, Campo Catálogo o Campo Calculado."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	solicitanteID := strings.ToUpper(strings.TrimSpace(req.CalculadoraID))
	if solicitanteID == "" {
		solicitanteID = strings.ToUpper(strings.TrimSpace(req.CotizadorID))
	}
	if solicitanteID != "" {
		var calculadoraDuena string
		err := h.DB.QueryRow(ctx, `SELECT calculadora_id FROM tabs_cotizador WHERE tab_id=$1`, req.TabID).Scan(&calculadoraDuena)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La sección indicada no existe."})
			return
		}
		if err != nil {
			log.Printf("cotizador elementos: error validando dueño de %s: %v", req.TabID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la sección."})
			return
		}
		if calculadoraDuena != solicitanteID {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "La sección pertenece a otro cotizador y su estructura es de solo lectura."})
			return
		}
	}
	if req.Tipo == "OPCIONES_PROPUESTA" {
		campoPrincipalID := strings.TrimSpace(fmt.Sprint(configuracion["campo_principal_id"]))
		if campoPrincipalID != "" {
			if campoPrincipalID == req.ElementoID {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "campo_principal_id debe referenciar otro elemento."})
				return
			}
			var tabCampo string
			var activoCampo bool
			err := h.DB.QueryRow(ctx, `SELECT tab_id, activo FROM elementos_tab_cotizador WHERE elemento_id=$1`, campoPrincipalID).Scan(&tabCampo, &activoCampo)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && (tabCampo != req.TabID || !activoCampo)) {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "campo_principal_id debe ser otro elemento activo del mismo tab."})
				return
			}
			if err != nil {
				log.Printf("cotizador elementos: error validando campo principal %s: %v", campoPrincipalID, err)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar campo_principal_id."})
				return
			}
		}
	}

	if req.FuncionCampo != "NORMAL" {
		var calculadoraID string
		err := h.DB.QueryRow(ctx, `SELECT calculadora_id FROM tabs_cotizador WHERE tab_id=$1`, req.TabID).Scan(&calculadoraID)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El tab_id indicado no existe."})
			return
		}
		if err != nil {
			log.Printf("cotizador elementos: error resolviendo calculadora de %s: %v", req.TabID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar funcion_campo."})
			return
		}
		var otroElementoID string
		err = h.DB.QueryRow(ctx, `
			SELECT e.elemento_id FROM elementos_tab_cotizador e
			JOIN tabs_cotizador t ON t.tab_id = e.tab_id
			WHERE t.calculadora_id = $1 AND e.activo = true AND e.funcion_campo = $2 AND e.elemento_id <> $3
			LIMIT 1`, calculadoraID, req.FuncionCampo, req.ElementoID).Scan(&otroElementoID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("cotizador elementos: error validando unicidad de funcion_campo: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar funcion_campo."})
			return
		}
		if otroElementoID != "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El elemento %s ya tiene la función %s en este cotizador.", otroElementoID, req.FuncionCampo)})
			return
		}
	}

	if req.Tipo == "CAMPO_CALCULADO" {
		tipoFormula := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["tipo_formula"])))
		if tipoFormula == "" || tipoFormula == "<NIL>" {
			tipoFormula = "SIMPLE"
		}
		if tipoFormula != "SIMPLE" && tipoFormula != "AVANZADA" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_formula debe ser SIMPLE o AVANZADA."})
			return
		}
		configuracion["tipo_formula"] = tipoFormula

		tipoResultado := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["tipo_resultado"])))
		if tipoResultado != "NUMERO" && tipoResultado != "MONEDA" && tipoResultado != "PORCENTAJE" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_resultado debe ser NUMERO, MONEDA o PORCENTAJE."})
			return
		}
		configuracion["tipo_resultado"] = tipoResultado

		decimales, ok := enteroDesdeConfiguracion(configuracion, "decimales")
		if !ok {
			decimales = 2
		}
		if decimales < 0 || decimales > 4 {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "decimales debe estar entre 0 y 4."})
			return
		}
		configuracion["decimales"] = decimales

		if tipoFormula == "AVANZADA" {
			if err := h.prepararFormulaAvanzada(ctx, req.ElementoID, req.TabID, configuracion); err != nil {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
				return
			}
		} else {
			delete(configuracion, "formula_texto")
			delete(configuracion, "tokens")
			delete(configuracion, "tokens_condicion")
			delete(configuracion, "tokens_operandos")
			operacion := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["operacion"])))
			if !operacionesCalculoValidas[operacion] {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "operacion debe ser SUMA, RESTA, MULTIPLICACION, DIVISION o PROMEDIO."})
				return
			}
			configuracion["operacion"] = operacion
			operandos := operandosDesdeConfiguracion(configuracion)
			minimoOperandos := 2
			if operacion == "PROMEDIO" {
				minimoOperandos = 1
			}
			if len(operandos) < minimoOperandos {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("La operación %s necesita al menos %d operando(s).", operacion, minimoOperandos)})
				return
			}
			for _, opID := range operandos {
				if opID == req.ElementoID {
					escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un Campo Calculado no puede tener a sí mismo como operando."})
					return
				}
				var tipoOp, tabOp string
				var activoOp bool
				var configOp map[string]any
				var catalogoOpID *string
				err := h.DB.QueryRow(ctx, `SELECT tipo, tab_id, activo, configuracion, catalogo_id FROM elementos_tab_cotizador WHERE elemento_id=$1`, opID).Scan(&tipoOp, &tabOp, &activoOp, &configOp, &catalogoOpID)
				if errors.Is(err, pgx.ErrNoRows) {
					escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s no existe.", opID)})
					return
				}
				if err != nil {
					log.Printf("cotizador elementos: error validando operando %s: %v", opID, err)
					escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los operandos."})
					return
				}
				if !activoOp {
					escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s está inactivo.", opID)})
					return
				}
				if tabOp != req.TabID {
					escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s debe pertenecer a la misma sección (tab_id).", opID)})
					return
				}
				if tipoOp == "CAMPO" {
					tipoCampo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configOp["tipo_campo"])))
					if tipoCampo != "NUMERO" && tipoCampo != "MONEDA" && tipoCampo != "PORCENTAJE" {
						escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s debe ser un Campo numérico (Número, Moneda o Porcentaje).", opID)})
						return
					}
				} else if tipoOp == "CAMPO_CATALOGO" {
					catalogoID := ""
					if catalogoOpID != nil {
						catalogoID = strings.TrimSpace(*catalogoOpID)
					}
					if catalogoID == "" {
						escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s no tiene catálogo asignado.", opID)})
						return
					}
					var tipoCalculoCatalogo, nombreCatalogo string
					err := h.DB.QueryRow(ctx, `SELECT tipo_calculo, nombre_catalogo FROM catalogos WHERE catalogo_id=$1`, catalogoID).Scan(&tipoCalculoCatalogo, &nombreCatalogo)
					if errors.Is(err, pgx.ErrNoRows) {
						escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s apunta a un catálogo (%s) que no existe.", opID, catalogoID)})
						return
					}
					if err != nil {
						log.Printf("cotizador elementos: error validando catálogo del operando %s: %v", opID, err)
						escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los operandos."})
						return
					}
					if tipoCalculoCatalogo == "SIN_VALOR" {
						escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s usa el catálogo %s, que es solo descriptivo (SIN_VALOR) y no tiene valores de cálculo.", opID, nombreCatalogo)})
						return
					}
				} else if !tiposOperandoCalculadoValidos[tipoOp] {
					escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s debe ser un Campo numérico, un Campo Catálogo con valores de cálculo, una Lista de Precios, una Tabla o un Campo Calculado.", opID)})
					return
				}
			}
			circular, err := h.tieneReferenciaCircular(ctx, req.ElementoID, operandos, map[string]bool{})
			if err != nil {
				log.Printf("cotizador elementos: error detectando referencia circular en %s: %v", req.ElementoID, err)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los operandos."})
				return
			}
			if circular {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Los operandos generan una referencia circular: un Campo Calculado no puede depender de sí mismo, ni directa ni indirectamente."})
				return
			}
		}
	}

	if req.Tipo == "LISTA_PRECIOS" {
		tipoLista := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["tipo_lista_precios"])))
		if tipoLista != "UNICA" && tipoLista != "MULTIPLE" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_lista_precios debe ser UNICA o MULTIPLE."})
			return
		}
		configuracion["tipo_lista_precios"] = tipoLista

		// valor_que_alimenta queda preparado para más opciones a futuro
		// (ej. costo unitario, margen) — hoy solo se implementó
		// PRECIO_UNITARIO (precio × cantidad), ver calcularOperacion en
		// calculo.go y valorListaPrecios.
		valorQueAlimenta := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["valor_que_alimenta"])))
		if valorQueAlimenta == "" || valorQueAlimenta == "<NIL>" {
			valorQueAlimenta = "PRECIO_UNITARIO"
		}
		if valorQueAlimenta != "PRECIO_UNITARIO" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "valor_que_alimenta todavía solo admite PRECIO_UNITARIO."})
			return
		}
		configuracion["valor_que_alimenta"] = valorQueAlimenta

		configuracion["mostrar_cantidad"] = boolDesdeConfiguracion(configuracion, "mostrar_cantidad", true)
		configuracion["seleccion_obligatoria"] = boolDesdeConfiguracion(configuracion, "seleccion_obligatoria", false)
		configuracion["mostrar_precio_unitario"] = boolDesdeConfiguracion(configuracion, "mostrar_precio_unitario", true)
		configuracion["mostrar_descripcion"] = boolDesdeConfiguracion(configuracion, "mostrar_descripcion", true)
		configuracion["mostrar_unidad_cobro"] = boolDesdeConfiguracion(configuracion, "mostrar_unidad_cobro", true)

		itemDefecto := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["item_seleccionado_por_defecto"])))
		if itemDefecto == "" || itemDefecto == "<NIL>" || itemDefecto == "PRIMERO_ACTIVO" {
			itemDefecto = "PRIMERO_ACTIVO"
		} else {
			var existeCodigo bool
			err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lista_precios_items WHERE elemento_id=$1 AND codigo=$2 AND activo=true)`, req.ElementoID, itemDefecto).Scan(&existeCodigo)
			if err != nil {
				log.Printf("cotizador elementos: error validando item_seleccionado_por_defecto de %s: %v", req.ElementoID, err)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el ítem por defecto."})
				return
			}
			if !existeCodigo {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("item_seleccionado_por_defecto debe ser PRIMERO_ACTIVO o el código de un ítem activo de este elemento (%s no existe).", itemDefecto)})
				return
			}
		}
		configuracion["item_seleccionado_por_defecto"] = itemDefecto
	}

	if req.Tipo == "TABLA" {
		tipoTabla := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["tipo_tabla"])))
		if tipoTabla == "" || tipoTabla == "<NIL>" {
			tipoTabla = "SIMPLE"
		}
		if tipoTabla != "SIMPLE" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_tabla debe ser SIMPLE (todavía no hay otro tipo de tabla)."})
			return
		}
		configuracion["tipo_tabla"] = tipoTabla

		etiquetaTotal := strings.TrimSpace(fmt.Sprint(configuracion["etiqueta_total"]))
		if etiquetaTotal == "" || etiquetaTotal == "<nil>" {
			etiquetaTotal = "TOTAL"
		}
		configuracion["etiqueta_total"] = etiquetaTotal

		unidad := strings.TrimSpace(fmt.Sprint(configuracion["unidad"]))
		if unidad == "<nil>" {
			unidad = ""
		}
		configuracion["unidad"] = unidad

		configuracion["permitir_agregar_filas"] = boolDesdeConfiguracion(configuracion, "permitir_agregar_filas", true)
		configuracion["permitir_eliminar_filas"] = boolDesdeConfiguracion(configuracion, "permitir_eliminar_filas", true)
		configuracion["tabla_editable"] = boolDesdeConfiguracion(configuracion, "tabla_editable", true)
		configuracion["editable"] = boolDesdeConfiguracion(configuracion, "editable", true)
		configuracion["obligatorio"] = boolDesdeConfiguracion(configuracion, "obligatorio", false)
		configuracion["visible_calculadora"] = boolDesdeConfiguracion(configuracion, "visible_calculadora", true)
		configuracion["visible_oferta"] = boolDesdeConfiguracion(configuracion, "visible_oferta", true)
	}

	if padreID != "" {
		if padreID == req.ElementoID {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "componente_padre_id no puede ser el propio elemento."})
			return
		}
		var tipoPadre, tabPadre string
		var activoPadre bool
		err := h.DB.QueryRow(ctx, `SELECT tipo, tab_id, activo FROM elementos_tab_cotizador WHERE elemento_id=$1`, padreID).Scan(&tipoPadre, &tabPadre, &activoPadre)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El componente padre indicado no existe."})
			return
		}
		if err != nil {
			log.Printf("cotizador elementos: error validando padre %s: %v", padreID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el componente padre."})
			return
		}
		if tipoPadre != "CONTENEDOR" && tipoPadre != "OPCIONES_PROPUESTA" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El componente padre debe ser de tipo CONTENEDOR u OPCIONES_PROPUESTA."})
			return
		}
		if !activoPadre {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El componente padre indicado está inactivo."})
			return
		}
		if tabPadre != req.TabID {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El componente padre debe pertenecer a la misma sección (tab_id)."})
			return
		}
	}

	if fuenteID != "" {
		if fuenteID == req.ElementoID {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "campo_fuente_id no puede ser el propio elemento."})
			return
		}
		var tabFuente string
		var activoFuente bool
		err := h.DB.QueryRow(ctx, `SELECT tab_id, activo FROM elementos_tab_cotizador WHERE elemento_id=$1`, fuenteID).Scan(&tabFuente, &activoFuente)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El campo fuente indicado no existe."})
			return
		}
		if err != nil {
			log.Printf("cotizador elementos: error validando campo fuente %s: %v", fuenteID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el campo fuente."})
			return
		}
		if !activoFuente {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El campo fuente indicado está inactivo."})
			return
		}
		if tabFuente != req.TabID {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El campo fuente debe pertenecer a la misma sección (tab_id)."})
			return
		}
	}

	_, err = h.DB.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador
			(elemento_id, tab_id, tipo, etiqueta, catalogo_id, componente_padre_id, campo_fuente_id, funcion_campo, columnas_ancho, orden, requerido, configuracion, activo)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), $8, $9, $10, $11, $12, $13)
		ON CONFLICT (elemento_id) DO UPDATE SET
			tab_id = EXCLUDED.tab_id, tipo = EXCLUDED.tipo, etiqueta = EXCLUDED.etiqueta,
			catalogo_id = EXCLUDED.catalogo_id, componente_padre_id = EXCLUDED.componente_padre_id,
			campo_fuente_id = EXCLUDED.campo_fuente_id, funcion_campo = EXCLUDED.funcion_campo,
			columnas_ancho = EXCLUDED.columnas_ancho,
			orden = EXCLUDED.orden, requerido = EXCLUDED.requerido,
			configuracion = EXCLUDED.configuracion, activo = EXCLUDED.activo`,
		req.ElementoID, req.TabID, req.Tipo, req.Etiqueta, catalogoID, padreID, fuenteID, req.FuncionCampo, columnas,
		int(req.Orden), req.Requerido, configuracion, req.Activo)
	if err != nil {
		log.Printf("cotizador elementos: error guardando %s: %v", req.ElementoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar el elemento."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Elemento guardado.", "elemento_id": req.ElementoID})
}

// operandosDesdeConfiguracion lee configuracion["operandos"] (un array de
// elemento_id) tolerando lo que json.Unmarshal produce: []any de strings.
func operandosDesdeConfiguracion(configuracion map[string]any) []string {
	raw, _ := configuracion["operandos"].([]any)
	operandos := make([]string, 0, len(raw))
	for _, v := range raw {
		id := strings.ToUpper(strings.TrimSpace(fmt.Sprint(v)))
		if id != "" {
			operandos = append(operandos, id)
		}
	}
	return operandos
}

// normalizarNombresSugeridos acepta el texto separado por comas que usa el
// Diseñador y tolera también un array JSON para clientes de API. Los vacíos
// se descartan; el runtime completa los nombres faltantes como "Opción N".
func normalizarNombresSugeridos(valor any) ([]string, error) {
	partes := make([]string, 0)
	switch v := valor.(type) {
	case nil:
		return partes, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return partes, nil
		}
		partes = strings.Split(v, ",")
	case []any:
		for _, item := range v {
			partes = append(partes, fmt.Sprint(item))
		}
	default:
		return nil, errors.New("nombres_sugeridos debe ser una lista separada por comas")
	}
	resultado := make([]string, 0, len(partes))
	for _, parte := range partes {
		nombre := strings.TrimSpace(parte)
		if nombre != "" {
			resultado = append(resultado, nombre)
		}
	}
	return resultado, nil
}

// tieneReferenciaCircular recorre, en profundidad, la cadena de operandos de
// un Campo Calculado (siguiendo solo los operandos que a su vez son otro
// CAMPO_CALCULADO ya guardado) buscando si en algún punto se vuelve a
// encontrar elementoID — el elemento que se está guardando ahora mismo. Los
// operandos ya guardados no pueden tener ciclos entre sí (se validaron al
// guardarse), así que "visitados" solo hace falta para no recorrer el mismo
// nodo dos veces en el mismo árbol, no para cortar un ciclo preexistente.
func (h *CotizadorTabsHandler) tieneReferenciaCircular(ctx context.Context, elementoID string, operandos []string, visitados map[string]bool) (bool, error) {
	for _, opID := range operandos {
		if opID == elementoID {
			return true, nil
		}
		if visitados[opID] {
			continue
		}
		visitados[opID] = true
		var tipoOp string
		var configOp map[string]any
		err := h.DB.QueryRow(ctx, `SELECT tipo, configuracion FROM elementos_tab_cotizador WHERE elemento_id=$1`, opID).Scan(&tipoOp, &configOp)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		if tipoOp != "CAMPO_CALCULADO" {
			continue
		}
		circular, err := h.tieneReferenciaCircular(ctx, elementoID, operandosDesdeConfiguracion(configOp), visitados)
		if err != nil || circular {
			return circular, err
		}
	}
	return false, nil
}

// enteroDesdeConfiguracion lee una clave numérica de la configuración JSON de
// un elemento. json.Unmarshal decodifica números como float64; el frontend
// también puede mandar un string ("2"), así que se aceptan ambos formatos.
func enteroDesdeConfiguracion(configuracion map[string]any, clave string) (int, bool) {
	valor, existe := configuracion[clave]
	if !existe {
		return 0, false
	}
	switch v := valor.(type) {
	case float64:
		return int(v), true
	case string:
		numero, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return numero, true
	default:
		return 0, false
	}
}

// boolDesdeConfiguracion lee una clave booleana de la configuración JSON de
// un elemento, con el mismo criterio tolerante que enteroDesdeConfiguracion:
// json.Unmarshal decodifica un booleano como bool, pero el frontend también
// puede mandarlo como string. Si la clave no viene, se usa porDefecto —
// mismo patrón que "editable !== false" ya usa el resto del Diseñador.
func boolDesdeConfiguracion(configuracion map[string]any, clave string, porDefecto bool) bool {
	valor, existe := configuracion[clave]
	if !existe || valor == nil {
		return porDefecto
	}
	switch v := valor.(type) {
	case bool:
		return v
	case string:
		s := strings.ToUpper(strings.TrimSpace(v))
		return s == "TRUE" || s == "SI" || s == "1"
	default:
		return porDefecto
	}
}

// EliminarTab inactiva la sección y todos sus elementos en una transacción.
func (h *CotizadorTabsHandler) EliminarTab(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la sección a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la eliminación."})
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE tabs_cotizador SET activo=false WHERE tab_id=$1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La sección indicada no existe."})
		return
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE elementos_tab_cotizador SET activo=false WHERE tab_id=$1`, id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("cotizador tabs: error eliminando sección %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar la sección."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Sección y elementos eliminados.", "tab_id": id})
}

// EliminarElemento conserva la fila y la excluye del diseñador/compilador.
func (h *CotizadorTabsHandler) EliminarElemento(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el elemento a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `UPDATE elementos_tab_cotizador SET activo=false WHERE elemento_id=$1`, id)
	if err != nil {
		log.Printf("cotizador elementos: error eliminando %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar el elemento."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "El elemento indicado no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Elemento eliminado.", "elemento_id": id})
}

func normalizarConfiguracionElemento(principal, alias json.RawMessage) (map[string]any, error) {
	raw := principal
	if len(raw) == 0 || string(raw) == "null" {
		raw = alias
	}
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return map[string]any{}, nil
	}
	if len(raw) > 0 && raw[0] == '"' {
		var texto string
		if err := json.Unmarshal(raw, &texto); err != nil {
			return nil, fmt.Errorf("configuracion no es JSON válido")
		}
		raw = []byte(texto)
	}
	var configuracion map[string]any
	if err := json.Unmarshal(raw, &configuracion); err != nil {
		return nil, fmt.Errorf("configuracion no es un objeto JSON válido")
	}
	if configuracion == nil {
		configuracion = map[string]any{}
	}
	return configuracion, nil
}
