package handlers

// Pruebas del PDF de la oferta (tarea 2). Las de integración pegan contra
// Postgres real (setupTestDB hace Skip sin DATABASE_URL). Gotenberg se
// reemplaza por un doble que hace EXACTAMENTE lo que haría la página
// publico.html dentro de Chromium: leer token y render de la URL y pedir
// GET /api/publico/cotizacion/{token}?render=... al handler real.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pdfFalso es lo que devuelve el doble de Gotenberg: empieza con %PDF-
// como cualquier PDF real.
var pdfFalso = []byte("%PDF-1.7\n% documento de prueba\n")

// convertidorQueAbreLaPagina simula a Chromium abriendo publico.html.
type convertidorQueAbreLaPagina struct {
	t           *testing.T              // para fallar desde adentro del doble
	publico     *EnlacesPublicosHandler // handler real del enlace público
	urlsVistas  []string                // URLs que el handler del PDF le pidió convertir
	codigoVisto int                     // status que respondió el GET público
}

// ConvertirURL cumple ConvertidorPDF.
func (c *convertidorQueAbreLaPagina) ConvertirURL(_ context.Context, destino string) ([]byte, error) {
	c.urlsVistas = append(c.urlsVistas, destino) // guarda la URL para revisarla en el test
	u, err := url.Parse(destino)                 // la URL tiene que ser parseable
	if err != nil {
		c.t.Fatalf("URL interna inválida %q: %v", destino, err)
	}
	token, render := u.Query().Get("token"), u.Query().Get("render") // lo mismo que lee publico.html
	rec := getPublicoConRender(c.t, c.publico, token, render)        // la llamada que hace la página
	c.codigoVisto = rec.Code                                         // se guarda para afirmarlo después
	if rec.Code != http.StatusOK {                                   // si la página no cargó, Gotenberg fallaría
		return nil, errors.New("la página pública no cargó")
	}
	return pdfFalso, nil // "PDF" generado
}

// getPublicoConRender es getPublico con ?render=pase.
func getPublicoConRender(t *testing.T, handler *EnlacesPublicosHandler, token, render string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()                                                         // router propio para resolver {token}
	router.Get("/api/publico/cotizacion/{token}", handler.VerCotizacion)              // misma ruta que main.go
	ruta := "/api/publico/cotizacion/" + token + "?render=" + url.QueryEscape(render) // con el pase
	rec := httptest.NewRecorder()                                                     // captura la respuesta
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))             // ejecuta sin sesión, como el cliente
	return rec
}

