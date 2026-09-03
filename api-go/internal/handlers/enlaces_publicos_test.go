package handlers

// Pruebas de integración de la Vista Previa / enlace público al
// cliente (esquema 0009). Mismo criterio que el resto del paquete:
// contra Postgres real, con fixtures propias que se limpian solas.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixtureEnlace struct {
	Handler       *EnlacesPublicosHandler
	CotizacionID  string
	CalculadoraID string
	TabID         string
	CampoID       string
	CatalogoElID  string
	LeyendaID     string
	ValorSistema  string
	TextoVisible  string
}

// crearFixtureEnlace arma una cotización "Enviada al Cliente" con una
// sección, un CAMPO con valor guardado, un CAMPO_CATALOGO con valor
// guardado (para probar la traducción a texto_visible) y una LEYENDA
// (sin valor — es texto fijo de la propuesta).
func crearFixtureEnlace(t *testing.T, pool *pgxpool.Pool) fixtureEnlace {
	t.Helper()
	ctx := context.Background()

	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")

	tabID := "TEST-ENLACE-TAB-" + sufijoUnico()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, orden, activo)
		VALUES ($1, $2, 'Datos generales', 1, true)`, tabID, calculadoraID); err != nil {
		t.Fatalf("no se pudo crear el tab de prueba: %v", err)
	}

	// Insertado a mano (no con crearValorCatalogoPrueba) para que
	// texto_visible y valor_sistema sean DISTINTOS a propósito: así la
	// prueba de abajo realmente confirma que el enlace traduce al texto
	// visible y no filtra el código interno. crearCatalogoPrueba ya
	// hace CASCADE sobre catalogo_valores al limpiar el catálogo.
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo enlace", "")
	valorSistema := "OPC-INTERNA-" + sufijoUnico()
	textoVisible := "Opción visible de prueba"
	if _, err := pool.Exec(ctx, `
		INSERT INTO catalogo_valores (valor_id, catalogo_id, texto_visible, valor_sistema, activo)
		VALUES ($1, $2, $3, $4, true)`,
		"test-val-"+sufijoUnico(), catalogoID, textoVisible, valorSistema); err != nil {
		t.Fatalf("no se pudo crear el valor de catálogo de prueba: %v", err)
	}

	campoID := "TEST-ENLACE-CAMPO-" + sufijoUnico()
	catalogoElID := "TEST-ENLACE-CATEL-" + sufijoUnico()
	leyendaID := "TEST-ENLACE-LEY-" + sufijoUnico()
	if _, err := pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id, tab_id, tipo, etiqueta, catalogo_id, orden, activo)
		VALUES
			($1, $4, 'CAMPO', 'Nombre del proyecto', NULL, 1, true),
			($2, $4, 'CAMPO_CATALOGO', 'Categoría', $5, 2, true),
			($3, $4, 'LEYENDA', 'Propuesta preparada para su revisión.', NULL, 3, true)`,
		campoID, catalogoElID, leyendaID, tabID, catalogoID); err != nil {
		t.Fatalf("no se pudo crear los elementos de prueba: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, valor)
		VALUES
			($1, 1, $2, to_jsonb('Data center San José'::text)),
			($1, 1, $3, to_jsonb($4::text))`,
		cotizacionID, campoID, catalogoElID, valorSistema); err != nil {
		t.Fatalf("no se pudo guardar los valores de prueba: %v", err)
	}

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM elementos_tab_cotizador WHERE tab_id = $1`, tabID)
		pool.Exec(ctx, `DELETE FROM tabs_cotizador WHERE tab_id = $1`, tabID)
	})

	return fixtureEnlace{
		Handler: &EnlacesPublicosHandler{DB: pool}, CotizacionID: cotizacionID, CalculadoraID: calculadoraID,
		TabID: tabID, CampoID: campoID, CatalogoElID: catalogoElID, LeyendaID: leyendaID,
		ValorSistema: valorSistema, TextoVisible: textoVisible,
	}
}

