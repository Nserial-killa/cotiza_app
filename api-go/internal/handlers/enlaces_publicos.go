package handlers

// EnlacesPublicosHandler cubre la Vista Previa / enlace público al
// cliente (versión acotada, esquema 0009): generar un token para una
// cotización+versión puntual, y la lectura sin sesión de esa versión
// como propuesta legible. El contenido y el estilo (colores, logo,
// tipografía) salen de la plantilla fijada al generar el enlace, ver
// fijarPlantillaCotizacionVersion en plantilla_renderizador.go.
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
// uno nuevo. En la misma transacción fija la plantilla de esa versión
// (migración 0032, fijarPlantillaCotizacionVersion): desde acá el enlace
// sigue mostrando esa versión de la plantilla aunque se publique otra.
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
		resuelta, err := resolverVersionCotizacion(ctx, h.DB, cotizacionID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cotización no encontrada."})
				return
			}
			log.Printf("enlaces_publicos: error leyendo version_actual de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
			return
		}
		version = resuelta
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

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		log.Printf("enlaces_publicos: error iniciando generación de enlace para %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
		return
	}
	defer tx.Rollback(ctx)

	// ON CONFLICT ... DO UPDATE (no-op) ... RETURNING es el truco
	// estándar para "insertar o traer el existente" en una sola vuelta:
	// si (cotizacion_id, version) ya tenía un enlace, el UPDATE no
	// cambia nada de verdad y el RETURNING trae el token que ya existía,
	// no el que se acaba de generar acá.
	var token string
	err = tx.QueryRow(ctx, `
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

	// token == nuevoToken solo cuando el INSERT realmente creó la fila
	// (no hubo conflicto); si se reutilizó un enlace existente, no se
	// vuelve a registrar en el historial.
	if token == nuevoToken {
		if err := insertarHistorial(ctx, tx, cotizacionID, &version, "ENLACE_GENERADO", nil, nil, "Enlace público generado.", usuarioID); err != nil {
			log.Printf("enlaces_publicos: error registrando historial de enlace de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
			return
		}
	}

	plantilla, err := fijarPlantillaCotizacionVersion(ctx, tx, cotizacionID, version)
	if err != nil {
		log.Printf("enlaces_publicos: error fijando la plantilla de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("enlaces_publicos: error confirmando enlace de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el enlace."})
		return
	}

	respuesta := map[string]any{"ok": true, "token": token, "url": "/publico.html?token=" + token,
		"plantilla_id_usada": nil, "plantilla_version_usada": nil}
	if plantilla != nil {
		respuesta["plantilla_id_usada"] = plantilla.ID
		respuesta["plantilla_version_usada"] = plantilla.Version
	}
	escribirJSON(w, http.StatusOK, respuesta)
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

	// construirDocumentoOferta es la MISMA cadena (cabecera + tabs/valores +
	// plantilla vinculada) que usa la Vista Previa de la Oferta protegida
	// por sesión (vista_previa_oferta.go, Ronda C / CTZ-TEC-004) — ninguna
	// de las dos tiene su propia lógica de resolución.
	doc, err := construirDocumentoOferta(ctx, h.DB, cotizacionID, version)
	if err != nil {
		log.Printf("enlaces_publicos: error armando la oferta de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar el enlace."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"cotizacion_id":    doc.CotizacionID,
		"version":          doc.Version,
		"codigo_oferta":    doc.CodigoOferta,
		"tipo_propuesta":   doc.TipoPropuesta,
		"cliente":          doc.Cliente,
		"empresa":          doc.Empresa,
		"cotizador_nombre": doc.CotizadorNombre,
		"estado":           doc.Estado,
		"moneda":           doc.Moneda,
		"total_precio":     doc.TotalPrecio,
		"tabs":             doc.Tabs,
		"plantilla":        doc.Plantilla,
	})
}

// documentoOfertaResuelto es la propuesta ya resuelta para una
// cotización+versión puntual: cabecera + tabs/valores crudos (fallback
// cuando el cotizador no tiene una plantilla vinculada) + la plantilla
// vinculada ya renderizada, si aplica (ver plantilla_renderizador.go).
// Es EXACTAMENTE el mismo documento que ve el cliente en el enlace
// público (VerCotizacion, arriba) y que ve el equipo de Cotiza en la
// Vista Previa de la Oferta (vista_previa_oferta.go, Ronda C) — los dos
// llaman a construirDocumentoOferta, ninguno tiene su propia lógica de
// resolución (CTZ-TEC-004: "para una misma cotización y plantilla,
// preview y publicación resuelven el mismo valor").
type documentoOfertaResuelto struct {
	CotizacionID    string                `json:"cotizacion_id"`
	Version         int                   `json:"version"`
	CodigoOferta    any                   `json:"codigo_oferta"`
	TipoPropuesta   any                   `json:"tipo_propuesta"`
	Cliente         any                   `json:"cliente"`
	Empresa         any                   `json:"empresa"`
	CotizadorNombre string                `json:"cotizador_nombre"`
	Estado          string                `json:"estado"`
	Moneda          string                `json:"moneda"`
	TotalPrecio     float64               `json:"total_precio"`
	Tabs            []*enlacePublicoTab   `json:"tabs"`
	Plantilla       *plantillaRenderizada `json:"plantilla"`
}

// construirDocumentoOferta arma documentoOfertaResuelto para (cotizacionID,
// version); version=0 cae a version_actual. Sin efectos secundarios: no
// marca visitas ni cambia estado — eso es responsabilidad de cada llamador
// (VerCotizacion sí lo hace, para el visitante anónimo real; la Vista
// Previa nunca).
func construirDocumentoOferta(ctx context.Context, db *pgxpool.Pool, cotizacionID string, version int) (*documentoOfertaResuelto, error) {
	if version <= 0 {
		resuelta, err := resolverVersionCotizacion(ctx, db, cotizacionID)
		if err != nil {
			return nil, err
		}
		version = resuelta
	}

	doc := &documentoOfertaResuelto{CotizacionID: cotizacionID, Version: version}
	var codigoOferta, tipoPropuesta, cliente, empresa *string
	err := db.QueryRow(ctx, `
		SELECT c.codigo_oferta, c.tipo_propuesta, cl.nombre_comercial, COALESCE(cl.razon_social, cl.nombre_comercial),
		       calc.nombre_calculadora, cv.estado, cv.moneda, cv.total_precio
		  FROM cotizaciones c
		  JOIN calculadoras calc ON calc.calculadora_id = c.calculadora_id
		  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id = c.cotizacion_id AND cv.numero_version = $2
		 WHERE c.cotizacion_id = $1`,
		cotizacionID, version,
	).Scan(&codigoOferta, &tipoPropuesta, &cliente, &empresa, &doc.CotizadorNombre, &doc.Estado, &doc.Moneda, &doc.TotalPrecio)
	if err != nil {
		return nil, err
	}
	doc.CodigoOferta = valorTexto(codigoOferta)
	doc.TipoPropuesta = valorTexto(tipoPropuesta)
	doc.Cliente = valorTexto(cliente)
	doc.Empresa = valorTexto(empresa)

	// Si la versión ya tiene plantilla fijada (se generó su enlace), la
	// propuesta se arma con esa, aunque hoy esté Archivada; si no, con la
	// Publicada que aplique ahora (resolverPlantillaOferta). Así la Vista
	// Previa y el enlace siguen resolviendo lo mismo antes y después de
	// fijar. La plantilla trae secciones/bloques/condiciones/tabla de
	// escenarios y su estilo. Sin una, se sigue mostrando "tabs" (las
	// secciones crudas del cotizador): no todo cotizador tiene todavía una plantilla armada,
	// y eso no es un error. Un error real al renderizar la plantilla SÍ se
	// propaga — a diferencia de "no hay plantilla", que es silencioso.
	tabs, err := (&EnlacesPublicosHandler{DB: db}).consultarTabsYValores(ctx, cotizacionID, version)
	if err != nil {
		return nil, err
	}
	doc.Tabs = tabs

	plantilla, err := renderizarPlantillaCotizacion(ctx, db, cotizacionID, version)
	if err != nil {
		return nil, err
	}
	doc.Plantilla = plantilla

	return doc, nil
}

// resolverVersionCotizacion devuelve version_actual de una cotización.
// Compartida por GenerarEnlace y construirDocumentoOferta para el mismo
// caso: "no me dieron una versión puntual, use la vigente". Propaga
// pgx.ErrNoRows tal cual cuando la cotización no existe.
func resolverVersionCotizacion(ctx context.Context, db *pgxpool.Pool, cotizacionID string) (int, error) {
	var version int
	err := db.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&version)
	return version, err
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
	var versionActual int
	if err := tx.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id=$1 FOR UPDATE`, cotizacionID).Scan(&versionActual); err != nil {
		return err
	}
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
	var rawSnapshot []byte
	if err := h.DB.QueryRow(ctx, `SELECT snapshot_json FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version).Scan(&rawSnapshot); err != nil {
		return nil, err
	}
	if len(rawSnapshot) > 0 {
		var snapshot snapshotCotizacion
		if err := json.Unmarshal(rawSnapshot, &snapshot); err != nil {
			return nil, err
		}
		return tabsPublicasSnapshot(snapshot), nil
	}
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
		  LEFT JOIN cotizacion_valores cv ON cv.cotizacion_id = $1 AND cv.version = $2
		   AND cv.elemento_id = e.elemento_id AND cv.opcion_id IS NULL
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
