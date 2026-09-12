package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlantillaEstructuraHandler administra el paso 2 del wizard: secciones,
// bloques y el orden estable de ambos niveles.
type PlantillaEstructuraHandler struct {
	DB *pgxpool.Pool
}

type crearSeccionRequest struct {
	Nombre        string `json:"nombre"`
	Titulo        string `json:"titulo"`
	MostrarTitulo *bool  `json:"mostrar_titulo"`
	Visibilidad   string `json:"visibilidad"`
	DisenoBloques string `json:"diseno_bloques"`
	MostrarWeb    *bool  `json:"mostrar_web"`
	MostrarPDF    *bool  `json:"mostrar_pdf"`
}

type editarSeccionRequest struct {
	Nombre        *string `json:"nombre"`
	Titulo        *string `json:"titulo"`
	MostrarTitulo *bool   `json:"mostrar_titulo"`
	Visibilidad   *string `json:"visibilidad"`
	DisenoBloques *string `json:"diseno_bloques"`
	MostrarWeb    *bool   `json:"mostrar_web"`
	MostrarPDF    *bool   `json:"mostrar_pdf"`
}

type crearBloqueRequest struct {
	TipoBloque    string `json:"tipo_bloque"`
	NombreInterno string `json:"nombre_interno"`
	Titulo        string `json:"titulo"`
	Contenido     string `json:"contenido"`
	Columna       int    `json:"columna"`
	MostrarWeb    *bool  `json:"mostrar_web"`
	MostrarPDF    *bool  `json:"mostrar_pdf"`
}

type editarBloqueRequest struct {
	TipoBloque    *string `json:"tipo_bloque"`
	NombreInterno *string `json:"nombre_interno"`
	Titulo        *string `json:"titulo"`
	Contenido     *string `json:"contenido"`
	Columna       *int    `json:"columna"`
	MostrarWeb    *bool   `json:"mostrar_web"`
	MostrarPDF    *bool   `json:"mostrar_pdf"`
}

var visibilidadesSeccion = map[string]bool{"SIEMPRE": true, "CONDICIONAL": true}
var disenosBloquesSeccion = map[string]bool{
	"UNA": true, "DOS_50_50": true, "DOS_30_70": true,
	"DOS_70_30": true, "TRES_IGUALES": true,
}

func (h *PlantillaEstructuraHandler) CrearSeccion(w http.ResponseWriter, r *http.Request) {
	plantillaID := strings.TrimSpace(chi.URLParam(r, "id"))
	var req crearSeccionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.Titulo = strings.TrimSpace(req.Titulo)
	req.Visibilidad = strings.ToUpper(strings.TrimSpace(req.Visibilidad))
	req.DisenoBloques = strings.ToUpper(strings.TrimSpace(req.DisenoBloques))
	if req.Titulo == "" {
		req.Titulo = req.Nombre
	}
	if req.Visibilidad == "" {
		req.Visibilidad = "SIEMPRE"
	}
	if req.DisenoBloques == "" {
		req.DisenoBloques = "UNA"
	}
	if plantillaID == "" || req.Nombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la plantilla y el nombre de la sección."})
		return
	}
	if !visibilidadesSeccion[req.Visibilidad] || !disenosBloquesSeccion[req.DisenoBloques] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La visibilidad o el diseño de bloques no es válido."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, plantillaID).Scan(&existe); err != nil {
		responderErrorEstructura(w, "validar la plantilla", err)
		return
	}
	if !existe {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	mostrarTitulo := boolPredeterminado(req.MostrarTitulo, true)
	mostrarWeb := boolPredeterminado(req.MostrarWeb, true)
	mostrarPDF := boolPredeterminado(req.MostrarPDF, true)
	var seccionID string
	var orden int
	err := h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_secciones
			(plantilla_id,nombre,titulo,mostrar_titulo,visibilidad,diseno_bloques,mostrar_web,mostrar_pdf,orden)
		SELECT $1::uuid,$2,NULLIF($3,''),$4,$5,$6,$7,$8,COALESCE(MAX(orden),-1)+1
		  FROM plantilla_secciones WHERE plantilla_id=$1::uuid
		RETURNING seccion_id::text, orden`, plantillaID, req.Nombre, req.Titulo,
		mostrarTitulo, req.Visibilidad, req.DisenoBloques, mostrarWeb, mostrarPDF).Scan(&seccionID, &orden)
	if err != nil {
		responderErrorEstructura(w, "crear la sección", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "seccion_id": seccionID, "orden": orden, "mensaje": "Sección creada."})
}

func (h *PlantillaEstructuraHandler) EditarSeccion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "seccion_id"))
	var req editarSeccionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	columnas := make([]string, 0, 7)
	valores := make([]any, 0, 8)
	agregar := func(columna string, valor any) {
		valores = append(valores, valor)
		columnas = append(columnas, columna+"=$"+strconv.Itoa(len(valores)))
	}
	if req.Nombre != nil {
		v := strings.TrimSpace(*req.Nombre)
		if v == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre de la sección no puede quedar vacío."})
			return
		}
		agregar("nombre", v)
	}
	if req.Titulo != nil {
		agregar("titulo", strings.TrimSpace(*req.Titulo))
	}
	if req.MostrarTitulo != nil {
		agregar("mostrar_titulo", *req.MostrarTitulo)
	}
	if req.Visibilidad != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.Visibilidad))
		if !visibilidadesSeccion[v] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La visibilidad no es válida."})
			return
		}
		agregar("visibilidad", v)
	}
	if req.DisenoBloques != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.DisenoBloques))
		if !disenosBloquesSeccion[v] {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El diseño de bloques no es válido."})
			return
		}
		agregar("diseno_bloques", v)
	}
	if req.MostrarWeb != nil {
		agregar("mostrar_web", *req.MostrarWeb)
	}
	if req.MostrarPDF != nil {
		agregar("mostrar_pdf", *req.MostrarPDF)
	}
	if len(columnas) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos una propiedad para editar."})
		return
	}
	valores = append(valores, id)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, "UPDATE plantilla_secciones SET "+strings.Join(columnas, ",")+
		" WHERE seccion_id::text=$"+strconv.Itoa(len(valores)), valores...)
	if err != nil {
		responderErrorEstructura(w, "editar la sección", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Sección no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "seccion_id": id, "mensaje": "Sección actualizada."})
}

func (h *PlantillaEstructuraHandler) EliminarSeccion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "seccion_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM plantilla_secciones WHERE seccion_id::text=$1`, id)
	if err != nil {
		responderErrorEstructura(w, "eliminar la sección", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Sección no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "seccion_id": id, "mensaje": "Sección eliminada."})
}