func postEnlace(t *testing.T, handler *EnlacesPublicosHandler, cotizacionID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/cotizaciones/{id}/enlace", handler.GenerarEnlace)
	ruta := "/api/cotizaciones/" + cotizacionID + "/enlace"
	if body == nil {
		req := httptest.NewRequest(http.MethodPost, ruta, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	return postCatalogos(t, func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) }, ruta, body)
}

func getPublico(t *testing.T, handler *EnlacesPublicosHandler, token string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/api/publico/cotizacion/{token}", handler.VerCotizacion)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/publico/cotizacion/"+token, nil))
	return rec
}

func TestEnlacesPublicosGenerar_ReutilizaElMismoTokenParaLaMismaVersion(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	rec := postEnlace(t, fixture.Handler, fixture.CotizacionID, map[string]any{"version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("generar enlace: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var primero struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	assertJSON(t, rec.Body.Bytes(), &primero)
	if primero.Token == "" || primero.URL != "/publico.html?token="+primero.Token {
		t.Fatalf("respuesta inesperada: %+v", primero)
	}

	rec = postEnlace(t, fixture.Handler, fixture.CotizacionID, map[string]any{"version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("segunda llamada: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var segundo struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &segundo)
	if segundo.Token != primero.Token {
		t.Fatalf("se generó un token nuevo en vez de reutilizar: %q != %q", segundo.Token, primero.Token)
	}

	var cantidad int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM cotizacion_enlaces_publicos WHERE cotizacion_id = $1`, fixture.CotizacionID,
	).Scan(&cantidad); err != nil {
		t.Fatal(err)
	}
	if cantidad != 1 {
		t.Fatalf("cantidad de enlaces = %d, esperaba 1 (no debe acumular enlaces de sobra)", cantidad)
	}

	// Sin body (version omitida): debe caer a version_actual (=1) y
	// seguir reutilizando el mismo enlace.
	rec = postEnlace(t, fixture.Handler, fixture.CotizacionID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sin version: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var tercero struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &tercero)
	if tercero.Token != primero.Token {
		t.Fatalf("version_actual por defecto no reutilizó el enlace: %q != %q", tercero.Token, primero.Token)
	}
}

func TestEnlacesPublicosVer_TokenValidoNoTraeCostoNiGananciaNiMargen(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	rec := postEnlace(t, fixture.Handler, fixture.CotizacionID, map[string]any{"version": 1})
	var generado struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &generado)

	rec = getPublico(t, fixture.Handler, generado.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("ver público: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)

	for _, prohibido := range []string{"total_costo", "total_ganancia", "margen_total", "costo", "ganancia", "margen"} {
		if _, existe := res[prohibido]; existe {
			t.Fatalf("la respuesta pública NUNCA debe traer %q, y vino: %v", prohibido, res)
		}
	}
	if res["moneda"] != "US$" || res["total_precio"] != 1000.0 {
		t.Fatalf("moneda/total_precio inesperados: %+v", res)
	}

	tabs, ok := res["tabs"].([]any)
	if !ok || len(tabs) != 1 {
		t.Fatalf("tabs inesperados: %+v", res["tabs"])
	}
	elementos := tabs[0].(map[string]any)["elementos"].([]any)
	if len(elementos) != 3 {
		t.Fatalf("esperaba 3 elementos, dio %d: %+v", len(elementos), elementos)
	}

	valores := map[string]any{}
	for _, elRaw := range elementos {
		el := elRaw.(map[string]any)
		valores[el["elemento_id"].(string)] = el["valor"]
	}
	if valores[fixture.CampoID] != "Data center San José" {
		t.Fatalf("valor de CAMPO inesperado: %+v", valores[fixture.CampoID])
	}
	if valores[fixture.CatalogoElID] != fixture.TextoVisible {
		t.Fatalf("CAMPO_CATALOGO debe traer texto_visible (%q), dio %+v", fixture.TextoVisible, valores[fixture.CatalogoElID])
	}
	if valores[fixture.LeyendaID] != "Propuesta preparada para su revisión." {
		t.Fatalf("LEYENDA debe traer su etiqueta tal cual, dio %+v", valores[fixture.LeyendaID])
	}
}

func TestEnlacesPublicosVer_TokenInventadoDa404(t *testing.T) {
	pool := setupTestDB(t)
	handler := &EnlacesPublicosHandler{DB: pool}

	rec := getPublico(t, handler, "token-que-no-existe-"+sufijoUnico())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("token inventado: esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestEnlacesPublicosVer_TokenVencidoDa404IgualQueElInventado(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	token := "test-token-vencido-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version, fecha_expiracion)
		VALUES ($1, $2, 1, $3)`,
		token, fixture.CotizacionID, time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("no se pudo crear el enlace vencido de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizacion_enlaces_publicos WHERE token = $1`, token)
	})

	recVencido := getPublico(t, fixture.Handler, token)
	recInventado := getPublico(t, fixture.Handler, "otro-token-que-no-existe-"+sufijoUnico())

	if recVencido.Code != http.StatusNotFound {
		t.Fatalf("token vencido: esperaba 404, dio %d: %s", recVencido.Code, recVencido.Body.String())
	}
	if recVencido.Body.String() != recInventado.Body.String() {
		t.Fatalf("vencido e inventado deben responder exactamente igual (sin dar pistas): %q != %q", recVencido.Body.String(), recInventado.Body.String())
	}
}

func TestEnlacesPublicosVer_EnviadaAlClientePasaAVistaPorElCliente(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	var estadoInicial string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id = $1`, fixture.CotizacionID).Scan(&estadoInicial); err != nil {
		t.Fatal(err)
	}
	if estadoInicial != "Enviada al Cliente" {
		t.Fatalf("la fixture debía nacer 'Enviada al Cliente', nació %q", estadoInicial)
	}

	rec := postEnlace(t, fixture.Handler, fixture.CotizacionID, map[string]any{"version": 1})
	var generado struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &generado)

	rec = getPublico(t, fixture.Handler, generado.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("ver público: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var estadoCotizacion, estadoVersion string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id = $1`, fixture.CotizacionID).Scan(&estadoCotizacion); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id = $1 AND numero_version = 1`, fixture.CotizacionID).Scan(&estadoVersion); err != nil {
		t.Fatal(err)
	}
	if estadoCotizacion != "Vista por el Cliente" || estadoVersion != "Vista por el Cliente" {
		t.Fatalf("estado tras visitar el enlace: cotizacion=%q version=%q, esperaba 'Vista por el Cliente' en ambos", estadoCotizacion, estadoVersion)
	}

	var existeHistorial bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM cotizacion_historial WHERE cotizacion_id = $1 AND accion = 'VISTA_POR_CLIENTE')`,
		fixture.CotizacionID,
	).Scan(&existeHistorial); err != nil || !existeHistorial {
		t.Fatalf("no se registró el historial de VISTA_POR_CLIENTE: existe=%v err=%v", existeHistorial, err)
	}

	var visitas int
	if err := pool.QueryRow(context.Background(), `SELECT visitas FROM cotizacion_enlaces_publicos WHERE token = $1`, generado.Token).Scan(&visitas); err != nil {
		t.Fatal(err)
	}
	if visitas != 1 {
		t.Fatalf("visitas = %d, esperaba 1", visitas)
	}

	// Una segunda visita no debe revertir ni duplicar la transición
	// (la versión ya no está en "Enviada al Cliente").
	rec = getPublico(t, fixture.Handler, generado.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("segunda visita: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if err := pool.QueryRow(context.Background(), `SELECT visitas FROM cotizacion_enlaces_publicos WHERE token = $1`, generado.Token).Scan(&visitas); err != nil {
		t.Fatal(err)
	}
	if visitas != 2 {
		t.Fatalf("visitas tras la segunda visita = %d, esperaba 2", visitas)
	}
}
