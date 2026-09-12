package handlers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReportesHandler expone el detalle comercial de las cotizaciones y su
// exportación CSV. Reutiliza dashboardFiltradoCTE para mantener una sola
// definición de la versión actual, cliente, empresa y vendedor.
type ReportesHandler struct {
	DB *pgxpool.Pool
}

type filtrosReporteCotizaciones struct {
	FechaDesde    string
	FechaHasta    string
	VendedorID    string
	CalculadoraID string
	Estado        string
}

type reporteCotizacion struct {
	CodigoOferta      *string   `json:"codigo_oferta,omitempty"`
	Cliente           *string   `json:"cliente,omitempty"`
	Empresa           *string   `json:"empresa,omitempty"`
	CalculadoraNombre string    `json:"calculadora_nombre"`
	Estado            string    `json:"estado"`
	TotalPrecio       float64   `json:"total_precio"`
	Moneda            string    `json:"moneda"`
	Vendedor          *string   `json:"vendedor,omitempty"`
	FechaCreacion     time.Time `json:"fecha_creacion"`
	MargenTotal       *float64  `json:"margen_total,omitempty"`
}

const reporteCotizacionesSQL = `
	SELECT codigo_oferta, cliente, empresa, nombre_calculadora, estado,
	       total_precio, moneda, vendedor, fecha_creacion,
	       CASE WHEN $7 THEN margen_total ELSE NULL END
	  FROM filtradas
	 WHERE ($4='' OR fecha_creacion >= $4::date)
	   AND ($5='' OR fecha_creacion < ($5::date + INTERVAL '1 day'))
	   AND ($6='' OR estado=$6)
	 ORDER BY fecha_creacion DESC, cotizacion_id DESC`

// Listar responde GET /api/reportes/cotizaciones.
func (h *ReportesHandler) Listar(w http.ResponseWriter, r *http.Request) {
	filtros, err := leerFiltrosReporteCotizaciones(r)
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	filas, _, err := h.consultarCotizaciones(ctx, r, filtros)
	if err != nil {
		log.Printf("reportes: error consultando cotizaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar el reporte."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "filas": filas})
}

