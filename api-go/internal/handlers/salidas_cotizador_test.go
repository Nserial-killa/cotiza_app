package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fixtureSalidas struct {
	diseno                fixtureFormulaAvanzada
	runtime               fixtureRuntime
	precio, costo, moneda string
}

func mapearSalidaPrueba(t *testing.T, f fixtureFormulaAvanzada, clave, tipo, fuente, propiedad string, requerido bool) *httptest.ResponseRecorder {
	t.Helper()
	return postCatalogos(t, (&SalidasCotizadorHandler{DB: f.handler.DB}).Guardar, "/api/cotizador/salidas", map[string]any{
		"calculadora_id": f.calculadoraID, "clave_salida": clave, "tipo_fuente": tipo, "fuente_id": fuente, "propiedad_fuente": propiedad, "requerido": requerido,
	})
}

func crearFixtureSalidasISA(t *testing.T) fixtureSalidas {
	t.Helper()
	f := crearFixtureFormulaAvanzada(t)
	p := f.crear(t, "CAMPO", "PRECIO", map[string]any{"tipo_campo": "MONEDA"}, nil)
	c := f.crear(t, "CAMPO", "COSTO", map[string]any{"tipo_campo": "MONEDA"}, nil)
	m := f.crear(t, "CAMPO", "MONEDA", map[string]any{"tipo_campo": "TEXTO"}, nil)
	g := f.crear(t, "CAMPO_CALCULADO", "GANANCIA", configFormulaPrueba("PRECIO - COSTO"), nil)
	cfg := configFormulaPrueba("GANANCIA / PRECIO")
	cfg["tipo_resultado"] = "PORCENTAJE"
	cfg["decimales"] = 4
	mar := f.crear(t, "CAMPO_CALCULADO", "MARGEN", cfg, nil)
	for _, s := range []struct{ clave, tipo, id string }{{"TOTAL_PRECIO", "CAMPO", p}, {"TOTAL_COSTO", "CAMPO", c}, {"TOTAL_GANANCIA", "CALCULADO", g}, {"MARGEN_TOTAL", "CALCULADO", mar}, {"MONEDA", "CAMPO", m}} {
		rec := mapearSalidaPrueba(t, f, s.clave, s.tipo, s.id, "", true)
		if rec.Code != 200 {
			t.Fatalf("mapear %s: %d %s", s.clave, rec.Code, rec.Body.String())
		}
	}
	return fixtureSalidas{f, f.runtime(t), p, c, m}
}

func (f fixtureSalidas) guardar(t *testing.T, version int, precio string) *httptest.ResponseRecorder {
	t.Helper()
	return postValoresRuntime(t, f.runtime, map[string]any{"version": version, "valores": map[string]any{f.precio: precio, f.costo: "1230", f.moneda: "USD"}})
}

