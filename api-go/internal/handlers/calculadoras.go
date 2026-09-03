package handlers

// CalculadorasHandler expone una lectura simple del catálogo de
// calculadoras (cotizadores) — hoy solo alimenta selectores (filtro de
// Cotizaciones, diseñador de Tabs/Elementos). No hay alta/edición acá
// todavía; esa pantalla no existe en ningún sprint hecho hasta ahora.

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CalculadorasHandler struct {
	DB *pgxpool.Pool
}

type calculadoraSimple struct {
	CalculadoraID string  `json:"calculadora_id"`
	NombreCalc    string  `json:"nombre_calculadora"`
	LineaNegocio  *string `json:"linea_negocio,omitempty"`
	ServicioBase  *string `json:"servicio_base,omitempty"`
	Descripcion   *string `json:"descripcion,omitempty"`
}

// Listar devuelve las calculadoras disponibles, ordenadas por nombre.
// El compilador cambia una calculadora de Activo a Publicado; ambos
// estados deben seguir disponibles para crear y llenar cotizaciones.
func (h *CalculadorasHandler) Listar(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT calculadora_id, nombre_calculadora, linea_negocio, servicio_base, descripcion
		  FROM calculadoras
		 WHERE estado IN ('Activo', 'Publicado')
		 ORDER BY nombre_calculadora`)
	if err != nil {
		log.Printf("calculadoras: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los cotizadores."})
		return
	}
	defer rows.Close()

	calculadoras := make([]calculadoraSimple, 0)
	for rows.Next() {
		var item calculadoraSimple
		if err := rows.Scan(&item.CalculadoraID, &item.NombreCalc, &item.LineaNegocio, &item.ServicioBase, &item.Descripcion); err != nil {
			log.Printf("calculadoras: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los cotizadores."})
			return
		}
		calculadoras = append(calculadoras, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("calculadoras: error leyendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los cotizadores."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "calculadoras": calculadoras})
}
