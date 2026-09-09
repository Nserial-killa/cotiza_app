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
var prioridadesSolicitudValidas = map[string]bool{
	"Baja": true, "Media": true, "Alta": true, "Urgente": true,
}

type solicitudListado struct {
	SolicitudID          string    `json:"solicitud_id"`
	Origen               string    `json:"origen"`
	IntegracionID        *string   `json:"integracion_id,omitempty"`
	IntegracionNombre    *string   `json:"integracion_nombre,omitempty"`
	Titulo               *string   `json:"titulo,omitempty"`
	CRMID                *string   `json:"crm_id,omitempty"`
	ClienteID            *string   `json:"cliente_id,omitempty"`
	ClienteNombre        string    `json:"cliente_nombre"`
	ContactoNombre       *string   `json:"contacto_nombre,omitempty"`
	ContactoCorreo       *string   `json:"contacto_correo,omitempty"`
	ContactoTelefono     *string   `json:"contacto_telefono,omitempty"`
	CalculadoraID        *string   `json:"calculadora_id,omitempty"`
	CalculadoraNombre    *string   `json:"calculadora_nombre,omitempty"`
	Descripcion          *string   `json:"descripcion,omitempty"`
	Prioridad            string    `json:"prioridad"`
	FechaRequerida       *string   `json:"fecha_requerida,omitempty"`
	VendedorID           *string   `json:"vendedor_id,omitempty"`
	VendedorNombre       *string   `json:"vendedor_nombre,omitempty"`
	AnalistaID           *string   `json:"analista_id,omitempty"`
	AnalistaNombre       *string   `json:"analista_nombre,omitempty"`
	LiderProductoID      *string   `json:"lider_producto_id,omitempty"`
	LiderProductoNombre  *string   `json:"lider_producto_nombre,omitempty"`
	CreadoPor            *string   `json:"creado_por,omitempty"`
	CreadoPorNombre      *string   `json:"creado_por_nombre,omitempty"`
	Estado               string    `json:"estado"`
	CotizacionIDGenerada *string   `json:"cotizacion_id_generada,omitempty"`
	FechaCreacion        time.Time `json:"fecha_creacion"`
	FechaActualizacion   time.Time `json:"fecha_actualizacion"`
}

type crearSolicitudManualRequest struct {
	Titulo           string `json:"titulo"`
	CRMID            string `json:"crm_id"`
	ClienteNombre    string `json:"cliente_nombre"`
	ContactoNombre   string `json:"contacto_nombre"`
	ContactoCorreo   string `json:"contacto_correo"`
	ContactoTelefono string `json:"contacto_telefono"`
	CalculadoraID    string `json:"calculadora_id"`
	Prioridad        string `json:"prioridad"`
	FechaRequerida   string `json:"fecha_requerida"`
	VendedorID       string `json:"vendedor_id"`
	AnalistaID       string `json:"analista_id"`
	LiderProductoID  string `json:"lider_producto_id"`
	Descripcion      string `json:"descripcion"`
}

type cambiarEstadoSolicitudRequest struct {
	Estado string `json:"estado"`
}

type convertirSolicitudRequest struct {
	CalculadoraID string `json:"calculadora_id"`
}

const consultaSolicitudes = `
	SELECT s.solicitud_id::text, s.origen, s.integracion_id::text, i.nombre,
	       s.titulo, s.crm_id, s.cliente_id, s.cliente_nombre,
	       s.contacto_nombre, s.contacto_correo, s.contacto_telefono,
	       s.calculadora_id, c.nombre_calculadora, s.descripcion,
	       s.prioridad, to_char(s.fecha_requerida, 'YYYY-MM-DD'),
	       s.vendedor_id, vendedor.nombre, s.analista_id, analista.nombre,
	       s.lider_producto_id, lider.nombre, s.creado_por, creador.nombre,
	       s.estado, s.cotizacion_id_generada, s.fecha_creacion, s.fecha_actualizacion
	  FROM solicitudes s
	  LEFT JOIN integraciones_api i ON i.integracion_id = s.integracion_id
	  LEFT JOIN calculadoras c ON c.calculadora_id = s.calculadora_id
	  LEFT JOIN usuarios vendedor ON vendedor.usuario_id = s.vendedor_id
	  LEFT JOIN usuarios analista ON analista.usuario_id = s.analista_id
	  LEFT JOIN usuarios lider ON lider.usuario_id = s.lider_producto_id
	  LEFT JOIN usuarios creador ON creador.usuario_id = s.creado_por`