func exigirGuardadoSalida(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func leerSalidaNumeroPrueba(t *testing.T, rt fixtureRuntime, version int, clave string) float64 {
	t.Helper()
	var valor float64
	if err := rt.Handler.DB.QueryRow(context.Background(), `SELECT valor_numero FROM cotizacion_salidas WHERE cotizacion_id=$1 AND numero_version=$2 AND clave_salida=$3`, rt.CotizacionID, version, clave).Scan(&valor); err != nil {
		t.Fatal(err)
	}
	return valor
}

func huellaVersionSalidas(t *testing.T, rt fixtureRuntime, version int) string {
	t.Helper()
	var huella string
	err := rt.Handler.DB.QueryRow(context.Background(), `SELECT jsonb_build_object(
		'snapshot',cv.snapshot_json,
		'salidas',(SELECT jsonb_agg(to_jsonb(s) ORDER BY clave_salida) FROM cotizacion_salidas s WHERE s.cotizacion_id=cv.cotizacion_id AND s.numero_version=cv.numero_version),
		'items',(SELECT jsonb_agg(to_jsonb(i) ORDER BY orden) FROM cotizacion_items i WHERE i.cotizacion_id=cv.cotizacion_id AND i.numero_version=cv.numero_version))::text
		FROM cotizacion_versiones cv WHERE cotizacion_id=$1 AND numero_version=$2`, rt.CotizacionID, version).Scan(&huella)
	if err != nil {
		t.Fatal(err)
	}
	return huella
}

func crearV2Salidas(t *testing.T, rt fixtureRuntime) {
	t.Helper()
	rec := postCotizacionSubruta(t, (&CotizacionesHandler{DB: rt.Handler.DB}).CrearVersion, "/api/cotizaciones/{id}/version", rt.CotizacionID, map[string]any{"nombre_version": "V2"})
	exigirGuardadoSalida(t, rec)
}

func TestSalidas_AT01_GuardarV1ISASnapshotYCincoSalidas(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	for clave, esperado := range map[string]float64{"TOTAL_PRECIO": 2350, "TOTAL_COSTO": 1230, "TOTAL_GANANCIA": 1120, "MARGEN_TOTAL": 0.4766} {
		if v := leerSalidaNumeroPrueba(t, f.runtime, 1, clave); v != esperado {
			t.Fatalf("%s=%v, esperado %v", clave, v, esperado)
		}
	}
	var count int
	var moneda string
	var raw []byte
	if err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_salidas WHERE cotizacion_id=$1`, f.runtime.CotizacionID).Scan(&count); err != nil || count != 5 {
		t.Fatalf("salidas=%d, err=%v", count, err)
	}
	if err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT valor_texto FROM cotizacion_salidas WHERE cotizacion_id=$1 AND clave_salida='MONEDA'`, f.runtime.CotizacionID).Scan(&moneda); err != nil || moneda != "USD" {
		t.Fatalf("moneda=%s err=%v", moneda, err)
	}
	if err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT snapshot_json FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, f.runtime.CotizacionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var s snapshotCotizacion
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Version != 1 || s.CompiladoID == "" || len(s.Salidas) != 5 || len(s.Items) != 1 || s.Valores[f.precio] != "2350" || s.Estructura["salidas"] == nil {
		t.Fatalf("snapshot incompleto: %s", raw)
	}
	if s.Items[0].Categoria != "Total de la oferta" {
		t.Fatal("se inventó una categoría")
	}
}

func TestSalidas_AT02_BorradorRegeneraSinDuplicados(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	exigirGuardadoSalida(t, f.guardar(t, 1, "2500"))
	if v := leerSalidaNumeroPrueba(t, f.runtime, 1, "TOTAL_PRECIO"); v != 2500 {
		t.Fatal(v)
	}
	var cantidad, claves int
	if err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*),COUNT(DISTINCT clave_salida) FROM cotizacion_salidas WHERE cotizacion_id=$1 AND numero_version=1`, f.runtime.CotizacionID).Scan(&cantidad, &claves); err != nil || cantidad != 5 || claves != 5 {
		t.Fatalf("%d filas/%d claves: %v", cantidad, claves, err)
	}
}

func TestSalidas_AT03_V2NoModificaV1(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	antes := huellaVersionSalidas(t, f.runtime, 1)
	crearV2Salidas(t, f.runtime)
	if v := leerSalidaNumeroPrueba(t, f.runtime, 2, "TOTAL_PRECIO"); v != 2350 {
		t.Fatal(v)
	}
	exigirGuardadoSalida(t, f.guardar(t, 2, "2000"))
	if huellaVersionSalidas(t, f.runtime, 1) != antes {
		t.Fatal("crear/guardar V2 alteró V1")
	}
	if v := leerSalidaNumeroPrueba(t, f.runtime, 2, "TOTAL_PRECIO"); v != 2000 {
		t.Fatal(v)
	}
	if rec := f.guardar(t, 1, "1"); rec.Code != 409 {
		t.Fatalf("se permitió escribir V1: %d %s", rec.Code, rec.Body.String())
	}
	if huellaVersionSalidas(t, f.runtime, 1) != antes {
		t.Fatal("el rechazo alteró V1")
	}
}

func TestSalidas_AT04_AceptarV1ConV2Actual(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	crearV2Salidas(t, f.runtime)
	exigirGuardadoSalida(t, f.guardar(t, 2, "2000"))
	rec := postCotizacionSubruta(t, (&CotizacionesHandler{DB: f.runtime.Handler.DB}).CambiarEstado, "/api/cotizaciones/{id}/estado", f.runtime.CotizacionID, map[string]any{"version": 1, "estado": "Aceptada", "aceptada_por": "Cliente QA"})
	exigirGuardadoSalida(t, rec)
	var actual, aceptada int
	var monto float64
	err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT c.version_actual,c.version_aceptada,s.valor_numero FROM cotizaciones c JOIN cotizacion_salidas s ON s.cotizacion_id=c.cotizacion_id AND s.numero_version=c.version_aceptada AND s.clave_salida='TOTAL_PRECIO' WHERE c.cotizacion_id=$1`, f.runtime.CotizacionID).Scan(&actual, &aceptada, &monto)
	if err != nil || actual != 2 || aceptada != 1 || monto != 2350 {
		t.Fatalf("actual=%d aceptada=%d monto=%v err=%v", actual, aceptada, monto, err)
	}
}