func (h *PlantillaEstructuraHandler) OrdenarSecciones(w http.ResponseWriter, r *http.Request) {
	anclaID := strings.TrimSpace(chi.URLParam(r, "seccion_id"))
	ids, err := decodificarOrden(r, "seccion_ids")
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var plantillaID string
	err = h.DB.QueryRow(ctx, `SELECT plantilla_id::text FROM plantilla_secciones WHERE seccion_id::text=$1`, anclaID).Scan(&plantillaID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Sección no encontrada."})
		return
	}
	if err != nil {
		responderErrorEstructura(w, "consultar la sección", err)
		return
	}
	existentes, err := consultarIDs(ctx, h.DB, `SELECT seccion_id::text FROM plantilla_secciones WHERE plantilla_id::text=$1`, plantillaID)
	if err != nil {
		responderErrorEstructura(w, "consultar las secciones", err)
		return
	}
	if err := validarOrdenCompleto(ids, existentes); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := aplicarOrden(ctx, h.DB, "plantilla_secciones", "seccion_id", ids); err != nil {
		responderErrorEstructura(w, "reordenar las secciones", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "seccion_ids": ids, "mensaje": "Secciones reordenadas."})
}

func (h *PlantillaEstructuraHandler) CrearBloque(w http.ResponseWriter, r *http.Request) {
	seccionID := strings.TrimSpace(chi.URLParam(r, "seccion_id"))
	var req crearBloqueRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.TipoBloque = strings.ToUpper(strings.TrimSpace(req.TipoBloque))
	req.NombreInterno = strings.TrimSpace(req.NombreInterno)
	req.Titulo = strings.TrimSpace(req.Titulo)
	req.Contenido = strings.TrimSpace(req.Contenido)
	if seccionID == "" || req.TipoBloque == "" || req.NombreInterno == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar sección, tipo_bloque y nombre_interno."})
		return
	}
	if req.Columna < 0 || req.Columna > 3 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "columna debe estar entre 0 y 3."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantilla_secciones WHERE seccion_id::text=$1)`, seccionID).Scan(&existe); err != nil {
		responderErrorEstructura(w, "validar la sección", err)
		return
	}
	if !existe {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Sección no encontrada."})
		return
	}
	var bloqueID string
	var orden int
	err := h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_bloques
			(seccion_id,tipo_bloque,nombre_interno,titulo,contenido,columna,mostrar_web,mostrar_pdf,orden)
		SELECT $1::uuid,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7,$8,COALESCE(MAX(orden),-1)+1
		  FROM plantilla_bloques WHERE seccion_id=$1::uuid
		RETURNING bloque_id::text, orden`, seccionID, req.TipoBloque, req.NombreInterno,
		req.Titulo, req.Contenido, req.Columna, boolPredeterminado(req.MostrarWeb, true),
		boolPredeterminado(req.MostrarPDF, true)).Scan(&bloqueID, &orden)
	if err != nil {
		responderErrorEstructura(w, "crear el bloque", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "bloque_id": bloqueID, "orden": orden, "mensaje": "Bloque creado."})
}

