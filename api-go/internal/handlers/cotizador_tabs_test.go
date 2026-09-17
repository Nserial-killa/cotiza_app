package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func crearCalculadoraTabsPrueba(t *testing.T) (*CotizadorTabsHandler, string) {
	t.Helper()
	pool := setupTestDB(t)
	id := "TEST-CALC-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora) VALUES ($1, 'Cotizador de prueba')`, id); err != nil {
		t.Fatalf("no se pudo crear la calculadora: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id)
	})
	return &CotizadorTabsHandler{DB: pool}, id
}

func TestCotizadorTabs_CrearYListar(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Datos generales",
		"alcance": "PROPIO", "orden": 2, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/tabs?calculadora_id="+url.QueryEscape(calculadoraID), nil)
	rec = httptest.NewRecorder()
	handler.ListarTabs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar tabs: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK   bool           `json:"ok"`
		Tabs []tabCotizador `json:"tabs"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || len(res.Tabs) != 1 || res.Tabs[0].TabID != tabID || res.Tabs[0].Nombre != "Datos generales" {
		t.Fatalf("tab guardado no apareció correctamente: %+v", res)
	}
}

func TestCotizadorElementos_CreaLosCuatroTiposSimples(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-EL-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Elementos", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %d: %s", rec.Code, rec.Body.String())
	}
	catalogoID := crearCatalogoPrueba(t, handler.DB, "Catálogo para elemento", "")
	tipos := []string{"CAMPO", "CAMPO_CATALOGO", "LEYENDA", "TEXTO_INFORMATIVO"}
	for i, tipo := range tipos {
		body := map[string]any{
			"elemento_id": "TEST-EL-" + tipo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": tipo, "etiqueta": "Elemento " + tipo, "columnas_ancho": 1,
			"orden": i + 1, "requerido": tipo == "CAMPO", "configuracion": map[string]any{"prueba": true}, "activo": true,
		}
		if tipo == "CAMPO_CATALOGO" {
			body["catalogo_id"] = catalogoID
		}
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("crear %s: esperaba 200, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	rec = httptest.NewRecorder()
	handler.ListarElementos(rec, req)
	var res struct {
		OK        bool                   `json:"ok"`
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || !res.OK || len(res.Elementos) != len(tipos) {
		t.Fatalf("esperaba %d elementos persistidos, obtuvo %d: %s", len(tipos), len(res.Elementos), rec.Body.String())
	}
}

func TestCotizadorElementos_ValidaCatalogoSegunTipo(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-VALID-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Validaciones", "activo": true,
	})
	catalogoID := crearCatalogoPrueba(t, handler.DB, "Catálogo validación", "")

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-SIN-CAT-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO_CATALOGO", "etiqueta": "Sin catálogo", "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CAMPO_CATALOGO sin catalogo_id: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	for _, tipo := range []string{"CAMPO", "LEYENDA", "TEXTO_INFORMATIVO"} {
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": "TEST-EL-CAT-INVALIDO-" + tipo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": tipo, "etiqueta": "Catálogo inválido", "catalogo_id": catalogoID, "activo": true,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s con catalogo_id: esperaba 400, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}
}

// TestCotizadorElementos_ContenedorConHijos cubre la Ronda 1 de tipos
// nuevos (migración 0017): crea un Contenedor de 2 columnas y dos Campos
// con componente_padre_id apuntando a él, dentro del mismo tab.
func TestCotizadorElementos_ContenedorConHijos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CONT-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Contenedores", "activo": true,
	})
	contenedorID := "TEST-EL-CONT-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": contenedorID, "tab_id": tabID, "tipo": "CONTENEDOR", "etiqueta": "Datos del cliente",
		"orden": 1, "configuracion": map[string]any{"columnas": 2, "estilo": "card"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear contenedor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	for i, sufijo := range []string{"A", "B"} {
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": "TEST-EL-HIJO-" + sufijo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": "CAMPO", "etiqueta": "Campo " + sufijo, "componente_padre_id": contenedorID,
			"orden": i + 2, "activo": true,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("crear hijo %s: esperaba 200, dio %d: %s", sufijo, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	rec = httptest.NewRecorder()
	handler.ListarElementos(rec, req)
	var res struct {
		OK        bool                   `json:"ok"`
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || len(res.Elementos) != 3 {
		t.Fatalf("esperaba 3 elementos (contenedor + 2 hijos): %+v", res)
	}
	hijos := 0
	for _, el := range res.Elementos {
		if el.ComponentePadreID != nil && *el.ComponentePadreID == contenedorID {
			hijos++
		}
	}
	if hijos != 2 {
		t.Fatalf("esperaba 2 hijos con componente_padre_id=%s, obtuvo %d: %+v", contenedorID, hijos, res.Elementos)
	}
}

func TestCotizadorElementos_ContenedorValidaColumnasYSinPadre(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CONT-VAL-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Validación contenedor", "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CONT-BAD-COLS-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CONTENEDOR", "etiqueta": "Malo", "configuracion": map[string]any{"columnas": 5}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("columnas=5: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	otroContenedorID := "TEST-EL-CONT-OTRO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": otroContenedorID, "tab_id": tabID, "tipo": "CONTENEDOR", "etiqueta": "Otro",
		"configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CONT-CON-PADRE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CONTENEDOR", "etiqueta": "No debería poder tener padre", "componente_padre_id": otroContenedorID,
		"configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("contenedor con padre: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizadorElementos_PadreDebeSerContenedorActivoDelMismoTab(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-PADRE-" + sufijoUnico()
	otroTabID := "TEST-TAB-PADRE-OTRO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Padre", "activo": true,
	})
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": calculadoraID, "nombre": "Otro tab", "activo": true,
	})

	campoID := "TEST-EL-NO-CONTENEDOR-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "No es contenedor", "activo": true,
	})
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-HIJO-BAD-TIPO-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Hijo", "componente_padre_id": campoID, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("padre no es CONTENEDOR: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	contenedorOtroTabID := "TEST-EL-CONT-OTRO-TAB-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": contenedorOtroTabID, "tab_id": otroTabID, "tipo": "CONTENEDOR", "etiqueta": "Contenedor de otro tab",
		"configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-HIJO-OTRO-TAB-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Hijo cruzado", "componente_padre_id": contenedorOtroTabID, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("padre de otro tab: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizadorElementos_CajaValorValidaCampoFuente(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CAJA-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Caja de valor", "activo": true,
	})
	campoID := "TEST-EL-FUENTE-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Total", "activo": true,
	})

	cajaID := "TEST-EL-CAJA-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": cajaID, "tab_id": tabID, "tipo": "CAJA_VALOR", "etiqueta": "Total mostrado",
		"campo_fuente_id": campoID, "configuracion": map[string]any{"prefijo": "US$ ", "valor_por_defecto": "0.00"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear caja de valor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAJA-SIN-FUENTE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAJA_VALOR", "etiqueta": "Sin campo fuente", "configuracion": map[string]any{"valor_por_defecto": "0.00"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("caja de valor sin campo fuente debe ser válida: %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAJA-FUENTE-INEXISTENTE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAJA_VALOR", "etiqueta": "Fuente inexistente", "campo_fuente_id": "NO-EXISTE-" + sufijoUnico(), "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("campo_fuente_id inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAMPO-CON-FUENTE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "No debería aceptar fuente", "campo_fuente_id": campoID, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("campo_fuente_id en tipo CAMPO: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_CampoCalculadoValidaOperandos cubre la Ronda 2 del
// Diseñador (migración 0018): un Campo Calculado con dos operandos CAMPO
// numéricos válidos se guarda; sin operandos suficientes, con un operando
// inexistente, de otro tab, o no numérico (CAMPO texto), se rechaza.
func TestCotizadorElementos_CampoCalculadoValidaOperandos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CALC-" + sufijoUnico()
	otroTabID := "TEST-TAB-CALC-OTRO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Cálculo", "activo": true,
	})
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": calculadoraID, "nombre": "Otro tab", "activo": true,
	})

	campoNumericoID := "TEST-EL-NUM-A-" + sufijoUnico()
	campoNumericoBID := "TEST-EL-NUM-B-" + sufijoUnico()
	campoTextoID := "TEST-EL-TXT-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoNumericoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Costo",
		"configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoNumericoBID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Margen",
		"configuracion": map[string]any{"tipo_campo": "PORCENTAJE"}, "activo": true,
	})
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoTextoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Nombre",
		"configuracion": map[string]any{"tipo_campo": "TEXTO"}, "activo": true,
	})
	campoOtroTabID := "TEST-EL-OTRO-TAB-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoOtroTabID, "tab_id": otroTabID, "tipo": "CAMPO", "etiqueta": "De otro tab",
		"configuracion": map[string]any{"tipo_campo": "NUMERO"}, "activo": true,
	})

	casos := []struct {
		nombre    string
		operandos []string
		operacion string
		esperaOK  bool
	}{
		{"dos operandos numéricos válidos", []string{campoNumericoID, campoNumericoBID}, "SUMA", true},
		{"un solo operando en SUMA (mínimo 2)", []string{campoNumericoID}, "SUMA", false},
		{"un solo operando en PROMEDIO sí alcanza", []string{campoNumericoID}, "PROMEDIO", true},
		{"operando inexistente", []string{campoNumericoID, "NO-EXISTE-" + sufijoUnico()}, "SUMA", false},
		{"operando de otro tab", []string{campoNumericoID, campoOtroTabID}, "SUMA", false},
		{"operando de tipo texto", []string{campoNumericoID, campoTextoID}, "SUMA", false},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
				"elemento_id": "TEST-EL-CALC-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO_CALCULADO",
				"etiqueta": "Calculado", "configuracion": map[string]any{
					"operacion": c.operacion, "tipo_resultado": "MONEDA", "decimales": 2, "operandos": c.operandos,
				}, "activo": true,
			})
			if c.esperaOK && rec.Code != http.StatusOK {
				t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
			}
			if !c.esperaOK && rec.Code != http.StatusBadRequest {
				t.Fatalf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCotizadorElementos_CampoCalculadoAnidadoYCircular cubre dependencia de
// 2 niveles (B depende de A, C depende de B) y el rechazo de un ciclo (A
// pasa a depender de C, que depende de B, que depende de A).
func TestCotizadorElementos_CampoCalculadoAnidadoYCircular(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CIRC-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Circular", "activo": true,
	})
	campoBaseID := "TEST-EL-BASE-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoBaseID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Base",
		"configuracion": map[string]any{"tipo_campo": "NUMERO"}, "activo": true,
	})
	calcAID := "TEST-EL-CALC-A-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcAID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "A",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{campoBaseID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear A: %s", rec.Body.String())
	}
	calcBID := "TEST-EL-CALC-B-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcBID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "B (depende de A)",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{calcAID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear B: %s", rec.Body.String())
	}
	calcCID := "TEST-EL-CALC-C-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcCID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "C (depende de B)",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{calcBID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear C (anidado 2 niveles): %s", rec.Body.String())
	}

	// Ahora A pasa a depender de C: A -> C -> B -> A, ciclo.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcAID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "A",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{calcCID}}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("referencia circular: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_FuncionCampoUnicaPorCotizadorNoGlobal cubre que la
// unicidad de funcion_campo (distinta de NORMAL) es por calculadora_id, no
// global: el mismo rol se rechaza dos veces en el mismo cotizador pero se
// permite en cotizadores distintos.
func TestCotizadorElementos_FuncionCampoUnicaPorCotizadorNoGlobal(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	_, otraCalculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-FUNC-" + sufijoUnico()
	otroTabID := "TEST-TAB-FUNC-OTRA-CALC-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Función", "activo": true,
	})
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": otraCalculadoraID, "nombre": "Función otra calc", "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-FUNC-1-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Total 1",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("primer TOTAL_PRECIO_OFERTA: %s", rec.Body.String())
	}
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-FUNC-2-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Total 2",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("segundo TOTAL_PRECIO_OFERTA en el mismo cotizador: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-FUNC-OTRA-CALC-" + sufijoUnico(), "tab_id": otroTabID, "tipo": "CAMPO", "etiqueta": "Total otra calc",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mismo rol en otro cotizador debería permitirse: %d: %s", rec.Code, rec.Body.String())
	}
	// NORMAL sí puede repetirse libremente.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-NORMAL-1-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Normal 1", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("normal 1: %s", rec.Body.String())
	}
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-NORMAL-2-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Normal 2", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("NORMAL repetido debería permitirse: %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_FuncionCampoSoloEnTiposConValorPropio cubre que
// funcion_campo (distinta de NORMAL) se rechaza en TITULO/CONTENEDOR/etc.
func TestCotizadorElementos_FuncionCampoSoloEnTiposConValorPropio(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-FUNC-TIPO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Función por tipo", "activo": true,
	})
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-TITULO-FUNC-" + sufijoUnico(), "tab_id": tabID, "tipo": "TITULO", "etiqueta": "Título",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("TITULO con funcion_campo: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizadorTabs_EliminarInactivaTabYElementos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-DELETE-" + sufijoUnico()
	elementoID := "TEST-EL-DELETE-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Eliminar", "activo": true,
	})
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Campo", "activo": true,
	})
	rec := deleteConRuta(t, "/api/cotizador/tabs/{id}", "/api/cotizador/tabs/"+tabID, handler.EliminarTab)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar tab: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var tabActivo, elementoActivo bool
	if err := handler.DB.QueryRow(context.Background(), `SELECT activo FROM tabs_cotizador WHERE tab_id=$1`, tabID).Scan(&tabActivo); err != nil {
		t.Fatal(err)
	}
	if err := handler.DB.QueryRow(context.Background(), `SELECT activo FROM elementos_tab_cotizador WHERE elemento_id=$1`, elementoID).Scan(&elementoActivo); err != nil {
		t.Fatal(err)
	}
	if tabActivo || elementoActivo {
		t.Fatalf("tab y elemento debían quedar inactivos: tab=%v elemento=%v", tabActivo, elementoActivo)
	}
}
