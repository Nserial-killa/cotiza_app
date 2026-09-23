package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

// DashboardHandler agrega exclusivamente salidas normalizadas. El Dashboard
// no consulta snapshot_json, cotizacion_valores ni las columnas de totales de
// cotizacion_versiones; estas últimas quedan como caché de compatibilidad.
type DashboardHandler struct{ DB *pgxpool.Pool }

var errDashboardSinSesion = errors.New("la sesión no tiene usuario")

type dashboardFiltros struct {
	CalculadoraID string
	VendedorID    string
	TipoCliente   string
	FechaDesde    string
	FechaHasta    string
}

type dashboardMonto struct {
	Moneda string  `json:"moneda"`
	Monto  float64 `json:"monto"`
}

type dashboardResumen struct {
	Cotizaciones            int              `json:"cotizaciones"`
	CotizacionesVigentes    int              `json:"cotizaciones_vigentes"`
	MontosCotizados         []dashboardMonto `json:"montos_cotizados"`
	Aceptadas               int              `json:"aceptadas"`
	MontosAceptados         []dashboardMonto `json:"montos_aceptados"`
	Ganadas                 int              `json:"ganadas"`
	MontosGanados           []dashboardMonto `json:"montos_ganados"`
	Perdidas                int              `json:"perdidas"`
	VencidasCanceladas      int              `json:"vencidas_canceladas"`
	Prospectos              int              `json:"prospectos"`
	ClientesExistentes      int              `json:"clientes_existentes"`
	TicketsPromedio         []dashboardMonto `json:"tickets_promedio"`
	MargenPromedio          *float64         `json:"margen_promedio,omitempty"`
	TiempoPromedioCicloDias *float64         `json:"tiempo_promedio_ciclo_dias,omitempty"`
	TasaAceptacion          float64          `json:"tasa_aceptacion"`
	TasaGanancia            float64          `json:"tasa_ganancia"`
}

type dashboardSerie struct {
	Clave          string           `json:"clave"`
	Etiqueta       string           `json:"etiqueta"`
	Cantidad       int              `json:"cantidad"`
	Montos         []dashboardMonto `json:"montos"`
	MargenPromedio *float64         `json:"margen_promedio,omitempty"`
}

type dashboardCotizador struct {
	CalculadoraID     string           `json:"calculadora_id"`
	NombreCalculadora string           `json:"nombre_calculadora"`
	Cantidad          int              `json:"cantidad"`
	Montos            []dashboardMonto `json:"montos"`
	MargenPromedio    *float64         `json:"margen_promedio,omitempty"`
}

