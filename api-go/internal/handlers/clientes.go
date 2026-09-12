package handlers

// ClientesHandler expone la pantalla de gestión de Clientes: alta,
// edición y listado con búsqueda/filtro y conteo de cotizaciones por
// cliente. No confundir con CotizacionesHandler.ListarClientes (GET
// /api/clientes/), que sigue siendo el selector reducido —solo
// clientes Activo, sin conteos— que alimenta "Nueva cotización" y no
// debe romperse.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

type ClientesHandler struct {
	DB *pgxpool.Pool
}

var estadosClienteValidos = map[string]bool{"Activo": true, "Inactivo": true}

type clienteGestion struct {
	ClienteID         string    `json:"cliente_id"`
	NombreComercial   string    `json:"nombre_comercial"`
	RazonSocial       *string   `json:"razon_social,omitempty"`
	Estado            string    `json:"estado"`
	Origen            *string   `json:"origen,omitempty"`
	FechaCreacion     time.Time `json:"fecha_creacion"`
	TotalCotizaciones int       `json:"total_cotizaciones"`
}

type crearClienteRequest struct {
	NombreComercial string `json:"nombre_comercial"`
	RazonSocial     string `json:"razon_social"`
}

type editarClienteRequest struct {
	NombreComercial *string `json:"nombre_comercial"`
	RazonSocial     *string `json:"razon_social"`
	Estado          *string `json:"estado"`
}

// Listar responde GET /api/clientes/gestion: todas las columnas que
// necesita la pantalla (a diferencia de ListarClientes, que solo trae
// clientes Activo) más el conteo de cotizaciones de cada cliente.
func (h *ClientesHandler) Listar(w http.ResponseWriter, r *http.Request) {
	busqueda := strings.TrimSpace(r.URL.Query().Get("busqueda"))
	estado := strings.TrimSpace(r.URL.Query().Get("estado"))
	if estado != "" && !estadosClienteValidos[estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "estado no es válido."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT c.cliente_id, c.nombre_comercial, c.razon_social, c.estado, c.origen,
		       c.fecha_creacion, COUNT(co.cotizacion_id)
		  FROM clientes c
		  LEFT JOIN cotizaciones co ON co.cliente_id = c.cliente_id
		 WHERE ($1::text = '' OR c.estado = $1)
		   AND ($2::text = '' OR concat_ws(' ', c.nombre_comercial, c.razon_social) ILIKE '%' || $2 || '%')
		 GROUP BY c.cliente_id
		 ORDER BY c.nombre_comercial, c.cliente_id`, estado, busqueda)
	if err != nil {
		log.Printf("clientes: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los clientes."})
		return
	}
	defer rows.Close()

	clientes := make([]clienteGestion, 0)
	for rows.Next() {
		var item clienteGestion
		if err := rows.Scan(&item.ClienteID, &item.NombreComercial, &item.RazonSocial, &item.Estado, &item.Origen, &item.FechaCreacion, &item.TotalCotizaciones); err != nil {
			log.Printf("clientes: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los clientes."})
			return
		}
		clientes = append(clientes, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("clientes: error recorriendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los clientes."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "clientes": clientes})
}

// Crear responde POST /api/clientes (alta manual desde la pantalla de
// gestión). origen siempre 'COTIZA' y estado siempre 'Activo' al
// nacer, igual que el alta implícita en crearCotizacionEnTx.
func (h *ClientesHandler) Crear(w http.ResponseWriter, r *http.Request) {
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		escribirJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No fue posible identificar al usuario de la sesión."})
		return
	}

	var req crearClienteRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.NombreComercial = strings.TrimSpace(req.NombreComercial)
	req.RazonSocial = strings.TrimSpace(req.RazonSocial)
	if req.NombreComercial == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el nombre comercial del cliente."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var razonSocial any
	if req.RazonSocial != "" {
		razonSocial = req.RazonSocial
	}

	clienteID, err := generarIDDisponible(ctx, h.DB, "cli", "clientes", "cliente_id")
	if err != nil {
		log.Printf("clientes: error generando id: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el identificador del cliente."})
		return
	}
	if _, err := h.DB.Exec(ctx, `
		INSERT INTO clientes (cliente_id, origen, nombre_comercial, razon_social, estado, usuario_creador_id)
		VALUES ($1, 'COTIZA', $2, $3, 'Activo', $4)`,
		clienteID, req.NombreComercial, razonSocial, usuarioID,
	); err != nil {
		log.Printf("clientes: error creando %s: %v", clienteID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear el cliente."})
		return
	}

	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "cliente_id": clienteID})
}

// Editar responde PATCH /api/clientes/{id}. Mismo patrón de
// editarUsuarioRequest: punteros para distinguir "no vino" de "vino
// vacío". Si estado se aleja de 'Activo', se rechaza con 409 cuando
// el cliente tiene cotizaciones en un estado no terminal.
func (h *ClientesHandler) Editar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el cliente a editar."})
		return
	}

	var req editarClienteRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	var nombreComercial, razonSocial, estado *string
	if req.NombreComercial != nil {
		v := strings.TrimSpace(*req.NombreComercial)
		nombreComercial = &v
	}
	if req.RazonSocial != nil {
		v := strings.TrimSpace(*req.RazonSocial)
		razonSocial = &v
	}
	if req.Estado != nil {
		v := strings.TrimSpace(*req.Estado)
		estado = &v
	}

	if nombreComercial != nil && *nombreComercial == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre comercial no puede quedar vacío."})
		return
	}
	if estado != nil && !estadosClienteValidos[*estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El estado debe ser Activo o Inactivo."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if estado != nil && *estado != "Activo" {
		activas, err := clienteConCotizacionesActivas(ctx, h.DB, id)
		if err != nil {
			log.Printf("clientes: error validando cotizaciones activas de %s: %v", id, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar las cotizaciones del cliente."})
			return
		}
		if activas > 0 {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": fmt.Sprintf("No se puede cambiar el estado: el cliente tiene %d cotización(es) activa(s).", activas)})
			return
		}
	}

	columnas := make([]string, 0, 3)
	valores := make([]any, 0, 3)
	agregar := func(columna string, valor *string) {
		if valor == nil {
			return
		}
		valores = append(valores, *valor)
		columnas = append(columnas, columna+" = $"+strconv.Itoa(len(valores)))
	}
	agregar("nombre_comercial", nombreComercial)
	agregar("razon_social", razonSocial)
	agregar("estado", estado)

	if len(columnas) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos un campo para editar."})
		return
	}

	valores = append(valores, id)
	consulta := "UPDATE clientes SET " + strings.Join(columnas, ", ") + " WHERE cliente_id = $" + strconv.Itoa(len(valores))
	tag, err := h.DB.Exec(ctx, consulta, valores...)
	if err != nil {
		log.Printf("clientes: error editando %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los cambios."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cliente no encontrado."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "cliente_id": id})
}

// clienteConCotizacionesActivas cuenta cotizaciones de este cliente en
// un estado no terminal ('Perdida'/'Cancelada' excluidos). Mismo
// criterio de "en uso" que catalogoEnUsoActivo en catalogos.go.
func clienteConCotizacionesActivas(ctx context.Context, db *pgxpool.Pool, clienteID string) (int, error) {
	var activas int
	err := db.QueryRow(ctx, `SELECT COUNT(*) FROM cotizaciones WHERE cliente_id=$1 AND estado NOT IN ('Perdida','Cancelada')`, clienteID).Scan(&activas)
	return activas, err
}
