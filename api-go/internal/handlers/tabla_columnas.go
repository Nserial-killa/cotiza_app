package handlers

// TablaColumnasHandler administra las columnas de un elemento TABLA
// (Ronda 4 del Diseñador, migración 0020). A diferencia de los ítems de
// Lista de Precios (Ronda 3, siempre propios del elemento), una columna
// puede REUSAR un campo existente de la misma sección (origen=
// CAMPO_EXISTENTE) o ser propia de esta tabla (origen=PROPIA) — nunca
// ambas cosas, el CHECK chk_tabla_columnas_forma de la migración ya lo
// obliga, pero se valida también acá para dar un error legible.
//
// No hay costo_interno/margen_porcentaje en una columna de Tabla hoy (son
// Texto/Número/Moneda/Porcentaje simples) — si en el futuro una columna
// necesitara mostrar datos financieros internos, aplicar el mismo
// ocultamiento por rol que ya existe para Lista de Precios (ver
// sesionPuedeVerPrice en cotizaciones.go y su uso en
// cotizador_tabs.go/lista_precios_items.go).

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TablaColumnasHandler struct {
	DB *pgxpool.Pool
}

var tiposDatoColumnaValidos = map[string]bool{
	"TEXTO": true, "NUMERO": true, "MONEDA": true, "PORCENTAJE": true,
}

// tiposOperandoComoColumnaValidos son los tipos de elemento que una
// columna CAMPO_EXISTENTE puede referenciar — no otra TABLA ni una Lista
// de Precios ni un Contenedor: evita anidar tablas dentro de tablas por
// ahora (mismo criterio que "TABLA" no acepta CAMPO_CATALOGO como operando
// de Campo Calculado en su momento, ver tiposOperandoCalculadoValidos).
var tiposOperandoComoColumnaValidos = map[string]bool{
	"CAMPO": true, "CAMPO_CATALOGO": true, "CAMPO_CALCULADO": true,
}

type tablaColumna struct {
	ColumnaID        string  `json:"columna_id"`
	ElementoID       string  `json:"elemento_id"`
	Origen           string  `json:"origen"`
	CampoExistenteID *string `json:"campo_existente_id"`
	TipoDato         *string `json:"tipo_dato"`
	Etiqueta         *string `json:"etiqueta"`
	Orden            int     `json:"orden"`
}

type crearColumnaTablaRequest struct {
	Origen           string         `json:"origen"`
	CampoExistenteID string         `json:"campo_existente_id"`
	TipoDato         string         `json:"tipo_dato"`
	Etiqueta         string         `json:"etiqueta"`
	Orden            enteroFlexible `json:"orden"`
}

// Crear agrega una columna a un elemento TABLA ya existente.
// POST /api/cotizador/elementos/{elemento_id}/columnas
func (h *TablaColumnasHandler) Crear(w http.ResponseWriter, r *http.Request) {
	elementoID := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "elemento_id")))
	if elementoID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el elemento (Tabla)."})
		return
	}
	var req crearColumnaTablaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Origen = strings.ToUpper(strings.TrimSpace(req.Origen))
	if req.Origen != "CAMPO_EXISTENTE" && req.Origen != "PROPIA" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "origen debe ser CAMPO_EXISTENTE o PROPIA."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var tipoElemento, tabID string
	err := h.DB.QueryRow(ctx, `SELECT tipo, tab_id FROM elementos_tab_cotizador WHERE elemento_id=$1`, elementoID).Scan(&tipoElemento, &tabID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El elemento indicado no existe."})
		return
	}
	if err != nil {
		log.Printf("tabla columnas: error validando elemento %s: %v", elementoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el elemento."})
		return
	}
	if tipoElemento != "TABLA" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El elemento indicado no es una Tabla."})
		return
	}

	var campoExistenteID, tipoDato, etiqueta *string
	if req.Origen == "CAMPO_EXISTENTE" {
		campoID := strings.ToUpper(strings.TrimSpace(req.CampoExistenteID))
		if campoID == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar campo_existente_id."})
			return
		}
		var tipoCampo, tabCampo string
		var activoCampo bool
		err := h.DB.QueryRow(ctx, `SELECT tipo, tab_id, activo FROM elementos_tab_cotizador WHERE elemento_id=$1`, campoID).Scan(&tipoCampo, &tabCampo, &activoCampo)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El campo indicado no existe."})
			return
		}
		if err != nil {
			log.Printf("tabla columnas: error validando campo %s: %v", campoID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el campo."})
			return
		}
		if !activoCampo {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El campo indicado está inactivo."})
			return
		}
		if tabCampo != tabID {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El campo debe pertenecer a la misma sección (tab_id) que la tabla."})
			return
		}
		if !tiposOperandoComoColumnaValidos[tipoCampo] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La columna solo puede integrar un Campo, Campo Catálogo o Campo Calculado."})
			return
		}
		if req.TipoDato != "" || req.Etiqueta != "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Una columna CAMPO_EXISTENTE no debe traer tipo_dato ni etiqueta propios."})
			return
		}
		campoExistenteID = &campoID
	} else {
		if req.CampoExistenteID != "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Una columna PROPIA no debe traer campo_existente_id."})
			return
		}
		tipo := strings.ToUpper(strings.TrimSpace(req.TipoDato))
		if !tiposDatoColumnaValidos[tipo] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_dato debe ser TEXTO, NUMERO, MONEDA o PORCENTAJE."})
			return
		}
		etiquetaTexto := strings.TrimSpace(req.Etiqueta)
		if etiquetaTexto == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Una columna propia necesita etiqueta."})
			return
		}
		tipoDato = &tipo
		etiqueta = &etiquetaTexto
	}

	var columnaID string
	err = h.DB.QueryRow(ctx, `
		INSERT INTO tabla_columnas (elemento_id, origen, campo_existente_id, tipo_dato, etiqueta, orden)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING columna_id::text`,
		elementoID, req.Origen, campoExistenteID, tipoDato, etiqueta, int(req.Orden),
	).Scan(&columnaID)
	if err != nil {
		log.Printf("tabla columnas: error creando columna de %s: %v", elementoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la columna."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Columna creada.", "columna_id": columnaID})
}