func TestSalidas_AT05_CambioEstadoNoRecalcula(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	antes := huellaVersionSalidas(t, f.runtime, 1)
	rec := postCotizacionSubruta(t, (&CotizacionesHandler{DB: f.runtime.Handler.DB}).CambiarEstado, "/api/cotizaciones/{id}/estado", f.runtime.CotizacionID, map[string]any{"version": 1, "estado": "Enviada al Cliente"})
	exigirGuardadoSalida(t, rec)
	if huellaVersionSalidas(t, f.runtime, 1) != antes {
		t.Fatal("cambiar estado reescribió datos financieros")
	}
	var historial int
	if err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_historial WHERE cotizacion_id=$1 AND estado_nuevo='Enviada al Cliente'`, f.runtime.CotizacionID).Scan(&historial); err != nil || historial != 1 {
		t.Fatalf("historial=%d, err=%v", historial, err)
	}
	if rec := f.guardar(t, 1, "1"); rec.Code != 409 {
		t.Fatal("se pudo editar una versión enviada")
	}
}

func TestSalidas_AT06_TresOpcionesSoloLaRecomendada(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	padre := f.crear(t, "OPCIONES_PROPUESTA", "PLANES", map[string]any{"cantidad_inicial": 3, "nombres_sugeridos": "A,B,C"}, nil)
	precio := f.crear(t, "CAMPO", "PRECIO", map[string]any{"tipo_campo": "MONEDA"}, map[string]any{"componente_padre_id": padre})
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "ESCENARIO", precio, "", true))
	rt := f.runtime(t)
	rec := getRuntime(t, rt, "")
	exigirGuardadoSalida(t, rec)
	ops, err := listarOpcionesPropuesta(context.Background(), f.handler.DB, rt.CotizacionID, 1, padre)
	if err != nil || len(ops) != 3 {
		t.Fatalf("opciones=%v err=%v", ops, err)
	}
	valores := []any{}
	for i, op := range ops {
		valores = append(valores, map[string]any{"elemento_id": precio, "opcion_id": op.OpcionID, "valor": []string{"10000", "12000", "15000"}[i]})
	}
	rec = postValoresRuntime(t, rt, map[string]any{"version": 1, "valores_por_opcion": valores})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "recomendada") {
		t.Fatalf("sin efectiva: %d %s", rec.Code, rec.Body.String())
	}
	exigirGuardadoSalida(t, postOpcionesRuntime(t, rt, map[string]any{"version": 1, "elemento_padre_id": padre, "accion": "RECOMENDAR", "opcion_id": ops[1].OpcionID}))
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores_por_opcion": valores}))
	if v := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO"); v != 12000 {
		t.Fatal(v)
	}
	// Cambiar la recomendada actualiza la misma salida de forma atómica.
	exigirGuardadoSalida(t, postOpcionesRuntime(t, rt, map[string]any{"version": 1, "elemento_padre_id": padre, "accion": "RECOMENDAR", "opcion_id": ops[2].OpcionID}))
	if v := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO"); v != 15000 {
		t.Fatal(v)
	}
	antes := huellaVersionSalidas(t, rt, 1)
	crearV2Salidas(t, rt)
	nuevas, err := listarOpcionesPropuesta(context.Background(), f.handler.DB, rt.CotizacionID, 2, padre)
	if err != nil || len(nuevas) != 3 {
		t.Fatalf("copia opciones: %v %v", nuevas, err)
	}
	for _, n := range nuevas {
		for _, o := range ops {
			if n.OpcionID == o.OpcionID {
				t.Fatal("V2 reutilizó IDs de V1")
			}
		}
	}
	if huellaVersionSalidas(t, rt, 1) != antes {
		t.Fatal("copiar opciones alteró V1")
	}
	rec = postOpcionesRuntime(t, rt, map[string]any{"version": 1, "elemento_padre_id": padre, "accion": "RECOMENDAR", "opcion_id": ops[0].OpcionID})
	if rec.Code != 409 {
		t.Fatal("se modificó una opción histórica")
	}
}

func TestSalidas_AT07_RequeridaSinFuenteBloqueaPublicacion(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	f.crear(t, "CAMPO", "N", map[string]any{"tipo_campo": "NUMERO"}, nil)
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "CAMPO", "", "", true))
	res := postCompilador(t, (&CompiladorHandler{DB: f.handler.DB}).Compilar, f.calculadoraID)
	if res.Valido || res.Compilado || !strings.Contains(fmt.Sprint(res.Errores), "TOTAL_PRECIO") {
		t.Fatalf("publicación incorrecta: %+v", res)
	}
}

func TestSalidas_AT08_TextoNoPuedeSerTotalPrecio(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	id := f.crear(t, "CAMPO", "TEXTO", map[string]any{"tipo_campo": "TEXTO"}, nil)
	rec := mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "CAMPO", id, "", true)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "TOTAL_PRECIO requiere") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// AT-09 completo pertenece a la Ronda C (Dashboard). Aquí se verifica
// únicamente su precondición: consultar salidas tipadas sin leer snapshot_json.
func TestSalidas_AT09_ContratoNormalizadoParaRondaC(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	var monto float64
	var moneda string
	err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT s.valor_numero,s.moneda FROM cotizacion_salidas s JOIN cotizaciones c ON c.cotizacion_id=s.cotizacion_id AND c.version_actual=s.numero_version WHERE c.cotizacion_id=$1 AND s.clave_salida='TOTAL_PRECIO'`, f.runtime.CotizacionID).Scan(&monto, &moneda)
	if err != nil || monto != 2350 || moneda != "USD" {
		t.Fatalf("contrato normalizado: %v %s %v", monto, moneda, err)
	}
}