// dashboardFiltradoCTE conserva una sola definición de versión vigente,
// segmentación y vendedor para Dashboard y Reportes. TOTAL_PRECIO,
// MARGEN_TOTAL y MONEDA proceden únicamente de cotizacion_salidas.
// El monto aceptado se obtiene expresamente de version_aceptada (AT-04).
const dashboardFiltradoCTE = `
	WITH historico AS (
		SELECT c.cotizacion_id, c.calculadora_id, c.cliente_id, c.codigo_oferta,
		       c.estado, c.fecha_creacion, c.fecha_actualizacion, c.version_actual,
		       c.version_aceptada,
		       COUNT(*) OVER (
		         PARTITION BY c.cliente_id
		         ORDER BY c.fecha_creacion, c.cotizacion_id
		         ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
		       ) AS cotizaciones_previas
		  FROM cotizaciones c
	), base AS (
		SELECT h.*,
		       precio.valor_numero::double precision AS total_precio,
		       CASE WHEN precio.valor_numero IS NULL THEN NULL
		            ELSE COALESCE(NULLIF(precio.moneda,''), moneda.valor_texto, 'SIN_MONEDA') END AS moneda,
		       margen.valor_numero::double precision AS margen_total,
		       aceptado.valor_numero::double precision AS total_aceptado,
		       COALESCE(NULLIF(aceptado.moneda,''), moneda_aceptada.valor_texto, 'SIN_MONEDA') AS moneda_aceptada,
		       calc.nombre_calculadora,
		       cl.nombre_comercial AS cliente,
		       COALESCE(cl.razon_social, cl.nombre_comercial) AS empresa,
		       vendedor.usuario_id AS vendedor_id, vendedor.nombre AS vendedor,
		       CASE WHEN h.cliente_id IS NOT NULL AND h.cotizaciones_previas > 0
		            THEN 'Cliente existente' ELSE 'Prospecto' END AS tipo_cliente,
		       cierre.fecha_cierre,
		       (h.estado IN ('Enviada al Cliente','Vista por el Cliente','Cambios solicitados','Aceptada','Ganada','Perdida','Cancelada','Vencida')
		        OR EXISTS (SELECT 1 FROM cotizacion_historial he
		                    WHERE he.cotizacion_id=h.cotizacion_id
		                      AND he.estado_nuevo IN ('Enviada al Cliente','Vista por el Cliente','Cambios solicitados','Aceptada','Ganada','Perdida','Cancelada','Vencida'))) AS ingreso_embudo
		  FROM historico h
		  JOIN calculadoras calc ON calc.calculadora_id=h.calculadora_id
		  LEFT JOIN clientes cl ON cl.cliente_id=h.cliente_id
		  LEFT JOIN cotizacion_salidas precio
		    ON precio.cotizacion_id=h.cotizacion_id AND precio.numero_version=h.version_actual
		   AND precio.clave_salida='TOTAL_PRECIO'
		  LEFT JOIN cotizacion_salidas margen
		    ON margen.cotizacion_id=h.cotizacion_id AND margen.numero_version=h.version_actual
		   AND margen.clave_salida='MARGEN_TOTAL'
		  LEFT JOIN cotizacion_salidas moneda
		    ON moneda.cotizacion_id=h.cotizacion_id AND moneda.numero_version=h.version_actual
		   AND moneda.clave_salida='MONEDA'
		  LEFT JOIN cotizacion_salidas aceptado
		    ON aceptado.cotizacion_id=h.cotizacion_id AND aceptado.numero_version=h.version_aceptada
		   AND aceptado.clave_salida='TOTAL_PRECIO'
		  LEFT JOIN cotizacion_salidas moneda_aceptada
		    ON moneda_aceptada.cotizacion_id=h.cotizacion_id AND moneda_aceptada.numero_version=h.version_aceptada
		   AND moneda_aceptada.clave_salida='MONEDA'
		  LEFT JOIN LATERAL (
		    SELECT MIN(cu.usuario_id) AS usuario_id, MIN(u.nombre) AS nombre
		      FROM cotizacion_usuarios cu
		      JOIN usuarios u ON u.usuario_id=cu.usuario_id
		     WHERE cu.cotizacion_id=h.cotizacion_id AND cu.funcion='Vendedor'
		  ) vendedor ON true
		  LEFT JOIN LATERAL (
		    -- El historial no garantiza un evento inicial uniforme. Se usa
		    -- cotizaciones.fecha_creacion como inicio y el primer cambio a un
		    -- estado de cierre como fin del ciclo comercial.
		    SELECT MIN(ch.fecha) AS fecha_cierre
		      FROM cotizacion_historial ch
		     WHERE ch.cotizacion_id=h.cotizacion_id
		       AND ch.estado_nuevo IN ('Aceptada','Ganada','Perdida','Vencida','Cancelada')
		  ) cierre ON true
	), filtradas AS (
		SELECT * FROM base
		 WHERE ($1='' OR calculadora_id=$1)
		   AND ($2='' OR EXISTS (
		       SELECT 1 FROM cotizacion_usuarios cu
		        WHERE cu.cotizacion_id=base.cotizacion_id
		          AND cu.funcion='Vendedor' AND cu.usuario_id=$2))
		   AND ($3='' OR tipo_cliente=$3)
	)`

const dashboardFiltroFechas = `
	 WHERE ($4='' OR fecha_creacion >= $4::date)
	   AND ($5='' OR fecha_creacion < ($5::date + INTERVAL '1 day'))`

func leerFiltrosDashboard(r *http.Request) (dashboardFiltros, error) {
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if calculadoraID == "" {
		// El Anexo denomina cotizador_id al identificador que el esquema
		// físico ya expone como calculadora_id. Se aceptan ambos nombres.
		calculadoraID = strings.TrimSpace(r.URL.Query().Get("cotizador_id"))
	}
	f := dashboardFiltros{
		CalculadoraID: calculadoraID,
		VendedorID:    strings.TrimSpace(r.URL.Query().Get("vendedor_id")),
		TipoCliente:   strings.TrimSpace(r.URL.Query().Get("tipo_cliente")),
		FechaDesde:    strings.TrimSpace(r.URL.Query().Get("fecha_desde")),
		FechaHasta:    strings.TrimSpace(r.URL.Query().Get("fecha_hasta")),
	}
	if f.TipoCliente != "" && f.TipoCliente != "Prospecto" && f.TipoCliente != "Cliente existente" {
		return f, errors.New("tipo_cliente debe ser Prospecto o Cliente existente")
	}
	if err := validarFechaReporte(f.FechaDesde, "fecha_desde"); err != nil {
		return f, err
	}
	if err := validarFechaReporte(f.FechaHasta, "fecha_hasta"); err != nil {
		return f, err
	}
	if f.FechaDesde != "" && f.FechaHasta != "" && f.FechaDesde > f.FechaHasta {
		return f, errors.New("fecha_desde no puede ser posterior a fecha_hasta")
	}
	return f, nil
}

