package handlers

// ReglasHandler administra el catálogo de reglas globales (esquema 0003,
// tabla "reglas"). Esto es solo el catálogo/administración de reglas
// disponibles — cómo se aplican a un elemento del cotizador es
// "reglas_cotizador", una tabla y sprint aparte.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ReglasHandler struct {
	DB *pgxpool.Pool
}

type reglaGlobal struct {
	ReglaID          string          `json:"regla_id"`
	Nombre           string          `json:"nombre"`
	Categoria        *string         `json:"categoria,omitempty"`
	Tipo             *string         `json:"tipo,omitempty"`
	Descripcion      *string         `json:"descripcion,omitempty"`
	Severidad        string          `json:"severidad"`
	Momento          string          `json:"momento"`
	Mensaje          *string         `json:"mensaje,omitempty"`
	Orden            int             `json:"orden"`
	ParametrosSchema json.RawMessage `json:"parametros_schema"`
	Activo           bool            `json:"activo"`
}

type guardarReglaRequest struct {
	ReglaID          string          `json:"regla_id"`
	Nombre           string          `json:"nombre"`
	Categoria        string          `json:"categoria"`
	Tipo             string          `json:"tipo"`
	Descripcion      string          `json:"descripcion"`
	Severidad        string          `json:"severidad"`
	Momento          string          `json:"momento"`
	Mensaje          string          `json:"mensaje"`
	Orden            enteroFlexible  `json:"orden"`
	ParametrosSchema json.RawMessage `json:"parametros_schema"`
	Activo           bool            `json:"activo"`
}

// severidadesReglaValidas y momentosReglaValidos reflejan los CHECK de
// 0003_disenador_esquema.sql. Se validan también acá para devolver un
// mensaje claro al frontend en vez de dejar que la base rechace con un
// error genérico de constraint.
var severidadesReglaValidas = map[string]bool{
	"INFORMATIVA": true, "ADVERTENCIA": true, "BLOQUEANTE": true, "APROBACION": true,
}

var momentosReglaValidos = map[string]bool{
	"AL_CAMBIAR_CAMPO": true, "AL_CARGAR": true, "AL_VALIDAR": true, "AL_GUARDAR": true, "AL_PUBLICAR": true,
}

// Listar devuelve el catálogo de reglas globales. Los filtros por query
// param son opcionales — hoy la pantalla carga todo una vez y filtra en
// el array ya cargado, pero quedan disponibles para otros consumidores.
func (h *ReglasHandler) Listar(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	categoria := strings.TrimSpace(r.URL.Query().Get("categoria"))
	tipo := strings.TrimSpace(r.URL.Query().Get("tipo"))
	severidad := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("severidad")))
	estado := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("estado")))

	rows, err := h.DB.Query(ctx, `
		SELECT regla_id, nombre, categoria, tipo, descripcion, severidad,
		       momento, mensaje, orden, parametros_schema, activo
		  FROM reglas
		 WHERE ($1 = '' OR categoria = $1)
		   AND ($2 = '' OR tipo = $2)
		   AND ($3 = '' OR severidad = $3)
		   AND ($4 = '' OR $4 = 'TODOS' OR ($4 = 'ACTIVO' AND activo) OR ($4 = 'INACTIVO' AND NOT activo))
		 ORDER BY orden, nombre`,
		categoria, tipo, severidad, estado)
	if err != nil {
		log.Printf("reglas: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las reglas."})
		return
	}
	defer rows.Close()

	reglas := make([]reglaGlobal, 0)
	for rows.Next() {
		var item reglaGlobal
		if err := rows.Scan(&item.ReglaID, &item.Nombre, &item.Categoria, &item.Tipo, &item.Descripcion,
			&item.Severidad, &item.Momento, &item.Mensaje, &item.Orden, &item.ParametrosSchema, &item.Activo); err != nil {
			log.Printf("reglas: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las reglas."})
			return
		}
		reglas = append(reglas, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("reglas: error leyendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las reglas."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "reglas": reglas})
}

