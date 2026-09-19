package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func postOpcionesRuntime(t *testing.T, fixture fixtureRuntime, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/cotizador/runtime/{cotizacion_id}/opciones", fixture.Handler.AdministrarOpciones)
	ruta := "/api/cotizador/runtime/" + fixture.CotizacionID + "/opciones"
	return postCatalogos(t, func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) }, ruta, body)
}

func fixtureRuntimeOpciones(t *testing.T, permitirDuplicar bool) (fixtureRuntime, string, string, string) {
	t.Helper()
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	sufijo := sufijoUnico()
	tabID := "TEST-RUNTIME-TAB-OPC-" + sufijo
	padreID := "TEST-RUNTIME-OPC-" + sufijo
	hijoID := "TEST-RUNTIME-OPC-HIJO-" + sufijo
	calculadoID := "TEST-RUNTIME-OPC-CALC-" + sufijo
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, orden, activo) VALUES ($1,$2,'Opciones',1,true)`, tabID, calculadoraID); err != nil {
		t.Fatal(err)
	}
	configuracionPadre := map[string]any{
		"cantidad_inicial": 2, "nombres_sugeridos": "Starter, Premium", "vista_editar": "PESTANAS",
		"vista_resumen": "CAJAS", "vista_oferta": "TABLA_COMPARATIVA", "permitir_duplicar": permitirDuplicar,
		"permitir_eliminar": true, "permitir_renombrar": true, "permitir_recomendado": true,
	}
	configJSON, _ := json.Marshal(configuracionPadre)
	if _, err := pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
		VALUES ($1,$2,'OPCIONES_PROPUESTA','Planes',1,$3,true),
		       ($4,$2,'CAMPO','Precio',2,'{"tipo_campo":"MONEDA"}'::jsonb,true),
		       ($5,$2,'CAMPO_CALCULADO','Precio doble',3,$6,true)`, padreID, tabID, string(configJSON), hijoID, calculadoID,
		fmt.Sprintf(`{"tipo_formula":"SIMPLE","operacion":"SUMA","operandos":[%q,%q],"decimales":2}`, hijoID, hijoID)); err != nil {
		t.Fatal(err)
	}
	estructura := map[string]any{
		"calculadora_id": calculadoraID, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": tabID, "nombre": "Planes", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{map[string]any{
				"elemento_id": padreID, "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Opciones de propuesta",
				"columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": configuracionPadre,
				"hijos": []any{map[string]any{
					"elemento_id": hijoID, "tipo": "CAMPO", "etiqueta": "Precio", "columnas_ancho": 1,
					"orden": 2, "requerido": true, "configuracion": map[string]any{"tipo_campo": "MONEDA"},
				}, map[string]any{
					"elemento_id": calculadoID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Precio doble", "columnas_ancho": 1,
					"orden": 3, "configuracion": map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "operandos": []any{hijoID, hijoID}, "decimales": 2},
				}},
			}},
		}},
	}
	raw, _ := json.Marshal(estructura)
	var compiladoID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO cotizadores_compilados (calculadora_id,version,estado,configuracion)
		VALUES ($1,1,'ACTIVA',$2) RETURNING compilado_id::text`, calculadoraID, string(raw)).Scan(&compiladoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE cotizaciones SET compilado_id_usado=NULL WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
		pool.Exec(context.Background(), `DELETE FROM elementos_tab_cotizador WHERE elemento_id=ANY($1)`, []string{calculadoID, hijoID, padreID})
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE tab_id=$1`, tabID)
	})
	return fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID, CompiladoID: compiladoID}, padreID, hijoID, calculadoID
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

