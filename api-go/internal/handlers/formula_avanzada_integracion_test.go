package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixtureFormulaAvanzada struct {
	handler              *CotizadorTabsHandler
	calculadoraID, tabID string
}

func crearFixtureFormulaAvanzada(t *testing.T) fixtureFormulaAvanzada {
	t.Helper()
	h, calc := crearCalculadoraTabsPrueba(t)
	f := fixtureFormulaAvanzada{h, calc, "TEST-TAB-AV-" + sufijoUnico()}
	rec := postCatalogos(t, h.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": f.tabID, "calculadora_id": calc, "nombre": "ISA Custom", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	return f
}

func (f fixtureFormulaAvanzada) guardar(t *testing.T, id, tipo, nombre string, cfg map[string]any, extra map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["nombre_elemento"] = nombre // Compatibilidad con el nombre interno del diseñador legado.
	body := map[string]any{"elemento_id": id, "tab_id": f.tabID, "tipo": tipo, "etiqueta": nombre, "configuracion": cfg, "activo": true}
	for k, v := range extra {
		body[k] = v
	}
	return postCatalogos(t, f.handler.GuardarElemento, "/api/cotizador/elementos", body)
}

func (f fixtureFormulaAvanzada) crear(t *testing.T, tipo, nombre string, cfg map[string]any, extra map[string]any) string {
	t.Helper()
	id := "TEST-EL-AV-" + sufijoUnico()
	rec := f.guardar(t, id, tipo, nombre, cfg, extra)
	if rec.Code != http.StatusOK {
		t.Fatalf("crear %s: %d %s", nombre, rec.Code, rec.Body.String())
	}
	return id
}

func configFormulaPrueba(texto string) map[string]any {
	return map[string]any{"tipo_formula": "AVANZADA", "formula_texto": texto, "tipo_resultado": "MONEDA", "decimales": 2,
		"operandos": []string{"NO-CONFIAR-EN-EL-CLIENTE"}, "tokens_operandos": map[string]string{"FALSO": "ID-FALSO"}}
}

func (f fixtureFormulaAvanzada) catalogo(t *testing.T, nombre, seleccion string, calculo float64) string {
	t.Helper()
	h := &CatalogosHandler{DB: f.handler.DB}
	id := crearCatalogoPrueba(t, f.handler.DB, nombre, "")
	rec := postCatalogos(t, h.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": id, "nombre_catalogo": nombre, "activo": true, "tipo_calculo": "NUMERO",
	})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec = postCatalogos(t, h.GuardarValor, "/api/catalogos/valores", map[string]any{
		"valor_id": "TEST-VAL-AV-" + sufijoUnico(), "catalogo_id": id, "clave": seleccion,
		"texto_visible": seleccion, "valor_sistema": seleccion, "valor_calculo": calculo, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	return f.crear(t, "CAMPO_CATALOGO", nombre, nil, map[string]any{"catalogo_id": id})
}

func (f fixtureFormulaAvanzada) runtime(t *testing.T) fixtureRuntime {
	t.Helper()
	res := postCompilador(t, (&CompiladorHandler{DB: f.handler.DB}).Compilar, f.calculadoraID)
	if !res.OK || !res.Compilado || !res.Valido {
		t.Fatalf("compilar: %+v", res)
	}
	id, _, _ := crearCotizacionPrueba(t, f.handler.DB, "Borrador", "", "")
	if _, err := f.handler.DB.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, f.calculadoraID, id); err != nil {
		t.Fatal(err)
	}
	return fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: f.handler.DB}, CotizacionID: id}
}

