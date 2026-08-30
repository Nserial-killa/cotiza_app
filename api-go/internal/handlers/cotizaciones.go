package handlers

// CotizacionesHandler es el "cascarón" administrativo del Gestor de
// Cotizaciones (esquema 0007, Sprint 4): listar, ver detalle, cambiar
// estado, crear una versión nueva. Llenar una cotización de verdad
// (el Cotizador en modo de uso real) depende de un motor de ejecución
// que todavía no existe, y la publicación al cliente (link público)
// tampoco — ninguna de las dos se implementa acá. Las cotizaciones
// nuevas nacen desde una Solicitud (sprint futuro); este handler no
// tiene un POST de creación.

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

type CotizacionesHandler struct {
	DB *pgxpool.Pool
}

// estadosCotizacionValidos refleja el CHECK de
// 0007_cotizaciones_shell.sql, que a su vez copia el arreglo ESTADOS
// de cotiza_scripts.html. Si ese arreglo cambia, este mapa y el CHECK
// tienen que cambiar junto con él.
var estadosCotizacionValidos = map[string]bool{
	"Borrador": true, "Revisión Comercial": true, "Enviada al Cliente": true,
	"Vista por el Cliente": true, "Cambios solicitados": true, "Aceptada": true,
	"Ganada": true, "Perdida": true, "Vencida": true, "Cancelada": true,
}

// estadosCotizacionBloqueados son los estados finales en los que ya
// no tiene sentido seguir editando esa versión.
var estadosCotizacionBloqueados = map[string]bool{
	"Aceptada": true, "Ganada": true, "Perdida": true, "Vencida": true, "Cancelada": true,
}

type cotizacionListado struct {
	CotizacionID       string    `json:"cotizacion_id"`
	Version            int       `json:"version"`
	NombreVersion      *string   `json:"nombre_version,omitempty"`
	VersionAceptada    *int      `json:"version_aceptada,omitempty"`
	CodigoOferta       *string   `json:"codigo_oferta,omitempty"`
	Cliente            *string   `json:"cliente,omitempty"`
	Empresa            *string   `json:"empresa,omitempty"`
	CalculadoraNombre  string    `json:"calculadora_nombre"`
	CalculadoraID      string    `json:"calculadora_id"`
	TipoPropuesta      *string   `json:"tipo_propuesta,omitempty"`
	Estado             string    `json:"estado"`
	TotalPrecio        float64   `json:"total_precio"`
	Moneda             string    `json:"moneda"`
	Vendedor           *string   `json:"vendedor,omitempty"`
	Analista           *string   `json:"analista,omitempty"`
	FechaActualizacion time.Time `json:"fecha_actualizacion"`
}

