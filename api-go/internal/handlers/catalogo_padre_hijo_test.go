package handlers

// Catálogo padre-hijo en runtime (CAT-010…014, Ronda D). El esquema
// (catalogos.catalogo_padre_id, catalogo_relaciones) ya existía desde el
// Sprint 0; estas pruebas cubren lo que conecta cotizador_runtime.go:
// filtrar las opciones del hijo según el valor actual del padre
// (CAT-012), limpiar el valor guardado del hijo cuando deja de aplicar al
// nuevo padre (CAT-013), y confirmar que la relación viaja intacta por
// compilador -> runtime -> Vista Previa (CAT-014).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixtureCatalogoPadreHijo struct {
	rt                              fixtureRuntime
	catalogoPadreID, catalogoHijoID string
	padreElID, hijoElID             string
	valorNorte, valorSur            string
	valorCiudadA, valorCiudadB      string
}

// crearFixtureCatalogoPadreHijo arma un cotizador compilado a mano (mismo
// patrón que crearFixtureRuntime) con dos Campo Catálogo: "Región" (padre)
// y "Ciudad" (hijo), y dos relaciones cruzadas: Norte -> CiudadA,
// Sur -> CiudadB. CiudadA nunca es válida bajo Sur, ni CiudadB bajo Norte.
func crearFixtureCatalogoPadreHijo(t *testing.T) fixtureCatalogoPadreHijo {
	t.Helper()
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	catalogoPadreID := crearCatalogoPrueba(t, pool, "Región", "")
	valorNorte := crearValorCatalogoPrueba(t, pool, catalogoPadreID, "Norte", "")
	valorSur := crearValorCatalogoPrueba(t, pool, catalogoPadreID, "Sur", "")

	catalogoHijoID := crearCatalogoPrueba(t, pool, "Ciudad", catalogoPadreID)
	valorCiudadA := crearValorCatalogoPrueba(t, pool, catalogoHijoID, "CiudadA", "")
	valorCiudadB := crearValorCatalogoPrueba(t, pool, catalogoHijoID, "CiudadB", "")

	crearRelacionPrueba(t, pool, catalogoPadreID, valorNorte, catalogoHijoID, valorCiudadA)
	crearRelacionPrueba(t, pool, catalogoPadreID, valorSur, catalogoHijoID, valorCiudadB)

	padreElID := "TEST-PH-PADRE-" + sufijoUnico()
	hijoElID := "TEST-PH-HIJO-" + sufijoUnico()
	compiladoID := compilarEstructuraCatalogoPadreHijoPrueba(t, pool, calculadoraID, padreElID, catalogoPadreID, hijoElID, catalogoHijoID)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE cotizaciones SET compilado_id_usado=NULL WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
	})

	return fixtureCatalogoPadreHijo{
		rt:              fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID, CompiladoID: compiladoID},
		catalogoPadreID: catalogoPadreID, catalogoHijoID: catalogoHijoID,
		padreElID: padreElID, hijoElID: hijoElID,
		valorNorte: valorNorte, valorSur: valorSur,
		valorCiudadA: valorCiudadA, valorCiudadB: valorCiudadB,
	}
}

func compilarEstructuraCatalogoPadreHijoPrueba(t *testing.T, pool consultadorContextoRuntime, calculadoraID, padreElID, catalogoPadreID, hijoElID, catalogoHijoID string) string {
	t.Helper()
	estructura := map[string]any{
		"calculadora_id": calculadoraID, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": "TEST-PH-TAB-" + sufijoUnico(), "nombre": "Ubicación", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{
				map[string]any{"elemento_id": padreElID, "tipo": "CAMPO_CATALOGO", "etiqueta": "Región", "catalogo_id": catalogoPadreID, "columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": map[string]any{}},
				map[string]any{"elemento_id": hijoElID, "tipo": "CAMPO_CATALOGO", "etiqueta": "Ciudad", "catalogo_id": catalogoHijoID, "columnas_ancho": 1, "orden": 2, "requerido": false, "configuracion": map[string]any{}},
			},
		}},
	}
	raw, err := json.Marshal(estructura)
	if err != nil {
		t.Fatal(err)
	}
	var compiladoID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
		VALUES ($1, 1, 'ACTIVA', $2) RETURNING compilado_id::text`, calculadoraID, string(raw)).Scan(&compiladoID); err != nil {
		t.Fatalf("no se pudo crear el compilado de prueba: %v", err)
	}
	return compiladoID
}

// opcionesElementoRuntime extrae los valor_sistema de las "opciones" que
// incluirOpcionesCatalogo dejó en la estructura de la respuesta de Obtener.
func opcionesElementoRuntime(t *testing.T, rec *httptest.ResponseRecorder, elementoID string) []string {
	t.Helper()
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	tabs, _ := res.Estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		for _, elRaw := range elementos {
			el, _ := elRaw.(map[string]any)
			if fmt.Sprint(el["elemento_id"]) == elementoID {
				opciones, _ := el["opciones"].([]any)
				valores := make([]string, 0, len(opciones))
				for _, o := range opciones {
					opt, _ := o.(map[string]any)
					valores = append(valores, fmt.Sprint(opt["valor_sistema"]))
				}
				return valores
			}
		}
	}
	t.Fatalf("elemento %s no encontrado en la estructura de la respuesta", elementoID)
	return nil
}

func valorGuardadoElemento(t *testing.T, rec *httptest.ResponseRecorder, elementoID string) (string, bool) {
	t.Helper()
	var res struct {
		Valores map[string]any `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	v, existe := res.Valores[elementoID]
	if !existe || v == nil {
		return "", false
	}
	texto, _ := v.(string)
	return texto, true
}