func TestSalidas_AT10_CambioPrecioNoAlteraHistoricas(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	lista := f.crear(t, "LISTA_PRECIOS", "Implementación", map[string]any{"tipo_lista_precios": "UNICA"}, nil)
	_, resItem := crearItemListaPrecios(t, &ListaPreciosItemsHandler{DB: f.handler.DB}, lista, map[string]any{"codigo": "ISA", "nombre": "Implementación ISA", "precio": 1500, "costo_interno": 800})
	item := fmt.Sprint(resItem["item_id"])
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "LISTA_PRECIO", lista, "total_precio", true))
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_COSTO", "LISTA_PRECIO", lista, "total_costo", true))
	rt := f.runtime(t)
	valores := map[string]any{lista: map[string]any{"item_id": item, "cantidad": 2}}
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": valores}))
	antes := huellaVersionSalidas(t, rt, 1)
	crearV2Salidas(t, rt)
	if _, err := f.handler.DB.Exec(context.Background(), `UPDATE lista_precios_items SET precio=2000,costo_interno=900,nombre='Nombre nuevo' WHERE item_id::text=$1`, item); err != nil {
		t.Fatal(err)
	}
	rec := getRuntime(t, rt, "version=1")
	exigirGuardadoSalida(t, rec)
	var respuesta struct {
		Estructura  map[string]any `json:"estructura"`
		SoloLectura bool           `json:"solo_lectura"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	if !respuesta.SoloLectura || elementoPorIDEnEstructura(respuesta.Estructura, lista)["valor_resuelto"] != 3000.0 {
		t.Fatalf("histórico cambió: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "costo_interno") || strings.Contains(rec.Body.String(), "Nombre nuevo") {
		t.Fatal("se filtraron costos o se leyó la fuente mutable")
	}
	publicas, err := (&EnlacesPublicosHandler{DB: f.handler.DB}).consultarTabsYValores(context.Background(), rt.CotizacionID, 1)
	if err != nil || len(publicas) != 1 || publicas[0].Elementos[0].Valor != 3000.0 {
		t.Fatalf("lectura pública no congelada: %+v %v", publicas, err)
	}
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 2, "valores": valores}))
	if v := leerSalidaNumeroPrueba(t, rt, 2, "TOTAL_PRECIO"); v != 4000 {
		t.Fatal(v)
	}
	if huellaVersionSalidas(t, rt, 1) != antes {
		t.Fatal("los nuevos precios reescribieron V1")
	}
	var categoria string
	var precio, costo float64
	if err := f.handler.DB.QueryRow(context.Background(), `SELECT categoria,total_precio,total_costo FROM cotizacion_items WHERE cotizacion_id=$1 AND numero_version=1`, rt.CotizacionID).Scan(&categoria, &precio, &costo); err != nil || categoria != "Implementación" || precio != 3000 || costo != 1600 {
		t.Fatalf("desglose: %s %v %v %v", categoria, precio, costo, err)
	}
}

func TestSalidas_AtomicidadSalidaRequerida(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	exigirGuardadoSalida(t, f.guardar(t, 1, "2350"))
	antes := huellaVersionSalidas(t, f.runtime, 1)
	var eventosAntes, eventosDespues int
	f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_historial WHERE cotizacion_id=$1`, f.runtime.CotizacionID).Scan(&eventosAntes)
	rec := f.guardar(t, 1, "0") // MARGEN = GANANCIA / PRECIO: división entre cero.
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "MARGEN_TOTAL") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if huellaVersionSalidas(t, f.runtime, 1) != antes {
		t.Fatal("fallo parcial: snapshot/salidas/items cambiaron")
	}
	var precio string
	if err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT valor #>> '{}' FROM cotizacion_valores WHERE cotizacion_id=$1 AND elemento_id=$2`, f.runtime.CotizacionID, f.precio).Scan(&precio); err != nil || precio != "2350" {
		t.Fatalf("valor no revertido: %s %v", precio, err)
	}
	f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_historial WHERE cotizacion_id=$1`, f.runtime.CotizacionID).Scan(&eventosDespues)
	if eventosAntes != eventosDespues {
		t.Fatal("se registró historial de un guardado fallido")
	}
}

func TestSalidas_CRUDComposicionCiclosYPertenencia(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	n := f.crear(t, "CAMPO", "N", map[string]any{"tipo_campo": "NUMERO"}, nil)
	h := &SalidasCotizadorHandler{DB: f.handler.DB}
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "CAMPO", n, "", true))
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_COSTO", "SALIDA", "TOTAL_PRECIO", "", true))
	rec := mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "SALIDA", "TOTAL_COSTO", "", true)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "circular") {
		t.Fatalf("ciclo: %d %s", rec.Code, rec.Body.String())
	}
	rec = mapearSalidaPrueba(t, f, "MONEDA", "SALIDA", "TOTAL_PRECIO", "", false)
	if rec.Code != 400 {
		t.Fatal("se aceptó composición incompatible")
	}
	otra := crearFixtureFormulaAvanzada(t)
	otro := otra.crear(t, "CAMPO", "OTRO", map[string]any{"tipo_campo": "NUMERO"}, nil)
	rec = mapearSalidaPrueba(t, f, "SUBTOTAL", "CAMPO", otro, "", false)
	if rec.Code != 400 {
		t.Fatal("se aceptó fuente ajena")
	}
	router := chi.NewRouter()
	router.Get("/salidas", h.Listar)
	router.Delete("/salidas/{clave_salida}", h.Eliminar)
	peticion := func(metodo, ruta string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(metodo, ruta+"?calculadora_id="+url.QueryEscape(f.calculadoraID), nil))
		return rec
	}
	rec = peticion(http.MethodGet, "/salidas")
	exigirGuardadoSalida(t, rec)
	if !strings.Contains(rec.Body.String(), "TOTAL_COSTO") {
		t.Fatal(rec.Body.String())
	}
	if rec = peticion(http.MethodDelete, "/salidas/TOTAL_PRECIO"); rec.Code != 409 {
		t.Fatal("eliminó una dependencia utilizada")
	}
	rt := f.runtime(t)
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{n: "42"}}))
	if v := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_COSTO"); v != 42 {
		t.Fatal(v)
	}
	exigirGuardadoSalida(t, peticion(http.MethodDelete, "/salidas/TOTAL_COSTO"))
	exigirGuardadoSalida(t, peticion(http.MethodDelete, "/salidas/TOTAL_PRECIO"))
	if rec = peticion(http.MethodDelete, "/salidas/TOTAL_PRECIO"); rec.Code != 404 {
		t.Fatal("borrado inexistente")
	}
}

func TestSalidas_CatalogoCongelaEtiquetaYValorCalculo(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	cat := f.catalogo(t, "MARGEN", "M30", 0.30)
	calc := f.crear(t, "CAMPO_CALCULADO", "TOTAL", configFormulaPrueba("70 / (1 - MARGEN)"), nil)
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "CALCULADO", calc, "", true))
	rt := f.runtime(t)
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{cat: "M30"}}))
	crearV2Salidas(t, rt)
	if _, err := f.handler.DB.Exec(context.Background(), `UPDATE catalogo_valores SET valor_calculo=0.50,texto_visible='Nuevo 50%' WHERE catalogo_id=(SELECT catalogo_id FROM elementos_tab_cotizador WHERE elemento_id=$1)`, cat); err != nil {
		t.Fatal(err)
	}
	rec := getRuntime(t, rt, "version=1")
	exigirGuardadoSalida(t, rec)
	if strings.Contains(rec.Body.String(), "Nuevo 50%") {
		t.Fatal("etiqueta histórica cambió")
	}
	if v := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO"); v != 100 {
		t.Fatal(v)
	}
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 2, "valores": map[string]any{cat: "M30"}}))
	if v := leerSalidaNumeroPrueba(t, rt, 2, "TOTAL_PRECIO"); v != 140 {
		t.Fatal(v)
	}
}

func TestSalidas_TablaAportaSusFilas(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	tabla := f.crear(t, "TABLA", "Consultoría", map[string]any{"permitir_agregar_filas": true, "permitir_eliminar_filas": true}, nil)
	_, col := crearColumnaTabla(t, &TablaColumnasHandler{DB: f.handler.DB}, tabla, map[string]any{"origen": "PROPIA", "tipo_dato": "MONEDA", "etiqueta": "Precio", "orden": 1})
	colID := fmt.Sprint(col["columna_id"])
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "TOTAL_TABLA", tabla, "", true))
	rt := f.runtime(t)
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{tabla: map[string]any{"filas": []any{map[string]any{colID: 100}, map[string]any{colID: 200}}}}}))
	if v := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO"); v != 300 {
		t.Fatal(v)
	}
	var filas int
	var suma float64
	if err := f.handler.DB.QueryRow(context.Background(), `SELECT COUNT(*),SUM(total_precio) FROM cotizacion_items WHERE cotizacion_id=$1 AND categoria='Consultoría'`, rt.CotizacionID).Scan(&filas, &suma); err != nil || filas != 2 || suma != 300 {
		t.Fatalf("filas=%d suma=%v err=%v", filas, suma, err)
	}
}

func TestSalidas_PrimerGuardadoFallidoNoFijaCompiladoNiSnapshot(t *testing.T) {
	f := crearFixtureSalidasISA(t)
	rec := f.guardar(t, 1, "0")
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var vacio bool
	var campos, historial int
	err := f.runtime.Handler.DB.QueryRow(context.Background(), `SELECT c.compilado_id_usado IS NULL AND cv.snapshot_json IS NULL,
		(SELECT COUNT(*) FROM cotizacion_valores WHERE cotizacion_id=c.cotizacion_id),
		(SELECT COUNT(*) FROM cotizacion_historial WHERE cotizacion_id=c.cotizacion_id)
		FROM cotizaciones c JOIN cotizacion_versiones cv USING(cotizacion_id) WHERE c.cotizacion_id=$1`, f.runtime.CotizacionID).Scan(&vacio, &campos, &historial)
	if err != nil || !vacio || campos != 0 || historial != 0 {
		t.Fatalf("guardado parcial: %v %d %d %v", vacio, campos, historial, err)
	}
}

func TestSalidas_ReferenciaRotaBloqueaCompilacion(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	campo := f.crear(t, "CAMPO", "N", map[string]any{"tipo_campo": "NUMERO"}, nil)
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, f, "TOTAL_PRECIO", "CAMPO", campo, "", true))
	if _, err := f.handler.DB.Exec(context.Background(), `UPDATE elementos_tab_cotizador SET activo=false WHERE elemento_id=$1`, campo); err != nil {
		t.Fatal(err)
	}
	res := postCompilador(t, (&CompiladorHandler{DB: f.handler.DB}).Compilar, f.calculadoraID)
	if res.Compilado || res.Valido || !strings.Contains(fmt.Sprint(res.Errores), "TOTAL_PRECIO") {
		t.Fatalf("referencia rota: %+v", res)
	}
}