type escanerSolicitud interface {
	Scan(dest ...any) error
}

func leerSolicitud(escaner escanerSolicitud, item *solicitudListado) error {
	return escaner.Scan(
		&item.SolicitudID, &item.Origen, &item.IntegracionID, &item.IntegracionNombre,
		&item.Titulo, &item.CRMID, &item.ClienteID, &item.ClienteNombre,
		&item.ContactoNombre, &item.ContactoCorreo, &item.ContactoTelefono,
		&item.CalculadoraID, &item.CalculadoraNombre, &item.Descripcion,
		&item.Prioridad, &item.FechaRequerida, &item.VendedorID, &item.VendedorNombre,
		&item.AnalistaID, &item.AnalistaNombre, &item.LiderProductoID, &item.LiderProductoNombre,
		&item.CreadoPor, &item.CreadoPorNombre, &item.Estado, &item.CotizacionIDGenerada,
		&item.FechaCreacion, &item.FechaActualizacion,
	)
}

// Crear responde POST /api/solicitudes para el alta manual desde la
// sesión. Es deliberadamente un endpoint distinto al externo: no
// acepta integracion_id y toma creado_por del usuario autenticado.
func (h *SolicitudesHandler) Crear(w http.ResponseWriter, r *http.Request) {
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		escribirJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No fue posible identificar al usuario de la sesión."})
		return
	}

	var req crearSolicitudManualRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Titulo = strings.TrimSpace(req.Titulo)
	req.CRMID = strings.TrimSpace(req.CRMID)
	req.ClienteNombre = strings.TrimSpace(req.ClienteNombre)
	req.ContactoNombre = strings.TrimSpace(req.ContactoNombre)
	req.ContactoCorreo = strings.TrimSpace(req.ContactoCorreo)
	req.ContactoTelefono = strings.TrimSpace(req.ContactoTelefono)
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.Prioridad = strings.TrimSpace(req.Prioridad)
	req.FechaRequerida = strings.TrimSpace(req.FechaRequerida)
	req.VendedorID = strings.TrimSpace(req.VendedorID)
	req.AnalistaID = strings.TrimSpace(req.AnalistaID)
	req.LiderProductoID = strings.TrimSpace(req.LiderProductoID)
	req.Descripcion = strings.TrimSpace(req.Descripcion)
	if req.Prioridad == "" {
		req.Prioridad = "Media"
	}

	switch {
	case req.Titulo == "":
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el título de la solicitud."})
		return
	case req.CRMID == "":
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el ID de oportunidad CRM."})
		return
	case req.ClienteNombre == "":
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la empresa o cliente."})
		return
	case req.CalculadoraID == "":
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el cotizador."})
		return
	case req.VendedorID == "":
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el vendedor responsable."})
		return
	case req.Descripcion == "":
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la descripción o alcance solicitado."})
		return
	case req.ContactoCorreo != "" && !patronCorreoValido.MatchString(req.ContactoCorreo):
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El correo del contacto no tiene un formato válido."})
		return
	case !prioridadesSolicitudValidas[req.Prioridad]:
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La prioridad debe ser Baja, Media, Alta o Urgente."})
		return
	}
	if req.FechaRequerida != "" {
		if _, err := time.Parse("2006-01-02", req.FechaRequerida); err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "fecha_requerida debe usar el formato AAAA-MM-DD."})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM calculadoras WHERE calculadora_id = $1 AND estado IN ('Activo', 'Publicado'))`, req.CalculadoraID).Scan(&existe); err != nil {
		log.Printf("solicitudes: error validando cotizador %s: %v", req.CalculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el cotizador."})
		return
	}
	if !existe {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador indicado no existe o no está disponible."})
		return
	}
	for _, responsable := range []struct {
		id    string
		campo string
	}{{req.VendedorID, "vendedor"}, {req.AnalistaID, "analista"}, {req.LiderProductoID, "líder de producto"}} {
		if responsable.id == "" {
			continue
		}
		if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM usuarios WHERE usuario_id = $1 AND estado = 'Activo')`, responsable.id).Scan(&existe); err != nil {
			log.Printf("solicitudes: error validando %s %s: %v", responsable.campo, responsable.id, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los responsables."})
			return
		}
		if !existe {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El " + responsable.campo + " indicado no existe o no está activo."})
			return
		}
	}

	var solicitudID string
	err := h.DB.QueryRow(ctx, `
		INSERT INTO solicitudes
			(origen, titulo, crm_id, cliente_nombre, contacto_nombre, contacto_correo,
			 contacto_telefono, calculadora_id, prioridad, fecha_requerida, vendedor_id,
			 analista_id, lider_producto_id, descripcion, creado_por, estado)
		VALUES ('MANUAL', $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
		        $7, $8, NULLIF($9, '')::date, $10, NULLIF($11, ''), NULLIF($12, ''),
		        $13, $14, 'Nueva')
		RETURNING solicitud_id::text`,
		req.Titulo, req.CRMID, req.ClienteNombre, req.ContactoNombre, req.ContactoCorreo,
		req.ContactoTelefono, req.CalculadoraID, req.Prioridad, req.FechaRequerida,
		req.VendedorID, req.AnalistaID, req.LiderProductoID, req.Descripcion, usuarioID,
	).Scan(&solicitudID)
	if err != nil {
		log.Printf("solicitudes: error creando solicitud manual: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la solicitud."})
		return
	}

	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "solicitud_id": solicitudID, "estado": "Nueva", "mensaje": "Solicitud creada."})
}

