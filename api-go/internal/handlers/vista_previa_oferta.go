package handlers

// Vista Previa de la Oferta (Ronda C — VPO-001…007, B-04, CP-12, caso ISA
// Custom). No confundir con "Publicar" del Diseñador (compilador.go) ni con
// "Publicada" de Plantillas (plantillas.go): esto es ver el documento que
// recibiría el cliente ANTES de generarle su Link Público de verdad
// (enlaces_publicos.go).
//
// CTZ-TEC-004 (criterio de aceptación no negociable): preview y
// publicación deben resolver EXACTAMENTE el mismo valor. Por eso este
// handler no tiene ninguna lógica propia de resolución — llama a
// construirDocumentoOferta, la misma cadena que arma el documento real en
// enlaces_publicos.go (VerCotizacion). La única diferencia es que acá:
//   - se exige sesión (RequiereSesion en main.go), nunca un token público;
//   - funciona en cualquier estado de la cotización, Borrador incluido
//     (VPO-001: "durante la creación/edición");
//   - NO inserta nada en cotizacion_enlaces_publicos, NO llama a
//     marcarVistaPorElCliente, NO cambia ningún estado — se puede invocar
//     tantas veces como se quiera sin efecto secundario (VPO-004).

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
)

type VistaPreviaOfertaHandler struct {
	DB *pgxpool.Pool
}

// Ver responde GET /api/cotizaciones/{id}/vista-previa-oferta?version=N.
// version es opcional; sin ella, usa version_actual (mismo criterio que
// GenerarEnlace).
func (h *VistaPreviaOfertaHandler) Ver(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "id"))
	if cotizacionID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la cotización."})
		return
	}
	versionTexto := strings.TrimSpace(r.URL.Query().Get("version"))
	version, err := versionOpcional(versionTexto)
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	doc, err := construirDocumentoOferta(ctx, h.DB, cotizacionID, version)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Cotización o versión no encontrada."})
		return
	}
	if err != nil {
		log.Printf("vista_previa_oferta: error armando la oferta de %s v%d: %v", cotizacionID, version, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible generar la vista previa."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"vista_previa":     true,
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
