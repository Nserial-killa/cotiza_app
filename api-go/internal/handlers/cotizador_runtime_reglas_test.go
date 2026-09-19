package handlers

// Pruebas de integración del motor de Reglas en tiempo real
// (migración 0024) contra el Motor de Ejecución real — usando los
// ejemplos reales del documento de definición funcional del jefe (caso
// ISA Custom): R01 (USA_TELEFONIA), R05 (CONVERSACIONES_EXTRA), R07
// (CANTIDAD_AGENTES), y una contradicción directa entre dos reglas.

import (
	"context"
	"net/http"
	"testing"
)

// crearFixtureReglasRuntime recibe una función que arma las reglas a partir
// de los elementos ya creados (necesita sus IDs, que solo se conocen tras
// crearFixtureReglasCotizador), las guarda, compila la calculadora y deja
// una cotización apuntándole.
func crearFixtureReglasRuntime(t *testing.T, armarReglas func(el map[string]string) []map[string]any) (fixtureRuntime, fixtureReglasCotizador) {
	t.Helper()
	base := crearFixtureReglasCotizador(t)
	for _, regla := range armarReglas(base.Elementos) {
		regla["calculadora_id"] = base.CalculadoraID
		rec := postCatalogos(t, base.ReglasHandler.Guardar, "/api/cotizador/reglas", regla)
		if rec.Code != http.StatusOK {
			t.Fatalf("crear regla %v: %d: %s", regla["regla_id"], rec.Code, rec.Body.String())
		}
	}

	recComp := postCatalogos(t, base.Compilador.Compilar, "/api/cotizador/compilar", map[string]any{"calculadora_id": base.CalculadoraID})
	var resComp respuestaCompiladorTest
	assertJSON(t, recComp.Body.Bytes(), &resComp)
	if !resComp.OK || !resComp.Valido || !resComp.Compilado {
		t.Fatalf("compilar: esperaba válido y compilado, obtuvo %+v", resComp)
	}

	cotizacionID, _, _ := crearCotizacionPrueba(t, base.TabsHandler.DB, "Borrador", "", "")
	if _, err := base.TabsHandler.DB.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, base.CalculadoraID, cotizacionID); err != nil {
		t.Fatal(err)
	}

	return fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: base.TabsHandler.DB}, CotizacionID: cotizacionID}, base
}

