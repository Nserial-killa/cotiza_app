package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlantillaVinculacionesHandler administra las fuentes y vinculaciones del
// paso 3 para cada cotizador asociado a una plantilla.
type PlantillaVinculacionesHandler struct {
	DB *pgxpool.Pool
}

type fuentePlantilla struct {
	FuenteID     string  `json:"fuente_id"`
	FuenteTipo   string  `json:"fuente_tipo"`
	Nombre       string  `json:"nombre"`
	TipoElemento *string `json:"tipo_elemento,omitempty"`
	TabID        *string `json:"tab_id,omitempty"`
	TabNombre    *string `json:"tab_nombre,omitempty"`
}

type guardarVinculacionRequest struct {
	CalculadoraID string `json:"calculadora_id"`
	FuenteTipo    string `json:"fuente_tipo"`
	FuenteID      string `json:"fuente_id"`
}

var fuentesCotizacionBase = []fuentePlantilla{
	{FuenteID: "cliente", FuenteTipo: "COTIZACION_BASE", Nombre: "Cliente"},
	{FuenteID: "empresa", FuenteTipo: "COTIZACION_BASE", Nombre: "Empresa"},
	{FuenteID: "codigo_oferta", FuenteTipo: "COTIZACION_BASE", Nombre: "Código de oferta"},
	{FuenteID: "tipo_propuesta", FuenteTipo: "COTIZACION_BASE", Nombre: "Tipo de propuesta"},
	{FuenteID: "total_precio", FuenteTipo: "COTIZACION_BASE", Nombre: "Precio total"},
	{FuenteID: "moneda", FuenteTipo: "COTIZACION_BASE", Nombre: "Moneda"},
	{FuenteID: "fecha_creacion", FuenteTipo: "COTIZACION_BASE", Nombre: "Fecha de creación"},
	{FuenteID: "vendedor", FuenteTipo: "COTIZACION_BASE", Nombre: "Vendedor"},
	{FuenteID: "estado", FuenteTipo: "COTIZACION_BASE", Nombre: "Estado"},
}

var idsCotizacionBase = func() map[string]bool {
	resultado := make(map[string]bool, len(fuentesCotizacionBase))
	for _, fuente := range fuentesCotizacionBase {
		resultado[fuente.FuenteID] = true
	}
	return resultado
}()

// Fuentes devuelve campos activos del Diseñador y los datos base comunes.
// Las "salidas especiales" (resultados calculados por fórmula) no se ofrecen
// todavía porque el sistema no persiste ningún dato de ese tipo; cuando exista
// una fuente real para ellas se podrá agregar aquí sin inventar valores.
func (h *PlantillaVinculacionesHandler) Fuentes(w http.ResponseWriter, r *http.Request) {
	plantillaID := strings.TrimSpace(chi.URLParam(r, "id"))
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if plantillaID == "" || calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar plantilla y calculadora_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe, asociada bool
	err := h.DB.QueryRow(ctx, `
		SELECT true, EXISTS(
			SELECT 1 FROM plantilla_calculadoras pc
			WHERE pc.plantilla_id=p.plantilla_id AND pc.calculadora_id=$2)
		FROM plantillas p WHERE p.plantilla_id::text=$1`, plantillaID, calculadoraID).Scan(&existe, &asociada)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		responderErrorVinculacion(w, "validar la plantilla", err)
		return
	}
	if !asociada {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador no está asociado a esta plantilla."})
		return
	}

	rows, err := h.DB.Query(ctx, `
		SELECT e.elemento_id, 'CAMPO', COALESCE(NULLIF(e.etiqueta,''),e.elemento_id),
		       e.tipo, t.tab_id, t.nombre
		  FROM tabs_cotizador t
		  JOIN elementos_tab_cotizador e ON e.tab_id=t.tab_id
		 WHERE t.calculadora_id=$1 AND t.activo=true AND e.activo=true
		   AND e.tipo IN ('CAMPO','CAMPO_CATALOGO')
		 ORDER BY t.orden, e.orden, e.elemento_id`, calculadoraID)
	if err != nil {
		responderErrorVinculacion(w, "consultar las fuentes", err)
		return
	}
	defer rows.Close()
	campos := make([]fuentePlantilla, 0)
	for rows.Next() {
		var fuente fuentePlantilla
		if err := rows.Scan(&fuente.FuenteID, &fuente.FuenteTipo, &fuente.Nombre,
			&fuente.TipoElemento, &fuente.TabID, &fuente.TabNombre); err != nil {
			responderErrorVinculacion(w, "leer las fuentes", err)
			return
		}
		campos = append(campos, fuente)
	}
	if err := rows.Err(); err != nil {
		responderErrorVinculacion(w, "leer las fuentes", err)
		return
	}
	base := append([]fuentePlantilla(nil), fuentesCotizacionBase...)
	todas := make([]fuentePlantilla, 0, len(campos)+len(base))
	todas = append(todas, campos...)
	todas = append(todas, base...)
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "calculadora_id": calculadoraID, "fuentes": todas,
		"campos": campos, "datos_cotizacion": base,
	})
}

