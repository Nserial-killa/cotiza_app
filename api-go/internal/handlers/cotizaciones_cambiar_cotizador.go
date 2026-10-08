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
)

type cambiarCotizadorEntrada struct {
	CalculadoraID string `json:"calculadora_id"`
}

// CambiarCotizador crea una versión vacía para no reinterpretar valores,
// importes ni enlaces anteriores con fórmulas de otra calculadora.
func (h *CotizacionesHandler) CambiarCotizador(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}
	var req cambiarCotizadorEntrada
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	if req.CalculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Seleccione un cotizador publicado."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p, ok := exigirPuedeEditarBorrador(ctx, w, r, h.DB)
	if !ok || !exigirAlcanceCotizacion(ctx, w, h.DB, p, id) {
		return
	}
	if !p.PuedeCrearVersion {
		responderSinPermiso(w, p, permisoCrearVersion)
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible cambiar el cotizador."})
		return
	}
	defer tx.Rollback(ctx)
	var actual int
	var creador, anterior string
	var estado string
	err = tx.QueryRow(ctx, `SELECT c.version_actual, COALESCE(c.creado_por,''),
	       COALESCE(v.calculadora_id,c.calculadora_id), v.estado
	  FROM cotizaciones c JOIN cotizacion_versiones v
	    ON v.cotizacion_id=c.cotizacion_id AND v.numero_version=c.version_actual
	 WHERE c.cotizacion_id=$1 FOR UPDATE OF c,v`, id).Scan(&actual, &creador, &anterior, &estado)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, 404, map[string]any{"ok": false, "error": "Cotización no encontrada."})
		return
	}
	if err != nil {
		log.Printf("cambio de cotizador: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible consultar la cotización."})
		return
	}
	if p.Rol != "Administrador" && p.Rol != "Gerente Comercial" && creador != p.UsuarioID {
		escribirJSON(w, 403, map[string]any{"ok": false, "error": "Solo quien creó la cotización, un Administrador o un Gerente Comercial puede cambiar su cotizador."})
		return
	}
	if !estadoVersionEditable(estado) {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "La versión actual está cerrada. Cree una versión editable antes de cambiar el cotizador."})
		return
	}
	if anterior == req.CalculadoraID {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "La cotización ya usa ese cotizador."})
		return
	}
	var compiladoID, nombreNuevo string
	err = tx.QueryRow(ctx, `SELECT cc.compilado_id::text, c.nombre_calculadora
	  FROM calculadoras c JOIN cotizadores_compilados cc
	    ON cc.calculadora_id=c.calculadora_id AND cc.estado='ACTIVA'
	 WHERE c.calculadora_id=$1 AND c.estado='Publicado'
	   AND (EXISTS (
	     SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(cc.configuracion->'salidas')='array' THEN cc.configuracion->'salidas' ELSE '[]'::jsonb END) s
	      WHERE s->>'clave_salida'='TOTAL_PRECIO' AND COALESCE((s->>'activo')::boolean,false)
	        AND COALESCE(s->>'fuente_id','')<>'')
	     OR (NOT EXISTS (
	       SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(cc.configuracion->'salidas')='array' THEN cc.configuracion->'salidas' ELSE '[]'::jsonb END) s
	        WHERE s->>'clave_salida'='TOTAL_PRECIO')
	       AND jsonb_path_exists(cc.configuracion, '$.** ? (@.funcion_campo == "TOTAL_PRECIO_OFERTA")')))
	 FOR SHARE OF c,cc`, req.CalculadoraID).Scan(&compiladoID, &nombreNuevo)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "El cotizador elegido debe estar publicado y tener TOTAL_PRECIO configurado."})
		return
	}
	if err != nil {
		log.Printf("cambio de cotizador: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible validar el cotizador."})
		return
	}
	nueva := actual + 1
	comentario := "Cotizador cambiado de " + anterior + " a " + req.CalculadoraID + ". La versión nueva comienza sin valores; las anteriores se conservan."
	_, err = tx.Exec(ctx, `INSERT INTO cotizacion_versiones
	 (cotizacion_id,numero_version,nombre_version,resumen_cambios,estado,moneda,calculadora_id,compilado_id_usado)
	 VALUES($1,$2,$3,$4,'Borrador','US$',$5,$6::uuid)`, id, nueva, "Cotizador: "+nombreNuevo, comentario, req.CalculadoraID, compiladoID)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE cotizaciones SET calculadora_id=$2,compilado_id_usado=$3::uuid,
		 version_actual=$4,estado='Borrador' WHERE cotizacion_id=$1`, id, req.CalculadoraID, compiladoID, nueva)
	}
	if err == nil {
		err = insertarHistorial(ctx, tx, id, &nueva, "CAMBIAR_COTIZADOR", &estado, strPtr("Borrador"), comentario, p.UsuarioID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("cambio de cotizador: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible cambiar el cotizador."})
		return
	}
	escribirJSON(w, 200, map[string]any{"ok": true, "cotizacion_id": id, "version": nueva,
		"calculadora_id": req.CalculadoraID, "compilado_id": compiladoID})
}
