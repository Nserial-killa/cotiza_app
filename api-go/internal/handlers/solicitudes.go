package handlers

// SolicitudesHandler es la pantalla del equipo de Cotiza para revisar
// las solicitudes que llegaron por API externo (ver
// solicitudes_externas.go) y convertirlas en una cotización real.
// Protegido por sesión (middleware.RequiereSesion), no por
// middleware.RequiereApiKey.

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

	"cotiza/api/internal/middleware"
)

// SolicitudesHandler necesita CotizacionesHandler para reusar
// crearCotizacionEnTx al convertir una solicitud, en vez de duplicar
// esa lógica (validar cotizador, resolver/crear cliente, generar
// código de oferta e ids, insertar cotización+versión+historial).
type SolicitudesHandler struct {
	DB           *pgxpool.Pool
	Cotizaciones *CotizacionesHandler
}

// estadosSolicitudValidos son los cuatro estados posibles de una
// solicitud (para validar el filtro de Listar). estadosSolicitudManuales
// es el subconjunto que CambiarEstado permite asignar a mano: 'Nueva'
// es el estado inicial (nunca se vuelve a esa etiqueta) y 'Convertida'
// solo la asigna Convertir, nunca este endpoint.
var estadosSolicitudValidos = map[string]bool{
	"Nueva": true, "En revisión": true, "Descartada": true, "Convertida": true,
}
var estadosSolicitudManuales = map[string]bool{
	"En revisión": true, "Descartada": true,
}

type solicitudListado struct {
	SolicitudID          string    `json:"solicitud_id"`
	Origen               string    `json:"origen"`
	IntegracionID        *string   `json:"integracion_id,omitempty"`
	ClienteID            *string   `json:"cliente_id,omitempty"`
	ClienteNombre        string    `json:"cliente_nombre"`
	ContactoNombre       *string   `json:"contacto_nombre,omitempty"`
	ContactoCorreo       *string   `json:"contacto_correo,omitempty"`
	ContactoTelefono     *string   `json:"contacto_telefono,omitempty"`
	CalculadoraID        *string   `json:"calculadora_id,omitempty"`
	Descripcion          *string   `json:"descripcion,omitempty"`
	Estado               string    `json:"estado"`
	CotizacionIDGenerada *string   `json:"cotizacion_id_generada,omitempty"`
	FechaCreacion        time.Time `json:"fecha_creacion"`
	FechaActualizacion   time.Time `json:"fecha_actualizacion"`
}

type cambiarEstadoSolicitudRequest struct {
	Estado string `json:"estado"`
}

type convertirSolicitudRequest struct {
	CalculadoraID string `json:"calculadora_id"`
}

