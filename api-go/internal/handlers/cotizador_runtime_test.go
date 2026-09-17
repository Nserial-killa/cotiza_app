package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

type fixtureRuntime struct {
	Handler      *CotizadorRuntimeHandler
	CotizacionID string
	CompiladoID  string
	CampoID      string
	CatalogoID   string
	CatalogoElID string
	ValorSistema string
}

func crearFixtureRuntime(t *testing.T) fixtureRuntime {
	t.Helper()
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo runtime", "")
	valorID := crearValorCatalogoPrueba(t, pool, catalogoID, "Opción runtime", "")
	var valorSistema string
	if err := pool.QueryRow(context.Background(), `SELECT valor_sistema FROM catalogo_valores WHERE valor_id=$1`, valorID).Scan(&valorSistema); err != nil {
		t.Fatal(err)
	}
	campoID := "TEST-RUNTIME-CAMPO-" + sufijoUnico()
	catalogoElID := "TEST-RUNTIME-CAT-" + sufijoUnico()
	estructura := map[string]any{
		"calculadora_id": calculadoraID, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": "TEST-RUNTIME-TAB", "nombre": "Datos", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{
				map[string]any{"elemento_id": campoID, "tipo": "CAMPO", "etiqueta": "Nombre", "catalogo_id": nil, "columnas_ancho": 1, "orden": 1, "requerido": true, "configuracion": map[string]any{}},
				map[string]any{"elemento_id": catalogoElID, "tipo": "CAMPO_CATALOGO", "etiqueta": "Categoría", "catalogo_id": catalogoID, "columnas_ancho": 1, "orden": 2, "requerido": false, "configuracion": map[string]any{}},
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
		t.Fatalf("no se pudo crear compilado runtime: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE cotizaciones SET compilado_id_usado=NULL WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
	})
	return fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID, CompiladoID: compiladoID, CampoID: campoID, CatalogoID: catalogoID, CatalogoElID: catalogoElID, ValorSistema: valorSistema}
}

func getRuntime(t *testing.T, fixture fixtureRuntime, query string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/api/cotizador/runtime/{cotizacion_id}", fixture.Handler.Obtener)
	ruta := "/api/cotizador/runtime/" + fixture.CotizacionID
	if query != "" {
		ruta += "?" + query
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))
	return rec
}

func postValoresRuntime(t *testing.T, fixture fixtureRuntime, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/cotizador/runtime/{cotizacion_id}/valores", fixture.Handler.GuardarValores)
	ruta := "/api/cotizador/runtime/" + fixture.CotizacionID + "/valores"
	return postCatalogos(t, func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) }, ruta, body)
}

func TestCotizadorRuntime_ResuelveCompiladoYTraeOpciones(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	rec := getRuntime(t, fixture, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("obtener runtime: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK         bool           `json:"ok"`
		Estructura map[string]any `json:"estructura"`
		Valores    map[string]any `json:"valores"`
		Version    int            `json:"version"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || res.Version != 1 || len(res.Valores) != 0 {
		t.Fatalf("respuesta runtime inesperada: %+v", res)
	}
	var fijado string
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT compilado_id_usado::text FROM cotizaciones WHERE cotizacion_id=$1`, fixture.CotizacionID).Scan(&fijado); err != nil {
		t.Fatal(err)
	}
	if fijado != fixture.CompiladoID {
		t.Fatalf("compilado fijado=%q, esperaba %q", fijado, fixture.CompiladoID)
	}
	tabs := res.Estructura["tabs"].([]any)
	elementos := tabs[0].(map[string]any)["elementos"].([]any)
	opciones := elementos[1].(map[string]any)["opciones"].([]any)
	if len(opciones) != 1 || opciones[0].(map[string]any)["valor_sistema"] != fixture.ValorSistema {
		t.Fatalf("opciones de catálogo inesperadas: %+v", opciones)
	}
}

func TestCotizadorRuntime_GuardaYReleeValores(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	rec := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{fixture.CampoID: "Daniel", fixture.CatalogoElID: fixture.ValorSistema}})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar valores: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, fixture, "version=1")
	var res struct {
		Valores map[string]any `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if res.Valores[fixture.CampoID] != "Daniel" || res.Valores[fixture.CatalogoElID] != fixture.ValorSistema {
		t.Fatalf("valores re-leídos inesperados: %+v", res.Valores)
	}
	var historial bool
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM cotizacion_historial WHERE cotizacion_id=$1 AND numero_version=1 AND accion='valores_actualizados')`, fixture.CotizacionID).Scan(&historial); err != nil || !historial {
		t.Fatalf("historial de valores no registrado: existe=%v err=%v", historial, err)
	}
}