// Listar responde GET /api/cotizaciones con los filtros que ya usa la
// pantalla (busqueda, estado, calculadora_id, filtro_usuario_id,
// fecha_desde) — ver webBuscarCotizacionesV2 en cotiza_scripts.html.
func (h *CotizacionesHandler) Listar(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	q := r.URL.Query()
	busqueda := strings.TrimSpace(q.Get("busqueda"))
	estado := strings.TrimSpace(q.Get("estado"))
	calculadoraID := strings.TrimSpace(q.Get("calculadora_id"))
	filtroUsuarioID := strings.TrimSpace(q.Get("filtro_usuario_id"))
	fechaDesde := strings.TrimSpace(q.Get("fecha_desde"))

	rows, err := h.DB.Query(ctx, `
		SELECT c.cotizacion_id, c.version_actual, cv.nombre_version, c.version_aceptada,
		       c.codigo_oferta, cl.nombre_comercial, COALESCE(cl.razon_social, cl.nombre_comercial),
		       calc.nombre_calculadora, c.calculadora_id, c.tipo_propuesta, c.estado,
		       COALESCE(cv.total_precio, 0), COALESCE(cv.moneda, 'US$'),
		       STRING_AGG(DISTINCT CASE WHEN cu.funcion = 'Vendedor' THEN u.nombre END, ', '),
		       STRING_AGG(DISTINCT CASE WHEN cu.funcion = 'Analista' THEN u.nombre END, ', '),
		       c.fecha_actualizacion
		  FROM cotizaciones c
		  JOIN calculadoras calc ON calc.calculadora_id = c.calculadora_id
		  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
		  LEFT JOIN cotizacion_versiones cv ON cv.cotizacion_id = c.cotizacion_id AND cv.numero_version = c.version_actual
		  LEFT JOIN cotizacion_usuarios cu ON cu.cotizacion_id = c.cotizacion_id
		  LEFT JOIN usuarios u ON u.usuario_id = cu.usuario_id
		 WHERE ($1 = '' OR c.codigo_oferta ILIKE '%' || $1 || '%'
		        OR cl.nombre_comercial ILIKE '%' || $1 || '%'
		        OR cl.razon_social ILIKE '%' || $1 || '%')
		   AND ($2 = '' OR c.estado = $2)
		   AND ($3 = '' OR c.calculadora_id = $3)
		   AND ($4 = '' OR EXISTS (
		        SELECT 1 FROM cotizacion_usuarios cu2
		         WHERE cu2.cotizacion_id = c.cotizacion_id AND cu2.usuario_id = $4))
		   AND ($5 = '' OR c.fecha_actualizacion::date >= $5::date)
		 GROUP BY c.cotizacion_id, c.version_actual, cv.nombre_version, c.version_aceptada,
		          c.codigo_oferta, cl.nombre_comercial, cl.razon_social, calc.nombre_calculadora,
		          c.calculadora_id, c.tipo_propuesta, c.estado, cv.total_precio, cv.moneda,
		          c.fecha_actualizacion
		 ORDER BY c.fecha_actualizacion DESC`,
		busqueda, estado, calculadoraID, filtroUsuarioID, fechaDesde)
	if err != nil {
		log.Printf("cotizaciones: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las cotizaciones."})
		return
	}
	defer rows.Close()

	items := make([]cotizacionListado, 0)
	for rows.Next() {
		var item cotizacionListado
		if err := rows.Scan(&item.CotizacionID, &item.Version, &item.NombreVersion, &item.VersionAceptada,
			&item.CodigoOferta, &item.Cliente, &item.Empresa, &item.CalculadoraNombre, &item.CalculadoraID,
			&item.TipoPropuesta, &item.Estado, &item.TotalPrecio, &item.Moneda,
			&item.Vendedor, &item.Analista, &item.FechaActualizacion); err != nil {
			log.Printf("cotizaciones: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las cotizaciones."})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("cotizaciones: error leyendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las cotizaciones."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "cotizaciones": items})
}

