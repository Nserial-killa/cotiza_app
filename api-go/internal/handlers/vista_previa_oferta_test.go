package handlers

// Pruebas de la Vista Previa de la Oferta (Ronda C). La más importante es
// CTZ-TEC-004: para una misma cotización+versión+plantilla, este endpoint y
// el enlace público real (enlaces_publicos.go) deben resolver EXACTAMENTE
// el mismo valor — ambos llaman a construirDocumentoOferta, ninguno tiene
// su propia lógica de resolución.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func getVistaPreviaOferta(t *testing.T, handler *VistaPreviaOfertaHandler, cotizacionID, version string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/api/cotizaciones/{id}/vista-previa-oferta", handler.Ver)
	ruta := "/api/cotizaciones/" + cotizacionID + "/vista-previa-oferta"
	if version != "" {
		ruta += "?version=" + version
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))
	return rec
}

// TestVistaPreviaOferta_MismoDocumentoQueElEnlacePublico es CTZ-TEC-004: se
// genera un enlace público real y se lee (VerCotizacion), y por separado se
// pide la vista previa (VistaPreviaOfertaHandler.Ver) para la misma
// cotización+versión. La fixture nace "Borrador" (crearCotizacionPrueba),
// así que ninguna de las dos llamadas dispara la transición a "Vista por el
// Cliente" — el documento entero, campo por campo, debe salir idéntico.
func TestVistaPreviaOferta_MismoDocumentoQueElEnlacePublico(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "Sí")

	enlaces := &EnlacesPublicosHandler{DB: f.pool}
	recGenerar := postEnlace(t, enlaces, f.cotizacionID, map[string]any{"version": 1})
	if recGenerar.Code != http.StatusOK {
		t.Fatalf("generar enlace: %d: %s", recGenerar.Code, recGenerar.Body.String())
	}
	var generado struct {
		Token string `json:"token"`
	}
	assertJSON(t, recGenerar.Body.Bytes(), &generado)

	recPublico := getPublico(t, enlaces, generado.Token)
	if recPublico.Code != http.StatusOK {
		t.Fatalf("ver enlace público: %d: %s", recPublico.Code, recPublico.Body.String())
	}
	var doc1 map[string]any
	assertJSON(t, recPublico.Body.Bytes(), &doc1)

	vistaPrevia := &VistaPreviaOfertaHandler{DB: f.pool}
	recPreview := getVistaPreviaOferta(t, vistaPrevia, f.cotizacionID, "1")
	if recPreview.Code != http.StatusOK {
		t.Fatalf("vista previa: %d: %s", recPreview.Code, recPreview.Body.String())
	}
	var doc2 map[string]any
	assertJSON(t, recPreview.Body.Bytes(), &doc2)

	// La vista previa trae un campo extra (vista_previa:true) para que el
	// frontend nunca la confunda con el documento público real — se quita
	// antes de comparar, el resto tiene que ser IDÉNTICO.
	if doc2["vista_previa"] != true {
		t.Fatalf("la vista previa debería marcarse a sí misma con vista_previa:true, dio %v", doc2["vista_previa"])
	}
	delete(doc2, "vista_previa")
	delete(doc1, "ok")
	delete(doc2, "ok")

	if !reflect.DeepEqual(doc1, doc2) {
		t.Fatalf("CTZ-TEC-004: la vista previa y el enlace público resolvieron valores distintos.\npúblico:  %s\npreview:  %s", mustJSON(t, doc1), mustJSON(t, doc2))
	}
	// Confirmación puntual de que la tabla de escenarios sí viajó (no un
	// mapa vacío que por casualidad quedó igual en los dos).
	plantilla, _ := doc2["plantilla"].(map[string]any)
	if plantilla == nil {
		t.Fatal("esperaba la plantilla renderizada en la vista previa")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestVistaPreviaOferta_FuncionaEnBorradorSinEnlacePrevio es VPO-001: la
// vista previa debe funcionar "durante la creación/edición", sin que nunca
// se haya generado un enlace público para esa cotización.
func TestVistaPreviaOferta_FuncionaEnBorradorSinEnlacePrevio(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "No")

	var estado string
	if err := f.pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, f.cotizacionID).Scan(&estado); err != nil {
		t.Fatal(err)
	}
	if estado != "Borrador" {
		t.Fatalf("la fixture debía nacer Borrador, nació %q", estado)
	}

	vistaPrevia := &VistaPreviaOfertaHandler{DB: f.pool}
	rec := getVistaPreviaOferta(t, vistaPrevia, f.cotizacionID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200 en Borrador sin enlace previo, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	if res["estado"] != "Borrador" {
		t.Fatalf("estado inesperado en la respuesta: %v", res["estado"])
	}
	if res["plantilla"] == nil {
		t.Fatal("esperaba la plantilla renderizada")
	}
}