// getPDF pide el PDF con actorID en el contexto ("" = sin usuario).
func getPDF(t *testing.T, handler *OfertaPDFHandler, actorID, cotizacionID, query string) *httptest.ResponseRecorder {
	t.Helper()
	ruta := "/api/cotizaciones/" + cotizacionID + "/enlace/pdf" + query // ruta real
	req := httptest.NewRequest(http.MethodGet, ruta, nil)               // GET sin cuerpo
	if actorID != "" {
		req = conActor(req, actorID) // simula la sesión que pondría RequiereSesion
	}
	router := chi.NewRouter()                                          // resuelve {id}
	router.Get("/api/cotizaciones/{id}/enlace/pdf", handler.Descargar) // misma ruta que main.go
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// fotoEfectos es lo que NO debe cambiar al renderizar el PDF.
type fotoEfectos struct {
	estadoCotizacion, estadoVersion string // estado en ambas tablas
	visitas                         int    // contador del enlace
	ultimaVisita                    *time.Time
	historial                       int // eventos de Actividad
}

// tomarFoto lee el estado actual de la cotización y su enlace.
func tomarFoto(t *testing.T, pool *pgxpool.Pool, cotizacionID, token string) fotoEfectos {
	t.Helper()
	ctx := context.Background()
	var f fotoEfectos
	if err := pool.QueryRow(ctx, `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&f.estadoCotizacion); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, cotizacionID).Scan(&f.estadoVersion); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT visitas, ultima_visita FROM cotizacion_enlaces_publicos WHERE token=$1`, token).Scan(&f.visitas, &f.ultimaVisita); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cotizacion_historial WHERE cotizacion_id=$1`, cotizacionID).Scan(&f.historial); err != nil {
		t.Fatal(err)
	}
	return f
}

// afirmarSinEfectos compara dos fotos campo por campo.
func afirmarSinEfectos(t *testing.T, antes, despues fotoEfectos, contexto string) {
	t.Helper()
	if antes.estadoCotizacion != despues.estadoCotizacion || antes.estadoVersion != despues.estadoVersion {
		t.Fatalf("%s: cambió el estado: antes=%q/%q después=%q/%q", contexto, antes.estadoCotizacion, antes.estadoVersion, despues.estadoCotizacion, despues.estadoVersion)
	}
	if antes.visitas != despues.visitas || (antes.ultimaVisita == nil) != (despues.ultimaVisita == nil) {
		t.Fatalf("%s: se registró una visita: antes=%d después=%d", contexto, antes.visitas, despues.visitas)
	}
	if antes.historial != despues.historial {
		t.Fatalf("%s: se insertó historial: antes=%d después=%d", contexto, antes.historial, despues.historial)
	}
}

func TestOfertaPDF_DescargarNoSumaVisitasNiCambiaEstado(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool) // nace "Enviada al Cliente": la visita real la pasaría a "Vista"
	pases := NuevosPasesRender()           // registro compartido, igual que en main.go
	fixture.Handler.PasesRender = pases    // el handler público consume del mismo registro

	rec := postEnlace(t, fixture.Handler, fixture.CotizacionID, map[string]any{"version": 1}) // genera el enlace
	var generado struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &generado)

	antes := tomarFoto(t, pool, fixture.CotizacionID, generado.Token) // estado de partida
	if antes.estadoVersion != "Enviada al Cliente" {
		t.Fatalf("la fixture debía estar 'Enviada al Cliente', está %q", antes.estadoVersion)
	}

	doble := &convertidorQueAbreLaPagina{t: t, publico: fixture.Handler}                                      // Gotenberg simulado
	handler := &OfertaPDFHandler{DB: pool, Convertidor: doble, BaseInterna: "http://api:8080/", Pases: pases} // base con "/" final a propósito
	rec = getPDF(t, handler, actorAdminCompartido(t, pool), fixture.CotizacionID, "?version=1")

	if rec.Code != http.StatusOK { // el PDF se entregó
		t.Fatalf("PDF: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("Content-Type = %q, esperaba application/pdf", ct)
	}
	if _, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition")); err != nil || !strings.HasSuffix(params["filename"], "-V1.pdf") {
		t.Fatalf("Content-Disposition inválido: %q (%v)", rec.Header().Get("Content-Disposition"), err)
	}
	if !bytes.Equal(rec.Body.Bytes(), pdfFalso) {
		t.Fatal("el cuerpo no es el PDF que devolvió el convertidor")
	}
	if doble.codigoVisto != http.StatusOK {
		t.Fatalf("la página pública con pase respondió %d", doble.codigoVisto)
	}

	// La URL que vio Gotenberg: armada por el servidor, interna y sin imprimir=1.
	if len(doble.urlsVistas) != 1 {
		t.Fatalf("se esperaba 1 conversión, hubo %d", len(doble.urlsVistas))
	}
	u, _ := url.Parse(doble.urlsVistas[0])
	if u.Scheme+"://"+u.Host+u.Path != "http://api:8080/publico.html" {
		t.Fatalf("URL interna inesperada: %s", doble.urlsVistas[0])
	}
	if u.Query().Get("token") != generado.Token || u.Query().Get("render") == "" || u.Query().Has("imprimir") {
		t.Fatalf("query de la URL interna inesperada: %s", u.RawQuery)
	}

	despues := tomarFoto(t, pool, fixture.CotizacionID, generado.Token)
	afirmarSinEfectos(t, antes, despues, "tras descargar el PDF") // lo central de la prueba

	// El pase es de un solo uso: reutilizarlo da 404 y tampoco deja rastro.
	rec = getPublicoConRender(t, fixture.Handler, generado.Token, u.Query().Get("render"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pase reutilizado: esperaba 404, dio %d", rec.Code)
	}
	// Un pase inventado tampoco cae a "visita normal".
	rec = getPublicoConRender(t, fixture.Handler, generado.Token, "pase-inventado")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pase inventado: esperaba 404, dio %d", rec.Code)
	}
	afirmarSinEfectos(t, antes, tomarFoto(t, pool, fixture.CotizacionID, generado.Token), "tras pases inválidos")

	// Control: la visita REAL sí deja rastro; si esto no cambiara, la
	// prueba de arriba no estaría probando nada.
	if rec = getPublico(t, fixture.Handler, generado.Token); rec.Code != http.StatusOK {
		t.Fatalf("visita real: esperaba 200, dio %d", rec.Code)
	}
	real := tomarFoto(t, pool, fixture.CotizacionID, generado.Token)
	if real.visitas != antes.visitas+1 || real.estadoVersion != "Vista por el Cliente" || real.historial <= antes.historial {
		t.Fatalf("la visita real no dejó rastro: %+v", real)
	}
}

// Un pase emitido para un enlace no sirve para otro enlace.
func TestOfertaPDF_PaseDeOtroEnlaceNoSirve(t *testing.T) {
	pool := setupTestDB(t)
	fixtureA := crearFixtureEnlace(t, pool) // dos cotizaciones con enlace
	fixtureB := crearFixtureEnlace(t, pool)
	pases := NuevosPasesRender()
	fixtureA.Handler.PasesRender = pases
	tokenDe := func(f fixtureEnlace) string { // genera y devuelve el token del enlace
		rec := postEnlace(t, f.Handler, f.CotizacionID, map[string]any{"version": 1})
		var g struct {
			Token string `json:"token"`
		}
		assertJSON(t, rec.Body.Bytes(), &g)
		return g.Token
	}
	tokenA, tokenB := tokenDe(fixtureA), tokenDe(fixtureB)
	antesB := tomarFoto(t, pool, fixtureB.CotizacionID, tokenB)
	pase, err := pases.Emitir(tokenA) // pase legítimo... del enlace A
	if err != nil {
		t.Fatal(err)
	}
	if rec := getPublicoConRender(t, fixtureA.Handler, tokenB, pase); rec.Code != http.StatusNotFound { // usado sobre B
		t.Fatalf("pase de A sobre B: esperaba 404, dio %d", rec.Code)
	}
	afirmarSinEfectos(t, antesB, tomarFoto(t, pool, fixtureB.CotizacionID, tokenB), "pase cruzado")
}

func TestOfertaPDF_Permisos(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	vendedor := crearActorRol(t, pool, rolVendedor)     // alcance propio por función Vendedor
	otroVendedor := crearActorRol(t, pool, rolVendedor) // dueño de la ajena
	consultor := crearActorRol(t, pool, rolConsultor)   // alcance propio por función Analista
	soloConsulta := crearActorRol(t, pool, rolSolo)     // sin alcance propio: ve todas, igual que el detalle
	propia := cotizacionAsignada(t, pool, vendedor, "Vendedor", "Enviada al Cliente")
	ajena := cotizacionAsignada(t, pool, otroVendedor, "Vendedor", "Enviada al Cliente")
	sinEnlace := cotizacionAsignada(t, pool, vendedor, "Vendedor", "Borrador")

	// Enlaces insertados directo: estas pruebas son de permisos, no de GenerarEnlace.
	for _, id := range []string{propia, ajena} {
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version) VALUES ($1,$2,1)`, "tok-pdf-"+sufijoUnico(), id); err != nil {
			t.Fatalf("no se pudo crear el enlace de prueba: %v", err)
		}
	}

	// Doble mínimo: no abre la página, solo cuenta conversiones.
	conversiones := 0
	convertidor := convertidorFunc(func(context.Context, string) ([]byte, error) { conversiones++; return pdfFalso, nil })
	handler := &OfertaPDFHandler{DB: pool, Convertidor: convertidor, BaseInterna: "http://api:8080", Pases: NuevosPasesRender()}

	// Sin usuario en el contexto: 401 (en producción ni llega: RequiereSesion corta antes).
	if rec := getPDF(t, handler, "", propia, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin sesión: esperaba 401, dio %d: %s", rec.Code, rec.Body.String())
	}
	// alcance_propio: Vendedor sobre la de otro, Consultor sobre una donde no es Analista.
	afirmar403(t, getPDF(t, handler, vendedor, ajena, ""), "alcance_propio", "Vendedor descarga la de otro")
	afirmar403(t, getPDF(t, handler, consultor, propia, ""), "alcance_propio", "Consultor descarga una donde no es Analista")
	// Inexistente para un rol con alcance: mismo 403 (no delata si existe).
	afirmar403(t, getPDF(t, handler, vendedor, errIDInexistente, ""), "alcance_propio", "Vendedor pide una inexistente")
	if conversiones != 0 {
		t.Fatalf("se llamó a Gotenberg %d veces en casos rechazados", conversiones)
	}

	// Permitidos.
	afirmarNo403(t, getPDF(t, handler, vendedor, propia, ""), http.StatusOK, "Vendedor descarga la propia")
	afirmarNo403(t, getPDF(t, handler, soloConsulta, ajena, ""), http.StatusOK, "Solo Consulta descarga (ve todas, como en el detalle)")

	// Sin enlace generado para esa versión: 404 claro, sin convertir nada.
	antes := conversiones
	afirmarNo403(t, getPDF(t, handler, vendedor, sinEnlace, ""), http.StatusNotFound, "cotización sin enlace")
	// Cotización inexistente para un rol sin alcance propio: 404.
	afirmarNo403(t, getPDF(t, handler, actorAdminCompartido(t, pool), errIDInexistente, ""), http.StatusNotFound, "Administrador pide una inexistente")
	// Versión que no tiene enlace: 404.
	afirmarNo403(t, getPDF(t, handler, vendedor, propia, "?version=9"), http.StatusNotFound, "versión sin enlace")
	// Versión no numérica: 400.
	afirmarNo403(t, getPDF(t, handler, vendedor, propia, "?version=abc"), http.StatusBadRequest, "versión inválida")
	if conversiones != antes {
		t.Fatal("no se debía llamar a Gotenberg cuando no hay enlace")
	}

	// Servidor sin Gotenberg configurado: 503 explícito.
	sinGotenberg := &OfertaPDFHandler{DB: pool, Pases: NuevosPasesRender()}
	afirmarNo403(t, getPDF(t, sinGotenberg, vendedor, propia, ""), http.StatusServiceUnavailable, "sin Gotenberg")

	// Gotenberg falla: 502 con mensaje, nunca un PDF vacío.
	falla := &OfertaPDFHandler{DB: pool, BaseInterna: "http://api:8080", Pases: NuevosPasesRender(),
		Convertidor: convertidorFunc(func(context.Context, string) ([]byte, error) { return nil, errors.New("caído") })}
	afirmarNo403(t, getPDF(t, falla, vendedor, propia, ""), http.StatusBadGateway, "Gotenberg caído")
}

