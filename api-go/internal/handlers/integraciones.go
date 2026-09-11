package handlers

// IntegracionesHandler deja que un Administrador genere y gestione las
// claves de API externo que usan sistemas como Bitrix24 para crear
// solicitudes sin un login de usuario (ver
// internal/middleware/apikey.go y solicitudes_externas.go). Solo
// administra las claves — llamar HACIA un CRM externo queda fuera de
// alcance a propósito (ver 0011_solicitudes.sql).

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"cotiza/api/internal/middleware"
)

type IntegracionesHandler struct {
	DB *pgxpool.Pool
}

// costoApiKeyHash usa el mismo costo que usuarios.costoPin: la clave
// en sí ya tiene 256 bits de entropía (crypto/rand), así que el costo
// de bcrypt acá importa menos que con un PIN corto, pero no hay razón
// para bajarlo.
const costoApiKeyHash = 12

type integracionListado struct {
	IntegracionID string     `json:"integracion_id"`
	Nombre        string     `json:"nombre"`
	Estado        string     `json:"estado"`
	FechaCreacion time.Time  `json:"fecha_creacion"`
	UltimaUso     *time.Time `json:"ultima_uso,omitempty"`
}

type crearIntegracionRequest struct {
	Nombre string `json:"nombre"`
}

type editarIntegracionRequest struct {
	Estado string `json:"estado"`
}

// obtenerSesion resuelve el usuario_id que middleware.RequiereSesion
// dejó en el contexto al rol que tiene HOY en la base — mismo patrón
// que UsuariosHandler.obtenerSesion en usuarios.go.
func (h *IntegracionesHandler) obtenerSesion(ctx context.Context, r *http.Request) (usuarioID, rol string, err error) {
	usuarioID, _ = r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		return "", "", errors.New("la sesión no tiene un usuario_id asociado")
	}
	err = h.DB.QueryRow(ctx, `SELECT rol FROM usuarios WHERE usuario_id = $1`, usuarioID).Scan(&rol)
	return usuarioID, rol, err
}

// Crear da de alta una integración y responde con la clave en texto
// plano UNA SOLA VEZ. Después de esta respuesta, la clave real ya no
// se puede recuperar de ningún lado: la base solo guarda su hash
// bcrypt (api_key_hash), igual que usuarios.pin_hash con los PIN. Si
// el cliente pierde la clave, la única salida es revocar esta
// integración (PATCH estado=Inactivo) y crear una nueva.
func (h *IntegracionesHandler) Crear(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	usuarioID, rol, err := h.obtenerSesion(ctx, r)
	if err != nil {
		log.Printf("integraciones: error validando la sesión: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return
	}
	if rol != "Administrador" {
		escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo un Administrador puede crear integraciones."})
		return
	}

	var req crearIntegracionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	nombre := strings.TrimSpace(req.Nombre)
	if nombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar un nombre para la integración."})
		return
	}

	// Mismo generador que el token de sesión (auth.go): crypto/rand,
	// 32 bytes en hex. A diferencia de un token de sesión, esta clave
	// no lleva fecha de expiración propia — solo se apaga revocando
	// la integración.
	apiKey, err := generarToken()
	if err != nil {
		log.Printf("integraciones: error generando la clave: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar la clave."})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(apiKey), costoApiKeyHash)
	if err != nil {
		log.Printf("integraciones: error hasheando la clave: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar la clave."})
		return
	}

	// Igual que en usuarios.go: bcrypt es trabajo de CPU que no acepta
	// context, así que no puede descontarse del presupuesto reservado
	// para el INSERT — si no, crear una integración válida terminaría
	// en 500 por "context deadline exceeded" en una máquina cargada.
	ctxEscritura, cancelEscritura := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancelEscritura()

	var integracionID string
	err = h.DB.QueryRow(ctxEscritura, `
		INSERT INTO integraciones_api (nombre, api_key_hash, creado_por)
		VALUES ($1, $2, $3)
		RETURNING integracion_id::text`,
		nombre, string(hash), usuarioID,
	).Scan(&integracionID)
	if err != nil {
		log.Printf("integraciones: error creando %q: %v", nombre, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la integración."})
		return
	}

	// *** ÚNICA VEZ que "api_key" viaja en texto plano. Ningún otro
	// endpoint de este archivo la vuelve a exponer — Listar solo
	// devuelve metadatos, nunca la clave ni su hash. ***
	escribirJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "integracion_id": integracionID, "api_key": apiKey,
		"mensaje": "Integración creada. Copie la clave ahora: no podrá verse de nuevo.",
	})
}

// Listar devuelve las integraciones sin exponer ninguna clave ni su
// hash — solo lo necesario para administrarlas desde la pantalla.
func (h *IntegracionesHandler) Listar(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, rol, err := h.obtenerSesion(ctx, r)
	if err != nil {
		log.Printf("integraciones: error validando la sesión: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return
	}
	if rol != "Administrador" {
		escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo un Administrador puede ver las integraciones."})
		return
	}

	rows, err := h.DB.Query(ctx, `
		SELECT integracion_id::text, nombre, estado, fecha_creacion, ultima_uso
		  FROM integraciones_api
		 ORDER BY fecha_creacion DESC`)
	if err != nil {
		log.Printf("integraciones: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las integraciones."})
		return
	}
	defer rows.Close()

	integraciones := make([]integracionListado, 0)
	for rows.Next() {
		var item integracionListado
		if err := rows.Scan(&item.IntegracionID, &item.Nombre, &item.Estado, &item.FechaCreacion, &item.UltimaUso); err != nil {
			log.Printf("integraciones: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las integraciones."})
			return
		}
		integraciones = append(integraciones, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("integraciones: error recorriendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las integraciones."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "integraciones": integraciones})
}

// Editar solo permite revocar (estado=Inactivo). Reactivar generando
// una clave nueva desde el mismo registro queda deliberadamente sin
// soportar: para eso hay que crear una integración nueva, así el
// historial de qué clave estuvo activa cuándo no se pisa.
func (h *IntegracionesHandler) Editar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la integración."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, rol, err := h.obtenerSesion(ctx, r)
	if err != nil {
		log.Printf("integraciones: error validando la sesión: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return
	}
	if rol != "Administrador" {
		escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo un Administrador puede revocar integraciones."})
		return
	}

	var req editarIntegracionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Estado = strings.TrimSpace(req.Estado)
	if req.Estado != "Inactivo" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Solo se puede revocar una integración (estado Inactivo); para reactivarla cree una integración nueva."})
		return
	}

	tag, err := h.DB.Exec(ctx, `UPDATE integraciones_api SET estado = 'Inactivo' WHERE integracion_id::text = $1`, id)
	if err != nil {
		log.Printf("integraciones: error revocando %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible revocar la integración."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Integración no encontrada."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "integracion_id": id, "estado": "Inactivo", "mensaje": "Integración revocada."})
}
