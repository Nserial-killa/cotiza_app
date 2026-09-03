package handlers

// EnlacesPublicosHandler cubre la Vista Previa / enlace público al
// cliente (versión acotada, esquema 0009): generar un token para una
// cotización+versión puntual, y la lectura sin sesión de esa versión
// como propuesta legible. No es el diseñador de plantillas
// personalizable (colores, secciones arrastrables) — eso queda para
// una fase posterior.
//
// Regla que no tiene excepción: esta vista NUNCA expone
// costo/ganancia/margen, sin importar el rol de quien generó el
// enlace — es para el cliente externo. Por eso la consulta de
// VerCotizacion ni siquiera selecciona esas columnas de
// cotizacion_versiones; no hay forma de que se filtren por accidente.

import (
	"context"
	"encoding/json"
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

type EnlacesPublicosHandler struct {
	DB *pgxpool.Pool
}

// mensajeEnlaceNoDisponible es intencionalmente el mismo para un
// token inexistente y uno vencido — mismo criterio anti-pistas que
// mensajeCredencialesInvalidas en auth.go.
const mensajeEnlaceNoDisponible = "Enlace no disponible."

type generarEnlaceRequest struct {
	Version enteroFlexible `json:"version"`
}

// GenerarEnlace responde POST /api/cotizaciones/{id}/enlace. Body
// opcional {"version":N}; sin ella, usa version_actual. Si ya existe
// un enlace para esa cotización+versión, lo reutiliza en vez de crear
// uno nuevo.
func (h *EnlacesPublicosHandler) GenerarEnlace(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if cotizacionID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}

	var req generarEnlaceRequest
	if r.ContentLength != 0 {
		if err := decodificarJSON(r, &req); err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	version := int(req.Version)

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if version <= 0 {
		if err := h.DB.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&version); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cotización no encontrada."})
				return
			}
			log.Printf("enlaces_publicos: error leyendo version_actual de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
			return
		}
	} else {
		var existeVersion bool
		if err := h.DB.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM cotizacion_versiones WHERE cotizacion_id = $1 AND numero_version = $2)`,
			cotizacionID, version,
		).Scan(&existeVersion); err != nil {
			log.Printf("enlaces_publicos: error validando versión de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
			return
		}
		if !existeVersion {
			escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La versión indicada no existe."})
			return
		}
	}

	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	var creadoPor any
	if usuarioID != "" {
		creadoPor = usuarioID
	}

	// generarToken() es la misma función que auth.go usa para el token
	// de sesión (crypto/rand, 32 bytes en hex) — no se reinventa acá.
	nuevoToken, err := generarToken()
	if err != nil {
		log.Printf("enlaces_publicos: error generando token para %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
		return
	}

	// ON CONFLICT ... DO UPDATE (no-op) ... RETURNING es el truco
	// estándar para "insertar o traer el existente" en una sola vuelta:
	// si (cotizacion_id, version) ya tenía un enlace, el UPDATE no
	// cambia nada de verdad y el RETURNING trae el token que ya existía,
	// no el que se acaba de generar acá.
	var token string
	err = h.DB.QueryRow(ctx, `
		INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version, creado_por)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (cotizacion_id, version) DO UPDATE SET cotizacion_id = EXCLUDED.cotizacion_id
		RETURNING token`,
		nuevoToken, cotizacionID, version, creadoPor,
	).Scan(&token)
	if err != nil {
		log.Printf("enlaces_publicos: error guardando enlace de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token, "url": "/publico.html?token=" + token})
}

type enlacePublicoElemento struct {
	ElementoID string `json:"elemento_id"`
	Tipo       string `json:"tipo"`
	Etiqueta   string `json:"etiqueta,omitempty"`
	Orden      int    `json:"orden"`
	Valor      any    `json:"valor,omitempty"`
}

type enlacePublicoTab struct {
	TabID     string                  `json:"tab_id"`
	Nombre    string                  `json:"nombre"`
	Orden     int                     `json:"orden"`
	Elementos []enlacePublicoElemento `json:"elementos"`
}

// VerCotizacion responde GET /api/publico/cotizacion/{token}. Sin
// sesión — va fuera del r.Group protegido en main.go. Nunca incluye
// costo/ganancia/margen (ver el comentario de paquete arriba).
func (h *EnlacesPublicosHandler) VerCotizacion(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": mensajeEnlaceNoDisponible})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var cotizacionID string
	var version int
	var fechaExpiracion *time.Time
	err := h.DB.QueryRow(ctx, `
		SELECT cotizacion_id, version, fecha_expiracion
		  FROM cotizacion_enlaces_publicos WHERE token = $1`, token,
	).Scan(&cotizacionID, &version, &fechaExpiracion)
	if errors.Is(err, pgx.ErrNoRows) {
		// Mismo 404 genérico que un token vencido — no hay forma de
		// distinguir "no existe" de "venció" desde la respuesta.
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": mensajeEnlaceNoDisponible})
		return
	}
	if err != nil {
		log.Printf("enlaces_publicos: error consultando token: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar el enlace."})
		return
	}
	if fechaExpiracion != nil && time.Now().After(*fechaExpiracion) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": mensajeEnlaceNoDisponible})
		return
	}

	// Sello de visita: mejor-esfuerzo, igual que fecha_ultimo_uso en
	// middleware/auth.go — si falla, el enlace sigue siendo válido para
	// esta petición.
	if _, err := h.DB.Exec(ctx, `
		UPDATE cotizacion_enlaces_publicos SET ultima_visita = now(), visitas = visitas + 1
		 WHERE token = $1`, token); err != nil {
		log.Printf("enlaces_publicos: no se pudo sellar la visita de %s: %v", token, err)
	}

	if err := h.marcarVistaPorElCliente(ctx, cotizacionID, version); err != nil {
		log.Printf("enlaces_publicos: error registrando 'Vista por el Cliente' de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar el enlace."})
		return
	}

	var codigoOferta, tipoPropuesta, cliente, empresa *string
	var calculadoraNombre, estado, moneda string
	var totalPrecio float64
	err = h.DB.QueryRow(ctx, `
		SELECT c.codigo_oferta, c.tipo_propuesta, cl.nombre_comercial, COALESCE(cl.razon_social, cl.nombre_comercial),
		       calc.nombre_calculadora, cv.estado, cv.moneda, cv.total_precio
		  FROM cotizaciones c
		  JOIN calculadoras calc ON calc.calculadora_id = c.calculadora_id
		  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id = c.cotizacion_id AND cv.numero_version = $2
		 WHERE c.cotizacion_id = $1`,
		cotizacionID, version,
	).Scan(&codigoOferta, &tipoPropuesta, &cliente, &empresa, &calculadoraNombre, &estado, &moneda, &totalPrecio)
	if err != nil {
		log.Printf("enlaces_publicos: error consultando cabecera de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar el enlace."})
		return
	}

	tabs, err := h.consultarTabsYValores(ctx, cotizacionID, version)
	if err != nil {
		log.Printf("enlaces_publicos: error consultando tabs/elementos de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar el enlace."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"cotizacion_id":    cotizacionID,
		"version":          version,
		"codigo_oferta":    valorTexto(codigoOferta),
		"tipo_propuesta":   valorTexto(tipoPropuesta),
		"cliente":          valorTexto(cliente),
		"empresa":          valorTexto(empresa),
		"cotizador_nombre": calculadoraNombre,
		"estado":           estado,
		"moneda":           moneda,
		"total_precio":     totalPrecio,
		"tabs":             tabs,
	})
}

// marcarVistaPorElCliente cambia la versión a "Vista por el Cliente"
// la primera vez que alguien abre el enlace, con el mismo mecanismo
// que CambiarEstado (UPDATE de cotizacion_versiones + sincronizar
// cotizaciones.estado si es la versión activa + insertarHistorial).
// Sin usuario_id: el visitante es anónimo, así que el historial queda
// sin autor (NULLIF vacío en insertarHistorial).
func (h *EnlacesPublicosHandler) marcarVistaPorElCliente(ctx context.Context, cotizacionID string, version int) error {
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var estadoActual string
	if err := tx.QueryRow(ctx, `
		SELECT estado FROM cotizacion_versiones
		 WHERE cotizacion_id = $1 AND numero_version = $2 FOR UPDATE`,
		cotizacionID, version,
	).Scan(&estadoActual); err != nil {
		return err
	}
	if estadoActual != "Enviada al Cliente" {
		return nil
	}

	const nuevoEstado = "Vista por el Cliente"
	if _, err := tx.Exec(ctx, `
		UPDATE cotizacion_versiones SET estado = $3 WHERE cotizacion_id = $1 AND numero_version = $2`,
		cotizacionID, version, nuevoEstado); err != nil {
		return err
	}

	var versionActual int
	if err := tx.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&versionActual); err != nil {
		return err
	}
	if version == versionActual {
		if _, err := tx.Exec(ctx, `UPDATE cotizaciones SET estado = $2 WHERE cotizacion_id = $1`, cotizacionID, nuevoEstado); err != nil {
			return err
		}
	}

	if err := insertarHistorial(ctx, tx, cotizacionID, &version, "VISTA_POR_CLIENTE", &estadoActual, strPtr(nuevoEstado), "Abierta desde el enlace público.", ""); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// consultarTabsYValores cruza elementos_tab_cotizador (estructura
// vigente del cotizador) con cotizacion_valores (lo que se llenó en
// esta versión) y catalogo_valores (para traducir CAMPO_CATALOGO al
// texto_visible en vez del código interno). LEYENDA/TEXTO_INFORMATIVO
// no tienen valor guardado — son texto fijo, se muestra su etiqueta.
func (h *EnlacesPublicosHandler) consultarTabsYValores(ctx context.Context, cotizacionID string, version int) ([]*enlacePublicoTab, error) {
	var calculadoraID string
	if err := h.DB.QueryRow(ctx, `SELECT calculadora_id FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&calculadoraID); err != nil {
		return nil, err
	}

	rows, err := h.DB.Query(ctx, `
		SELECT t.tab_id, t.nombre, t.orden,
		       e.elemento_id, e.tipo, COALESCE(e.etiqueta, ''), e.orden,
		       cv.valor, catv.texto_visible
		  FROM tabs_cotizador t
		  JOIN elementos_tab_cotizador e ON e.tab_id = t.tab_id AND e.activo = true
		  LEFT JOIN cotizacion_valores cv ON cv.cotizacion_id = $1 AND cv.version = $2 AND cv.elemento_id = e.elemento_id
		  LEFT JOIN catalogo_valores catv ON catv.catalogo_id = e.catalogo_id AND catv.valor_sistema = (cv.valor #>> '{}')
		 WHERE t.calculadora_id = $3 AND t.activo = true
		 ORDER BY t.orden, t.tab_id, e.orden, e.elemento_id`,
		cotizacionID, version, calculadoraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tabs := make([]*enlacePublicoTab, 0)
	indice := make(map[string]*enlacePublicoTab)
	for rows.Next() {
		var tabID, tabNombre, elementoID, tipo, etiqueta string
		var tabOrden, elementoOrden int
		var valorRaw []byte
		var textoVisible *string
		if err := rows.Scan(&tabID, &tabNombre, &tabOrden, &elementoID, &tipo, &etiqueta, &elementoOrden, &valorRaw, &textoVisible); err != nil {
			return nil, err
		}

		tab, ok := indice[tabID]
		if !ok {
			tab = &enlacePublicoTab{TabID: tabID, Nombre: tabNombre, Orden: tabOrden, Elementos: make([]enlacePublicoElemento, 0)}
			indice[tabID] = tab
			tabs = append(tabs, tab)
		}

		elemento := enlacePublicoElemento{ElementoID: elementoID, Tipo: tipo, Etiqueta: etiqueta, Orden: elementoOrden}
		switch tipo {
		case "LEYENDA", "TEXTO_INFORMATIVO":
			elemento.Valor = etiqueta
		case "CAMPO_CATALOGO":
			if textoVisible != nil {
				elemento.Valor = *textoVisible
			}
		default: // CAMPO
			if len(valorRaw) > 0 {
				var valor any
				if err := json.Unmarshal(valorRaw, &valor); err != nil {
					return nil, err
				}
				elemento.Valor = valor
			}
		}
		tab.Elementos = append(tab.Elementos, elemento)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tabs, nil
}