// convertidorFunc adapta una función a ConvertidorPDF.
type convertidorFunc func(context.Context, string) ([]byte, error)

// ConvertirURL llama a la función envuelta.
func (f convertidorFunc) ConvertirURL(ctx context.Context, u string) ([]byte, error) {
	return f(ctx, u)
}

// --- Pruebas unitarias (no necesitan Postgres) ---

func TestUnitPasesRender_UnSoloUsoYVencimiento(t *testing.T) {
	pases := NuevosPasesRender()     // registro vacío
	ahora := time.Unix(1_000_000, 0) // reloj fijo
	pases.ahora = func() time.Time { return ahora }

	pase, err := pases.Emitir("token-a")
	if err != nil {
		t.Fatal(err)
	}
	if pases.Consumir(pase, "token-b") { // token equivocado
		t.Fatal("un pase no debe servir para otro enlace")
	}
	if pases.Consumir(pase, "token-a") { // ya se quemó con el intento anterior
		t.Fatal("un pase presentado con el token equivocado debe quedar inutilizado")
	}

	pase, _ = pases.Emitir("token-a")
	if !pases.Consumir(pase, "token-a") { // uso legítimo
		t.Fatal("el pase válido debía aceptarse")
	}
	if pases.Consumir(pase, "token-a") { // segundo uso
		t.Fatal("el pase debe ser de un solo uso")
	}

	pase, _ = pases.Emitir("token-a")
	ahora = ahora.Add(vidaPaseRender + time.Second) // pasa el tiempo
	if pases.Consumir(pase, "token-a") {
		t.Fatal("un pase vencido no debe aceptarse")
	}
	var nulo *PasesRender
	if nulo.Consumir("x", "token-a") || pases.Consumir("", "token-a") { // sin registro o sin pase
		t.Fatal("sin registro o sin pase no se acepta nada")
	}
}