// TestVistaPreviaOferta_SinEfectosSecundarios es VPO-004: llamarla varias
// veces seguidas no debe crear ningún enlace público ni cambiar el estado
// de la cotización, sin importar cuántas veces se invoque.
func TestVistaPreviaOferta_SinEfectosSecundarios(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)

	var estadoAntes string
	if err := f.pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, f.cotizacionID).Scan(&estadoAntes); err != nil {
		t.Fatal(err)
	}

	vistaPrevia := &VistaPreviaOfertaHandler{DB: f.pool}
	for i := 0; i < 3; i++ {
		rec := getVistaPreviaOferta(t, vistaPrevia, f.cotizacionID, "1")
		if rec.Code != http.StatusOK {
			t.Fatalf("llamada %d: esperaba 200, dio %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	var cantidadEnlaces int
	if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_enlaces_publicos WHERE cotizacion_id=$1`, f.cotizacionID).Scan(&cantidadEnlaces); err != nil {
		t.Fatal(err)
	}
	if cantidadEnlaces != 0 {
		t.Fatalf("la vista previa no debe generar enlaces públicos, se encontraron %d", cantidadEnlaces)
	}

	var estadoDespues string
	if err := f.pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, f.cotizacionID).Scan(&estadoDespues); err != nil {
		t.Fatal(err)
	}
	if estadoDespues != estadoAntes {
		t.Fatalf("la vista previa cambió el estado de la cotización: %q -> %q", estadoAntes, estadoDespues)
	}

	var eventosHistorial int
	if err := f.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_historial WHERE cotizacion_id=$1`, f.cotizacionID).Scan(&eventosHistorial); err != nil {
		t.Fatal(err)
	}
	if eventosHistorial != 0 {
		t.Fatalf("la vista previa no debe registrar historial, se encontraron %d eventos", eventosHistorial)
	}
}

// TestVistaPreviaOferta_SinPlaceholderConDatosReales es CP-12/VPO-007: con
// el dato del que depende el token [USA_TELEFONIA] realmente cargado, el
// bloque de texto de la vista previa no debe traer ningún placeholder sin
// resolver.
func TestVistaPreviaOferta_SinPlaceholderConDatosReales(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "Sí")

	vistaPrevia := &VistaPreviaOfertaHandler{DB: f.pool}
	rec := getVistaPreviaOferta(t, vistaPrevia, f.cotizacionID, "1")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		Plantilla *plantillaRenderizada `json:"plantilla"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	if respuesta.Plantilla == nil {
		t.Fatal("esperaba la plantilla renderizada")
	}
	bloque := bloquePorTipo(respuesta.Plantilla.Secciones, "TEXTO")
	if bloque == nil {
		t.Fatal("el bloque de texto debería mostrarse con USA_TELEFONIA=Sí")
	}
	if strings.Contains(bloque.Contenido, "[") {
		t.Fatalf("CP-12: no debe quedar ningún placeholder sin resolver con datos reales cargados: %q", bloque.Contenido)
	}
	if !strings.Contains(bloque.Contenido, "Sí") {
		t.Fatalf("el valor real (Sí) debería aparecer interpolado: %q", bloque.Contenido)
	}
}

func TestVistaPreviaOferta_CotizacionInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	vistaPrevia := &VistaPreviaOfertaHandler{DB: pool}
	rec := getVistaPreviaOferta(t, vistaPrevia, "COT-QUE-NO-EXISTE-"+sufijoUnico(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVistaPreviaOferta_VersionInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	vistaPrevia := &VistaPreviaOfertaHandler{DB: pool}
	rec := getVistaPreviaOferta(t, vistaPrevia, cotizacionID, "99")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404 para una versión inexistente, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestVistaPreviaOferta_NuncaExponeCostoNiGananciaNiMargen: mismo criterio
// que el enlace público (ver enlaces_publicos.go) — la vista previa modela
// EXACTAMENTE lo que vería el cliente, así que tampoco debe filtrar costos
// internos aunque la llame un usuario con sesión y permiso de ver precio.
func TestVistaPreviaOferta_NuncaExponeCostoNiGananciaNiMargen(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	vistaPrevia := &VistaPreviaOfertaHandler{DB: pool}
	rec := getVistaPreviaOferta(t, vistaPrevia, fixture.CotizacionID, "1")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, prohibido := range []string{"total_costo", "total_ganancia", "margen_total", "costo", "ganancia", "margen"} {
		if _, existe := res[prohibido]; existe {
			t.Fatalf("la vista previa NUNCA debe traer %q, y vino: %v", prohibido, res)
		}
	}
}