type editarColumnaTablaRequest struct {
	Etiqueta *string         `json:"etiqueta"`
	Orden    *enteroFlexible `json:"orden"`
}

// Editar reordena y/o cambia la etiqueta de una columna. Solo una columna
// PROPIA tiene etiqueta editable directamente — una CAMPO_EXISTENTE muestra
// la etiqueta del campo que integra, así que cambiarla implica editar ese
// campo, no la columna.
// PATCH /api/cotizador/columnas/{columna_id}
func (h *TablaColumnasHandler) Editar(w http.ResponseWriter, r *http.Request) {
	columnaID := strings.TrimSpace(chi.URLParam(r, "columna_id"))
	if columnaID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la columna a editar."})
		return
	}
	var req editarColumnaTablaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Etiqueta == nil && req.Orden == nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos un campo para editar."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if req.Etiqueta != nil {
		var origen string
		err := h.DB.QueryRow(ctx, `SELECT origen FROM tabla_columnas WHERE columna_id::text=$1`, columnaID).Scan(&origen)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La columna indicada no existe."})
			return
		}
		if err != nil {
			log.Printf("tabla columnas: error validando columna %s: %v", columnaID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la columna."})
			return
		}
		if origen != "PROPIA" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La etiqueta de una columna CAMPO_EXISTENTE no se edita acá — edite el campo que integra."})
			return
		}
		if strings.TrimSpace(*req.Etiqueta) == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La etiqueta no puede quedar vacía."})
			return
		}
	}

	columnas := make([]string, 0, 2)
	valores := make([]any, 0, 2)
	agregar := func(columna string, valor any) {
		valores = append(valores, valor)
		columnas = append(columnas, columna+" = $"+strconv.Itoa(len(valores)))
	}
	if req.Etiqueta != nil {
		agregar("etiqueta", strings.TrimSpace(*req.Etiqueta))
	}
	if req.Orden != nil {
		agregar("orden", int(*req.Orden))
	}

	valores = append(valores, columnaID)
	consulta := "UPDATE tabla_columnas SET " + strings.Join(columnas, ", ") + " WHERE columna_id::text = $" + strconv.Itoa(len(valores))
	tag, err := h.DB.Exec(ctx, consulta, valores...)
	if err != nil {
		log.Printf("tabla columnas: error editando %s: %v", columnaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible editar la columna."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La columna indicada no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Columna actualizada.", "columna_id": columnaID})
}

// Eliminar borra la columna físicamente (a diferencia de un elemento, que
// solo se inactiva) — pero no si alguna cotización ya guardó una fila que
// la usa: eso dejaría datos huérfanos en cotizacion_valores.
// DELETE /api/cotizador/columnas/{columna_id}
func (h *TablaColumnasHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	columnaID := strings.TrimSpace(chi.URLParam(r, "columna_id"))
	if columnaID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la columna a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var elementoID string
	err := h.DB.QueryRow(ctx, `SELECT elemento_id FROM tabla_columnas WHERE columna_id::text=$1`, columnaID).Scan(&elementoID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La columna indicada no existe."})
		return
	}
	if err != nil {
		log.Printf("tabla columnas: error validando columna %s: %v", columnaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la columna."})
		return
	}

	var tieneDatos bool
	err = h.DB.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM cotizacion_valores cv, jsonb_array_elements(COALESCE(cv.valor->'filas', '[]'::jsonb)) AS fila
			WHERE cv.elemento_id = $1 AND fila ? $2
		)`, elementoID, columnaID).Scan(&tieneDatos)
	if err != nil {
		log.Printf("tabla columnas: error validando datos guardados de %s: %v", columnaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar si la columna tiene datos guardados."})
		return
	}
	if tieneDatos {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "No se puede eliminar: alguna cotización ya tiene filas guardadas usando esta columna."})
		return
	}

	tag, err := h.DB.Exec(ctx, `DELETE FROM tabla_columnas WHERE columna_id::text=$1`, columnaID)
	if err != nil {
		log.Printf("tabla columnas: error eliminando %s: %v", columnaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar la columna."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La columna indicada no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Columna eliminada.", "columna_id": columnaID})
}