func TestUnitGotenberg_EnviaLosCamposDocumentados(t *testing.T) {
	var campos map[string]string // lo que recibió el Gotenberg simulado
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/forms/chromium/convert/url" { // ruta oficial
			http.Error(w, "ruta inesperada "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type")) // boundary del multipart
		lector := multipart.NewReader(r.Body, params["boundary"])
		campos = map[string]string{}
		for {
			parte, err := lector.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			valor, _ := io.ReadAll(parte)
			campos[parte.FormName()] = string(valor)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(pdfFalso)
	}))
	defer servidor.Close()

	pdf, err := GotenbergPDF{BaseURL: servidor.URL + "/"}.ConvertirURL(context.Background(), "http://api:8080/publico.html?token=t&render=r")
	if err != nil {
		t.Fatalf("conversión: %v", err)
	}
	if !bytes.Equal(pdf, pdfFalso) {
		t.Fatal("no devolvió los bytes del PDF")
	}
	esperados := map[string]string{
		"url":                  "http://api:8080/publico.html?token=t&render=r",
		"waitForExpression":    expresionPropuestaVisible,
		"emulatedMediaType":    "print",
		"printBackground":      "true",
		"preferCssPageSize":    "true",
		"skipNetworkIdleEvent": "false",
	}
	for campo, valor := range esperados {
		if campos[campo] != valor {
			t.Errorf("campo %s = %q, esperaba %q", campo, campos[campo], valor)
		}
	}
}

func TestUnitGotenberg_ErroresNoDevuelvenPDF(t *testing.T) {
	casos := map[string]http.HandlerFunc{
		"status 409": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Invalid HTTP status code", http.StatusConflict)
		},
		"no es PDF": func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>error</html>")) },
	}
	for nombre, responder := range casos {
		servidor := httptest.NewServer(responder)
		pdf, err := GotenbergPDF{BaseURL: servidor.URL}.ConvertirURL(context.Background(), "http://api:8080/publico.html")
		servidor.Close()
		if err == nil || pdf != nil {
			t.Errorf("%s: esperaba error y sin PDF; vino err=%v len=%d", nombre, err, len(pdf))
		}
	}
	if _, err := (GotenbergPDF{}).ConvertirURL(context.Background(), "http://x"); err == nil { // sin URL base
		t.Error("sin BaseURL debía fallar")
	}
}