func TestCotizadorRuntime_OpcionesInicialesYValoresIndependientes(t *testing.T) {
	fixture, padreID, hijoID, calculadoID := fixtureRuntimeOpciones(t, true)
	rec := getRuntime(t, fixture, "version=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("primera apertura: %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Estructura map[string]any `json:"estructura"`
		Valores    map[string]any `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	padre := buscarElementoEstructura(res.Estructura, padreID)
	opciones := padre["opciones"].([]any)
	if len(opciones) != 2 || opciones[0].(map[string]any)["nombre"] != "Starter" || opciones[1].(map[string]any)["nombre"] != "Premium" {
		t.Fatalf("opciones iniciales inesperadas: %+v", opciones)
	}
	opcionA := opciones[0].(map[string]any)["opcion_id"].(string)
	opcionB := opciones[1].(map[string]any)["opcion_id"].(string)
	rec = postValoresRuntime(t, fixture, map[string]any{
		"version": 1,
		"valores_por_opcion": []map[string]any{
			{"elemento_id": hijoID, "opcion_id": opcionA, "valor": "100"},
			{"elemento_id": hijoID, "opcion_id": opcionB, "valor": "250"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar valores por opción: %d: %s", rec.Code, rec.Body.String())
	}
	rec = getRuntime(t, fixture, "version=1")
	assertJSON(t, rec.Body.Bytes(), &res)
	porOpcion := res.Valores[hijoID].(map[string]any)
	if porOpcion[opcionA] != "100" || porOpcion[opcionB] != "250" {
		t.Fatalf("los valores por opción se pisaron o no se resolvieron: %+v", porOpcion)
	}
	calculado := buscarElementoEstructura(res.Estructura, calculadoID)
	resueltos, ok := calculado["valores_resueltos_por_opcion"].(map[string]any)
	if !ok {
		t.Fatalf("el campo calculado no devolvió valores_resueltos_por_opcion: %+v", calculado)
	}
	if resueltos[opcionA] != float64(200) || resueltos[opcionB] != float64(500) {
		t.Fatalf("el campo calculado no se resolvió por opción: %+v", resueltos)
	}
	var filas int
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_valores WHERE cotizacion_id=$1 AND elemento_id=$2`, fixture.CotizacionID, hijoID).Scan(&filas); err != nil || filas != 2 {
		t.Fatalf("esperaba dos filas físicas para el mismo campo: filas=%d err=%v", filas, err)
	}
}

