package handlers

// SolicitudesExternasHandler expone el único endpoint que un sistema
// externo (Bitrix24 u otro) llama directamente, protegido por
// middleware.RequiereApiKey en vez de una sesión de usuario (ver
// cmd/server/main.go: va en su propio grupo de rutas, fuera del
// r.Group que exige sesión). Nunca llama HACIA afuera — solo recibe.

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

type SolicitudesExternasHandler struct {
	DB *pgxpool.Pool
}

type crearSolicitudExternaRequest struct {
	ClienteNombre    string `json:"cliente_nombre"`
	ContactoNombre   string `json:"contacto_nombre"`
	ContactoCorreo   string `json:"contacto_correo"`
	ContactoTelefono string `json:"contacto_telefono"`
	CalculadoraID    string `json:"calculadora_id"`
	Descripcion      string `json:"descripcion"`
}

// Crear responde POST /api/externo/solicitudes. El integracion_id
// viene del contexto (lo dejó middleware.RequiereApiKey al validar la
// clave) — nunca se confía en un valor que el cuerpo pudiera mandar.
func (h *SolicitudesExternasHandler) Crear(w http.ResponseWriter, r *http.Request) {
	integracionID, _ := r.Context().Value(middleware.IntegracionIDKey).(string)
	if integracionID == "" {
		log.Printf("solicitudes externas: falta integracion_id en el contexto (¿falta el middleware?)")
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible identificar la integración."})
		return
	}

	var req crearSolicitudExternaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.ClienteNombre = strings.TrimSpace(req.ClienteNombre)
	req.ContactoNombre = strings.TrimSpace(req.ContactoNombre)
	req.ContactoCorreo = strings.TrimSpace(req.ContactoCorreo)
	req.ContactoTelefono = strings.TrimSpace(req.ContactoTelefono)
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.Descripcion = strings.TrimSpace(req.Descripcion)

	if req.ClienteNombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar cliente_nombre."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if req.CalculadoraID != "" {
		var existe bool
		if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM calculadoras WHERE calculadora_id = $1)`, req.CalculadoraID).Scan(&existe); err != nil {
			log.Printf("solicitudes externas: error validando calculadora_id %s: %v", req.CalculadoraID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el cotizador indicado."})
			return
		}
		if !existe {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador indicado no existe."})
			return
		}
	}

	var solicitudID string
	err := h.DB.QueryRow(ctx, `
		INSERT INTO solicitudes
			(origen, integracion_id, cliente_nombre, contacto_nombre, contacto_correo,
			 contacto_telefono, calculadora_id, descripcion, estado)
		VALUES ('API_EXTERNA', $1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), 'Nueva')
		RETURNING solicitud_id::text`,
		integracionID, req.ClienteNombre, req.ContactoNombre, req.ContactoCorreo,
		req.ContactoTelefono, req.CalculadoraID, req.Descripcion,
	).Scan(&solicitudID)
	if err != nil {
		log.Printf("solicitudes externas: error creando solicitud: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la solicitud."})
		return
	}

	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "solicitud_id": solicitudID})
}