// Listar responde GET /api/solicitudes. "Etapa" en la pantalla se
// envía como estado; prioridad, responsable y búsqueda son filtros
// independientes y no alteran el modelo de estados existente.
func (h *SolicitudesHandler) Listar(w http.ResponseWriter, r *http.Request) {
	estado := strings.TrimSpace(r.URL.Query().Get("estado"))
	if estado != "" && !estadosSolicitudValidos[estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "estado no es válido."})
		return
	}
	prioridad := strings.TrimSpace(r.URL.Query().Get("prioridad"))
	if prioridad != "" && !prioridadesSolicitudValidas[prioridad] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "prioridad no es válida."})
		return
	}
	responsableID := strings.TrimSpace(r.URL.Query().Get("responsable_id"))
	busqueda := strings.TrimSpace(r.URL.Query().Get("busqueda"))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, consultaSolicitudes+`
		 WHERE ($1::text = '' OR s.estado = $1)
		   AND ($2::text = '' OR s.prioridad = $2)
		   AND ($3::text = '' OR s.vendedor_id = $3 OR s.analista_id = $3 OR s.lider_producto_id = $3)
		   AND ($4::text = '' OR concat_ws(' ', s.solicitud_id::text, s.titulo, s.crm_id,
		       s.cliente_nombre, s.contacto_nombre, s.contacto_correo, s.descripcion) ILIKE '%' || $4 || '%')
		 ORDER BY s.fecha_creacion DESC`, estado, prioridad, responsableID, busqueda)
	if err != nil {
		log.Printf("solicitudes: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las solicitudes."})
		return
	}
	defer rows.Close()

	solicitudes := make([]solicitudListado, 0)
	for rows.Next() {
		var item solicitudListado
		if err := leerSolicitud(rows, &item); err != nil {
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

// Detalle responde GET /api/solicitudes/{id} con los mismos campos
// enriquecidos del listado, incluidos nombres de cotizador y
// responsables, para que la pantalla no tenga que reconstruirlos.
func (h *SolicitudesHandler) Detalle(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la solicitud."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var item solicitudListado
	err := leerSolicitud(h.DB.QueryRow(ctx, consultaSolicitudes+` WHERE s.solicitud_id::text = $1`, id), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Solicitud no encontrada."})
		return
	}
	if err != nil {
		log.Printf("solicitudes: error consultando detalle %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la solicitud."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "solicitud": item})
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
	if estadoActual == "Descartada" {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Una solicitud descartada no se puede convertir en cotización."})
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