// Detalle responde GET /api/cotizaciones/{id} — la versión actual, o
// ?version=N para una versión puntual. Ver
// webObtenerGestionCotizacionV2/renderDetalleCotizacionV2 en
// cotiza_scripts.html para la forma exacta que espera la pantalla.
func (h *CotizacionesHandler) Detalle(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if cotizacionID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}
	versionSolicitada := strings.TrimSpace(r.URL.Query().Get("version"))

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var (
		calculadoraID, calculadoraNombre                    string
		codigoOferta, tipoPropuesta, cliente, empresa       *string
		versionActual                                       int
		versionAceptada                                     *int
		numeroVersion                                       int
		nombreVersion, resumenCambios                       *string
		estado, moneda                                      string
		totalPrecio, totalCosto, totalGanancia, margenTotal float64
		fechaAceptacion                                     *time.Time
		aceptadaPor, origenAceptacion                       *string
		fechaActualizacion                                  time.Time
	)

	err := h.DB.QueryRow(ctx, `
		SELECT c.calculadora_id, calc.nombre_calculadora, c.codigo_oferta, c.tipo_propuesta,
		       cl.nombre_comercial, COALESCE(cl.razon_social, cl.nombre_comercial),
		       c.version_actual, c.version_aceptada,
		       cv.numero_version, cv.nombre_version, cv.resumen_cambios, cv.estado, cv.moneda,
		       cv.total_precio, cv.total_costo, cv.total_ganancia, cv.margen_total,
		       cv.fecha_aceptacion, cv.aceptada_por, cv.origen_aceptacion, cv.fecha_actualizacion
		  FROM cotizaciones c
		  JOIN calculadoras calc ON calc.calculadora_id = c.calculadora_id
		  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id = c.cotizacion_id
		       AND cv.numero_version = CASE WHEN $2 = '' THEN c.version_actual ELSE $2::int END
		 WHERE c.cotizacion_id = $1`,
		cotizacionID, versionSolicitada,
	).Scan(&calculadoraID, &calculadoraNombre, &codigoOferta, &tipoPropuesta, &cliente, &empresa,
		&versionActual, &versionAceptada,
		&numeroVersion, &nombreVersion, &resumenCambios, &estado, &moneda,
		&totalPrecio, &totalCosto, &totalGanancia, &margenTotal,
		&fechaAceptacion, &aceptadaPor, &origenAceptacion, &fechaActualizacion)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cotización no encontrada."})
		return
	case err != nil:
		log.Printf("cotizaciones: error consultando detalle de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la cotización."})
		return
	}

	vendedor, analista, liderProducto, err := h.consultarPersonasAsignadas(ctx, cotizacionID)
	if err != nil {
		log.Printf("cotizaciones: error consultando personas asignadas de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la cotización."})
		return
	}

	versiones, err := h.consultarVersiones(ctx, cotizacionID)
	if err != nil {
		log.Printf("cotizaciones: error consultando versiones de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la cotización."})
		return
	}

	historial, err := h.consultarHistorial(ctx, cotizacionID)
	if err != nil {
		log.Printf("cotizaciones: error consultando historial de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la cotización."})
		return
	}

	cotizacion := map[string]any{
		"cotizacion_id":       cotizacionID,
		"calculadora_id":      calculadoraID,
		"calculadora_nombre":  calculadoraNombre,
		"codigo_oferta":       valorTexto(codigoOferta),
		"tipo_propuesta":      valorTexto(tipoPropuesta),
		"cliente":             valorTexto(cliente),
		"empresa":             valorTexto(empresa),
		"version":             numeroVersion,
		"version_actual":      versionActual,
		"version_aceptada":    valorIntPtr(versionAceptada),
		"nombre_version":      valorTexto(nombreVersion),
		"resumen_cambios":     valorTexto(resumenCambios),
		"estado":              estado,
		"puede_editar":        !estadosCotizacionBloqueados[estado],
		"moneda":              moneda,
		"total_precio":        totalPrecio,
		"total_costo":         totalCosto,
		"total_ganancia":      totalGanancia,
		"margen_total":        margenTotal,
		"vendedor":            vendedor,
		"analista":            analista,
		"lider_producto":      liderProducto,
		"fecha_aceptacion":    valorFechaPtr(fechaAceptacion),
		"aceptada_por":        valorTexto(aceptadaPor),
		"origen_aceptacion":   valorTexto(origenAceptacion),
		"fecha_actualizacion": fechaActualizacion,
	}

	// La publicación al cliente (link público) es de un sprint futuro
	// — se deja vacío a propósito, no simulado.
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"cotizacion": cotizacion,
		"versiones":  versiones,
		"historial":  historial,
		"links":      []any{},
	})
}

type versionResumen struct {
	NumeroVersion      int       `json:"numero_version"`
	NombreVersion      *string   `json:"nombre_version,omitempty"`
	Estado             string    `json:"estado"`
	FechaCreacion      time.Time `json:"fecha_creacion"`
	FechaActualizacion time.Time `json:"fecha_actualizacion"`
	ResumenCambios     *string   `json:"resumen_cambios,omitempty"`
	TotalPrecio        float64   `json:"total_precio"`
	Moneda             string    `json:"moneda"`
}