func TestCotizadorRuntime_OpcionesRecomendadaUnicaYNoEliminaLaUltima(t *testing.T) {
	fixture, padreID, _, _ := fixtureRuntimeOpciones(t, true)
	rec := getRuntime(t, fixture, "version=1")
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	opciones := buscarElementoEstructura(res.Estructura, padreID)["opciones"].([]any)
	opcionA := opciones[0].(map[string]any)["opcion_id"].(string)
	opcionB := opciones[1].(map[string]any)["opcion_id"].(string)
	for _, opcionID := range []string{opcionA, opcionB} {
		rec = postOpcionesRuntime(t, fixture, map[string]any{
			"version": 1, "elemento_padre_id": padreID, "accion": "RECOMENDAR", "opcion_id": opcionID,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("marcar recomendada %s: %d: %s", opcionID, rec.Code, rec.Body.String())
		}
	}
	var recomendadas int
	var recomendadaID string
	if err := fixture.Handler.DB.QueryRow(context.Background(), `
		SELECT COUNT(*) FILTER (WHERE es_recomendada), COALESCE(MAX(opcion_id) FILTER (WHERE es_recomendada),'')
		FROM cotizacion_opciones WHERE cotizacion_id=$1 AND numero_version=1 AND elemento_padre_id=$2`, fixture.CotizacionID, padreID).Scan(&recomendadas, &recomendadaID); err != nil {
		t.Fatal(err)
	}
	if recomendadas != 1 || recomendadaID != opcionB {
		t.Fatalf("recomendada no quedó única: cantidad=%d id=%s", recomendadas, recomendadaID)
	}
	rec = postOpcionesRuntime(t, fixture, map[string]any{
		"version": 1, "elemento_padre_id": padreID, "accion": "ELIMINAR", "opcion_id": opcionA,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar con dos opciones: %d: %s", rec.Code, rec.Body.String())
	}
	rec = postOpcionesRuntime(t, fixture, map[string]any{
		"version": 1, "elemento_padre_id": padreID, "accion": "ELIMINAR", "opcion_id": opcionB,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("eliminar la única opción: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorRuntime_OpcionesAdvertenciaSinRecomendadaYAutoRecomiendaUnica
// cubre R09 del documento: con más de una opción y ninguna recomendada,
// cualquier mutación debe avisar (no bloquear); al quedar una sola, se
// recomienda sola sin que nadie tenga que marcarla.
func TestCotizadorRuntime_OpcionesAdvertenciaSinRecomendadaYAutoRecomiendaUnica(t *testing.T) {
	fixture, padreID, _, _ := fixtureRuntimeOpciones(t, true)
	rec := getRuntime(t, fixture, "version=1")
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	opciones := buscarElementoEstructura(res.Estructura, padreID)["opciones"].([]any)
	opcionA := opciones[0].(map[string]any)["opcion_id"].(string)
	opcionB := opciones[1].(map[string]any)["opcion_id"].(string)

	rec = postOpcionesRuntime(t, fixture, map[string]any{
		"version": 1, "elemento_padre_id": padreID, "accion": "RENOMBRAR", "opcion_id": opcionA, "nombre": "Starter Plus",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("renombrar: %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Advertencia string `json:"advertencia"`
	}
	assertJSON(t, rec.Body.Bytes(), &resp)
	if resp.Advertencia == "" {
		t.Fatal("esperaba una advertencia: hay 2 opciones y ninguna está recomendada")
	}

	rec = postOpcionesRuntime(t, fixture, map[string]any{
		"version": 1, "elemento_padre_id": padreID, "accion": "ELIMINAR", "opcion_id": opcionA,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar: %d: %s", rec.Code, rec.Body.String())
	}
	var respFinal struct {
		Opciones    []cotizacionOpcion `json:"opciones"`
		Advertencia string             `json:"advertencia"`
	}
	assertJSON(t, rec.Body.Bytes(), &respFinal)
	if respFinal.Advertencia != "" {
		t.Fatalf("con una sola opción ya recomendada automáticamente no debería haber advertencia: %q", respFinal.Advertencia)
	}
	if len(respFinal.Opciones) != 1 || respFinal.Opciones[0].OpcionID != opcionB || !respFinal.Opciones[0].EsRecomendada {
		t.Fatalf("la única opción restante debía quedar recomendada automáticamente: %+v", respFinal.Opciones)
	}
}

func TestCotizadorRuntime_RechazaAgregarSiNoPermiteDuplicar(t *testing.T) {
	fixture, padreID, _, _ := fixtureRuntimeOpciones(t, false)
	rec := postOpcionesRuntime(t, fixture, map[string]any{
		"version": 1, "elemento_padre_id": padreID, "accion": "AGREGAR", "nombre": "Enterprise",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("agregar sin permiso: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
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

// fixtureRuntimeConCampoCalculado arma un compilado con dos CAMPO base
// (numéricos) y dos CAMPO_CALCULADO anidados: calc1 = SUMA(campoA, campoB) y
// calc2 = PROMEDIO(calc1) — dos niveles de dependencia — con
// funcion_campo=TOTAL_PRECIO_OFERTA en calc2, para probar tanto la
// resolución anidada como la actualización de cotizacion_versiones.
func fixtureRuntimeConCampoCalculado(t *testing.T) (fixture fixtureRuntime, campoAID, campoBID, calc1ID, calc2ID string) {
	t.Helper()
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	campoAID = "TEST-RUNTIME-CALC-A-" + sufijoUnico()
	campoBID = "TEST-RUNTIME-CALC-B-" + sufijoUnico()
	calc1ID = "TEST-RUNTIME-CALC1-" + sufijoUnico()
	calc2ID = "TEST-RUNTIME-CALC2-" + sufijoUnico()
	estructura := map[string]any{
		"calculadora_id": calculadoraID, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": "TEST-RUNTIME-TAB-CALC", "nombre": "Datos", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{
				map[string]any{"elemento_id": campoAID, "tipo": "CAMPO", "etiqueta": "Costo A", "catalogo_id": nil, "columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": map[string]any{"tipo_campo": "NUMERO"}},
				map[string]any{"elemento_id": campoBID, "tipo": "CAMPO", "etiqueta": "Costo B", "catalogo_id": nil, "columnas_ancho": 1, "orden": 2, "requerido": false, "configuracion": map[string]any{"tipo_campo": "NUMERO"}},
				map[string]any{"elemento_id": calc1ID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Subtotal", "catalogo_id": nil, "columnas_ancho": 1, "orden": 3, "requerido": false, "configuracion": map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []any{campoAID, campoBID}}},
				map[string]any{"elemento_id": calc2ID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Total", "catalogo_id": nil, "columnas_ancho": 1, "orden": 4, "requerido": false, "funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_formula": "SIMPLE", "operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []any{calc1ID}}},
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
	fixture = fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID, CompiladoID: compiladoID}
	return fixture, campoAID, campoBID, calc1ID, calc2ID
}

func TestCotizadorRuntime_CampoCalculadoAnidadoResuelveDosNiveles(t *testing.T) {
	fixture, campoAID, campoBID, calc1ID, calc2ID := fixtureRuntimeConCampoCalculado(t)

	rec := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{campoAID: "100", campoBID: "50"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar valores base: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = getRuntime(t, fixture, "version=1")
	if rec.Code != http.StatusOK {
		t.Fatalf("obtener runtime: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	tabs := res.Estructura["tabs"].([]any)
	elementos := tabs[0].(map[string]any)["elementos"].([]any)
	var valorCalc1, valorCalc2 any
	for _, elRaw := range elementos {
		el := elRaw.(map[string]any)
		if el["elemento_id"] == calc1ID {
			valorCalc1 = el["valor_resuelto"]
		}
		if el["elemento_id"] == calc2ID {
			valorCalc2 = el["valor_resuelto"]
		}
	}
	if valorCalc1 != 150.0 {
		t.Fatalf("calc1 (SUMA de 100+50) esperaba 150, obtuvo %v", valorCalc1)
	}
	if valorCalc2 != 150.0 {
		t.Fatalf("calc2 (PROMEDIO de calc1=150) esperaba 150, obtuvo %v", valorCalc2)
	}

	var totalPrecio float64
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT total_precio FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, fixture.CotizacionID).Scan(&totalPrecio); err != nil {
		t.Fatal(err)
	}
	if totalPrecio != 150 {
		t.Fatalf("cotizacion_versiones.total_precio esperaba 150 (funcion_campo=TOTAL_PRECIO_OFERTA de calc2), obtuvo %v", totalPrecio)
	}
}

// fixtureListaPreciosCompilada monta, con los endpoints reales (tabs,
// elementos, ítems, compilador, cotización), una calculadora con:
//   - una LISTA_PRECIOS "UNICA" con 3 ítems.
//   - una LISTA_PRECIOS "MULTIPLE" con 2 ítems.
//   - un CAMPO_CALCULADO que suma ambas listas.
//
// Ronda 3 (migración 0019), tarea 9: prueba end-to-end contra el pipeline
// real (no una estructura armada a mano) para que la ausencia de
// costo_interno/margen_porcentaje en el compilado quede probada de verdad,
// no solo asumida.
type fixtureListaPreciosCompilada struct {
	Runtime         *CotizadorRuntimeHandler
	CotizacionID    string
	Version         int
	UnicaID         string
	MultipleID      string
	CalculadoID     string
	ItemUnicaAID    string
	ItemUnicaBID    string
	ItemMultipleAID string
	ItemMultipleBID string
}

func crearFixtureListaPreciosCompilada(t *testing.T) fixtureListaPreciosCompilada {
	t.Helper()
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	itemsHandler := &ListaPreciosItemsHandler{DB: tabsHandler.DB}
	compilador := &CompiladorHandler{DB: tabsHandler.DB}
	tabID := "TEST-TAB-LP-RT-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Precios", "activo": true,
	})

	unicaID := "TEST-EL-LP-UNICA-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": unicaID, "tab_id": tabID, "tipo": "LISTA_PRECIOS", "etiqueta": "Servicio único",
		"configuracion": map[string]any{"tipo_lista_precios": "UNICA"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear lista UNICA: %s", rec.Body.String())
	}
	_, resA := crearItemListaPrecios(t, itemsHandler, unicaID, map[string]any{
		"codigo": "A", "nombre": "Ítem A", "precio": 100, "costo_interno": 60, "margen_porcentaje": 40,
	})
	itemUnicaA, _ := resA["item_id"].(string)
	_, resB := crearItemListaPrecios(t, itemsHandler, unicaID, map[string]any{
		"codigo": "B", "nombre": "Ítem B", "precio": 200, "costo_interno": 120, "margen_porcentaje": 40,
	})
	itemUnicaB, _ := resB["item_id"].(string)
	crearItemListaPrecios(t, itemsHandler, unicaID, map[string]any{"codigo": "C", "nombre": "Ítem C", "precio": 300})

	multipleID := "TEST-EL-LP-MULTI-" + sufijoUnico()
	rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": multipleID, "tab_id": tabID, "tipo": "LISTA_PRECIOS", "etiqueta": "Servicios múltiples",
		"configuracion": map[string]any{"tipo_lista_precios": "MULTIPLE"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear lista MULTIPLE: %s", rec.Body.String())
	}
	_, resMA := crearItemListaPrecios(t, itemsHandler, multipleID, map[string]any{"codigo": "M1", "nombre": "Multi 1", "precio": 50})
	itemMultipleA, _ := resMA["item_id"].(string)
	_, resMB := crearItemListaPrecios(t, itemsHandler, multipleID, map[string]any{"codigo": "M2", "nombre": "Multi 2", "precio": 75})
	itemMultipleB, _ := resMB["item_id"].(string)

	calcID := "TEST-EL-LP-CALC-" + sufijoUnico()
	rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Total combinado",
		"configuracion": map[string]any{"operacion": "SUMA", "tipo_resultado": "MONEDA", "decimales": 2, "operandos": []string{unicaID, multipleID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear campo calculado: %s", rec.Body.String())
	}

	recComp := postCatalogos(t, compilador.Compilar, "/api/cotizador/compilar", map[string]any{"calculadora_id": calculadoraID})
	var resComp respuestaCompiladorTest
	assertJSON(t, recComp.Body.Bytes(), &resComp)
	if !resComp.OK || !resComp.Valido || !resComp.Compilado {
		t.Fatalf("compilar: esperaba válido y compilado, obtuvo %+v", resComp)
	}

	cotizacionID, _, _ := crearCotizacionPrueba(t, tabsHandler.DB, "Borrador", "", "")
	if _, err := tabsHandler.DB.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, calculadoraID, cotizacionID); err != nil {
		t.Fatalf("no se pudo apuntar la cotización a la calculadora de prueba: %v", err)
	}

	return fixtureListaPreciosCompilada{
		Runtime: &CotizadorRuntimeHandler{DB: tabsHandler.DB}, CotizacionID: cotizacionID, Version: 1,
		UnicaID: unicaID, MultipleID: multipleID, CalculadoID: calcID,
		ItemUnicaAID: itemUnicaA, ItemUnicaBID: itemUnicaB,
		ItemMultipleAID: itemMultipleA, ItemMultipleBID: itemMultipleB,
	}
}

func elementoPorIDEnEstructura(estructura map[string]any, elementoID string) map[string]any {
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		for _, elRaw := range elementos {
			el, _ := elRaw.(map[string]any)
			if el["elemento_id"] == elementoID {
				return el
			}
		}
	}
	return nil
}

func TestCotizadorRuntime_ListaPreciosUnicaYCampoCalculadoQueLaUsa(t *testing.T) {
	fixture := crearFixtureListaPreciosCompilada(t)

	rec := getRuntime(t, fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET runtime: %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	elementoUnica := elementoPorIDEnEstructura(res.Estructura, fixture.UnicaID)
	if elementoUnica == nil {
		t.Fatal("no se encontró la lista UNICA en la estructura")
	}
	items, _ := elementoUnica["configuracion"].(map[string]any)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("esperaba 3 ítems activos, obtuvo %d", len(items))
	}
	for _, itemRaw := range items {
		item, _ := itemRaw.(map[string]any)
		if _, tiene := item["costo_interno"]; tiene {
			t.Fatalf("costo_interno NUNCA debe viajar en el compilado/runtime, pero apareció: %+v", item)
		}
		if _, tiene := item["margen_porcentaje"]; tiene {
			t.Fatalf("margen_porcentaje NUNCA debe viajar en el compilado/runtime, pero apareció: %+v", item)
		}
	}

	// también se verifica directo contra lo persistido en cotizadores_compilados,
	// no solo contra la respuesta HTTP.
	var configuracionJSON []byte
	if err := fixture.Runtime.DB.QueryRow(context.Background(), `
		SELECT configuracion FROM cotizadores_compilados cc
		JOIN cotizaciones c ON c.compilado_id_usado = cc.compilado_id
		WHERE c.cotizacion_id = $1`, fixture.CotizacionID).Scan(&configuracionJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configuracionJSON), "costo_interno") || strings.Contains(string(configuracionJSON), "margen_porcentaje") {
		t.Fatal("el JSON compilado persistido no debe contener costo_interno/margen_porcentaje en ningún lado de una Lista de Precios")
	}

	// UNICA: item B (precio 200) x cantidad 3 = 600.
	recPost := postValoresRuntime(t, fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}, map[string]any{
		"version": 1, "valores": map[string]any{fixture.UnicaID: map[string]any{"item_id": fixture.ItemUnicaBID, "cantidad": 3}},
	})
	if recPost.Code != http.StatusOK {
		t.Fatalf("guardar selección UNICA: %d: %s", recPost.Code, recPost.Body.String())
	}

	// MULTIPLE: M1(50)x2 + M2(75)x2 = 100+150 = 250.
	recPost = postValoresRuntime(t, fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}, map[string]any{
		"version": 1, "valores": map[string]any{fixture.MultipleID: map[string]any{"filas": []any{
			map[string]any{"item_id": fixture.ItemMultipleAID, "cantidad": 2},
			map[string]any{"item_id": fixture.ItemMultipleBID, "cantidad": 2},
		}}},
	})
	if recPost.Code != http.StatusOK {
		t.Fatalf("guardar filas MULTIPLE: %d: %s", recPost.Code, recPost.Body.String())
	}

	rec = getRuntime(t, fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}, "version=1")
	assertJSON(t, rec.Body.Bytes(), &res)
	elementoUnica = elementoPorIDEnEstructura(res.Estructura, fixture.UnicaID)
	if elementoUnica["valor_resuelto"] != 600.0 {
		t.Fatalf("UNICA: esperaba valor_resuelto=600, obtuvo %v", elementoUnica["valor_resuelto"])
	}
	elementoMultiple := elementoPorIDEnEstructura(res.Estructura, fixture.MultipleID)
	if elementoMultiple["valor_resuelto"] != 250.0 {
		t.Fatalf("MULTIPLE: esperaba valor_resuelto=250, obtuvo %v", elementoMultiple["valor_resuelto"])
	}
	elementoCalculado := elementoPorIDEnEstructura(res.Estructura, fixture.CalculadoID)
	if elementoCalculado["valor_resuelto"] != 850.0 { // 600 + 250
		t.Fatalf("Campo Calculado(UNICA+MULTIPLE): esperaba 850, obtuvo %v", elementoCalculado["valor_resuelto"])
	}
}

// TestCotizadorRuntime_CampoCatalogoConValorCalculoEnCampoCalculado cubre
// el camino feliz del documento de definición funcional (caso ISA Custom,
// migración 0023): catálogo PORCENTAJE con M20=0.20/M30=0.30, un Campo
// Catálogo que lo usa, y un Campo Calculado que lo toma como operando. El
// resultado debe usar el valor_calculo real (0.30), nunca la etiqueta ni
// el código, y un campo todavía sin seleccionar no debe resolver ningún
// número (nada de "tomar el primer valor por defecto").
func TestCotizadorRuntime_CampoCatalogoConValorCalculoEnCampoCalculado(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	catalogosHandler := &CatalogosHandler{DB: tabsHandler.DB}
	compilador := &CompiladorHandler{DB: tabsHandler.DB}

	catalogoID := crearCatalogoPrueba(t, tabsHandler.DB, "Catálogo margen runtime", "")
	rec := postCatalogos(t, catalogosHandler.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": catalogoID, "nombre_catalogo": "Catálogo margen runtime", "activo": true, "tipo_calculo": "PORCENTAJE",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("fijar tipo_calculo: %s", rec.Body.String())
	}
	postCatalogos(t, catalogosHandler.GuardarValor, "/api/catalogos/valores", map[string]any{
		"valor_id": "TEST-VAL-M20-" + sufijoUnico(), "catalogo_id": catalogoID, "clave": "M20",
		"texto_visible": "20%", "valor_sistema": "M20", "activo": true, "valor_calculo": 0.20,
	})
	rec = postCatalogos(t, catalogosHandler.GuardarValor, "/api/catalogos/valores", map[string]any{
		"valor_id": "TEST-VAL-M30-" + sufijoUnico(), "catalogo_id": catalogoID, "clave": "M30",
		"texto_visible": "30%", "valor_sistema": "M30", "activo": true, "valor_calculo": 0.30,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear M30: %s", rec.Body.String())
	}

	tabID := "TEST-TAB-CATCALC-RT-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Margen", "activo": true,
	})
	campoCatalogoID := "TEST-EL-CATCALC-RT-" + sufijoUnico()
	rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoCatalogoID, "tab_id": tabID, "tipo": "CAMPO_CATALOGO",
		"etiqueta": "Margen", "catalogo_id": catalogoID, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear campo catálogo: %s", rec.Body.String())
	}
	calcID := "TEST-EL-CATCALC-CALC-RT-" + sufijoUnico()
	rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Margen calculado",
		"configuracion": map[string]any{
			"operacion": "PROMEDIO", "tipo_resultado": "PORCENTAJE", "decimales": 2, "operandos": []string{campoCatalogoID},
		}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear campo calculado: %s", rec.Body.String())
	}

	recComp := postCatalogos(t, compilador.Compilar, "/api/cotizador/compilar", map[string]any{"calculadora_id": calculadoraID})
	var resComp respuestaCompiladorTest
	assertJSON(t, recComp.Body.Bytes(), &resComp)
	if !resComp.OK || !resComp.Valido || !resComp.Compilado {
		t.Fatalf("compilar: esperaba válido y compilado, obtuvo %+v", resComp)
	}

	cotizacionID, _, _ := crearCotizacionPrueba(t, tabsHandler.DB, "Borrador", "", "")
	if _, err := tabsHandler.DB.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, calculadoraID, cotizacionID); err != nil {
		t.Fatal(err)
	}
	fixture := fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: tabsHandler.DB}, CotizacionID: cotizacionID}

	// Sin selección todavía (placeholder "Seleccione..."): el Campo
	// Calculado no debe tomar el primer valor del catálogo por defecto.
	rec = getRuntime(t, fixture, "")
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	elementoCalc := elementoPorIDEnEstructura(res.Estructura, calcID)
	if elementoCalc["valor_resuelto"] != nil {
		t.Fatalf("sin selección de catálogo, esperaba valor_resuelto=nil, obtuvo %v", elementoCalc["valor_resuelto"])
	}

	recPost := postValoresRuntime(t, fixture, map[string]any{
		"version": 1, "valores": map[string]any{campoCatalogoID: "M30"},
	})
	if recPost.Code != http.StatusOK {
		t.Fatalf("guardar selección de catálogo: %d: %s", recPost.Code, recPost.Body.String())
	}

	rec = getRuntime(t, fixture, "version=1")
	assertJSON(t, rec.Body.Bytes(), &res)
	elementoCalc = elementoPorIDEnEstructura(res.Estructura, calcID)
	if elementoCalc["valor_resuelto"] != 0.3 {
		t.Fatalf("esperaba valor_resuelto=0.3 (no 30), obtuvo %v", elementoCalc["valor_resuelto"])
	}
}

func TestCotizadorRuntime_ListaPreciosRechazaItemDeOtroElemento(t *testing.T) {
	fixture := crearFixtureListaPreciosCompilada(t)
	// ItemMultipleAID pertenece a MultipleID, no a UnicaID.
	rec := postValoresRuntime(t, fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}, map[string]any{
		"version": 1, "valores": map[string]any{fixture.UnicaID: map[string]any{"item_id": fixture.ItemMultipleAID, "cantidad": 1}},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ítem de otro elemento: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// fixtureTablaCompilada monta, con los endpoints reales (tabs, elementos,
// columnas, compilador, cotización), una TABLA con 2 columnas propias
// (NUMERO y TEXTO) y 1 columna CAMPO_EXISTENTE — Ronda 4 (migración 0020),
// tarea 8.
type fixtureTablaCompilada struct {
	Runtime        *CotizadorRuntimeHandler
	CotizacionID   string
	TablaID        string
	ColHorasID     string // PROPIA, NUMERO — la que se totaliza.
	ColPerfilID    string // PROPIA, TEXTO.
	ColClienteID   string // CAMPO_EXISTENTE, referencia CampoClienteID.
	CampoClienteID string
}

func crearFixtureTablaCompilada(t *testing.T, permitirAgregarFilas, permitirEliminarFilas bool) fixtureTablaCompilada {
	t.Helper()
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	columnasHandler := &TablaColumnasHandler{DB: tabsHandler.DB}
	compilador := &CompiladorHandler{DB: tabsHandler.DB}
	tabID := "TEST-TAB-TABLA-RT-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Perfiles", "activo": true,
	})

	campoClienteID := "TEST-EL-TABLA-CLIENTE-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoClienteID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Cliente",
		"configuracion": map[string]any{"tipo_campo": "TEXTO"}, "activo": true,
	})

	tablaID := "TEST-EL-TABLA-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": tablaID, "tab_id": tabID, "tipo": "TABLA", "etiqueta": "Perfiles del proyecto",
		"configuracion": map[string]any{"permitir_agregar_filas": permitirAgregarFilas, "permitir_eliminar_filas": permitirEliminarFilas}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tabla: %s", rec.Body.String())
	}

	_, resHoras := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "PROPIA", "tipo_dato": "NUMERO", "etiqueta": "Horas", "orden": 1})
	colHorasID, _ := resHoras["columna_id"].(string)
	_, resPerfil := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "PROPIA", "tipo_dato": "TEXTO", "etiqueta": "Perfil", "orden": 2})
	colPerfilID, _ := resPerfil["columna_id"].(string)
	_, resCliente := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "CAMPO_EXISTENTE", "campo_existente_id": campoClienteID, "orden": 3})
	colClienteID, _ := resCliente["columna_id"].(string)

	recComp := postCatalogos(t, compilador.Compilar, "/api/cotizador/compilar", map[string]any{"calculadora_id": calculadoraID})
	var resComp respuestaCompiladorTest
	assertJSON(t, recComp.Body.Bytes(), &resComp)
	if !resComp.OK || !resComp.Valido || !resComp.Compilado {
		t.Fatalf("compilar: esperaba válido y compilado, obtuvo %+v", resComp)
	}

	cotizacionID, _, _ := crearCotizacionPrueba(t, tabsHandler.DB, "Borrador", "", "")
	if _, err := tabsHandler.DB.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, calculadoraID, cotizacionID); err != nil {
		t.Fatalf("no se pudo apuntar la cotización a la calculadora de prueba: %v", err)
	}

	return fixtureTablaCompilada{
		Runtime: &CotizadorRuntimeHandler{DB: tabsHandler.DB}, CotizacionID: cotizacionID, TablaID: tablaID,
		ColHorasID: colHorasID, ColPerfilID: colPerfilID, ColClienteID: colClienteID, CampoClienteID: campoClienteID,
	}
}