func TestCotizadorRuntime_RechazaValorCatalogoInvalido(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	rec := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{fixture.CatalogoElID: "NO_EXISTE"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("catálogo inválido: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// fixtureRuntimeConCajaValor arma un compilado con un CONTENEDOR que anida
// un CAMPO numérico y una CAJA_VALOR cuyo campo_fuente_id apunta a ese
// campo — la forma que produce anidarHijosCompilado en compilador.go —
// para probar la resolución de valor_resuelto sin pasar por el compilador.
func fixtureRuntimeConCajaValor(t *testing.T) (fixtureRuntime, string, string, string) {
	t.Helper()
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	contenedorID := "TEST-RUNTIME-CONT-" + sufijoUnico()
	campoID := "TEST-RUNTIME-CAMPO-FUENTE-" + sufijoUnico()
	cajaID := "TEST-RUNTIME-CAJA-" + sufijoUnico()
	estructura := map[string]any{
		"calculadora_id": calculadoraID, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": "TEST-RUNTIME-TAB-CAJA", "nombre": "Datos", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{
				map[string]any{
					"elemento_id": contenedorID, "tipo": "CONTENEDOR", "etiqueta": "Datos", "catalogo_id": nil,
					"columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": map[string]any{"columnas": 2},
					"hijos": []any{
						map[string]any{"elemento_id": campoID, "tipo": "CAMPO", "etiqueta": "Monto", "catalogo_id": nil, "columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": map[string]any{}},
						map[string]any{"elemento_id": cajaID, "tipo": "CAJA_VALOR", "etiqueta": "Monto mostrado", "catalogo_id": nil, "columnas_ancho": 1, "orden": 2, "requerido": false, "configuracion": map[string]any{"campo_fuente_id": campoID, "prefijo": "US$ ", "valor_por_defecto": "0.00"}},
					},
				},
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
		t.Fatalf("no se pudo crear compilado runtime: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE cotizaciones SET compilado_id_usado=NULL WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
	})
	fixture := fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID, CompiladoID: compiladoID}
	return fixture, contenedorID, campoID, cajaID
}

func TestCotizadorRuntime_CajaValorResuelveValorGuardadoOPorDefecto(t *testing.T) {
	fixture, contenedorID, campoID, cajaID := fixtureRuntimeConCajaValor(t)

	// Sin valor guardado todavía: debe caer al valor_por_defecto.
	rec := getRuntime(t, fixture, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("obtener runtime: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK         bool           `json:"ok"`
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	elementoCaja := elementoRuntimePorID(t, res.Estructura, contenedorID, cajaID)
	if elementoCaja["valor_resuelto"] != "0.00" {
		t.Fatalf("esperaba valor_resuelto=0.00 (valor_por_defecto), obtuvo %+v", elementoCaja["valor_resuelto"])
	}

	// Con valor guardado en el campo fuente: la caja debe reflejarlo.
	recPost := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{campoID: "1500"}})
	if recPost.Code != http.StatusOK {
		t.Fatalf("guardar valor del campo fuente: esperaba 200, dio %d: %s", recPost.Code, recPost.Body.String())
	}
	rec = getRuntime(t, fixture, "version=1")
	assertJSON(t, rec.Body.Bytes(), &res)
	elementoCaja = elementoRuntimePorID(t, res.Estructura, contenedorID, cajaID)
	if elementoCaja["valor_resuelto"] != "1500" {
		t.Fatalf("esperaba valor_resuelto=1500 (tomado del campo fuente), obtuvo %+v", elementoCaja["valor_resuelto"])
	}
}

func elementoRuntimePorID(t *testing.T, estructura map[string]any, contenedorID, elementoID string) map[string]any {
	t.Helper()
	tabs := estructura["tabs"].([]any)
	elementos := tabs[0].(map[string]any)["elementos"].([]any)
	for _, elRaw := range elementos {
		el := elRaw.(map[string]any)
		if el["elemento_id"] == contenedorID {
			hijos := el["hijos"].([]any)
			for _, hijoRaw := range hijos {
				hijo := hijoRaw.(map[string]any)
				if hijo["elemento_id"] == elementoID {
					return hijo
				}
			}
		}
	}
	t.Fatalf("no se encontró el elemento %s dentro del contenedor %s en la estructura: %+v", elementoID, contenedorID, estructura)
	return nil
}

func TestCotizadorRuntime_RechazaElementoAjeno(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	rec := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{"ELEMENTO-DE-OTRO-COTIZADOR": "valor"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("elemento ajeno: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}