func (h *PlantillaVinculacionesHandler) Guardar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	var req guardarVinculacionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.FuenteTipo = strings.ToUpper(strings.TrimSpace(req.FuenteTipo))
	req.FuenteID = strings.TrimSpace(req.FuenteID)
	if bloqueID == "" || req.CalculadoraID == "" || req.FuenteID == "" ||
		(req.FuenteTipo != "CAMPO" && req.FuenteTipo != "COTIZACION_BASE") {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque, calculadora_id, fuente_id y una fuente_tipo válida."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var plantillaID string
	err := h.DB.QueryRow(ctx, `
		SELECT ps.plantilla_id::text
		  FROM plantilla_bloques pb
		  JOIN plantilla_secciones ps ON ps.seccion_id=pb.seccion_id
		 WHERE pb.bloque_id::text=$1`, bloqueID).Scan(&plantillaID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	if err != nil {
		responderErrorVinculacion(w, "validar el bloque", err)
		return
	}
	var asociada bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plantilla_calculadoras
		 WHERE plantilla_id::text=$1 AND calculadora_id=$2)`, plantillaID, req.CalculadoraID).Scan(&asociada); err != nil {
		responderErrorVinculacion(w, "validar el cotizador", err)
		return
	}
	if !asociada {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador no está asociado a la plantilla del bloque."})
		return
	}
	valida, err := h.fuenteValida(ctx, req)
	if err != nil {
		responderErrorVinculacion(w, "validar la fuente", err)
		return
	}
	if !valida {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La fuente indicada no existe o no está activa para ese cotizador."})
		return
	}
	var vinculacionID string
	err = h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_vinculaciones (bloque_id,calculadora_id,fuente_tipo,fuente_id)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (bloque_id,calculadora_id) DO UPDATE SET
			fuente_tipo=EXCLUDED.fuente_tipo, fuente_id=EXCLUDED.fuente_id
		RETURNING vinculacion_id::text`, bloqueID, req.CalculadoraID,
		req.FuenteTipo, req.FuenteID).Scan(&vinculacionID)
	if err != nil {
		responderErrorVinculacion(w, "guardar la vinculación", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "vinculacion_id": vinculacionID, "bloque_id": bloqueID,
		"calculadora_id": req.CalculadoraID, "mensaje": "Vinculación guardada.",
	})
}

func (h *PlantillaVinculacionesHandler) fuenteValida(ctx context.Context, req guardarVinculacionRequest) (bool, error) {
	if req.FuenteTipo == "COTIZACION_BASE" {
		return idsCotizacionBase[req.FuenteID], nil
	}
	var existe bool
	err := h.DB.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM elementos_tab_cotizador e
			JOIN tabs_cotizador t ON t.tab_id=e.tab_id
			WHERE e.elemento_id=$1 AND t.calculadora_id=$2
			  AND t.activo=true AND e.activo=true
			  AND e.tipo IN ('CAMPO','CAMPO_CATALOGO'))`,
		req.FuenteID, req.CalculadoraID).Scan(&existe)
	return existe, err
}

func (h *PlantillaVinculacionesHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if bloqueID == "" || calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque y calculadora_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `
		DELETE FROM plantilla_vinculaciones
		 WHERE bloque_id::text=$1 AND calculadora_id=$2`, bloqueID, calculadoraID)
	if err != nil {
		responderErrorVinculacion(w, "eliminar la vinculación", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Vinculación no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "bloque_id": bloqueID, "calculadora_id": calculadoraID, "mensaje": "Vinculación eliminada."})
}

func responderErrorVinculacion(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantilla vinculaciones: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
