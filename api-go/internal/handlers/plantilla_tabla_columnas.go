package handlers

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

// PlantillaTablaColumnasHandler administra las columnas de un bloque
// TABLA_INVERSION (cuarto hueco del documento del jefe, caso ISA Custom):
// Concepto, Implementación, Mensualidad, Recomendada, etc. Una columna es
// CAMPO/COTIZACION_BASE (igual que una vinculación normal) o una de las dos
// fuentes virtuales NOMBRE_ESCENARIO/ES_RECOMENDADA, que no vienen de un
// campo del cotizador sino de cotizacion_opciones — solo tienen sentido
// cuando el bloque usa origen_filas=OPCIONES_PROPUESTA (ver
// plantilla_renderizador.go).
type PlantillaTablaColumnasHandler struct {
	DB *pgxpool.Pool
}

var fuentesTipoColumnaTabla = map[string]bool{
	"CAMPO": true, "COTIZACION_BASE": true, "NOMBRE_ESCENARIO": true, "ES_RECOMENDADA": true,
}
var fuentesVirtualesColumnaTabla = map[string]bool{"NOMBRE_ESCENARIO": true, "ES_RECOMENDADA": true}

// tiposBloqueConColumnas son los tres bloques de tabla de la paleta: los
// tres se renderizan con filasTablaInversionPlantilla (plantilla_
// renderizador.go). TABLA_DATOS es la misma tabla sin suponer que sus
// filas son montos; OPCIONES_PROPUESTA es la tabla de escenarios con
// origen_filas fijo en OPCIONES_PROPUESTA.
var tiposBloqueConColumnas = map[string]bool{"TABLA_INVERSION": true, "TABLA_DATOS": true, "OPCIONES_PROPUESTA": true}

type crearColumnaPlantillaTablaRequest struct {
	CalculadoraID string `json:"calculadora_id"`
	Titulo        string `json:"titulo"`
	FuenteTipo    string `json:"fuente_tipo"`
	FuenteID      string `json:"fuente_id"`
}

type editarColumnaPlantillaTablaRequest struct {
	Titulo     *string `json:"titulo"`
	FuenteTipo *string `json:"fuente_tipo"`
	FuenteID   *string `json:"fuente_id"`
}

// Agregar responde POST /api/plantillas/bloques/{bloque_id}/columnas.
func (h *PlantillaTablaColumnasHandler) Agregar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "bloque", bloqueID) {
		return
	}
	var req crearColumnaPlantillaTablaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.Titulo = strings.TrimSpace(req.Titulo)
	req.FuenteTipo = strings.ToUpper(strings.TrimSpace(req.FuenteTipo))
	req.FuenteID = strings.TrimSpace(req.FuenteID)
	if bloqueID == "" || req.CalculadoraID == "" || req.Titulo == "" || !fuentesTipoColumnaTabla[req.FuenteTipo] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque, calculadora_id, titulo y una fuente_tipo válida."})
		return
	}
	if fuentesVirtualesColumnaTabla[req.FuenteTipo] {
		req.FuenteID = ""
	} else if req.FuenteID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "fuente_id es obligatorio para CAMPO o COTIZACION_BASE."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var plantillaID, tipoBloque string
	err := h.DB.QueryRow(ctx, `
		SELECT ps.plantilla_id::text, pb.tipo_bloque
		  FROM plantilla_bloques pb
		  JOIN plantilla_secciones ps ON ps.seccion_id=pb.seccion_id
		 WHERE pb.bloque_id::text=$1`, bloqueID).Scan(&plantillaID, &tipoBloque)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	if err != nil {
		responderErrorColumna(w, "validar el bloque", err)
		return
	}
	if !tiposBloqueConColumnas[tipoBloque] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Solo un bloque de tabla (Tabla de inversión, Tabla de datos u Opciones de propuesta) puede tener columnas."})
		return
	}
	var asociada bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plantilla_calculadoras
		 WHERE plantilla_id::text=$1 AND calculadora_id=$2)`, plantillaID, req.CalculadoraID).Scan(&asociada); err != nil {
		responderErrorColumna(w, "validar el cotizador", err)
		return
	}
	if !asociada {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador no está asociado a la plantilla del bloque."})
		return
	}
	if req.FuenteTipo == "CAMPO" || req.FuenteTipo == "COTIZACION_BASE" {
		valida, err := fuenteCondicionValida(ctx, h.DB, req.FuenteTipo, req.FuenteID, req.CalculadoraID)
		if err != nil {
			responderErrorColumna(w, "validar la fuente", err)
			return
		}
		if !valida {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La fuente indicada no existe o no está activa para ese cotizador."})
			return
		}
	}

	var columnaID string
	var orden int
	err = h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_tabla_columnas (bloque_id,calculadora_id,titulo,fuente_tipo,fuente_id,orden)
		SELECT $1::uuid,$2,$3,$4,NULLIF($5,''),COALESCE(MAX(orden),-1)+1
		  FROM plantilla_tabla_columnas WHERE bloque_id=$1::uuid AND calculadora_id=$2
		RETURNING columna_id::text, orden`, bloqueID, req.CalculadoraID, req.Titulo,
		req.FuenteTipo, req.FuenteID).Scan(&columnaID, &orden)
	if err != nil {
		responderErrorColumna(w, "crear la columna", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "columna_id": columnaID, "orden": orden, "mensaje": "Columna creada."})
}

