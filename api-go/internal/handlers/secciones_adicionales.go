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

// SeccionesAdicionalesHandler administra vínculos hacia secciones
// reutilizables. El contenido siempre permanece en el cotizador dueño: acá
// solo se crean o eliminan referencias.
type SeccionesAdicionalesHandler struct {
	DB *pgxpool.Pool
}

type seccionReutilizable struct {
	TabID                string  `json:"tab_id"`
	Nombre               string  `json:"nombre"`
	Descripcion          *string `json:"descripcion"`
	CalculadoraOrigenID  string  `json:"calculadora_origen_id"`
	CalculadoraOrigen    string  `json:"calculadora_origen"`
	Orden                int     `json:"orden"`
	Asociada             bool    `json:"asociada"`
	ElementoAsociacionID *string `json:"elemento_asociacion_id,omitempty"`
}

type asociarSeccionesRequest struct {
	TabIDs []string `json:"tab_ids"`
}

func (h *SeccionesAdicionalesHandler) ListarReutilizables(w http.ResponseWriter, r *http.Request) {
	calculadoraID := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("calculadora_id")))
	if calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rows, err := h.DB.Query(ctx, `
		SELECT t.tab_id, t.nombre, t.descripcion, t.calculadora_id,
		       c.nombre_calculadora, t.orden,
		       a.asociacion_id IS NOT NULL AS asociada, a.elemento_id
		FROM tabs_cotizador t
		JOIN calculadoras c ON c.calculadora_id=t.calculadora_id
		LEFT JOIN tabs_cotizador_asociaciones a
		       ON a.tab_id=t.tab_id AND a.calculadora_id=$1
		WHERE t.alcance='REUTILIZABLE' AND t.activo=true
		  AND t.calculadora_id<>$1
		ORDER BY c.nombre_calculadora, t.orden, t.nombre, t.tab_id`, calculadoraID)
	if err != nil {
		log.Printf("secciones adicionales: error listando para %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las secciones reutilizables."})
		return
	}
	defer rows.Close()

	secciones := make([]seccionReutilizable, 0)
	for rows.Next() {
		var seccion seccionReutilizable
		if err := rows.Scan(&seccion.TabID, &seccion.Nombre, &seccion.Descripcion,
			&seccion.CalculadoraOrigenID, &seccion.CalculadoraOrigen, &seccion.Orden,
			&seccion.Asociada, &seccion.ElementoAsociacionID); err != nil {
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer las secciones reutilizables."})
			return
		}
		secciones = append(secciones, seccion)
	}
	if err := rows.Err(); err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible leer las secciones reutilizables."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "secciones": secciones, "data": secciones})
}

func (h *SeccionesAdicionalesHandler) Asociar(w http.ResponseWriter, r *http.Request) {
	elementoID := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "elemento_id")))
	var req asociarSeccionesRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ids := normalizarIDsUnicos(req.TabIDs)
	if elementoID == "" || len(ids) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar elemento_id y al menos una sección."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la asociación."})
		return
	}
	defer tx.Rollback(ctx)

	var calculadoraID, tipo string
	var elementoActivo bool
	err = tx.QueryRow(ctx, `
		SELECT t.calculadora_id, e.tipo, e.activo
		FROM elementos_tab_cotizador e
		JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		WHERE e.elemento_id=$1
		FOR UPDATE`, elementoID).Scan(&calculadoraID, &tipo, &elementoActivo)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "El elemento indicado no existe."})
		return
	}
	if err != nil {
		log.Printf("secciones adicionales: error validando elemento %s: %v", elementoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el elemento."})
		return
	}
	if tipo != "SECCIONES_ADICIONALES" || !elementoActivo {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El elemento debe ser de tipo SECCIONES_ADICIONALES y estar activo."})
		return
	}

	for orden, tabID := range ids {
		var calculadoraOrigen, alcance string
		var activo bool
		err = tx.QueryRow(ctx, `SELECT calculadora_id, alcance, activo FROM tabs_cotizador WHERE tab_id=$1 FOR SHARE`, tabID).
			Scan(&calculadoraOrigen, &alcance, &activo)
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La sección " + tabID + " no existe."})
			return
		}
		if err != nil {
			log.Printf("secciones adicionales: error validando tab %s: %v", tabID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar las secciones."})
			return
		}
		if calculadoraOrigen == calculadoraID {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Una sección propia no se puede asociar como adicional."})
			return
		}
		if alcance != "REUTILIZABLE" || !activo {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La sección " + tabID + " no está activa o no es reutilizable."})
			return
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO tabs_cotizador_asociaciones (calculadora_id, elemento_id, tab_id, orden)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (calculadora_id, tab_id) DO UPDATE SET
				elemento_id=EXCLUDED.elemento_id, orden=EXCLUDED.orden`,
			calculadoraID, elementoID, tabID, orden)
		if err != nil {
			log.Printf("secciones adicionales: error asociando %s a %s: %v", tabID, calculadoraID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible asociar las secciones."})
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("secciones adicionales: error confirmando asociaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible confirmar las asociaciones."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Secciones asociadas.", "tab_ids": ids})
}

func (h *SeccionesAdicionalesHandler) Desasociar(w http.ResponseWriter, r *http.Request) {
	elementoID := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "elemento_id")))
	tabID := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "tab_id")))
	if elementoID == "" || tabID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar elemento_id y tab_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	resultado, err := h.DB.Exec(ctx, `DELETE FROM tabs_cotizador_asociaciones WHERE elemento_id=$1 AND tab_id=$2`, elementoID, tabID)
	if err != nil {
		log.Printf("secciones adicionales: error desasociando %s/%s: %v", elementoID, tabID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible desasociar la sección."})
		return
	}
	if resultado.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La asociación indicada no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Sección desasociada."})
}

func normalizarIDsUnicos(ids []string) []string {
	resultado := make([]string, 0, len(ids))
	vistos := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.ToUpper(strings.TrimSpace(id))
		if id != "" && !vistos[id] {
			vistos[id] = true
			resultado = append(resultado, id)
		}
	}
	return resultado
}