// Listar responde GET /api/solicitudes, con filtro opcional por
// estado exacto.
func (h *SolicitudesHandler) Listar(w http.ResponseWriter, r *http.Request) {
	estado := strings.TrimSpace(r.URL.Query().Get("estado"))
	if estado != "" && !estadosSolicitudValidos[estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "estado no es válido."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT solicitud_id::text, origen, integracion_id::text, cliente_id, cliente_nombre,
		       contacto_nombre, contacto_correo, contacto_telefono, calculadora_id, descripcion,
		       estado, cotizacion_id_generada, fecha_creacion, fecha_actualizacion
		  FROM solicitudes
		 WHERE ($1 = '' OR estado = $1)
		 ORDER BY fecha_creacion DESC`, estado)
	if err != nil {
		log.Printf("solicitudes: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las solicitudes."})
		return
	}
	defer rows.Close()

	solicitudes := make([]solicitudListado, 0)
	for rows.Next() {
		var item solicitudListado
		if err := rows.Scan(&item.SolicitudID, &item.Origen, &item.IntegracionID, &item.ClienteID,
			&item.ClienteNombre, &item.ContactoNombre, &item.ContactoCorreo, &item.ContactoTelefono,
			&item.CalculadoraID, &item.Descripcion, &item.Estado, &item.CotizacionIDGenerada,
			&item.FechaCreacion, &item.FechaActualizacion); err != nil {
			log.Printf("solicitudes: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las solicitudes."})
			return
		}
		solicitudes = append(solicitudes, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("solicitudes: error recorriendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las solicitudes."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "solicitudes": solicitudes})
}

// CambiarEstado responde PATCH /api/solicitudes/{id}: solo mueve a
// 'En revisión' o 'Descartada' a mano. Pasar a 'Convertida' es
// exclusivo de Convertir, y una solicitud ya convertida no se puede
// tocar desde acá (su estado ya representa un hecho consumado: existe
// una cotización real detrás).
func (h *SolicitudesHandler) CambiarEstado(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la solicitud."})
		return
	}

	var req cambiarEstadoSolicitudRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Estado = strings.TrimSpace(req.Estado)
	if !estadosSolicitudManuales[req.Estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El estado debe ser 'En revisión' o 'Descartada'."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var estadoActual string
	err := h.DB.QueryRow(ctx, `SELECT estado FROM solicitudes WHERE solicitud_id::text = $1`, id).Scan(&estadoActual)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Solicitud no encontrada."})
		return
	}
	if err != nil {
		log.Printf("solicitudes: error consultando %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la solicitud."})
		return
	}
	if estadoActual == "Convertida" {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Esta solicitud ya fue convertida en una cotización; su estado no se puede cambiar manualmente."})
		return
	}

	if _, err := h.DB.Exec(ctx, `UPDATE solicitudes SET estado = $1 WHERE solicitud_id::text = $2`, req.Estado, id); err != nil {
		log.Printf("solicitudes: error actualizando estado de %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible actualizar el estado."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "solicitud_id": id, "estado": req.Estado, "mensaje": "Solicitud actualizada."})
}

// Convertir responde POST /api/solicitudes/{id}/convertir: crea la
// cotización real reusando CotizacionesHandler.crearCotizacionEnTx
// (mismo núcleo que POST /api/cotizaciones) dentro de la MISMA
// transacción que marca la solicitud como Convertida, para que ambos
// cambios queden atómicos — o se crea la cotización y se marca la
// solicitud, o no pasa ninguna de las dos cosas.
func (h *SolicitudesHandler) Convertir(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la solicitud."})
		return
	}
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		escribirJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No fue posible identificar al usuario de la sesión."})
		return
	}

	var req convertirSolicitudRequest
	if r.ContentLength != 0 {
		if err := decodificarJSON(r, &req); err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var (
		clienteID     *string
		clienteNombre string
		calculadoraID *string
		estadoActual  string
	)
	err := h.DB.QueryRow(ctx, `
		SELECT cliente_id, cliente_nombre, calculadora_id, estado
		  FROM solicitudes WHERE solicitud_id::text = $1`, id,
	).Scan(&clienteID, &clienteNombre, &calculadoraID, &estadoActual)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Solicitud no encontrada."})
		return
	}
	if err != nil {
		log.Printf("solicitudes: error consultando %s para convertir: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la solicitud."})
		return
	}
	if estadoActual == "Convertida" {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Esta solicitud ya fue convertida en una cotización."})
		return
	}

	calculadoraIDResuelta := req.CalculadoraID
	if calculadoraIDResuelta == "" && calculadoraID != nil {
		calculadoraIDResuelta = *calculadoraID
	}
	if calculadoraIDResuelta == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id: la solicitud no trae un cotizador y no se indicó uno alternativo."})
		return
	}

	entrada := crearCotizacionEntrada{CalculadoraID: calculadoraIDResuelta, ClienteNombreNuevo: clienteNombre}
	if clienteID != nil {
		entrada.ClienteID = *clienteID
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		log.Printf("solicitudes: error iniciando conversión de %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible convertir la solicitud."})
		return
	}
	defer tx.Rollback(ctx)

	cotizacionID, codigoOferta, ok := h.Cotizaciones.crearCotizacionEnTx(w, ctx, tx, entrada, usuarioID, "Cotización creada a partir de la solicitud "+id+".")
	if !ok {
		return
	}

	if _, err := tx.Exec(ctx, `UPDATE solicitudes SET estado = 'Convertida', cotizacion_id_generada = $1 WHERE solicitud_id::text = $2`, cotizacionID, id); err != nil {
		log.Printf("solicitudes: error marcando %s como convertida: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible convertir la solicitud."})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		log.Printf("solicitudes: error confirmando conversión de %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible convertir la solicitud."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "solicitud_id": id, "cotizacion_id": cotizacionID, "codigo_oferta": codigoOferta,
		"estado": "Convertida", "mensaje": "Solicitud convertida en cotización.",
	})
}
