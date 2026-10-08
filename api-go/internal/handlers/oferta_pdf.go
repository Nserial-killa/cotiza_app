package handlers

// PDF de la oferta (tarea 2): GET /api/cotizaciones/{id}/enlace/pdf?version=N.
//
// Protegido por sesión, con los mismos permisos que abrir la cotización
// (exigirAccesoCotizacion: sesión + alcance_propio). El PDF es la página
// pública real (publico.html) impresa por Gotenberg, así que sale idéntica
// a lo que ve el cliente, firma incluida.
//
// Reglas de seguridad:
//   - La URL que recibe Gotenberg la arma SIEMPRE este handler a partir de
//     PDF_INTERNAL_BASE_URL + el token del enlace guardado en la base. El
//     cliente HTTP no manda ninguna URL ni ningún token.
//   - La URL lleva un pase de render de un solo uso (pases_render.go) para
//     que la visita de Chromium no cuente como visita del cliente.
//   - No se usa ?imprimir=1: dispararía window.print() dentro de Chromium.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OfertaPDFHandler sigue la convención del paquete: struct con DB.
type OfertaPDFHandler struct {
	DB          *pgxpool.Pool  // base de datos
	Convertidor ConvertidorPDF // Gotenberg (o un doble en pruebas); nil = PDF no configurado
	BaseInterna string         // PDF_INTERNAL_BASE_URL, ej. http://api:8080
	Pases       *PasesRender   // registro compartido con EnlacesPublicosHandler
}

// plazoPDF es el tiempo total que se le da a Gotenberg. Queda por debajo
// del chimiddleware.Timeout(30s) de main.go para responder un error propio
// en vez del 504 genérico del middleware.
const plazoPDF = 25 * time.Second

// caracteresNoSegurosArchivo es todo lo que no se deja en el nombre del
// archivo descargado (evita romper el header Content-Disposition).
var caracteresNoSegurosArchivo = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Descargar responde el PDF como adjunto o un JSON {"ok":false,...}.
func (h *OfertaPDFHandler) Descargar(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "id")) // id de la ruta
	if cotizacionID == "" {                                  // sin id no hay nada que hacer
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}
	version, err := versionOpcional(strings.TrimSpace(r.URL.Query().Get("version"))) // ?version=N opcional, validada
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), plazoPDF) // plazo total, incluida la conversión
	defer cancel()                                            // libera el temporizador

	// Mismos permisos que ver la cotización (detalle / vista previa).
	if _, ok := exigirAccesoCotizacion(ctx, w, r, h.DB, cotizacionID); !ok {
		return // exigirAccesoCotizacion ya escribió el 401/403/500
	}

	if version <= 0 { // sin versión explícita, la actual
		version, err = resolverVersionCotizacion(ctx, h.DB, cotizacionID)
		if errors.Is(err, pgx.ErrNoRows) { // la cotización no existe
			escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cotización no encontrada."})
			return
		}
		if err != nil { // error de base
			log.Printf("oferta_pdf: error leyendo version_actual de %s: %v", cotizacionID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el PDF."})
			return
		}
	}

	// El token sale de la base, nunca de la petición.
	var token, codigoOferta string // token del enlace y código para el nombre del archivo
	var fechaExpiracion *time.Time // vencimiento opcional del enlace
	err = h.DB.QueryRow(ctx, `
		SELECT e.token, COALESCE(c.codigo_oferta, c.cotizacion_id), e.fecha_expiracion
		  FROM cotizacion_enlaces_publicos e
		  JOIN cotizaciones c ON c.cotizacion_id = e.cotizacion_id
		 WHERE e.cotizacion_id = $1 AND e.version = $2`, cotizacionID, version,
	).Scan(&token, &codigoOferta, &fechaExpiracion)
	if errors.Is(err, pgx.ErrNoRows) { // todavía no hay enlace para esa versión
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Esta versión todavía no tiene enlace para el cliente. Genere el enlace primero."})
		return
	}
	if err != nil { // error de base
		log.Printf("oferta_pdf: error leyendo el enlace de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el PDF."})
		return
	}
	if fechaExpiracion != nil && time.Now().After(*fechaExpiracion) { // enlace vencido: la página no cargaría
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "El enlace de esta versión venció. Genere uno nuevo para obtener el PDF."})
		return
	}

	if h.Convertidor == nil || strings.TrimSpace(h.BaseInterna) == "" || h.Pases == nil { // falta configuración
		escribirJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "El servicio de PDF no está configurado en este servidor."})
		return
	}

	pase, err := h.Pases.Emitir(token) // pase de un solo uso atado a este enlace
	if err != nil {
		log.Printf("oferta_pdf: error emitiendo pase de render: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar el PDF."})
		return
	}

	// URL interna armada por el servidor: base fija + token y pase codificados.
	parametros := url.Values{}                                                                   // query string segura
	parametros.Set("token", token)                                                               // el enlace a imprimir
	parametros.Set("render", pase)                                                               // el pase sin efectos
	urlInterna := strings.TrimRight(h.BaseInterna, "/") + "/publico.html?" + parametros.Encode() // sin ?imprimir=1

	pdf, err := h.Convertidor.ConvertirURL(ctx, urlInterna) // Gotenberg abre la página y la imprime
	if err != nil {
		// Sin la URL en el log: lleva el token del enlace y el pase.
		log.Printf("oferta_pdf: Gotenberg falló para %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": "El servicio de PDF no respondió. Intente de nuevo en unos segundos."})
		return
	}

	// Nombre de archivo: código de oferta + versión, solo caracteres seguros.
	base := strings.Trim(caracteresNoSegurosArchivo.ReplaceAllString(codigoOferta, "-"), "-.") // limpia el código
	if base == "" {                                                                            // por si el código quedó vacío
		base = "oferta"
	}
	nombre := fmt.Sprintf("%s-V%d.pdf", base, version)                         // ej. CTZ-2026-001-V2.pdf
	w.Header().Set("Content-Type", "application/pdf")                          // tipo del contenido
	w.Header().Set("Content-Disposition", `attachment; filename="`+nombre+`"`) // descarga directa con nombre
	w.Header().Set("Cache-Control", "no-store")                                // nada de cachés intermedias
	w.Header().Set("X-Content-Type-Options", "nosniff")                        // el navegador no reinterpreta el tipo
	w.WriteHeader(http.StatusOK)                                               // 200
	if _, err := w.Write(pdf); err != nil {                                    // envía los bytes
		log.Printf("oferta_pdf: error escribiendo la respuesta: %v", err)
	}
}