func TestFormulaAvanzada_IntegracionISA(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	telefono := f.crear(t, "CAMPO", "USA_TELEFONIA", map[string]any{"tipo_campo": "TEXTO"}, nil)
	chat := f.crear(t, "CAMPO", "USA_CHAT", map[string]any{"tipo_campo": "TEXTO"}, nil)
	minutos := f.catalogo(t, "MINUTOS_MES", "PLAN_100", 100)
	margen := f.catalogo(t, "MARGEN_CHAT", "M30", 0.30)
	costo := f.crear(t, "CAMPO", "COSTO_MINUTO_VOZ", map[string]any{"tipo_campo": "MONEDA"}, nil)
	costoChat := f.crear(t, "CAMPO", "COSTO_CHAT", map[string]any{"tipo_campo": "NUMERO"}, nil)
	voz := f.crear(t, "CAMPO_CALCULADO", "PRECIO_VOZ", configFormulaPrueba("SI(USA_TELEFONIA; MINUTOS_MES * COSTO_MINUTO_VOZ; 0)"), nil)
	precioChat := f.crear(t, "CAMPO_CALCULADO", "PRECIO_CHAT", configFormulaPrueba("COSTO_CHAT / (1 - MARGEN_CHAT)"), nil)
	encadenado := f.crear(t, "CAMPO_CALCULADO", "TOTAL_ISA", configFormulaPrueba("SI(USA_TELEFONIA; PRECIO_VOZ; SI(USA_CHAT; PRECIO_CHAT; 0))"), map[string]any{"funcion_campo": "TOTAL_PRECIO_OFERTA"})
	rt := f.runtime(t)
	for _, caso := range []struct {
		nombre, voz, chat          string
		esperadoVoz, esperadoTotal float64
	}{
		{"telefonía", "Sí", "No", 25, 25}, {"chat", "No", "SI", 0, 100}, {"ninguno", "No", "No", 0, 0},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{
				telefono: caso.voz, chat: caso.chat, minutos: "PLAN_100", margen: "M30", costo: "0.25", costoChat: "70",
			}})
			if rec.Code != http.StatusOK {
				t.Fatalf("guardar: %s", rec.Body.String())
			}
			rec = getRuntime(t, rt, "version=1")
			if rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			var res struct {
				Estructura map[string]any `json:"estructura"`
			}
			assertJSON(t, rec.Body.Bytes(), &res)
			for id, esperado := range map[string]float64{voz: caso.esperadoVoz, precioChat: 100, encadenado: caso.esperadoTotal} {
				el := elementoPorIDEnEstructura(res.Estructura, id)
				if el["valor_resuelto"] != esperado {
					t.Fatalf("%s: esperado %v, dio %v", id, esperado, el["valor_resuelto"])
				}
			}
			cfg := elementoPorIDEnEstructura(res.Estructura, voz)["configuracion"].(map[string]any)
			mapa := cfg["tokens_operandos"].(map[string]any)
			if cfg["tipo_formula"] != "AVANZADA" || cfg["formula_texto"] == "" || len(cfg["tokens"].([]any)) != 3 || len(cfg["operandos"].([]any)) != 3 || mapa["MINUTOS_MES"] != minutos || mapa["FALSO"] != nil {
				t.Fatalf("dependencias compiladas incorrectas: %+v", cfg)
			}
			var total float64
			if err := f.handler.DB.QueryRow(context.Background(), `SELECT total_precio FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, rt.CotizacionID).Scan(&total); err != nil || total != caso.esperadoTotal {
				t.Fatalf("total persistido=%v, error=%v", total, err)
			}
		})
	}
	// El JSON publicado conserva la asociación por ID incluso si el diseñador renombra luego el operando.
	f.guardar(t, costoChat, "CAMPO", "COSTO_RENOMBRADO", map[string]any{"tipo_campo": "NUMERO"}, nil)
	rec := getRuntime(t, rt, "version=1")
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if elementoPorIDEnEstructura(res.Estructura, precioChat)["valor_resuelto"] != 100.0 {
		t.Fatal("el renombrado alteró el compilado anterior")
	}
	validacion := postCompilador(t, (&CompiladorHandler{DB: f.handler.DB}).Validar, f.calculadoraID)
	if validacion.Valido {
		t.Fatal("una nueva publicación debe detectar el token renombrado")
	}
}

func TestFormulaAvanzada_IntegracionValidacionesYCiclos(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	n := f.crear(t, "CAMPO", "N", map[string]any{"tipo_campo": "NUMERO"}, nil)
	f.crear(t, "CAMPO", "BOOLEANO", map[string]any{"tipo_campo": "TEXTO"}, nil)
	f.crear(t, "LEYENDA", "LEYENDA", nil, nil)
	a := f.crear(t, "CAMPO_CALCULADO", "A", configFormulaPrueba("N + 1"), nil)
	b := f.crear(t, "CAMPO_CALCULADO", "B", map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "tipo_resultado": "NUMERO", "operandos": []string{a, n}}, nil)
	c := f.crear(t, "CAMPO_CALCULADO", "C", configFormulaPrueba("B + 1"), nil)
	repetido := f.crear(t, "CAMPO", "REPETIDO", map[string]any{"tipo_campo": "NUMERO"}, nil)
	// Ronda F2: el API ya no deja crear el duplicado (único por cotizador)...
	if rec := f.guardar(t, "TEST-EL-AV-"+sufijoUnico(), "CAMPO", "REPETIDO", map[string]any{"tipo_campo": "NUMERO"}, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), repetido) {
		t.Fatalf("un nombre interno repetido debe rechazarse nombrando al otro elemento: %d %s", rec.Code, rec.Body.String())
	}
	// ...pero datos anteriores pueden traerlo: se simula con SQL directo y la
	// fórmula lo sigue reportando como ambiguo en vez de elegir uno al azar.
	duplicadoLegado := "TEST-EL-AV-LEGADO-" + sufijoUnico()
	if _, err := f.handler.DB.Exec(context.Background(), `
		INSERT INTO elementos_tab_cotizador (elemento_id, tab_id, tipo, etiqueta, configuracion, activo)
		VALUES ($1, $2, 'CAMPO', 'REPETIDO', '{"tipo_campo":"NUMERO","nombre_elemento":"REPETIDO"}', true)`, duplicadoLegado, f.tabID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.handler.DB.Exec(context.Background(), `DELETE FROM elementos_tab_cotizador WHERE elemento_id=$1`, duplicadoLegado)
	})
	f.crear(t, "CAMPO", "INACTIVO", map[string]any{"tipo_campo": "NUMERO"}, map[string]any{"activo": false})
	otra := crearFixtureFormulaAvanzada(t)
	otra.crear(t, "CAMPO", "DE_OTRO_COTIZADOR", map[string]any{"tipo_campo": "NUMERO"}, nil)
	for _, caso := range []struct{ texto, errorEsperado string }{
		{"B + 1", "circular"}, {"C + 1", "circular"}, {"A + 1", "circular"}, {"SI(B; 1; 0)", "circular"},
		{"REPETIDO + 1", "ambiguo"}, {"X + 1", "de este cotizador"}, {"DE_OTRO_COTIZADOR + 1", "de este cotizador"},
		{"BOOLEANO + 1", "numérico"}, {"SI(BOOLEANO; BOOLEANO + 1; 0)", "numérico"}, {"LEYENDA + 1", "operando"},
		{"INACTIVO + 1", "inactivo"}, {"SI(N; 1; 0; 2)", "fórmula"}, {"N > 0", "carácter"}, {"N.VALOR_CALCULO", "carácter"},
	} {
		t.Run(caso.texto, func(t *testing.T) {
			rec := f.guardar(t, a, "CAMPO_CALCULADO", "A", configFormulaPrueba(caso.texto), nil)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), caso.errorEsperado) {
				t.Fatalf("esperaba 400/%s: %d %s", caso.errorEsperado, rec.Code, rec.Body.String())
			}
		})
	}
	// El modo Simple también detecta dependencias que pasan por una fórmula avanzada.
	rec := f.guardar(t, b, "CAMPO_CALCULADO", "B", map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "tipo_resultado": "NUMERO", "operandos": []string{c, n}}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "circular") {
		t.Fatal(rec.Body.String())
	}
	// Rechazos no alteran la fórmula válida previamente persistida.
	var cfg map[string]any
	if err := f.handler.DB.QueryRow(context.Background(), `SELECT configuracion FROM elementos_tab_cotizador WHERE elemento_id=$1`, a).Scan(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["formula_texto"] != "N + 1" {
		t.Fatalf("se persistió una fórmula inválida: %+v", cfg)
	}
	f.crear(t, "CAMPO_CALCULADO", "SOLO_CONDICION", configFormulaPrueba("SI(BOOLEANO; 1; 0)"), nil)
}
