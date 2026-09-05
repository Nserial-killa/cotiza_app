package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

// DashboardHandler sirve los indicadores comerciales desde la versión
// actual de cada cotización. total_precio es precio de venta y se muestra
// a todos los roles; margen_total solo se entrega a roles autorizados.
type DashboardHandler struct {
	DB *pgxpool.Pool
}

type dashboardKPIs struct {
	CotizacionesMes       int      `json:"cotizaciones_mes"`
	Vigentes              int      `json:"vigentes"`
	MontoCotizado         float64  `json:"monto_cotizado"`
	Aceptadas             int      `json:"aceptadas"`
	MontoAceptado         float64  `json:"monto_aceptado"`
	Ganadas               int      `json:"ganadas"`
	MontoGanado           float64  `json:"monto_ganado"`
	Perdidas              int      `json:"perdidas"`
	VencidasCanceladas    int      `json:"vencidas_canceladas"`
	TicketPromedioGeneral float64  `json:"ticket_promedio_general"`
	MargenPromedio        *float64 `json:"margen_promedio,omitempty"`
}

type dashboardCotizacion struct {
	CotizacionID       string    `json:"cotizacion_id"`
	CodigoOferta       *string   `json:"codigo_oferta,omitempty"`
	Cliente            *string   `json:"cliente,omitempty"`
	Empresa            *string   `json:"empresa,omitempty"`
	CalculadoraID      string    `json:"calculadora_id"`
	CalculadoraNombre  string    `json:"calculadora_nombre"`
	Estado             string    `json:"estado"`
	TotalPrecio        float64   `json:"total_precio"`
	Moneda             string    `json:"moneda"`
	VendedorID         *string   `json:"vendedor_id,omitempty"`
	Vendedor           *string   `json:"vendedor,omitempty"`
	TipoCliente        string    `json:"tipo_cliente"`
	FechaCreacion      time.Time `json:"fecha_creacion"`
	FechaActualizacion time.Time `json:"fecha_actualizacion"`
	MargenTotal        *float64  `json:"margen_total,omitempty"`
}

const dashboardFiltradoCTE = `
	WITH historico AS (
		SELECT c.cotizacion_id, c.calculadora_id, c.cliente_id, c.codigo_oferta,
		       c.estado, c.fecha_creacion, c.fecha_actualizacion, c.version_actual,
		       COUNT(*) OVER (
		         PARTITION BY c.cliente_id
		         ORDER BY c.fecha_creacion, c.cotizacion_id
		         ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
		       ) AS cotizaciones_previas
		  FROM cotizaciones c
	), base AS (
		SELECT h.*, cv.total_precio, cv.moneda, cv.margen_total,
		       calc.nombre_calculadora,
		       cl.nombre_comercial AS cliente,
		       COALESCE(cl.razon_social, cl.nombre_comercial) AS empresa,
		       vendedor.usuario_id AS vendedor_id, vendedor.nombre AS vendedor,
		       CASE WHEN h.cliente_id IS NOT NULL AND h.cotizaciones_previas > 0
		            THEN 'Cliente existente' ELSE 'Prospecto' END AS tipo_cliente
		  FROM historico h
		  JOIN cotizacion_versiones cv
		    ON cv.cotizacion_id=h.cotizacion_id AND cv.numero_version=h.version_actual
		  JOIN calculadoras calc ON calc.calculadora_id=h.calculadora_id
		  LEFT JOIN clientes cl ON cl.cliente_id=h.cliente_id
		  LEFT JOIN LATERAL (
		    SELECT MIN(cu.usuario_id) AS usuario_id, MIN(u.nombre) AS nombre
		      FROM cotizacion_usuarios cu
		      JOIN usuarios u ON u.usuario_id=cu.usuario_id
		     WHERE cu.cotizacion_id=h.cotizacion_id AND cu.funcion='Vendedor'
		  ) vendedor ON true
	), filtradas AS (
		SELECT * FROM base
		 WHERE ($1='' OR calculadora_id=$1)
		   AND ($2='' OR EXISTS (
		       SELECT 1 FROM cotizacion_usuarios cu
		        WHERE cu.cotizacion_id=base.cotizacion_id
		          AND cu.funcion='Vendedor' AND cu.usuario_id=$2))
		   AND ($3='' OR tipo_cliente=$3)
	)`