// Editar responde PATCH /api/plantillas/columnas/{columna_id}.
func (h *PlantillaTablaColumnasHandler) Editar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "columna_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "columna", id) {
		return
	}
	var req editarColumnaPlantillaTablaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var calculadoraID, fuenteTipoActual string
	var fuenteIDActual *string
	if err := h.DB.QueryRow(ctx, `
		SELECT calculadora_id, fuente_tipo, fuente_id FROM plantilla_tabla_columnas WHERE columna_id::text=$1`,
		id).Scan(&calculadoraID, &fuenteTipoActual, &fuenteIDActual); errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Columna no encontrada."})
		return
	} else if err != nil {
		responderErrorColumna(w, "validar la columna", err)
		return
	}

	fuenteTipo := fuenteTipoActual
	fuenteID := ""
	if fuenteIDActual != nil {
		fuenteID = *fuenteIDActual
	}
	cambioFuente := false
	if req.FuenteTipo != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.FuenteTipo))
		if !fuentesTipoColumnaTabla[v] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "fuente_tipo no es válida."})
			return
		}
		fuenteTipo = v
		cambioFuente = true
	}
	if req.FuenteID != nil {
		fuenteID = strings.TrimSpace(*req.FuenteID)
		cambioFuente = true
	}
	if fuentesVirtualesColumnaTabla[fuenteTipo] {
		fuenteID = ""
	} else if cambioFuente && fuenteID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "fuente_id es obligatorio para CAMPO o COTIZACION_BASE."})
		return
	}
	if cambioFuente && (fuenteTipo == "CAMPO" || fuenteTipo == "COTIZACION_BASE") {
		valida, err := fuenteCondicionValida(ctx, h.DB, fuenteTipo, fuenteID, calculadoraID)
		if err != nil {
			responderErrorColumna(w, "validar la fuente", err)
			return
		}
		if !valida {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La fuente indicada no existe o no está activa para ese cotizador."})
			return
		}
	}

	columnas := make([]string, 0, 3)
	valores := make([]any, 0, 4)
	agregar := func(columna string, valor any) {
		valores = append(valores, valor)
		columnas = append(columnas, columna+"=$"+strconv.Itoa(len(valores)))
	}
	if req.Titulo != nil {
		v := strings.TrimSpace(*req.Titulo)
		if v == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "titulo no puede quedar vacío."})
			return
		}
		agregar("titulo", v)
	}
	if cambioFuente {
		agregar("fuente_tipo", fuenteTipo)
		var fuenteIDColumna any
		if fuenteID != "" {
			fuenteIDColumna = fuenteID
		}
		agregar("fuente_id", fuenteIDColumna)
	}
	if len(columnas) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos una propiedad para editar."})
		return
	}
	valores = append(valores, id)
	tag, err := h.DB.Exec(ctx, "UPDATE plantilla_tabla_columnas SET "+strings.Join(columnas, ",")+
		" WHERE columna_id::text=$"+strconv.Itoa(len(valores)), valores...)
	if err != nil {
		responderErrorColumna(w, "editar la columna", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Columna no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "columna_id": id, "mensaje": "Columna actualizada."})
}

// Eliminar responde DELETE /api/plantillas/columnas/{columna_id}.
func (h *PlantillaTablaColumnasHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "columna_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "columna", id) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM plantilla_tabla_columnas WHERE columna_id::text=$1`, id)
	if err != nil {
		responderErrorColumna(w, "eliminar la columna", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Columna no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "columna_id": id, "mensaje": "Columna eliminada."})
}

// Ordenar responde POST /api/plantillas/bloques/{bloque_id}/columnas/orden.
// A diferencia de OrdenarBloques/OrdenarSecciones, el "contenedor" no es solo
// el bloque: es (bloque_id, calculadora_id), porque cada cotizador asociado
// tiene su propio juego de columnas.
func (h *PlantillaTablaColumnasHandler) Ordenar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "bloque", bloqueID) {
		return
	}
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id."})
		return
	}
	ids, err := decodificarOrden(r, "columna_ids")
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// consultarIDs solo toma un parámetro posicional; acá el "contenedor" es
	// el par (bloque_id, calculadora_id), así que se usa una consulta propia
	// en vez de generalizar ese helper para un único uso.
	existentes, err := consultarIDsPorBloqueYCalculadora(ctx, h.DB, bloqueID, calculadoraID)
	if err != nil {
		responderErrorColumna(w, "consultar las columnas", err)
		return
	}
	if err := validarOrdenCompleto(ids, existentes); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := aplicarOrden(ctx, h.DB, "plantilla_tabla_columnas", "columna_id", ids); err != nil {
		responderErrorColumna(w, "reordenar las columnas", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "columna_ids": ids, "mensaje": "Columnas reordenadas."})
}

func consultarIDsPorBloqueYCalculadora(ctx context.Context, db *pgxpool.Pool, bloqueID, calculadoraID string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT columna_id::text FROM plantilla_tabla_columnas WHERE bloque_id::text=$1 AND calculadora_id=$2`, bloqueID, calculadoraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var actual string
		if err := rows.Scan(&actual); err != nil {
			return nil, err
		}
		ids = append(ids, actual)
	}
	return ids, rows.Err()
}

func responderErrorColumna(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantilla tabla columnas: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