func (h *CotizacionesHandler) consultarVersiones(ctx context.Context, cotizacionID string) ([]versionResumen, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT numero_version, nombre_version, estado, fecha_creacion, fecha_actualizacion,
		       resumen_cambios, total_precio, moneda
		  FROM cotizacion_versiones
		 WHERE cotizacion_id = $1
		 ORDER BY numero_version DESC`, cotizacionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versiones := make([]versionResumen, 0)
	for rows.Next() {
		var v versionResumen
		if err := rows.Scan(&v.NumeroVersion, &v.NombreVersion, &v.Estado, &v.FechaCreacion,
			&v.FechaActualizacion, &v.ResumenCambios, &v.TotalPrecio, &v.Moneda); err != nil {
			return nil, err
		}
		versiones = append(versiones, v)
	}
	return versiones, rows.Err()
}

type eventoHistorial struct {
	Accion        string    `json:"accion"`
	NombreUsuario *string   `json:"nombre_usuario,omitempty"`
	Fecha         time.Time `json:"fecha"`
	EstadoNuevo   *string   `json:"estado_nuevo,omitempty"`
}

func (h *CotizacionesHandler) consultarHistorial(ctx context.Context, cotizacionID string) ([]eventoHistorial, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT ch.accion, u.nombre, ch.fecha, ch.estado_nuevo
		  FROM cotizacion_historial ch
		  LEFT JOIN usuarios u ON u.usuario_id = ch.usuario_id
		 WHERE ch.cotizacion_id = $1
		 ORDER BY ch.fecha DESC
		 LIMIT 50`, cotizacionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	historial := make([]eventoHistorial, 0)
	for rows.Next() {
		var ev eventoHistorial
		if err := rows.Scan(&ev.Accion, &ev.NombreUsuario, &ev.Fecha, &ev.EstadoNuevo); err != nil {
			return nil, err
		}
		historial = append(historial, ev)
	}
	return historial, rows.Err()
}

func (h *CotizacionesHandler) consultarPersonasAsignadas(ctx context.Context, cotizacionID string) (vendedor, analista, liderProducto *string, err error) {
	rows, err := h.DB.Query(ctx, `
		SELECT cu.funcion, STRING_AGG(u.nombre, ', ')
		  FROM cotizacion_usuarios cu
		  JOIN usuarios u ON u.usuario_id = cu.usuario_id
		 WHERE cu.cotizacion_id = $1
		 GROUP BY cu.funcion`, cotizacionID)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var funcion string
		var nombres string
		if err := rows.Scan(&funcion, &nombres); err != nil {
			return nil, nil, nil, err
		}
		switch funcion {
		case "Vendedor":
			vendedor = &nombres
		case "Analista":
			analista = &nombres
		case "Líder de producto":
			liderProducto = &nombres
		}
	}
	return vendedor, analista, liderProducto, rows.Err()
}

type crearVersionRequest struct {
	NombreVersion  string `json:"nombre_version"`
	ResumenCambios string `json:"resumen_cambios"`
}

