package handlers

// Pruebas de integración de /api/cotizador/reglas — CRUD de
// reglas_cotizador (migración 0024). Mismo criterio que catalogos_test.go:
// contra Postgres real, con fixtures propias.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fixtureReglasCotizador arma una calculadora con un tab y los elementos
// simples que necesitan las pruebas de reglas_cotizador (todos CAMPO
// numéricos o de texto, sin catálogos — no hace falta más para probar
// condición/acción).
type fixtureReglasCotizador struct {
	TabsHandler   *CotizadorTabsHandler
	ReglasHandler *ReglasCotizadorHandler
	Compilador    *CompiladorHandler
	CalculadoraID string
	TabID         string
	Elementos     map[string]string
}

func crearFixtureReglasCotizador(t *testing.T) fixtureReglasCotizador {
	t.Helper()
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-RC-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Reglas", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %s", rec.Body.String())
	}

	elementos := map[string]string{}
	crearCampo := func(nombre, tipoCampo string) {
		id := "TEST-EL-" + nombre + "-" + sufijoUnico()
		rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": id, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": nombre,
			"configuracion": map[string]any{"tipo_campo": tipoCampo}, "activo": true,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("crear campo %s: %s", nombre, rec.Body.String())
		}
		elementos[nombre] = id
	}
	crearCampo("USA_TELEFONIA", "TEXTO")
	crearCampo("MINUTOS_MES", "NUMERO")
	crearCampo("MINUTOS_EXTRA", "NUMERO")
	crearCampo("COSTO_VOZ", "MONEDA")
	crearCampo("PRECIO_VOZ", "MONEDA")
	crearCampo("CONSUMO_EXTRA_VOZ", "MONEDA")
	crearCampo("CANTIDAD_AGENTES", "NUMERO")
	crearCampo("CONVERSACIONES_EXTRA", "NUMERO")
	crearCampo("CONSUMO_EXTRA_CHAT", "MONEDA")

	return fixtureReglasCotizador{
		TabsHandler:   tabsHandler,
		ReglasHandler: &ReglasCotizadorHandler{DB: tabsHandler.DB},
		Compilador:    &CompiladorHandler{DB: tabsHandler.DB},
		CalculadoraID: calculadoraID,
		TabID:         tabID,
		Elementos:     elementos,
	}
}

func getReglasCotizador(t *testing.T, handler *ReglasCotizadorHandler, calculadoraID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/reglas?calculadora_id="+calculadoraID, nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)
	return rec
}