// Guardar crea o actualiza una regla global usando regla_id como clave estable.
func (h *ReglasHandler) Guardar(w http.ResponseWriter, r *http.Request) {
	var req guardarReglaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	req.ReglaID = strings.ToUpper(strings.TrimSpace(req.ReglaID))
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.Categoria = strings.ToUpper(strings.TrimSpace(req.Categoria))
	req.Tipo = strings.ToUpper(strings.TrimSpace(req.Tipo))
	req.Descripcion = strings.TrimSpace(req.Descripcion)
	req.Severidad = strings.ToUpper(strings.TrimSpace(req.Severidad))
	if req.Severidad == "" {
		req.Severidad = "INFORMATIVA"
	}
	req.Momento = strings.ToUpper(strings.TrimSpace(req.Momento))
	if req.Momento == "" {
		req.Momento = "AL_CAMBIAR_CAMPO"
	}
	req.Mensaje = strings.TrimSpace(req.Mensaje)

	if req.ReglaID == "" || req.Nombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar regla_id y nombre."})
		return
	}
	if !severidadesReglaValidas[req.Severidad] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Severidad no válida."})
		return
	}
	if !momentosReglaValidos[req.Momento] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Momento no válido."})
		return
	}

	parametrosSchema, err := normalizarParametrosSchema(req.ParametrosSchema)
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	orden := int(req.Orden)
	if orden == 0 {
		orden = 10
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, err = h.DB.Exec(ctx, `
		INSERT INTO reglas
			(regla_id, nombre, categoria, tipo, descripcion, severidad, momento, mensaje, orden, parametros_schema, activo)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7, NULLIF($8, ''), $9, $10, $11)
		ON CONFLICT (regla_id) DO UPDATE SET
			nombre = EXCLUDED.nombre,
			categoria = EXCLUDED.categoria,
			tipo = EXCLUDED.tipo,
			descripcion = EXCLUDED.descripcion,
			severidad = EXCLUDED.severidad,
			momento = EXCLUDED.momento,
			mensaje = EXCLUDED.mensaje,
			orden = EXCLUDED.orden,
			parametros_schema = EXCLUDED.parametros_schema,
			activo = EXCLUDED.activo`,
		req.ReglaID, req.Nombre, req.Categoria, req.Tipo, req.Descripcion,
		req.Severidad, req.Momento, req.Mensaje, orden, parametrosSchema, req.Activo)
	if err != nil {
		log.Printf("reglas: error guardando %s: %v", req.ReglaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar la regla."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Regla guardada.", "regla_id": req.ReglaID})
}

// Eliminar borra una regla del catálogo por su regla_id.
func (h *ReglasHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	reglaID := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "id")))
	if reglaID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la regla a eliminar."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM reglas WHERE regla_id = $1`, reglaID)
	if err != nil {
		log.Printf("reglas: error eliminando %s: %v", reglaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar la regla."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Regla no encontrada."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Regla eliminada.", "regla_id": reglaID})
}

// normalizarParametrosSchema acepta tanto un arreglo JSON nativo como un
// arreglo JSON serializado como string (el editor "Ver JSON técnico" de
// la pantalla lo manda como texto de un <textarea>), y siempre devuelve
// un arreglo válido — nunca null, porque la columna es NOT NULL.
func normalizarParametrosSchema(raw json.RawMessage) (json.RawMessage, error) {
	texto := strings.TrimSpace(string(raw))
	if texto == "" || texto == "null" {
		return json.RawMessage("[]"), nil
	}
	if texto[0] == '"' {
		var interior string
		if err := json.Unmarshal(raw, &interior); err != nil {
			return nil, fmt.Errorf("parametros_schema no es JSON válido")
		}
		interior = strings.TrimSpace(interior)
		if interior == "" {
			return json.RawMessage("[]"), nil
		}
		raw = json.RawMessage(interior)
	}
	var comprobante any
	if err := json.Unmarshal(raw, &comprobante); err != nil {
		return nil, fmt.Errorf("parametros_schema no es JSON válido")
	}
	if _, esArreglo := comprobante.([]any); !esArreglo {
		return nil, fmt.Errorf("parametros_schema debe ser un arreglo JSON")
	}
	return raw, nil
}