// Obtener responde GET /api/dashboard con los mismos nombres consumidos
// por renderDashboard en cotiza_scripts.html.
func (h *DashboardHandler) Obtener(w http.ResponseWriter, r *http.Request) {
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	vendedorID := strings.TrimSpace(r.URL.Query().Get("vendedor_id"))
	tipoCliente := strings.TrimSpace(r.URL.Query().Get("tipo_cliente"))
	if tipoCliente != "" && tipoCliente != "Prospecto" && tipoCliente != "Cliente existente" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_cliente debe ser Prospecto o Cliente existente."})
		return
	}

	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		escribirJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No fue posible identificar al usuario de la sesión."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var puedeVerMargen bool
	if err := h.DB.QueryRow(ctx, `SELECT r.puede_ver_price FROM usuarios u JOIN roles r ON r.rol=u.rol WHERE u.usuario_id=$1`, usuarioID).Scan(&puedeVerMargen); err != nil {
		log.Printf("dashboard: error consultando permisos de %s: %v", usuarioID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el dashboard."})
		return
	}

	var k dashboardKPIs
	var margen *float64
	err := h.DB.QueryRow(ctx, dashboardFiltradoCTE+`
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE estado NOT IN ('Aceptada','Ganada','Perdida','Cancelada','Vencida'))::int,
		       COALESCE(SUM(total_precio) FILTER (WHERE estado NOT IN ('Aceptada','Ganada','Perdida','Cancelada','Vencida')),0),
		       COUNT(*) FILTER (WHERE estado='Aceptada')::int,
		       COALESCE(SUM(total_precio) FILTER (WHERE estado='Aceptada'),0),
		       COUNT(*) FILTER (WHERE estado='Ganada')::int,
		       COALESCE(SUM(total_precio) FILTER (WHERE estado='Ganada'),0),
		       COUNT(*) FILTER (WHERE estado='Perdida')::int,
		       COUNT(*) FILTER (WHERE estado IN ('Vencida','Cancelada'))::int,
		       COALESCE(AVG(total_precio),0),
		       CASE WHEN $4 THEN AVG(margen_total) ELSE NULL END
		  FROM filtradas`, calculadoraID, vendedorID, tipoCliente, puedeVerMargen).Scan(
		&k.CotizacionesMes, &k.Vigentes, &k.MontoCotizado,
		&k.Aceptadas, &k.MontoAceptado, &k.Ganadas, &k.MontoGanado,
		&k.Perdidas, &k.VencidasCanceladas, &k.TicketPromedioGeneral, &margen,
	)
	if err != nil {
		log.Printf("dashboard: error agregando indicadores: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el dashboard."})
		return
	}
	if puedeVerMargen {
		k.MargenPromedio = margen
	}

	rows, err := h.DB.Query(ctx, dashboardFiltradoCTE+`
		SELECT cotizacion_id, codigo_oferta, cliente, empresa,
		       calculadora_id, nombre_calculadora, estado, total_precio, moneda,
		       vendedor_id, vendedor, tipo_cliente, fecha_creacion, fecha_actualizacion,
		       CASE WHEN $4 THEN margen_total ELSE NULL END
		  FROM filtradas
		 ORDER BY fecha_creacion, cotizacion_id`, calculadoraID, vendedorID, tipoCliente, puedeVerMargen)
	if err != nil {
		log.Printf("dashboard: error consultando cotizaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el dashboard."})
		return
	}
	defer rows.Close()
	items := make([]dashboardCotizacion, 0)
	for rows.Next() {
		var item dashboardCotizacion
		if err := rows.Scan(&item.CotizacionID, &item.CodigoOferta, &item.Cliente, &item.Empresa,
			&item.CalculadoraID, &item.CalculadoraNombre, &item.Estado, &item.TotalPrecio, &item.Moneda,
			&item.VendedorID, &item.Vendedor, &item.TipoCliente, &item.FechaCreacion,
			&item.FechaActualizacion, &item.MargenTotal); err != nil {
			log.Printf("dashboard: error leyendo cotización: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el dashboard."})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("dashboard: error recorriendo cotizaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el dashboard."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "kpis": k, "cotizaciones_recientes": items})
}