func TestCatalogoPadreHijo_SinPadreSeleccionadoElHijoNoTraeOpciones(t *testing.T) {
	f := crearFixtureCatalogoPadreHijo(t)
	rec := getRuntime(t, f.rt, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if opciones := opcionesElementoRuntime(t, rec, f.hijoElID); len(opciones) != 0 {
		t.Fatalf("sin padre seleccionado, el hijo no debería ofrecer ninguna opción todavía: %v", opciones)
	}
}

func TestCatalogoPadreHijo_OpcionesDelHijoSeFiltranPorElValorActualDelPadre(t *testing.T) {
	f := crearFixtureCatalogoPadreHijo(t)

	exigirGuardadoSalida(t, postValoresRuntime(t, f.rt, map[string]any{"version": 1, "valores": map[string]any{f.padreElID: "Norte"}}))
	rec := getRuntime(t, f.rt, "")
	if opciones := opcionesElementoRuntime(t, rec, f.hijoElID); len(opciones) != 1 || opciones[0] != "CiudadA" {
		t.Fatalf("CAT-012: con padre=Norte, el hijo debería ofrecer solo CiudadA, dio %v", opciones)
	}

	exigirGuardadoSalida(t, postValoresRuntime(t, f.rt, map[string]any{"version": 1, "valores": map[string]any{f.padreElID: "Sur"}}))
	rec = getRuntime(t, f.rt, "")
	if opciones := opcionesElementoRuntime(t, rec, f.hijoElID); len(opciones) != 1 || opciones[0] != "CiudadB" {
		t.Fatalf("CAT-012: con padre=Sur, el hijo debería ofrecer solo CiudadB, dio %v", opciones)
	}
}

func TestCatalogoPadreHijo_CambiarElPadreLimpiaElValorDelHijoQueYaNoAplica(t *testing.T) {
	f := crearFixtureCatalogoPadreHijo(t)

	exigirGuardadoSalida(t, postValoresRuntime(t, f.rt, map[string]any{"version": 1, "valores": map[string]any{f.padreElID: "Norte", f.hijoElID: "CiudadA"}}))
	rec := getRuntime(t, f.rt, "")
	if texto, existe := valorGuardadoElemento(t, rec, f.hijoElID); !existe || texto != "CiudadA" {
		t.Fatalf("el hijo debería haber guardado CiudadA: existe=%v texto=%q", existe, texto)
	}

	exigirGuardadoSalida(t, postValoresRuntime(t, f.rt, map[string]any{"version": 1, "valores": map[string]any{f.padreElID: "Sur"}}))
	rec = getRuntime(t, f.rt, "")
	if texto, existe := valorGuardadoElemento(t, rec, f.hijoElID); existe {
		t.Fatalf("CAT-013: al cambiar el padre a Sur, el valor CiudadA del hijo debía limpiarse solo, quedó %q", texto)
	}
	// El padre en sí no se toca por esta limpieza.
	if texto, existe := valorGuardadoElemento(t, rec, f.padreElID); !existe || texto != "Sur" {
		t.Fatalf("el valor del padre no debería alterarse por la limpieza del hijo: existe=%v texto=%q", existe, texto)
	}
}

func TestCatalogoPadreHijo_GuardarPadreYHijoCompatiblesEnLaMismaPeticionNoSeLimpian(t *testing.T) {
	f := crearFixtureCatalogoPadreHijo(t)
	exigirGuardadoSalida(t, postValoresRuntime(t, f.rt, map[string]any{"version": 1, "valores": map[string]any{f.padreElID: "Sur", f.hijoElID: "CiudadB"}}))
	rec := getRuntime(t, f.rt, "")
	if texto, existe := valorGuardadoElemento(t, rec, f.hijoElID); !existe || texto != "CiudadB" {
		t.Fatalf("padre y hijo compatibles guardados en la misma petición deberían persistir juntos: existe=%v texto=%q", existe, texto)
	}
}

// TestCatalogoPadreHijo_SinRelacionesConfiguradasNoFiltra: catalogo_padre_id
// declarado a nivel de catálogo, pero sin ninguna fila en catalogo_relaciones
// todavía — el hijo debe seguir mostrando TODAS sus opciones activas, no
// bloquear un catálogo que nadie terminó de relacionar valor por valor.
func TestCatalogoPadreHijo_SinRelacionesConfiguradasNoFiltra(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	catalogoPadreID := crearCatalogoPrueba(t, pool, "Padre sin relaciones", "")
	crearValorCatalogoPrueba(t, pool, catalogoPadreID, "Valor1", "")
	catalogoHijoID := crearCatalogoPrueba(t, pool, "Hijo sin relaciones", catalogoPadreID)
	crearValorCatalogoPrueba(t, pool, catalogoHijoID, "OpcionA", "")
	crearValorCatalogoPrueba(t, pool, catalogoHijoID, "OpcionB", "")
	// A propósito: nunca se llama crearRelacionPrueba.

	padreElID := "TEST-PH-SINREL-PADRE-" + sufijoUnico()
	hijoElID := "TEST-PH-SINREL-HIJO-" + sufijoUnico()
	compiladoID := compilarEstructuraCatalogoPadreHijoPrueba(t, pool, calculadoraID, padreElID, catalogoPadreID, hijoElID, catalogoHijoID)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE cotizaciones SET compilado_id_usado=NULL WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
	})
	rt := fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID}

	rec := getRuntime(t, rt, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if opciones := opcionesElementoRuntime(t, rec, hijoElID); len(opciones) != 2 {
		t.Fatalf("sin relaciones configuradas, el hijo debería mostrar sus 2 opciones activas sin filtrar, dio %v", opciones)
	}
}