func TestReglasCotizadorGuardar_ExigeCampoCondicionDeLaMismaCalculadora(t *testing.T) {
	fixture := crearFixtureReglasCotizador(t)
	otraCalculadora := "TEST-CALC-OTRA-" + sufijoUnico()
	if _, err := fixture.TabsHandler.DB.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora) VALUES ($1, 'Otra')`, otraCalculadora); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fixture.TabsHandler.DB.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, otraCalculadora)
	})

	rec := postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": otraCalculadora,
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "OCULTAR", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"]}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("campo de otra calculadora: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReglasCotizadorGuardar_ExigeCamposObjetivoDeLaMismaCalculadora(t *testing.T) {
	fixture := crearFixtureReglasCotizador(t)
	otraCalculadora := "TEST-CALC-OTRA2-" + sufijoUnico()
	otroTab := "TEST-TAB-OTRA2-" + sufijoUnico()
	otroElemento := "TEST-EL-OTRA2-" + sufijoUnico()
	ctx := context.Background()
	if _, err := fixture.TabsHandler.DB.Exec(ctx, `INSERT INTO calculadoras (calculadora_id, nombre_calculadora) VALUES ($1, 'Otra 2')`, otraCalculadora); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.TabsHandler.DB.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, activo) VALUES ($1,$2,'Otro',true)`, otroTab, otraCalculadora); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.TabsHandler.DB.Exec(ctx, `INSERT INTO elementos_tab_cotizador (elemento_id, tab_id, tipo, etiqueta, activo) VALUES ($1,$2,'CAMPO','Otro',true)`, otroElemento, otroTab); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		fixture.TabsHandler.DB.Exec(ctx, `DELETE FROM elementos_tab_cotizador WHERE elemento_id=$1`, otroElemento)
		fixture.TabsHandler.DB.Exec(ctx, `DELETE FROM tabs_cotizador WHERE tab_id=$1`, otroTab)
		fixture.TabsHandler.DB.Exec(ctx, `DELETE FROM calculadoras WHERE calculadora_id=$1`, otraCalculadora)
	})

	rec := postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "OCULTAR", "campos_objetivo": []string{otroElemento}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("campo objetivo de otra calculadora: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReglasCotizadorGuardar_ValorComparacionObligatorioSalvoVacio(t *testing.T) {
	fixture := crearFixtureReglasCotizador(t)

	rec := postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A",
		"accion": "OCULTAR", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"]}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("IGUAL_A sin valor_comparacion: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "ESTA_VACIO",
		"accion": "OCULTAR", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"]}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ESTA_VACIO sin valor_comparacion: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReglasCotizadorGuardar_ExigirMinimoRequiereValorAccionNumerico(t *testing.T) {
	fixture := crearFixtureReglasCotizador(t)

	rec := postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "Sí",
		"accion": "EXIGIR_MINIMO", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"]}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("EXIGIR_MINIMO sin valor_accion: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "Sí",
		"accion": "EXIGIR_MINIMO", "valor_accion": "no-es-numero", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"]}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("EXIGIR_MINIMO con valor_accion no numérico: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReglasCotizadorGuardar_CamposObjetivoObligatoriosSalvoBloquearGuardado(t *testing.T) {
	fixture := crearFixtureReglasCotizador(t)

	rec := postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["CANTIDAD_AGENTES"], "operador": "MENOR_QUE", "valor_comparacion": "1",
		"accion": "OCULTAR", "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("OCULTAR sin campos_objetivo: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R07-" + sufijoUnico(), "calculadora_id": fixture.CalculadoraID,
		"campo_condicion_id": fixture.Elementos["CANTIDAD_AGENTES"], "operador": "MENOR_QUE", "valor_comparacion": "1",
		"accion": "BLOQUEAR_GUARDADO", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("BLOQUEAR_GUARDADO sin campos_objetivo (R07): esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestReglasCotizadorGuardarYListarYEliminar(t *testing.T) {
	fixture := crearFixtureReglasCotizador(t)
	reglaID := "TEST-R01-" + sufijoUnico()

	rec := postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": reglaID, "calculadora_id": fixture.CalculadoraID, "nombre": "R01",
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "OCULTAR", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"], fixture.Elementos["MINUTOS_EXTRA"]}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear R01: %s", rec.Body.String())
	}

	rec = getReglasCotizador(t, fixture.ReglasHandler, fixture.CalculadoraID)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar: %d: %s", rec.Code, rec.Body.String())
	}
	var listado struct {
		OK     bool                     `json:"ok"`
		Reglas []reglaCotizadorDesigner `json:"reglas"`
	}
	assertJSON(t, rec.Body.Bytes(), &listado)
	if !listado.OK || len(listado.Reglas) != 1 {
		t.Fatalf("esperaba 1 regla listada, obtuvo %+v", listado)
	}
	if len(listado.Reglas[0].CamposObjetivo) != 2 {
		t.Fatalf("esperaba 2 campos objetivo, obtuvo %+v", listado.Reglas[0].CamposObjetivo)
	}

	// Editar: reemplaza campos_objetivo (transaccional, mismo patrón que
	// GuardarRelaciones en catalogos.go).
	rec = postCatalogos(t, fixture.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": reglaID, "calculadora_id": fixture.CalculadoraID, "nombre": "R01",
		"campo_condicion_id": fixture.Elementos["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "OCULTAR", "campos_objetivo": []string{fixture.Elementos["MINUTOS_MES"]}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar R01: %s", rec.Body.String())
	}
	rec = getReglasCotizador(t, fixture.ReglasHandler, fixture.CalculadoraID)
	assertJSON(t, rec.Body.Bytes(), &listado)
	if len(listado.Reglas[0].CamposObjetivo) != 1 {
		t.Fatalf("esperaba que la edición reemplazara a 1 campo objetivo, obtuvo %+v", listado.Reglas[0].CamposObjetivo)
	}

	rec = deleteConRuta(t, "/api/cotizador/reglas/{id}", "/api/cotizador/reglas/"+reglaID, fixture.ReglasHandler.Eliminar)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getReglasCotizador(t, fixture.ReglasHandler, fixture.CalculadoraID)
	assertJSON(t, rec.Body.Bytes(), &listado)
	if len(listado.Reglas) != 0 {
		t.Fatalf("esperaba 0 reglas tras eliminar, obtuvo %+v", listado.Reglas)
	}
}