// Exportar responde GET /api/reportes/cotizaciones/exportar con las mismas
// filas y filtros de Listar. encoding/csv se encarga del escapado correcto.
func (h *ReportesHandler) Exportar(w http.ResponseWriter, r *http.Request) {
	filtros, err := leerFiltrosReporteCotizaciones(r)
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	filas, puedeVerPrice, err := h.consultarCotizaciones(ctx, r, filtros)
	if err != nil {
		log.Printf("reportes: error exportando cotizaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible exportar el reporte."})
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="reporte_cotizaciones.csv"`)
	w.WriteHeader(http.StatusOK)

	// BOM UTF-8: sin esto Excel abre el archivo con una codificación por
	// defecto que rompe los acentos (Pérez, cotización, etc.).
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		log.Printf("reportes: error escribiendo BOM del CSV: %v", err)
		return
	}

	escritor := csv.NewWriter(w)
	// Separador ';' en vez de ',': en configuración regional
	// latinoamericana (es-CR) el separador de listas del sistema es ';'
	// porque ',' está reservado como separador decimal — con ',' como
	// separador de columnas, Excel no las separaba al abrir el archivo.
	// Como consecuencia, los montos se formatean con ',' decimal (ver
	// formatoMontoCSV) para que Excel los reconozca como número y no
	// como texto en esa misma configuración regional.
	escritor.Comma = ';'
	encabezados := []string{"Código de oferta", "Cliente", "Empresa", "Cotizador", "Estado", "Total precio", "Moneda", "Vendedor", "Fecha de creación"}
	if puedeVerPrice {
		encabezados = append(encabezados, "Margen total")
	}
	if err := escritor.Write(encabezados); err != nil {
		log.Printf("reportes: error escribiendo encabezados CSV: %v", err)
		return
	}

	for _, fila := range filas {
		registro := []string{
			textoReporte(fila.CodigoOferta),
			textoReporte(fila.Cliente),
			textoReporte(fila.Empresa),
			fila.CalculadoraNombre,
			fila.Estado,
			formatoMontoCSV(fila.TotalPrecio),
			fila.Moneda,
			textoReporte(fila.Vendedor),
			fila.FechaCreacion.Format(time.RFC3339),
		}
		if puedeVerPrice {
			margen := ""
			if fila.MargenTotal != nil {
				margen = formatoMontoCSV(*fila.MargenTotal)
			}
			registro = append(registro, margen)
		}
		if err := escritor.Write(registro); err != nil {
			log.Printf("reportes: error escribiendo fila CSV: %v", err)
			return
		}
	}
	escritor.Flush()
	if err := escritor.Error(); err != nil {
		log.Printf("reportes: error finalizando CSV: %v", err)
	}
}

func (h *ReportesHandler) consultarCotizaciones(ctx context.Context, r *http.Request, filtros filtrosReporteCotizaciones) ([]reporteCotizacion, bool, error) {
	puedeVerPrice, err := (&CotizacionesHandler{DB: h.DB}).sesionPuedeVerPrice(ctx, r)
	if err != nil {
		return nil, false, fmt.Errorf("validar permiso de precio: %w", err)
	}

	rows, err := h.DB.Query(ctx, dashboardFiltradoCTE+reporteCotizacionesSQL,
		filtros.CalculadoraID, filtros.VendedorID, "", filtros.FechaDesde,
		filtros.FechaHasta, filtros.Estado, puedeVerPrice)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	filas := make([]reporteCotizacion, 0)
	for rows.Next() {
		var fila reporteCotizacion
		if err := rows.Scan(&fila.CodigoOferta, &fila.Cliente, &fila.Empresa,
			&fila.CalculadoraNombre, &fila.Estado, &fila.TotalPrecio, &fila.Moneda,
			&fila.Vendedor, &fila.FechaCreacion, &fila.MargenTotal); err != nil {
			return nil, false, err
		}
		filas = append(filas, fila)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return filas, puedeVerPrice, nil
}

func leerFiltrosReporteCotizaciones(r *http.Request) (filtrosReporteCotizaciones, error) {
	filtros := filtrosReporteCotizaciones{
		FechaDesde:    strings.TrimSpace(r.URL.Query().Get("fecha_desde")),
		FechaHasta:    strings.TrimSpace(r.URL.Query().Get("fecha_hasta")),
		VendedorID:    strings.TrimSpace(r.URL.Query().Get("vendedor_id")),
		CalculadoraID: strings.TrimSpace(r.URL.Query().Get("calculadora_id")),
		Estado:        strings.TrimSpace(r.URL.Query().Get("estado")),
	}
	if err := validarFechaReporte(filtros.FechaDesde, "fecha_desde"); err != nil {
		return filtrosReporteCotizaciones{}, err
	}
	if err := validarFechaReporte(filtros.FechaHasta, "fecha_hasta"); err != nil {
		return filtrosReporteCotizaciones{}, err
	}
	if filtros.FechaDesde != "" && filtros.FechaHasta != "" && filtros.FechaDesde > filtros.FechaHasta {
		return filtrosReporteCotizaciones{}, errors.New("fecha_desde no puede ser posterior a fecha_hasta")
	}
	if filtros.Estado != "" && !estadosCotizacionValidos[filtros.Estado] {
		return filtrosReporteCotizaciones{}, errors.New("estado no es válido")
	}
	return filtros, nil
}

func validarFechaReporte(valor, nombre string) error {
	if valor == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", valor); err != nil {
		return fmt.Errorf("%s debe tener formato AAAA-MM-DD", nombre)
	}
	return nil
}

func textoReporte(valor *string) string {
	if valor == nil {
		return ""
	}
	return *valor
}

// formatoMontoCSV usa ',' como separador decimal, a juego con el ';'
// como separador de columnas del CSV (ver Exportar) — así Excel en
// configuración regional latinoamericana reconoce el valor como
// número en vez de texto.
func formatoMontoCSV(valor float64) string {
	return strings.Replace(strconv.FormatFloat(valor, 'f', 2, 64), ".", ",", 1)
}
