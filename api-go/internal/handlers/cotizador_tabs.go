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
	TabID         string  `json:"tab_id"`
	CalculadoraID string  `json:"calculadora_id"`
	Nombre        string  `json:"nombre"`
	NombreTab     string  `json:"nombre_tab"`
	Descripcion   *string `json:"descripcion"`
	Alcance       string  `json:"alcance"`
	Orden         int     `json:"orden"`
	Activo        bool    `json:"activo"`
}

type elementoTabCotizador struct {
	ElementoID        string         `json:"elemento_id"`
	TabID             string         `json:"tab_id"`
	Tipo              string         `json:"tipo"`
	TipoElemento      string         `json:"tipo_elemento"`
	Etiqueta          string         `json:"etiqueta"`
	CatalogoID        *string        `json:"catalogo_id"`
	ComponentePadreID *string        `json:"componente_padre_id"`
	CampoFuenteID     *string        `json:"campo_fuente_id"`
	FuncionCampo      string         `json:"funcion_campo"`
	ColumnasAncho     int            `json:"columnas_ancho"`
	Orden             int            `json:"orden"`
	Requerido         bool           `json:"requerido"`
	Configuracion     map[string]any `json:"configuracion"`
	ConfigJSON        map[string]any `json:"config_json"`
	Activo            bool           `json:"activo"`
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
// agrega CAMPO_CALCULADO. LISTA_PRECIOS, ESCENARIOS y SECCIONES_ADICIONALES
// (ya armados en el HTML) llegan en rondas posteriores — no tocar esto sin
// su propia migración de esquema.
var tiposElementoSimple = map[string]bool{
	"CAMPO": true, "CAMPO_CATALOGO": true, "LEYENDA": true, "TEXTO_INFORMATIVO": true,
	"TITULO": true, "CONTENEDOR": true, "CAJA_VALOR": true, "CAMPO_CALCULADO": true,
}

// tiposConFuncionCampo son los únicos tipos donde "Función del campo" tiene
// sentido: alimentan un valor propio (numérico o de catálogo) que puede
// mapearse a un total de cotizacion_versiones. TITULO/CONTENEDOR/CAJA_VALOR/
// LEYENDA/TEXTO_INFORMATIVO no tienen un valor propio que exportar así.
var tiposConFuncionCampo = map[string]bool{
	"CAMPO": true, "CAMPO_CATALOGO": true, "CAMPO_CALCULADO": true,
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
		SELECT tab_id, calculadora_id, nombre, descripcion, alcance, orden, activo
		FROM tabs_cotizador
		WHERE calculadora_id = $1
		ORDER BY orden, nombre, tab_id`, calculadoraID)
	if err != nil {
		log.Printf("cotizador tabs: error listando %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las secciones."})
		return
	}
	defer rows.Close()
	tabs := make([]tabCotizador, 0)
	for rows.Next() {
		var tab tabCotizador
		if err := rows.Scan(&tab.TabID, &tab.CalculadoraID, &tab.Nombre, &tab.Descripcion, &tab.Alcance, &tab.Orden, &tab.Activo); err != nil {
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer las secciones."})
			return
		}
		tab.NombreTab = tab.Nombre
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
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, err := h.DB.Exec(ctx, `
		INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, descripcion, alcance, orden, activo)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tab_id) DO UPDATE SET
			calculadora_id = EXCLUDED.calculadora_id, nombre = EXCLUDED.nombre,
			descripcion = EXCLUDED.descripcion,
			alcance = EXCLUDED.alcance, orden = EXCLUDED.orden, activo = EXCLUDED.activo`,
		req.TabID, req.CalculadoraID, req.Nombre, req.Descripcion, req.Alcance, int(req.Orden), req.Activo)
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
	for rows.Next() {
		var el elementoTabCotizador
		if err := rows.Scan(&el.ElementoID, &el.TabID, &el.Tipo, &el.Etiqueta, &el.CatalogoID, &el.ComponentePadreID, &el.CampoFuenteID, &el.FuncionCampo, &el.ColumnasAncho, &el.Orden, &el.Requerido, &el.Configuracion, &el.Activo); err != nil {
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer los elementos."})
			return
		}
		el.TipoElemento = el.Tipo
		el.ConfigJSON = el.Configuracion
		elementos = append(elementos, el)
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
	if req.Tipo == "CONTENEDOR" && padreID != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un Contenedor no puede tener componente_padre_id: todavía no hay anidado de contenedores."})
		return
	}
	if req.Tipo == "CONTENEDOR" && fuenteID != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un Contenedor no puede tener campo_fuente_id."})
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
		if tipoFormula != "SIMPLE" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_formula debe ser SIMPLE (todavía no hay fórmula avanzada)."})
			return
		}
		configuracion["tipo_formula"] = tipoFormula

		operacion := strings.ToUpper(strings.TrimSpace(fmt.Sprint(configuracion["operacion"])))
		if !operacionesCalculoValidas[operacion] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "operacion debe ser SUMA, RESTA, MULTIPLICACION, DIVISION o PROMEDIO."})
			return
		}
		configuracion["operacion"] = operacion

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
			err := h.DB.QueryRow(ctx, `SELECT tipo, tab_id, activo, configuracion FROM elementos_tab_cotizador WHERE elemento_id=$1`, opID).Scan(&tipoOp, &tabOp, &activoOp, &configOp)
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
			} else if tipoOp != "CAMPO_CALCULADO" {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operando %s debe ser un Campo numérico o un Campo Calculado.", opID)})
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
		if tipoPadre != "CONTENEDOR" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El componente padre debe ser de tipo CONTENEDOR."})
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
