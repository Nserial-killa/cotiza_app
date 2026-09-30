package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Reproduce la cotización reportada: 10.000 con 50% de descuento debe
// mostrarse como 5.000 tanto en el campo como en los encabezados y el Gestor.
func TestCotizacion_TotalConDescuentoCoincideEnGestorYOferta(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	actor := crearUsuarioPrueba(t, f.handler.DB, "total."+sufijoUnico()+"@exceltecgroup.com", "9999", "Administrador", "Activo")
	base := f.crear(t, "CAMPO", "precio_base", map[string]any{"tipo_campo": "NUMERO"}, nil)
	descuento := f.crear(t, "CAMPO", "descuento", map[string]any{"tipo_campo": "NUMERO"}, nil)
	moneda := f.crear(t, "CAMPO", "moneda", map[string]any{"tipo_campo": "TEXTO"}, map[string]any{"funcion_campo": "MONEDA_OFERTA"})
	total := f.crear(t, "CAMPO_CALCULADO", "precio_con_descuento", configFormulaPrueba("precio_base - (precio_base * descuento / 100)"), map[string]any{"funcion_campo": "TOTAL_PRECIO_OFERTA"})
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "CALCULADO", total, "", true))
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "MONEDA", "CAMPO", moneda, "", true))
	rt := f.runtime(t)
	for _, caso := range []struct {
		descuento string
		total     float64
	}{{"50", 5000}, {"25", 7500}, {"100", 0}} {
		t.Run(caso.descuento, func(t *testing.T) {
			exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{base: "10000", descuento: caso.descuento, moneda: "USD"}}))
			if obtenido := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO"); obtenido != caso.total {
				t.Fatalf("salida=%v; esperado=%v", obtenido, caso.total)
			}
			rec := getRuntime(t, rt, "version=1")
			var runtime struct {
				Estructura map[string]any `json:"estructura"`
			}
			assertJSON(t, rec.Body.Bytes(), &runtime)
			if el := elementoPorIDEnEstructura(runtime.Estructura, total); el["valor_resuelto"] != caso.total {
				t.Fatalf("campo calculado=%v; esperado=%v", el["valor_resuelto"], caso.total)
			}
			cotizaciones := &CotizacionesHandler{DB: f.handler.DB}
			detalle := getDetalleCotizacion(t, cotizaciones, actor, rt.CotizacionID, "version=1")
			var res struct {
				Cotizacion map[string]any   `json:"cotizacion"`
				Versiones  []map[string]any `json:"versiones"`
			}
			assertJSON(t, detalle.Body.Bytes(), &res)
			if detalle.Code != 200 || res.Cotizacion["total_precio"] != caso.total || res.Cotizacion["moneda"] != "USD" {
				t.Fatalf("detalle: %d %s", detalle.Code, detalle.Body.String())
			}
			if len(res.Versiones) != 1 || res.Versiones[0]["total_precio"] != caso.total {
				t.Fatalf("importe del historial de versiones incorrecto: %+v", res.Versiones)
			}
			lista := httptest.NewRecorder()
			cotizaciones.Listar(lista, httptest.NewRequest(http.MethodGet, "/api/cotizaciones?calculadora_id="+url.QueryEscape(f.calculadoraID), nil))
			var listado struct {
				Cotizaciones []map[string]any `json:"cotizaciones"`
			}
			assertJSON(t, lista.Body.Bytes(), &listado)
			if len(listado.Cotizaciones) != 1 || listado.Cotizaciones[0]["total_precio"] != caso.total || listado.Cotizaciones[0]["moneda"] != "USD" {
				t.Fatalf("listado: %s", lista.Body.String())
			}
			preview := getVistaPreviaOferta(t, &VistaPreviaOfertaHandler{DB: f.handler.DB}, rt.CotizacionID, "1")
			var oferta map[string]any
			assertJSON(t, preview.Body.Bytes(), &oferta)
			if preview.Code != 200 || oferta["total_precio"] != caso.total || oferta["moneda"] != "USD" {
				t.Fatalf("cabecera oferta: %d %s", preview.Code, preview.Body.String())
			}
		})
	}
}

func TestCotizador_MonedaOfertaRechazaImportes(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	for _, tipo := range []string{"NUMERO", "MONEDA", "PORCENTAJE"} {
		rec := f.guardar(t, "TEST-MONEDA-"+sufijoUnico(), "CAMPO", "importe", map[string]any{"tipo_campo": tipo}, map[string]any{"funcion_campo": "MONEDA_OFERTA"})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "TOTAL_PRECIO") {
			t.Fatalf("%s: esperaba error accionable, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}
	// Un diseño antiguo pudo persistir esa combinación: también debe impedirse
	// su publicación, sin alterar las versiones previamente compiladas.
	id := f.crear(t, "CAMPO", "importe_legado", map[string]any{"tipo_campo": "NUMERO"}, nil)
	if _, err := f.handler.DB.Exec(context.Background(), `UPDATE elementos_tab_cotizador SET funcion_campo='MONEDA_OFERTA' WHERE elemento_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	res := postCompilador(t, (&CompiladorHandler{DB: f.handler.DB}).Compilar, f.calculadoraID)
	if res.Compilado || res.Valido || !strings.Contains(strings.Join(res.Errores, " "), "TOTAL_PRECIO") {
		t.Fatalf("se publicó un importe como moneda: %+v", res)
	}
}

func TestFuncionCampoMoneda_NoConvierteImporteEnDivisa(t *testing.T) {
	for _, valor := range []any{"10000", 10000.0, "0", "50.25"} {
		if _, ok := valorColumnaFuncionCampo("moneda", valor); ok {
			t.Fatalf("se aceptó %v como divisa", valor)
		}
	}
	for _, moneda := range []string{"USD", "CRC", "US$", "₡"} {
		if valor, ok := valorColumnaFuncionCampo("moneda", moneda); !ok || valor != moneda {
			t.Fatalf("se rechazó la divisa %s", moneda)
		}
	}
}