// CrearVersion responde POST /api/cotizaciones/{id}/version. La nueva
// versión arranca como copia de los totales de la versión actual —
// todavía no hay motor de ejecución que los recalcule; "editar" una
// cotización de verdad es un sprint futuro.
func (h *CotizacionesHandler) CrearVersion(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if cotizacionID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}

	var req crearVersionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.NombreVersion = strings.TrimSpace(req.NombreVersion)
	req.ResumenCambios = strings.TrimSpace(req.ResumenCambios)
	if req.NombreVersion == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el nombre de la nueva versión."})
		return
	}

	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la transacción."})
		return
	}
	defer tx.Rollback(ctx)

	var versionActual int
	var estadoActual string
	err = tx.QueryRow(ctx, `
		SELECT version_actual, estado FROM cotizaciones WHERE cotizacion_id = $1 FOR UPDATE`,
		cotizacionID,
	).Scan(&versionActual, &estadoActual)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cotización no encontrada."})
		return
	}
	if err != nil {
		log.Printf("cotizaciones: error leyendo %s antes de versionar: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la versión."})
		return
	}

	var moneda string
	var totalPrecio, totalCosto, totalGanancia, margenTotal float64
	err = tx.QueryRow(ctx, `
		SELECT moneda, total_precio, total_costo, total_ganancia, margen_total
		  FROM cotizacion_versiones WHERE cotizacion_id = $1 AND numero_version = $2`,
		cotizacionID, versionActual,
	).Scan(&moneda, &totalPrecio, &totalCosto, &totalGanancia, &margenTotal)
	if err != nil {
		log.Printf("cotizaciones: error leyendo versión actual de %s antes de versionar: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la versión."})
		return
	}

	nuevaVersion := versionActual + 1

	_, err = tx.Exec(ctx, `
		INSERT INTO cotizacion_versiones
			(cotizacion_id, numero_version, nombre_version, resumen_cambios, estado, moneda,
			 total_precio, total_costo, total_ganancia, margen_total)
		VALUES ($1, $2, $3, NULLIF($4, ''), 'Borrador', $5, $6, $7, $8, $9)`,
		cotizacionID, nuevaVersion, req.NombreVersion, req.ResumenCambios, moneda,
		totalPrecio, totalCosto, totalGanancia, margenTotal)
	if err != nil {
		log.Printf("cotizaciones: error insertando versión %d de %s: %v", nuevaVersion, cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la versión."})
		return
	}

	_, err = tx.Exec(ctx, `
		UPDATE cotizaciones SET version_actual = $2, estado = 'Borrador' WHERE cotizacion_id = $1`,
		cotizacionID, nuevaVersion)
	if err != nil {
		log.Printf("cotizaciones: error actualizando version_actual de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la versión."})
		return
	}

	if err := insertarHistorial(ctx, tx, cotizacionID, &nuevaVersion, "CREAR_VERSION", &estadoActual, strPtr("Borrador"), req.ResumenCambios, usuarioID); err != nil {
		log.Printf("cotizaciones: error registrando historial de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la versión."})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("cotizaciones: error confirmando versión nueva de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear la versión."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "cotizacion_id": cotizacionID, "version": nuevaVersion,
		"nombre_version": req.NombreVersion, "resumen_cambios": req.ResumenCambios,
	})
}

type cambiarEstadoRequest struct {
	Version         enteroFlexible `json:"version"`
	Estado          string         `json:"estado"`
	VersionAceptada enteroFlexible `json:"version_aceptada"`
	Comentario      string         `json:"comentario"`
	AceptadaPor     string         `json:"aceptada_por"`
}