func (h *PlantillaEstructuraHandler) EditarBloque(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	var req editarBloqueRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	columnas := make([]string, 0, 7)
	valores := make([]any, 0, 8)
	agregar := func(columna string, valor any) {
		valores = append(valores, valor)
		columnas = append(columnas, columna+"=$"+strconv.Itoa(len(valores)))
	}
	if req.TipoBloque != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.TipoBloque))
		if v == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_bloque no puede quedar vacío."})
			return
		}
		agregar("tipo_bloque", v)
	}
	if req.NombreInterno != nil {
		v := strings.TrimSpace(*req.NombreInterno)
		if v == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "nombre_interno no puede quedar vacío."})
			return
		}
		agregar("nombre_interno", v)
	}
	if req.Titulo != nil {
		agregar("titulo", strings.TrimSpace(*req.Titulo))
	}
	if req.Contenido != nil {
		agregar("contenido", strings.TrimSpace(*req.Contenido))
	}
	if req.Columna != nil {
		if *req.Columna < 0 || *req.Columna > 3 {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "columna debe estar entre 0 y 3."})
			return
		}
		agregar("columna", *req.Columna)
	}
	if req.MostrarWeb != nil {
		agregar("mostrar_web", *req.MostrarWeb)
	}
	if req.MostrarPDF != nil {
		agregar("mostrar_pdf", *req.MostrarPDF)
	}
	if len(columnas) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos una propiedad para editar."})
		return
	}
	valores = append(valores, id)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, "UPDATE plantilla_bloques SET "+strings.Join(columnas, ",")+
		" WHERE bloque_id::text=$"+strconv.Itoa(len(valores)), valores...)
	if err != nil {
		responderErrorEstructura(w, "editar el bloque", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "bloque_id": id, "mensaje": "Bloque actualizado."})
}

func (h *PlantillaEstructuraHandler) EliminarBloque(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM plantilla_bloques WHERE bloque_id::text=$1`, id)
	if err != nil {
		responderErrorEstructura(w, "eliminar el bloque", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "bloque_id": id, "mensaje": "Bloque eliminado."})
}

func (h *PlantillaEstructuraHandler) OrdenarBloques(w http.ResponseWriter, r *http.Request) {
	anclaID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	ids, err := decodificarOrden(r, "bloque_ids")
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var seccionID string
	err = h.DB.QueryRow(ctx, `SELECT seccion_id::text FROM plantilla_bloques WHERE bloque_id::text=$1`, anclaID).Scan(&seccionID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	if err != nil {
		responderErrorEstructura(w, "consultar el bloque", err)
		return
	}
	existentes, err := consultarIDs(ctx, h.DB, `SELECT bloque_id::text FROM plantilla_bloques WHERE seccion_id::text=$1`, seccionID)
	if err != nil {
		responderErrorEstructura(w, "consultar los bloques", err)
		return
	}
	if err := validarOrdenCompleto(ids, existentes); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := aplicarOrden(ctx, h.DB, "plantilla_bloques", "bloque_id", ids); err != nil {
		responderErrorEstructura(w, "reordenar los bloques", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "bloque_ids": ids, "mensaje": "Bloques reordenados."})
}

func decodificarOrden(r *http.Request, campo string) ([]string, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, errors.New("no fue posible leer el orden")
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		var objeto map[string]json.RawMessage
		if err := json.Unmarshal(raw, &objeto); err != nil {
			return nil, errors.New("el cuerpo debe ser una lista de IDs o un objeto con " + campo)
		}
		valor, existe := objeto[campo]
		if !existe || json.Unmarshal(valor, &ids) != nil {
			return nil, errors.New("debe indicar " + campo + " como una lista de IDs")
		}
	}
	for i := range ids {
		ids[i] = strings.TrimSpace(ids[i])
	}
	if len(ids) == 0 {
		return nil, errors.New("el orden debe incluir al menos un ID")
	}
	return ids, nil
}

func consultarIDs(ctx context.Context, db *pgxpool.Pool, consulta string, id string) ([]string, error) {
	rows, err := db.Query(ctx, consulta, id)
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

func validarOrdenCompleto(nuevo, existentes []string) error {
	if len(nuevo) != len(existentes) {
		return errors.New("el nuevo orden debe incluir exactamente todos los elementos del mismo contenedor")
	}
	esperados := make(map[string]bool, len(existentes))
	for _, id := range existentes {
		esperados[id] = true
	}
	vistos := make(map[string]bool, len(nuevo))
	for _, id := range nuevo {
		if id == "" || !esperados[id] || vistos[id] {
			return errors.New("el nuevo orden contiene IDs vacíos, repetidos o de otro contenedor")
		}
		vistos[id] = true
	}
	return nil
}

func aplicarOrden(ctx context.Context, db *pgxpool.Pool, tabla, columnaID string, ids []string) error {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	consulta := fmt.Sprintf("UPDATE %s SET orden=$1 WHERE %s::text=$2", tabla, columnaID)
	for orden, id := range ids {
		if _, err := tx.Exec(ctx, consulta, orden, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func responderErrorEstructura(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantilla estructura: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
