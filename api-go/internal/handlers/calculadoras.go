package handlers

// CalculadorasHandler expone el catálogo de calculadoras (cotizadores):
// Listar alimenta selectores (filtro de Cotizaciones, diseñador de
// Tabs/Elementos) y Crear da de alta el cascarón mínimo que necesitan
// tabs_cotizador/elementos_tab_cotizador/el compilador antes de poder
// diseñar y publicar un cotizador. Sigue sin existir una pantalla
// propia para esto — Crear lo usa hoy el script de datos de
// demostración (scripts/seed_demo.sh) llamando al API real, igual que
// lo haría cualquier otro cliente.

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CalculadorasHandler struct {
	DB *pgxpool.Pool
}

type calculadoraSimple struct {
	CalculadoraID        string  `json:"calculadora_id"`
	NombreCalc           string  `json:"nombre_calculadora"`
	LineaNegocio         *string `json:"linea_negocio,omitempty"`
	ServicioBase         *string `json:"servicio_base,omitempty"`
	Descripcion          *string `json:"descripcion,omitempty"`
	Estado               string  `json:"estado"`
	CompiladoID          *string `json:"compilado_id,omitempty"`
	VersionConfiguracion *int    `json:"version_configuracion,omitempty"`
	DisponibleCotizacion bool    `json:"disponible_cotizacion"`
}

type crearCalculadoraRequest struct {
	CalculadoraID     string `json:"calculadora_id"`
	NombreCalculadora string `json:"nombre_calculadora"`
	LineaNegocio      string `json:"linea_negocio"`
	ServicioBase      string `json:"servicio_base"`
	Descripcion       string `json:"descripcion"`
}

// Listar devuelve los cotizadores ordenados por nombre. El listado general
// conserva Activo/Publicado porque también alimenta el Diseñador y filtros.
// ?uso=cotizacion es deliberadamente más estricto: solo expone versiones
// publicadas, compiladas y con TOTAL_PRECIO normalizado; una cotización real
// nunca debe nacer apuntando a un borrador o a una fórmula sin salida.
func (h *CalculadorasHandler) Listar(w http.ResponseWriter, r *http.Request) {
	uso := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("uso")))
	if uso != "" && uso != "cotizacion" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El uso solicitado para los cotizadores no es válido."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT c.calculadora_id, c.nombre_calculadora, c.linea_negocio, c.servicio_base,
		       c.descripcion, c.estado, cc.compilado_id::text, cc.version,
		       (c.estado='Publicado' AND cc.compilado_id IS NOT NULL AND (EXISTS (
		          SELECT 1
		            FROM jsonb_array_elements(COALESCE(cc.configuracion->'salidas','[]'::jsonb)) salida
		           WHERE salida->>'clave_salida'='TOTAL_PRECIO'
		             AND COALESCE((salida->>'activo')::boolean, false)
		             AND COALESCE(salida->>'fuente_id','')<>''
		       ) OR (NOT EXISTS (
		          SELECT 1 FROM jsonb_array_elements(COALESCE(cc.configuracion->'salidas','[]'::jsonb)) configurada
		           WHERE configurada->>'clave_salida'='TOTAL_PRECIO'
		       ) AND jsonb_path_exists(cc.configuracion, '$.** ? (@.funcion_campo == "TOTAL_PRECIO_OFERTA")'))))
		       AS disponible_cotizacion
		  FROM calculadoras c
		  LEFT JOIN cotizadores_compilados cc
		    ON cc.calculadora_id=c.calculadora_id AND cc.estado='ACTIVA'
		 WHERE c.estado IN ('Activo', 'Publicado')
		   AND ($1::text <> 'cotizacion' OR
		        (c.estado='Publicado' AND cc.compilado_id IS NOT NULL AND (EXISTS (
		          SELECT 1
		            FROM jsonb_array_elements(COALESCE(cc.configuracion->'salidas','[]'::jsonb)) salida
		           WHERE salida->>'clave_salida'='TOTAL_PRECIO'
		             AND COALESCE((salida->>'activo')::boolean, false)
		             AND COALESCE(salida->>'fuente_id','')<>''
		        ) OR (NOT EXISTS (
		          SELECT 1 FROM jsonb_array_elements(COALESCE(cc.configuracion->'salidas','[]'::jsonb)) configurada
		           WHERE configurada->>'clave_salida'='TOTAL_PRECIO'
		        ) AND jsonb_path_exists(cc.configuracion, '$.** ? (@.funcion_campo == "TOTAL_PRECIO_OFERTA")')))))
		 ORDER BY c.nombre_calculadora`, uso)
	if err != nil {
		log.Printf("calculadoras: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los cotizadores."})
		return
	}
	defer rows.Close()

	calculadoras := make([]calculadoraSimple, 0)
	for rows.Next() {
		var item calculadoraSimple
		if err := rows.Scan(&item.CalculadoraID, &item.NombreCalc, &item.LineaNegocio, &item.ServicioBase,
			&item.Descripcion, &item.Estado, &item.CompiladoID, &item.VersionConfiguracion,
			&item.DisponibleCotizacion); err != nil {
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

// Crear da de alta (o actualiza, vía upsert por calculadora_id) el
// cotizador. estado no se toca acá: nace 'Activo' por el DEFAULT de la
// tabla y compilador.Compilar lo pasa a 'Publicado' — Crear no debe
// pisar ese valor si se vuelve a llamar sobre un cotizador ya
// publicado (por ejemplo, un segundo "docker compose up" sin -v).
func (h *CalculadorasHandler) Crear(w http.ResponseWriter, r *http.Request) {
	var req crearCalculadoraRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.ToUpper(strings.TrimSpace(req.CalculadoraID))
	req.NombreCalculadora = strings.TrimSpace(req.NombreCalculadora)
	req.LineaNegocio = strings.TrimSpace(req.LineaNegocio)
	req.ServicioBase = strings.TrimSpace(req.ServicioBase)
	req.Descripcion = strings.TrimSpace(req.Descripcion)
	if req.CalculadoraID == "" || req.NombreCalculadora == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id y nombre_calculadora."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, err := h.DB.Exec(ctx, `
		INSERT INTO calculadoras (calculadora_id, nombre_calculadora, linea_negocio, servicio_base, descripcion)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''))
		ON CONFLICT (calculadora_id) DO UPDATE SET
			nombre_calculadora = EXCLUDED.nombre_calculadora,
			linea_negocio = EXCLUDED.linea_negocio,
			servicio_base = EXCLUDED.servicio_base,
			descripcion = EXCLUDED.descripcion`,
		req.CalculadoraID, req.NombreCalculadora, req.LineaNegocio, req.ServicioBase, req.Descripcion)
	if err != nil {
		log.Printf("calculadoras: error guardando %s: %v", req.CalculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar el cotizador."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Cotizador guardado.", "calculadora_id": req.CalculadoraID})
}