// TestCatalogoPadreHijo_RelacionViajaPorCompiladorRuntimeYVistaPrevia es
// CAT-014: usa el compilador real (no la estructura armada a mano) y
// confirma que catalogo_id de ambos Campo Catálogo, y por lo tanto el
// filtrado padre-hijo, sobreviven el compilado; y que Vista Previa/el
// enlace público traducen el valor guardado del hijo a su texto_visible
// igual que cualquier otro Campo Catálogo.
func TestCatalogoPadreHijo_RelacionViajaPorCompiladorRuntimeYVistaPrevia(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	pool := f.handler.DB

	catalogoPadreID := crearCatalogoPrueba(t, pool, "Región CAT-014", "")
	valorNorte := crearValorCatalogoPrueba(t, pool, catalogoPadreID, "Norte", "")
	catalogoHijoID := crearCatalogoPrueba(t, pool, "Ciudad CAT-014", catalogoPadreID)
	valorCiudadA := crearValorCatalogoPrueba(t, pool, catalogoHijoID, "CiudadA", "")
	crearRelacionPrueba(t, pool, catalogoPadreID, valorNorte, catalogoHijoID, valorCiudadA)

	padreElID := f.crear(t, "CAMPO_CATALOGO", "Región", nil, map[string]any{"catalogo_id": catalogoPadreID})
	hijoElID := f.crear(t, "CAMPO_CATALOGO", "Ciudad", nil, map[string]any{"catalogo_id": catalogoHijoID})

	rt := f.runtime(t)

	var configuracionRaw []byte
	if err := pool.QueryRow(context.Background(), `SELECT configuracion FROM cotizadores_compilados WHERE calculadora_id=$1 AND estado='ACTIVA'`, f.calculadoraID).Scan(&configuracionRaw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configuracionRaw), catalogoPadreID) || !strings.Contains(string(configuracionRaw), catalogoHijoID) {
		t.Fatalf("CAT-014: catalogo_id de padre/hijo no viajó a la estructura compilada: %s", configuracionRaw)
	}

	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{padreElID: "Norte", hijoElID: "CiudadA"}}))
	rec := getRuntime(t, rt, "")
	if opciones := opcionesElementoRuntime(t, rec, hijoElID); len(opciones) != 1 || opciones[0] != "CiudadA" {
		t.Fatalf("CAT-014: el motor de ejecución no filtró usando la relación que llegó vía compilador: %v", opciones)
	}

	tabs, err := (&EnlacesPublicosHandler{DB: pool}).consultarTabsYValores(context.Background(), rt.CotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	encontrado := false
	for _, tab := range tabs {
		for _, el := range tab.Elementos {
			if el.ElementoID == hijoElID {
				encontrado = true
				if el.Valor != "CiudadA" {
					t.Fatalf("CAT-014: Vista Previa/enlace público debería mostrar el texto_visible del hijo (CiudadA), dio %v", el.Valor)
				}
			}
		}
	}
	if !encontrado {
		t.Fatal("CAT-014: el elemento hijo no apareció en la respuesta de Vista Previa/enlace público")
	}
}