// TestCotizadorRuntimeReglas_R01OcultaYPoneEnCero cubre el ejemplo textual
// de R01: USA_TELEFONIA=No oculta MINUTOS_MES/MINUTOS_EXTRA y pone en cero
// COSTO_VOZ/PRECIO_VOZ/CONSUMO_EXTRA_VOZ. El requisito más delicado
// (sección 6.1): un valor viejo de un campo recién oculto no debe seguir
// sumándose — se comprueba con un Campo Calculado que los suma.
func TestCotizadorRuntimeReglas_R01OcultaYPoneEnCero(t *testing.T) {
	base := crearFixtureReglasCotizador(t)
	el := base.Elementos

	rec := postCatalogos(t, base.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R01-OCULTAR-" + sufijoUnico(), "calculadora_id": base.CalculadoraID, "nombre": "R01",
		"campo_condicion_id": el["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "OCULTAR", "campos_objetivo": []string{el["MINUTOS_MES"], el["MINUTOS_EXTRA"]}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear R01 (ocultar): %s", rec.Body.String())
	}
	rec = postCatalogos(t, base.ReglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-R01-CERO-" + sufijoUnico(), "calculadora_id": base.CalculadoraID, "nombre": "R01",
		"campo_condicion_id": el["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "PONER_EN_CERO", "campos_objetivo": []string{el["COSTO_VOZ"], el["PRECIO_VOZ"], el["CONSUMO_EXTRA_VOZ"]}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear R01 (poner en cero): %s", rec.Body.String())
	}

	// Campo Calculado que suma los tres campos de voz puestos en cero —
	// para comprobar que un valor oculto no sigue sumándose silenciosamente.
	totalVozID := "TEST-EL-TOTAL-VOZ-" + sufijoUnico()
	recCalc := postCatalogos(t, base.TabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": totalVozID, "tab_id": base.TabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Total voz",
		"configuracion": map[string]any{
			"operacion": "SUMA", "tipo_resultado": "MONEDA", "decimales": 2,
			"operandos": []string{el["COSTO_VOZ"], el["PRECIO_VOZ"], el["CONSUMO_EXTRA_VOZ"]},
		}, "activo": true,
	})
	if recCalc.Code != http.StatusOK {
		t.Fatalf("crear Total voz: %s", recCalc.Body.String())
	}

	recComp := postCatalogos(t, base.Compilador.Compilar, "/api/cotizador/compilar", map[string]any{"calculadora_id": base.CalculadoraID})
	var resComp respuestaCompiladorTest
	assertJSON(t, recComp.Body.Bytes(), &resComp)
	if !resComp.OK || !resComp.Valido || !resComp.Compilado {
		t.Fatalf("compilar: %+v", resComp)
	}
	cotizacionID, _, _ := crearCotizacionPrueba(t, base.TabsHandler.DB, "Borrador", "", "")
	if _, err := base.TabsHandler.DB.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, base.CalculadoraID, cotizacionID); err != nil {
		t.Fatal(err)
	}
	fixture := fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: base.TabsHandler.DB}, CotizacionID: cotizacionID}

	// 1) Con USA_TELEFONIA=Sí, se cargan valores "reales" en los campos de voz.
	rec = postValoresRuntime(t, fixture, map[string]any{
		"version": 1,
		"valores": map[string]any{
			el["USA_TELEFONIA"]:     "Sí",
			el["MINUTOS_MES"]:       "500",
			el["MINUTOS_EXTRA"]:     "20",
			el["COSTO_VOZ"]:         "100",
			el["PRECIO_VOZ"]:        "150",
			el["CONSUMO_EXTRA_VOZ"]: "30",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar con USA_TELEFONIA=Sí: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, fixture, "version=1")
	var res struct {
		Estructura map[string]any `json:"estructura"`
		Valores    map[string]any `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	total := elementoPorIDEnEstructura(res.Estructura, totalVozID)
	if total["valor_resuelto"] != 280.0 {
		t.Fatalf("con USA_TELEFONIA=Sí esperaba Total voz=280, obtuvo %v", total["valor_resuelto"])
	}
	minutosMes := elementoPorIDEnEstructura(res.Estructura, el["MINUTOS_MES"])
	if estado, ok := minutosMes["estado_regla"].(map[string]any); ok && estado["visible"] == false {
		t.Fatalf("con USA_TELEFONIA=Sí, MINUTOS_MES no debería estar oculto: %+v", estado)
	}

	// 2) Cambiar a USA_TELEFONIA=No (sin volver a mandar los campos de
	// voz): deben quedar en 0 igual, y el total ya no debe arrastrar el
	// valor viejo (280) sino reflejar 0.
	rec = postValoresRuntime(t, fixture, map[string]any{
		"version": 1,
		"valores": map[string]any{el["USA_TELEFONIA"]: "No"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar con USA_TELEFONIA=No: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, fixture, "version=1")
	assertJSON(t, rec.Body.Bytes(), &res)

	for _, campo := range []string{"COSTO_VOZ", "PRECIO_VOZ", "CONSUMO_EXTRA_VOZ"} {
		if res.Valores[el[campo]] != "0" {
			t.Fatalf("%s debería haber quedado en \"0\" tras ocultar/forzar cero, obtuvo %v", campo, res.Valores[el[campo]])
		}
	}
	total = elementoPorIDEnEstructura(res.Estructura, totalVozID)
	if total["valor_resuelto"] != 0.0 {
		t.Fatalf("sección 6.1: un valor oculto no puede seguir sumándose — esperaba Total voz=0, obtuvo %v (no debe seguir en 280)", total["valor_resuelto"])
	}
	minutosMes = elementoPorIDEnEstructura(res.Estructura, el["MINUTOS_MES"])
	estado, ok := minutosMes["estado_regla"].(map[string]any)
	if !ok || estado["visible"] != false {
		t.Fatalf("con USA_TELEFONIA=No, MINUTOS_MES debería quedar oculto: %+v", minutosMes["estado_regla"])
	}

	// 3) Round-trip del literal de la sección 6.1: No -> Sí -> No, guardar,
	// "reabrir" (nuevo GET), los valores efectivos deben seguir consistentes.
	rec = postValoresRuntime(t, fixture, map[string]any{
		"version": 1,
		"valores": map[string]any{
			el["USA_TELEFONIA"]:     "Sí",
			el["COSTO_VOZ"]:         "999",
			el["PRECIO_VOZ"]:        "999",
			el["CONSUMO_EXTRA_VOZ"]: "999",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("volver a Sí: %d: %s", rec.Code, rec.Body.String())
	}
	rec = postValoresRuntime(t, fixture, map[string]any{
		"version": 1,
		"valores": map[string]any{el["USA_TELEFONIA"]: "No"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("volver a No: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, fixture, "version=1") // "reabrir"
	assertJSON(t, rec.Body.Bytes(), &res)
	total = elementoPorIDEnEstructura(res.Estructura, totalVozID)
	if total["valor_resuelto"] != 0.0 {
		t.Fatalf("tras No->Sí->No, esperaba Total voz=0 de forma consistente, obtuvo %v", total["valor_resuelto"])
	}
	for _, campo := range []string{"COSTO_VOZ", "PRECIO_VOZ", "CONSUMO_EXTRA_VOZ"} {
		if res.Valores[el[campo]] != "0" {
			t.Fatalf("tras No->Sí->No, %s debía quedar en \"0\", obtuvo %v", campo, res.Valores[el[campo]])
		}
	}
}

// TestCotizadorRuntimeReglas_R07BloqueaGuardadoSinPersistirNada cubre R07:
// CANTIDAD_AGENTES < 1 bloquea el guardado completo. Ni CANTIDAD_AGENTES
// ni ningún otro valor del mismo request debe quedar persistido.
func TestCotizadorRuntimeReglas_R07BloqueaGuardadoSinPersistirNada(t *testing.T) {
	rt, base := crearFixtureReglasRuntime(t, func(el map[string]string) []map[string]any {
		return []map[string]any{{
			"regla_id": "TEST-R07-" + sufijoUnico(), "nombre": "R07",
			"campo_condicion_id": el["CANTIDAD_AGENTES"], "operador": "MENOR_QUE", "valor_comparacion": "1",
			"accion": "BLOQUEAR_GUARDADO", "activo": true,
		}}
	})
	el := base.Elementos
	cotizacionID := rt.CotizacionID

	recPost := postValoresRuntime(t, rt, map[string]any{
		"version": 1,
		"valores": map[string]any{
			el["CANTIDAD_AGENTES"]: "0",
			el["MINUTOS_MES"]:      "500",
		},
	})
	if recPost.Code != http.StatusBadRequest {
		t.Fatalf("CANTIDAD_AGENTES=0 debía bloquear el guardado: esperaba 400, dio %d: %s", recPost.Code, recPost.Body.String())
	}

	var filas int
	if err := base.TabsHandler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_valores WHERE cotizacion_id=$1`, cotizacionID).Scan(&filas); err != nil {
		t.Fatal(err)
	}
	if filas != 0 {
		t.Fatalf("un guardado bloqueado no debe persistir NADA, pero quedaron %d fila(s)", filas)
	}

	// Con un valor válido, el guardado debe pasar.
	recPost = postValoresRuntime(t, rt, map[string]any{
		"version": 1,
		"valores": map[string]any{
			el["CANTIDAD_AGENTES"]: "3",
			el["MINUTOS_MES"]:      "500",
		},
	})
	if recPost.Code != http.StatusOK {
		t.Fatalf("CANTIDAD_AGENTES=3 debía guardar sin problema: %d: %s", recPost.Code, recPost.Body.String())
	}
}

// TestCotizadorRuntimeReglas_R05PoneEnCeroConsumoExtra cubre R05:
// CONVERSACIONES_EXTRA=0 pone CONSUMO_EXTRA_CHAT en cero. 0 no es "vacío"
// (es una respuesta válida), así que la condición debe ser IGUAL_A "0", no
// ESTA_VACIO.
func TestCotizadorRuntimeReglas_R05PoneEnCeroConsumoExtra(t *testing.T) {
	rt, base := crearFixtureReglasRuntime(t, func(el map[string]string) []map[string]any {
		return []map[string]any{{
			"regla_id": "TEST-R05-" + sufijoUnico(), "nombre": "R05",
			"campo_condicion_id": el["CONVERSACIONES_EXTRA"], "operador": "IGUAL_A", "valor_comparacion": "0",
			"accion": "PONER_EN_CERO", "campos_objetivo": []string{el["CONSUMO_EXTRA_CHAT"]}, "activo": true,
		}}
	})
	el := base.Elementos

	rec := postValoresRuntime(t, rt, map[string]any{
		"version": 1,
		"valores": map[string]any{
			el["CONVERSACIONES_EXTRA"]: "0",
			el["CONSUMO_EXTRA_CHAT"]:   "75",
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, rt, "version=1")
	var res struct {
		Valores map[string]any `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if res.Valores[el["CONSUMO_EXTRA_CHAT"]] != "0" {
		t.Fatalf("CONVERSACIONES_EXTRA=0 debía poner CONSUMO_EXTRA_CHAT en 0, obtuvo %v", res.Valores[el["CONSUMO_EXTRA_CHAT"]])
	}
}

// TestCotizadorRuntimeReglas_ConflictoOcultarGanaSobreMostrar cubre el
// criterio de conflicto documentado: dos reglas contradictorias sobre el
// mismo campo (una lo oculta, otra lo muestra) — gana la más restrictiva.
func TestCotizadorRuntimeReglas_ConflictoOcultarGanaSobreMostrar(t *testing.T) {
	rt, base := crearFixtureReglasRuntime(t, func(el map[string]string) []map[string]any {
		return []map[string]any{
			{
				"regla_id":           "TEST-CONFLICTO-OCULTAR-" + sufijoUnico(),
				"campo_condicion_id": el["USA_TELEFONIA"], "operador": "IGUAL_A", "valor_comparacion": "No",
				"accion": "OCULTAR", "campos_objetivo": []string{el["MINUTOS_MES"]}, "activo": true,
			},
			{
				"regla_id":           "TEST-CONFLICTO-MOSTRAR-" + sufijoUnico(),
				"campo_condicion_id": el["CANTIDAD_AGENTES"], "operador": "MAYOR_QUE", "valor_comparacion": "0",
				"accion": "MOSTRAR", "campos_objetivo": []string{el["MINUTOS_MES"]}, "activo": true,
			},
		}
	})
	el := base.Elementos

	// Ambas condiciones se cumplen a la vez: USA_TELEFONIA=No (oculta) y
	// CANTIDAD_AGENTES=5>0 (muestra). Debe ganar OCULTAR.
	rec := postValoresRuntime(t, rt, map[string]any{
		"version": 1,
		"valores": map[string]any{el["USA_TELEFONIA"]: "No", el["CANTIDAD_AGENTES"]: "5"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, rt, "version=1")
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	minutosMes := elementoPorIDEnEstructura(res.Estructura, el["MINUTOS_MES"])
	estado, ok := minutosMes["estado_regla"].(map[string]any)
	if !ok || estado["visible"] != false {
		t.Fatalf("con reglas contradictorias, OCULTAR debía ganar sobre MOSTRAR: %+v", minutosMes["estado_regla"])
	}
}