func (h *DashboardHandler) preparar(r *http.Request) (context.Context, context.CancelFunc, dashboardFiltros, bool, error) {
	f, err := leerFiltrosDashboard(r)
	if err != nil {
		return nil, func() {}, f, false, err
	}
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		return nil, func() {}, f, false, errDashboardSinSesion
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	permiso, err := (&CotizacionesHandler{DB: h.DB}).sesionPuedeVerPrice(ctx, r)
	if err != nil {
		cancel()
		return nil, func() {}, f, false, err
	}
	return ctx, cancel, f, permiso, nil
}

func argumentosDashboard(f dashboardFiltros, adicionales ...any) []any {
	args := []any{f.CalculadoraID, f.VendedorID, f.TipoCliente, f.FechaDesde, f.FechaHasta}
	return append(args, adicionales...)
}

func (h *DashboardHandler) fallo(w http.ResponseWriter, mensaje string, err error) {
	log.Printf("dashboard: %s: %v", mensaje, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el dashboard."})
}

// Obtener conserva temporalmente GET /api/dashboard como alias de resumen
// para clientes antiguos. El frontend actual usa los cinco endpoints nuevos.
func (h *DashboardHandler) Obtener(w http.ResponseWriter, r *http.Request) { h.Resumen(w, r) }

func (h *DashboardHandler) Resumen(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, f, puedeVerMargen, err := h.preparar(r)
	if err != nil {
		h.responderErrorEntrada(w, err)
		return
	}
	defer cancel()

	var res dashboardResumen
	err = h.DB.QueryRow(ctx, dashboardFiltradoCTE+`, alcance AS (SELECT * FROM filtradas`+dashboardFiltroFechas+`)
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE estado NOT IN ('Aceptada','Ganada','Perdida','Cancelada','Vencida'))::int,
		       COUNT(*) FILTER (WHERE version_aceptada IS NOT NULL)::int,
		       COUNT(*) FILTER (WHERE estado='Ganada')::int,
		       COUNT(*) FILTER (WHERE estado='Perdida')::int,
		       COUNT(*) FILTER (WHERE estado IN ('Vencida','Cancelada'))::int,
		       COUNT(*) FILTER (WHERE tipo_cliente='Prospecto')::int,
		       COUNT(*) FILTER (WHERE tipo_cliente='Cliente existente')::int,
		       AVG(margen_total) FILTER (WHERE $6),
		       AVG(EXTRACT(EPOCH FROM (fecha_cierre-fecha_creacion))/86400.0),
		       CASE WHEN COUNT(*) FILTER (WHERE ingreso_embudo)>0
		            THEN 100.0*COUNT(*) FILTER (WHERE version_aceptada IS NOT NULL)/COUNT(*) FILTER (WHERE ingreso_embudo) ELSE 0 END,
		       CASE WHEN COUNT(*) FILTER (WHERE ingreso_embudo)>0
		            THEN 100.0*COUNT(*) FILTER (WHERE estado='Ganada')/COUNT(*) FILTER (WHERE ingreso_embudo) ELSE 0 END
		  FROM alcance`, argumentosDashboard(f, puedeVerMargen)...).Scan(
		&res.Cotizaciones, &res.CotizacionesVigentes, &res.Aceptadas, &res.Ganadas,
		&res.Perdidas, &res.VencidasCanceladas, &res.Prospectos, &res.ClientesExistentes,
		&res.MargenPromedio, &res.TiempoPromedioCicloDias, &res.TasaAceptacion, &res.TasaGanancia)
	if err != nil {
		h.fallo(w, "agregando resumen", err)
		return
	}
	res.MontosCotizados, err = h.consultarMontos(ctx, f, `total_precio IS NOT NULL AND estado NOT IN ('Aceptada','Ganada','Perdida','Cancelada','Vencida')`, "moneda", "total_precio", false)
	if err == nil {
		res.MontosAceptados, err = h.consultarMontos(ctx, f, "version_aceptada IS NOT NULL AND total_aceptado IS NOT NULL", "moneda_aceptada", "total_aceptado", false)
	}
	if err == nil {
		res.MontosGanados, err = h.consultarMontos(ctx, f, "estado='Ganada' AND total_aceptado IS NOT NULL", "moneda_aceptada", "total_aceptado", false)
	}
	if err == nil {
		res.TicketsPromedio, err = h.consultarMontos(ctx, f, "total_precio IS NOT NULL", "moneda", "total_precio", true)
	}
	if err != nil {
		h.fallo(w, "agregando montos por moneda", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "resumen": res,
		"monedas": monedasDeMontos(res.MontosCotizados, res.MontosAceptados, res.MontosGanados, res.TicketsPromedio),
	})
}

func (h *DashboardHandler) consultarMontos(ctx context.Context, f dashboardFiltros, condicion, campoMoneda, campoMonto string, promedio bool) ([]dashboardMonto, error) {
	agregado := "SUM(" + campoMonto + ")"
	if promedio {
		agregado = "AVG(" + campoMonto + ")"
	}
	query := dashboardFiltradoCTE + `, alcance AS (SELECT * FROM filtradas` + dashboardFiltroFechas + `)
		SELECT ` + campoMoneda + `, COALESCE(` + agregado + `,0)::double precision
		  FROM alcance WHERE ` + condicion + ` GROUP BY ` + campoMoneda + ` ORDER BY ` + campoMoneda
	rows, err := h.DB.Query(ctx, query, argumentosDashboard(f)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultado := []dashboardMonto{}
	for rows.Next() {
		var m dashboardMonto
		if err := rows.Scan(&m.Moneda, &m.Monto); err != nil {
			return nil, err
		}
		resultado = append(resultado, m)
	}
	return resultado, rows.Err()
}

func monedasDeMontos(grupos ...[]dashboardMonto) []string {
	unicas := map[string]bool{}
	for _, grupo := range grupos {
		for _, monto := range grupo {
			unicas[monto.Moneda] = true
		}
	}
	return clavesOrdenadas(unicas)
}

func (h *DashboardHandler) Tendencia(w http.ResponseWriter, r *http.Request) {
	h.responderSeries(w, r, "tendencia", `TO_CHAR(DATE_TRUNC('month',fecha_creacion),'YYYY-MM')`, `TO_CHAR(DATE_TRUNC('month',fecha_creacion),'Mon YYYY')`)
}

func (h *DashboardHandler) Estados(w http.ResponseWriter, r *http.Request) {
	h.responderSeries(w, r, "estados", "estado", "estado")
}

func (h *DashboardHandler) SegmentacionClientes(w http.ResponseWriter, r *http.Request) {
	h.responderSeries(w, r, "segmentos", "tipo_cliente", "tipo_cliente")
}

func (h *DashboardHandler) responderSeries(w http.ResponseWriter, r *http.Request, claveRespuesta, expresionClave, expresionEtiqueta string) {
	ctx, cancel, f, puedeVerMargen, err := h.preparar(r)
	if err != nil {
		h.responderErrorEntrada(w, err)
		return
	}
	defer cancel()
	query := dashboardFiltradoCTE + `, alcance AS (SELECT * FROM filtradas` + dashboardFiltroFechas + `), grupos AS (
		SELECT ` + expresionClave + ` AS clave, ` + expresionEtiqueta + ` AS etiqueta,
		       COUNT(*)::int AS cantidad,
		       AVG(margen_total) FILTER (WHERE $6)::double precision AS margen_promedio
		  FROM alcance GROUP BY 1,2
	), montos AS (
		SELECT ` + expresionClave + ` AS clave, moneda, SUM(total_precio)::double precision AS monto
		  FROM alcance WHERE total_precio IS NOT NULL GROUP BY 1,2
	)
	SELECT g.clave,g.etiqueta,g.cantidad,g.margen_promedio,m.moneda,m.monto
	  FROM grupos g LEFT JOIN montos m ON m.clave=g.clave ORDER BY g.clave,m.moneda`
	rows, err := h.DB.Query(ctx, query, argumentosDashboard(f, puedeVerMargen)...)
	if err != nil {
		h.fallo(w, "agregando "+claveRespuesta, err)
		return
	}
	defer rows.Close()
	orden := []string{}
	porClave := map[string]*dashboardSerie{}
	monedas := map[string]bool{}
	for rows.Next() {
		var clave, etiqueta string
		var cantidad int
		var margen *float64
		var moneda *string
		var monto *float64
		if err := rows.Scan(&clave, &etiqueta, &cantidad, &margen, &moneda, &monto); err != nil {
			h.fallo(w, "leyendo "+claveRespuesta, err)
			return
		}
		serie := porClave[clave]
		if serie == nil {
			serie = &dashboardSerie{Clave: clave, Etiqueta: etiqueta, Cantidad: cantidad, MargenPromedio: margen, Montos: []dashboardMonto{}}
			porClave[clave] = serie
			orden = append(orden, clave)
		}
		if moneda != nil && monto != nil {
			serie.Montos = append(serie.Montos, dashboardMonto{Moneda: *moneda, Monto: *monto})
			monedas[*moneda] = true
		}
	}
	if err := rows.Err(); err != nil {
		h.fallo(w, "recorriendo "+claveRespuesta, err)
		return
	}
	series := make([]dashboardSerie, 0, len(orden))
	for _, clave := range orden {
		series = append(series, *porClave[clave])
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, claveRespuesta: series, "monedas": clavesOrdenadas(monedas)})
}

func (h *DashboardHandler) Cotizadores(w http.ResponseWriter, r *http.Request) {
	ctx, cancel, f, puedeVerMargen, err := h.preparar(r)
	if err != nil {
		h.responderErrorEntrada(w, err)
		return
	}
	defer cancel()
	query := dashboardFiltradoCTE + `, alcance AS (SELECT * FROM filtradas` + dashboardFiltroFechas + `), grupos AS (
		SELECT calculadora_id,nombre_calculadora,COUNT(*)::int cantidad,
		       AVG(margen_total) FILTER (WHERE $6)::double precision margen_promedio
		  FROM alcance GROUP BY calculadora_id,nombre_calculadora
	), montos AS (
		SELECT calculadora_id,moneda,SUM(total_precio)::double precision monto
		  FROM alcance WHERE total_precio IS NOT NULL GROUP BY calculadora_id,moneda
	)
	SELECT g.calculadora_id,g.nombre_calculadora,g.cantidad,g.margen_promedio,m.moneda,m.monto
	  FROM grupos g LEFT JOIN montos m USING(calculadora_id) ORDER BY g.nombre_calculadora,m.moneda`
	rows, err := h.DB.Query(ctx, query, argumentosDashboard(f, puedeVerMargen)...)
	if err != nil {
		h.fallo(w, "agregando cotizadores", err)
		return
	}
	defer rows.Close()
	orden := []string{}
	porID := map[string]*dashboardCotizador{}
	monedas := map[string]bool{}
	for rows.Next() {
		var id, nombre string
		var cantidad int
		var margen *float64
		var moneda *string
		var monto *float64
		if err := rows.Scan(&id, &nombre, &cantidad, &margen, &moneda, &monto); err != nil {
			h.fallo(w, "leyendo cotizadores", err)
			return
		}
		item := porID[id]
		if item == nil {
			item = &dashboardCotizador{CalculadoraID: id, NombreCalculadora: nombre, Cantidad: cantidad, MargenPromedio: margen, Montos: []dashboardMonto{}}
			porID[id] = item
			orden = append(orden, id)
		}
		if moneda != nil && monto != nil {
			item.Montos = append(item.Montos, dashboardMonto{Moneda: *moneda, Monto: *monto})
			monedas[*moneda] = true
		}
	}
	if err := rows.Err(); err != nil {
		h.fallo(w, "recorriendo cotizadores", err)
		return
	}
	resultado := make([]dashboardCotizador, 0, len(orden))
	for _, id := range orden {
		resultado = append(resultado, *porID[id])
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "cotizadores": resultado, "monedas": clavesOrdenadas(monedas)})
}

func (h *DashboardHandler) responderErrorEntrada(w http.ResponseWriter, err error) {
	if _, ok := err.(*time.ParseError); ok || strings.Contains(err.Error(), "fecha_") || strings.Contains(err.Error(), "tipo_cliente") {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if errors.Is(err, errDashboardSinSesion) {
		escribirJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No fue posible identificar al usuario de la sesión."})
		return
	}
	h.fallo(w, "validando permisos", err)
}

func clavesOrdenadas(valores map[string]bool) []string {
	resultado := make([]string, 0, len(valores))
	for clave := range valores {
		resultado = append(resultado, clave)
	}
	sort.Strings(resultado)
	return resultado
}