func TestCotizadorRuntime_TablaGuardaFilasYSumaTotal(t *testing.T) {
	fixture := crearFixtureTablaCompilada(t, true, true)
	filas := []any{
		map[string]any{fixture.ColHorasID: "10", fixture.ColPerfilID: "Junior", fixture.ColClienteID: "ACME"},
		map[string]any{fixture.ColHorasID: 20.0, fixture.ColPerfilID: "Senior", fixture.ColClienteID: "ACME"},
		map[string]any{fixture.ColHorasID: "5.5", fixture.ColPerfilID: "Lead", fixture.ColClienteID: "ACME"},
	}
	rt := fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}
	rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{fixture.TablaID: map[string]any{"filas": filas}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar filas: %d: %s", rec.Code, rec.Body.String())
	}

	recGet := getRuntime(t, rt, "version=1")
	if recGet.Code != http.StatusOK {
		t.Fatalf("GET runtime: %d: %s", recGet.Code, recGet.Body.String())
	}
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, recGet.Body.Bytes(), &res)
	elTabla := elementoPorIDEnEstructura(res.Estructura, fixture.TablaID)
	if elTabla == nil {
		t.Fatal("no se encontró la tabla en la estructura")
	}
	if elTabla["valor_resuelto"] != 35.5 {
		t.Fatalf("esperaba total 35.5 (10+20+5.5), obtuvo %v", elTabla["valor_resuelto"])
	}
	columnas, _ := elTabla["configuracion"].(map[string]any)["columnas"].([]any)
	if len(columnas) != 3 {
		t.Fatalf("esperaba 3 columnas compiladas, obtuvo %d: %+v", len(columnas), columnas)
	}
}