// CambiarEstado responde POST /api/cotizaciones/{id}/estado. Cubre
// tanto el cambio de estado directo (webActualizarEstadoCotizacion)
// como las transiciones de webEjecutarAccionCotizacion que se
// reducen limpiamente a "cambiar el estado de una versión" —
// REGISTRAR_ACEPTACION (-> Aceptada) y MARCAR_GANADA (-> Ganada). Las
// que dependen del estado "Liberada" (LIBERAR_COTIZACION,
// DEVOLVER_CONSTRUCCION, MARCAR_ENVIADA) no tienen ese estado en el
// CHECK de la tabla — cotiza_scripts.html las deja en "disponible
// próximamente" en vez de forzarlas a medias acá.
func (h *CotizacionesHandler) CambiarEstado(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if cotizacionID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}

	var req cambiarEstadoRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Estado = strings.TrimSpace(req.Estado)
	req.Comentario = strings.TrimSpace(req.Comentario)
	req.AceptadaPor = strings.TrimSpace(req.AceptadaPor)
	version := int(req.Version)

	if version <= 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la versión."})
		return
	}
	if !estadosCotizacionValidos[req.Estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Estado no válido."})
		return
	}

	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la transacción."})
		return
	}
	defer tx.Rollback(ctx)

	var estadoAnterior string
	err = tx.QueryRow(ctx, `
		SELECT estado FROM cotizacion_versiones
		 WHERE cotizacion_id = $1 AND numero_version = $2 FOR UPDATE`,
		cotizacionID, version,
	).Scan(&estadoAnterior)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Versión no encontrada."})
		return
	}
	if err != nil {
		log.Printf("cotizaciones: error leyendo versión %d de %s: %v", version, cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
		return
	}

	var versionActual int
	if err := tx.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&versionActual); err != nil {
		log.Printf("cotizaciones: error leyendo version_actual de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
		return
	}

	if req.Estado == "Aceptada" {
		_, err = tx.Exec(ctx, `
			UPDATE cotizacion_versiones
			   SET estado = $3, fecha_aceptacion = now(), aceptada_por = NULLIF($4, '')
			 WHERE cotizacion_id = $1 AND numero_version = $2`,
			cotizacionID, version, req.Estado, req.AceptadaPor)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE cotizacion_versiones SET estado = $3
			 WHERE cotizacion_id = $1 AND numero_version = $2`,
			cotizacionID, version, req.Estado)
	}
	if err != nil {
		log.Printf("cotizaciones: error actualizando estado de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
		return
	}

	// version_aceptada: al aceptar, la versión aceptada es esta misma
	// (el servidor la fija, no se confía en lo que mande el cliente).
	// Al marcar como Ganada, se acepta la que el cliente indique
	// (siempre que ya exista como versión de esta cotización).
	nuevaVersionAceptada := 0
	switch req.Estado {
	case "Aceptada":
		nuevaVersionAceptada = version
	case "Ganada":
		nuevaVersionAceptada = int(req.VersionAceptada)
	}
	if nuevaVersionAceptada > 0 {
		var existeVersion bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM cotizacion_versiones WHERE cotizacion_id = $1 AND numero_version = $2)`,
			cotizacionID, nuevaVersionAceptada,
		).Scan(&existeVersion); err != nil {
			log.Printf("cotizaciones: error validando version_aceptada de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
			return
		}
		if !existeVersion {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La versión aceptada indicada no existe."})
			return
		}
		if _, err := tx.Exec(ctx, `UPDATE cotizaciones SET version_aceptada = $2 WHERE cotizacion_id = $1`, cotizacionID, nuevaVersionAceptada); err != nil {
			log.Printf("cotizaciones: error fijando version_aceptada de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
			return
		}
	}

	// El estado de "cotizaciones" es una copia del de la versión
	// activa — solo se toca si la versión que cambió es esa.
	if version == versionActual {
		if _, err := tx.Exec(ctx, `UPDATE cotizaciones SET estado = $2 WHERE cotizacion_id = $1`, cotizacionID, req.Estado); err != nil {
			log.Printf("cotizaciones: error sincronizando estado de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
			return
		}
	}

	if err := insertarHistorial(ctx, tx, cotizacionID, &version, "CAMBIO_ESTADO", &estadoAnterior, &req.Estado, req.Comentario, usuarioID); err != nil {
		log.Printf("cotizaciones: error registrando historial de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("cotizaciones: error confirmando cambio de estado de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cambiar el estado."})
		return
	}

	respuesta := map[string]any{"ok": true, "cotizacion_id": cotizacionID, "version": version, "estado": req.Estado}
	if nuevaVersionAceptada > 0 {
		respuesta["version_aceptada"] = nuevaVersionAceptada
	}
	escribirJSON(w, http.StatusOK, respuesta)
}

func insertarHistorial(ctx context.Context, tx pgx.Tx, cotizacionID string, numeroVersion *int, accion string, estadoAnterior, estadoNuevo *string, comentario, usuarioID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO cotizacion_historial
			(cotizacion_id, numero_version, accion, estado_anterior, estado_nuevo, comentario, usuario_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''))`,
		cotizacionID, numeroVersion, accion, estadoAnterior, estadoNuevo, comentario, usuarioID)
	return err
}

func valorTexto(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func valorIntPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func valorFechaPtr(p *time.Time) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtr(s string) *string { return &s }