func TestCotizadorRuntime_TablaRechazaAgregarFilasSiNoPermitido(t *testing.T) {
	fixture := crearFixtureTablaCompilada(t, false, true)
	rt := fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}
	rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{
		fixture.TablaID: map[string]any{"filas": []any{map[string]any{fixture.ColHorasID: "1"}}},
	}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("permitir_agregar_filas=false: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizadorRuntime_TablaRechazaColumnaAjena(t *testing.T) {
	fixture := crearFixtureTablaCompilada(t, true, true)
	otraFixture := crearFixtureTablaCompilada(t, true, true)
	rt := fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}
	rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{
		fixture.TablaID: map[string]any{"filas": []any{map[string]any{otraFixture.ColHorasID: "1"}}},
	}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("columna de otra tabla: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTablaColumnas_EliminarRechazaSiTieneDatosGuardados(t *testing.T) {
	fixture := crearFixtureTablaCompilada(t, true, true)
	rt := fixtureRuntime{Handler: fixture.Runtime, CotizacionID: fixture.CotizacionID}
	rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{
		fixture.TablaID: map[string]any{"filas": []any{map[string]any{fixture.ColHorasID: "10"}}},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar fila base: %d: %s", rec.Code, rec.Body.String())
	}

	columnasHandler := &TablaColumnasHandler{DB: fixture.Runtime.DB}
	recDelete := deleteColumnaTabla(t, columnasHandler, fixture.ColHorasID)
	if recDelete.Code != http.StatusBadRequest {
		t.Fatalf("eliminar columna con datos guardados: esperaba 400, dio %d: %s", recDelete.Code, recDelete.Body.String())
	}
}

func TestCotizadorRuntime_RechazaElementoAjeno(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	rec := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{"ELEMENTO-DE-OTRO-COTIZADOR": "valor"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("elemento ajeno: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}
