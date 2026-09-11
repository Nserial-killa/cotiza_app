package handlers

// Auditoría de QA — dimensión 6 (Error Handling).
//
// La afirmación transversal de este archivo es una sola: ninguna
// entrada malformada puede terminar en 500 ni en panic. Un 500 con
// entrada basura significa que el handler dejó que el dato llegara
// hasta Postgres (o hasta un scan) sin validarlo, y el usuario recibe
// "no fue posible..." en vez de saber qué mandó mal.
//
// Tres clases de entrada por endpoint con cuerpo JSON:
//   1. JSON sintácticamente inválido            → 400
//   2. campo con tipo incorrecto                → 400
//   3. campo desconocido (DisallowUnknownFields)→ 400
//
// Más los 404 por id inexistente y los 400 por parámetro de filtro
// inválido, que eran el otro hueco de esta dimensión.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

const (
	errCuerpoRoto        = `{`
	errCampoDesconocido  = `{"campo_que_no_existe": 1}`
	errIDInexistente     = "no-existe-auditoria-qa"
	errMensajeSin500     = "devolvió 500 con entrada inválida: el handler tiene que validar antes de tocar la base"
	errMensajeEnvoltorio = `la respuesta de error no respeta el envoltorio {"ok":false,"error":"..."} que el frontend heredado sabe leer`
)

// errPeticion arma y ejecuta una petición contra un handler. Si el
// patrón no está vacío monta un router chi, porque los handlers que
// leen chi.URLParam necesitan el contexto de ruta para resolver {id}.
// Los decoradores opcionales sirven para los handlers que esperan algo
// más en el contexto que el usuario de sesión (hoy solo el API externo,
// que espera el integracion_id que deja middleware.RequiereApiKey).
func errPeticion(t *testing.T, metodo, patron, ruta, cuerpo, actorID string, handler http.HandlerFunc, decoradores ...func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	var lector io.Reader
	if cuerpo != "" {
		lector = strings.NewReader(cuerpo)
	}
	req := httptest.NewRequest(metodo, ruta, lector)
	req.Header.Set("Content-Type", "application/json")
	if actorID != "" {
		req = conActor(req, actorID)
	}
	for _, decorar := range decoradores {
		if decorar != nil {
			req = decorar(req)
		}
	}
	rec := httptest.NewRecorder()
	if patron == "" {
		handler(rec, req)
		return rec
	}
	router := chi.NewRouter()
	router.Method(metodo, patron, handler)
	router.ServeHTTP(rec, req)
	return rec
}

// errAfirmarError exige el status 4xx esperado y el envoltorio de error
// completo. Trata el 500 como un fallo distinto y más grave, con su
// propio mensaje, porque es el que delata la falta de validación.
func errAfirmarError(t *testing.T, rec *httptest.ResponseRecorder, esperado int, contexto string) {
	t.Helper()
	if rec.Code == http.StatusInternalServerError {
		t.Errorf("%s: %s. Cuerpo: %s", contexto, errMensajeSin500, rec.Body.String())
		return
	}
	if rec.Code != esperado {
		t.Errorf("%s: se esperaba %d y devolvió %d. Cuerpo: %s", contexto, esperado, rec.Code, rec.Body.String())
		return
	}
	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	if ok, _ := res["ok"].(bool); ok {
		t.Errorf("%s: %s (vino ok:true en una respuesta de error)", contexto, errMensajeEnvoltorio)
	}
	if mensaje, _ := res["error"].(string); strings.TrimSpace(mensaje) == "" {
		t.Errorf("%s: %s (el campo error vino vacío, el usuario no se enteraría de qué mandó mal)", contexto, errMensajeEnvoltorio)
	}
}

func errAfirmarOK(t *testing.T, rec *httptest.ResponseRecorder, contexto string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Errorf("%s: la entrada era válida y devolvió %d. Cuerpo: %s", contexto, rec.Code, rec.Body.String())
	}
}

// errEndpointConCuerpo describe un endpoint que recibe JSON, con el
// body de "tipo incorrecto" propio de sus campos.
type errEndpointConCuerpo struct {
	nombre  string
	metodo  string
	patron  string
	ruta    string
	actorID string
	handler http.HandlerFunc
	// tipoIncorrecto es un JSON válido con un campo real y tipo equivocado.
	tipoIncorrecto string
	// sinCampoDesconocido marca los endpoints que NO usan decodificarJSON
	// (los de reordenamiento leen el cuerpo a mano), así que no pueden
	// rechazar un campo desconocido.
	sinCampoDesconocido bool
	// decorar agrega al contexto lo que el handler necesite además del
	// actor de sesión (ver errPeticion).
	decorar func(*http.Request) *http.Request
}

// errFixtureCotizacion crea calculadora + cliente + cotización + versión 1
// descartables, para las pruebas que necesitan un id que exista de verdad.
func errFixtureCotizacion(t *testing.T, pool *pgxpool.Pool) (cotizacionID, calculadoraID string) {
	t.Helper()
	sufijo := strings.ReplaceAll(sufijoUnico(), ".", "")
	calculadoraID = "TEST-ERR-CALC-" + sufijo
	clienteID := "TEST-ERR-CLI-" + sufijo
	cotizacionID = "TEST-ERR-COT-" + sufijo
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1, 'Errores QA', 'Activo')`, calculadoraID); err != nil {
		t.Fatalf("no se pudo crear la calculadora de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO clientes (cliente_id, nombre_comercial, razon_social, estado) VALUES ($1, 'Cliente Errores QA', 'Cliente Errores QA S.A.', 'Activo')`, clienteID); err != nil {
		t.Fatalf("no se pudo crear el cliente de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cotizaciones (cotizacion_id, calculadora_id, cliente_id, codigo_oferta, estado, version_actual)
		VALUES ($1, $2, $3, $4, 'Borrador', 1)`,
		cotizacionID, calculadoraID, clienteID, "OF-ERR-"+sufijo); err != nil {
		t.Fatalf("no se pudo crear la cotización de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, nombre_version, estado, moneda, total_precio)
		VALUES ($1, 1, 'Versión inicial', 'Borrador', 'US$', 0)`, cotizacionID); err != nil {
		t.Fatalf("no se pudo crear la versión de prueba: %v", err)
	}

	t.Cleanup(func() {
		limpieza := context.Background()
		pool.Exec(limpieza, `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID)
		pool.Exec(limpieza, `DELETE FROM clientes WHERE cliente_id = $1`, clienteID)
		pool.Exec(limpieza, `DELETE FROM calculadoras WHERE calculadora_id = $1`, calculadoraID)
	})
	return cotizacionID, calculadoraID
}

// ---------------------------------------------------------------------
// BUG-1 y BUG-2: parámetros de query que entraban al SQL sin validar.
// Estas dos pruebas fijan el arreglo; si alguien vuelve a pasar un
// parámetro crudo a un cast de Postgres, vuelve el 500 y fallan.
// ---------------------------------------------------------------------

func TestErroresCotizaciones_VersionInvalidaEnDetalleDa400(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _ := errFixtureCotizacion(t, pool)

	// La versión entra al SQL como $2::int. Antes del arreglo, Postgres
	// rechazaba el valor y el handler respondía 500 genérico.
	for _, version := range []string{"abc", "0", "-1", "3.5", "1e3", "null"} {
		t.Run("version="+version, func(t *testing.T) {
			rec := errPeticion(t, http.MethodGet, "/api/cotizaciones/{id}",
				"/api/cotizaciones/"+cotizacionID+"?version="+version, "", actor, handler.Detalle)
			errAfirmarError(t, rec, http.StatusBadRequest, "GET /api/cotizaciones/{id}?version="+version)
		})
	}

	t.Run("version vacía sirve la versión actual", func(t *testing.T) {
		rec := errPeticion(t, http.MethodGet, "/api/cotizaciones/{id}",
			"/api/cotizaciones/"+cotizacionID+"?version=", "", actor, handler.Detalle)
		errAfirmarOK(t, rec, "GET /api/cotizaciones/{id}?version= (vacía)")
	})

	t.Run("version válida responde 200", func(t *testing.T) {
		rec := errPeticion(t, http.MethodGet, "/api/cotizaciones/{id}",
			"/api/cotizaciones/"+cotizacionID+"?version=1", "", actor, handler.Detalle)
		errAfirmarOK(t, rec, "GET /api/cotizaciones/{id}?version=1")
	})
}

func TestErroresCotizaciones_FechaDesdeInvalidaEnListarDa400(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}

	// fecha_desde entraba como $5::date sin validación previa.
	for _, fecha := range []string{"basura", "10/09/2026", "2026-13-45", "ayer"} {
		t.Run("fecha_desde="+fecha, func(t *testing.T) {
			rec := errPeticion(t, http.MethodGet, "", "/api/cotizaciones?fecha_desde="+fecha, "", "", handler.Listar)
			errAfirmarError(t, rec, http.StatusBadRequest, "GET /api/cotizaciones?fecha_desde="+fecha)
		})
	}

	t.Run("fecha válida responde 200", func(t *testing.T) {
		rec := errPeticion(t, http.MethodGet, "", "/api/cotizaciones?fecha_desde=2026-09-10", "", "", handler.Listar)
		errAfirmarOK(t, rec, "GET /api/cotizaciones?fecha_desde=2026-09-10")
	})
}

// ---------------------------------------------------------------------
// Batería general sobre todos los endpoints con cuerpo JSON.
// ---------------------------------------------------------------------

// errEndpointsConCuerpo arma la tabla completa. Está en una función
// aparte porque necesita el pool y los actores, y la comparten las tres
// pruebas de clase de entrada.
func errEndpointsConCuerpo(t *testing.T, pool *pgxpool.Pool) []errEndpointConCuerpo {
	t.Helper()
	admin := crearAdminActorPrueba(t, pool)
	integracionID, _ := crearIntegracionPruebaExterna(t, pool, "Activo")

	usuarios := &UsuariosHandler{DB: pool}
	clientes := &ClientesHandler{DB: pool}
	integraciones := &IntegracionesHandler{DB: pool}
	solicitudes := &SolicitudesHandler{DB: pool, Cotizaciones: &CotizacionesHandler{DB: pool}}
	externas := &SolicitudesExternasHandler{DB: pool}
	catalogos := &CatalogosHandler{DB: pool}
	tabs := &CotizadorTabsHandler{DB: pool}
	compilador := &CompiladorHandler{DB: pool}
	cotizaciones := &CotizacionesHandler{DB: pool}
	runtime := &CotizadorRuntimeHandler{DB: pool}
	plantillas := &PlantillasHandler{DB: pool}
	estructura := &PlantillaEstructuraHandler{DB: pool}
	estilo := &PlantillaEstiloHandler{DB: pool}
	vinculaciones := &PlantillaVinculacionesHandler{DB: pool}
	enlaces := &EnlacesPublicosHandler{DB: pool}

	return []errEndpointConCuerpo{
		{
			nombre: "POST /api/usuarios", metodo: http.MethodPost, ruta: "/api/usuarios",
			actorID: admin, handler: usuarios.Crear, tipoIncorrecto: `{"nombre": 123}`,
		},
		{
			nombre: "PATCH /api/usuarios/{id}", metodo: http.MethodPatch, patron: "/api/usuarios/{id}",
			ruta: "/api/usuarios/" + errIDInexistente, actorID: admin, handler: usuarios.Editar,
			tipoIncorrecto: `{"nombre": 123}`,
		},
		{
			nombre: "POST /api/clientes", metodo: http.MethodPost, ruta: "/api/clientes",
			actorID: admin, handler: clientes.Crear, tipoIncorrecto: `{"nombre_comercial": 123}`,
		},
		{
			nombre: "PATCH /api/clientes/{id}", metodo: http.MethodPatch, patron: "/api/clientes/{id}",
			ruta: "/api/clientes/" + errIDInexistente, actorID: admin, handler: clientes.Editar,
			tipoIncorrecto: `{"nombre_comercial": 123}`,
		},
		{
			nombre: "POST /api/integraciones", metodo: http.MethodPost, ruta: "/api/integraciones",
			actorID: admin, handler: integraciones.Crear, tipoIncorrecto: `{"nombre": 123}`,
		},
		{
			nombre: "PATCH /api/integraciones/{id}", metodo: http.MethodPatch, patron: "/api/integraciones/{id}",
			ruta: "/api/integraciones/" + errIDInexistente, actorID: admin, handler: integraciones.Editar,
			tipoIncorrecto: `{"estado": 123}`,
		},
		{
			nombre: "POST /api/solicitudes", metodo: http.MethodPost, ruta: "/api/solicitudes",
			actorID: admin, handler: solicitudes.Crear, tipoIncorrecto: `{"titulo": 123}`,
		},
		{
			nombre: "PATCH /api/solicitudes/{id}", metodo: http.MethodPatch, patron: "/api/solicitudes/{id}",
			ruta: "/api/solicitudes/" + errIDInexistente, actorID: admin, handler: solicitudes.CambiarEstado,
			tipoIncorrecto: `{"estado": 123}`,
		},
		{
			nombre: "POST /api/solicitudes/{id}/convertir", metodo: http.MethodPost,
			patron: "/api/solicitudes/{id}/convertir", ruta: "/api/solicitudes/" + errIDInexistente + "/convertir",
			actorID: admin, handler: solicitudes.Convertir, tipoIncorrecto: `{"calculadora_id": 123}`,
		},
		{
			// El API externo se autentica con X-Api-Key: sin el
			// integracion_id que deja RequiereApiKey en el contexto, el
			// handler corta antes de mirar el cuerpo (y con razón: es una
			// falla de configuración, no de entrada). Para probar la
			// validación de entrada hay que simular esa parte.
			nombre: "POST /api/externo/solicitudes", metodo: http.MethodPost, ruta: "/api/externo/solicitudes",
			handler: externas.Crear, tipoIncorrecto: `{"cliente_nombre": 123}`,
			decorar: func(req *http.Request) *http.Request {
				return req.WithContext(context.WithValue(req.Context(), middleware.IntegracionIDKey, integracionID))
			},
		},
		{
			nombre: "POST /api/catalogos", metodo: http.MethodPost, ruta: "/api/catalogos",
			handler: catalogos.GuardarCatalogo, tipoIncorrecto: `{"orden": "abc"}`,
		},
		{
			nombre: "POST /api/catalogos/valores", metodo: http.MethodPost, ruta: "/api/catalogos/valores",
			handler: catalogos.GuardarValor, tipoIncorrecto: `{"texto_visible": 123}`,
		},
		{
			nombre: "POST /api/catalogos/relaciones", metodo: http.MethodPost, ruta: "/api/catalogos/relaciones",
			handler: catalogos.GuardarRelaciones, tipoIncorrecto: `{"_valor_padre_ids": "no-es-una-lista"}`,
		},
		{
			nombre: "POST /api/cotizador/tabs", metodo: http.MethodPost, ruta: "/api/cotizador/tabs",
			handler: tabs.GuardarTab, tipoIncorrecto: `{"orden": "abc"}`,
		},
		{
			nombre: "POST /api/cotizador/elementos", metodo: http.MethodPost, ruta: "/api/cotizador/elementos",
			handler: tabs.GuardarElemento, tipoIncorrecto: `{"columnas_ancho": "abc"}`,
		},
		{
			nombre: "POST /api/cotizador/validar", metodo: http.MethodPost, ruta: "/api/cotizador/validar",
			handler: compilador.Validar, tipoIncorrecto: `{"calculadora_id": 123}`,
		},
		{
			nombre: "POST /api/cotizador/compilar", metodo: http.MethodPost, ruta: "/api/cotizador/compilar",
			handler: compilador.Compilar, tipoIncorrecto: `{"calculadora_id": 123}`,
		},
		{
			nombre: "POST /api/cotizaciones", metodo: http.MethodPost, ruta: "/api/cotizaciones",
			actorID: admin, handler: cotizaciones.Crear, tipoIncorrecto: `{"cliente_nombre_nuevo": 123}`,
		},
		{
			nombre: "POST /api/cotizaciones/{id}/version", metodo: http.MethodPost,
			patron: "/api/cotizaciones/{id}/version", ruta: "/api/cotizaciones/" + errIDInexistente + "/version",
			actorID: admin, handler: cotizaciones.CrearVersion, tipoIncorrecto: `{"nombre_version": 123}`,
		},
		{
			nombre: "POST /api/cotizaciones/{id}/estado", metodo: http.MethodPost,
			patron: "/api/cotizaciones/{id}/estado", ruta: "/api/cotizaciones/" + errIDInexistente + "/estado",
			actorID: admin, handler: cotizaciones.CambiarEstado, tipoIncorrecto: `{"version": "abc"}`,
		},
		{
			nombre: "POST /api/cotizador/runtime/{cotizacion_id}/valores", metodo: http.MethodPost,
			patron:  "/api/cotizador/runtime/{cotizacion_id}/valores",
			ruta:    "/api/cotizador/runtime/" + errIDInexistente + "/valores",
			actorID: admin, handler: runtime.GuardarValores, tipoIncorrecto: `{"version": "abc"}`,
		},
		{
			nombre: "POST /api/plantillas", metodo: http.MethodPost, ruta: "/api/plantillas",
			handler: plantillas.Crear, tipoIncorrecto: `{"calculadora_ids": "no-es-una-lista"}`,
		},
		{
			nombre: "PATCH /api/plantillas/{id}", metodo: http.MethodPatch, patron: "/api/plantillas/{id}",
			ruta: "/api/plantillas/" + errIDInexistente, handler: plantillas.Editar,
			tipoIncorrecto: `{"nombre": 123}`,
		},
		{
			nombre: "POST /api/plantillas/{id}/secciones", metodo: http.MethodPost,
			patron: "/api/plantillas/{id}/secciones", ruta: "/api/plantillas/" + errIDInexistente + "/secciones",
			handler: estructura.CrearSeccion, tipoIncorrecto: `{"nombre": 123}`,
		},
		{
			nombre: "PATCH /api/plantillas/secciones/{seccion_id}", metodo: http.MethodPatch,
			patron: "/api/plantillas/secciones/{seccion_id}", ruta: "/api/plantillas/secciones/" + errIDInexistente,
			handler: estructura.EditarSeccion, tipoIncorrecto: `{"titulo": 123}`,
		},
		{
			nombre: "POST /api/plantillas/secciones/{seccion_id}/bloques", metodo: http.MethodPost,
			patron:  "/api/plantillas/secciones/{seccion_id}/bloques",
			ruta:    "/api/plantillas/secciones/" + errIDInexistente + "/bloques",
			handler: estructura.CrearBloque, tipoIncorrecto: `{"tipo_bloque": 123}`,
		},
		{
			nombre: "PATCH /api/plantillas/bloques/{bloque_id}", metodo: http.MethodPatch,
			patron: "/api/plantillas/bloques/{bloque_id}", ruta: "/api/plantillas/bloques/" + errIDInexistente,
			handler: estructura.EditarBloque, tipoIncorrecto: `{"titulo": 123}`,
		},
		{
			nombre: "POST /api/plantillas/secciones/{seccion_id}/orden", metodo: http.MethodPost,
			patron:  "/api/plantillas/secciones/{seccion_id}/orden",
			ruta:    "/api/plantillas/secciones/" + errIDInexistente + "/orden",
			handler: estructura.OrdenarSecciones, tipoIncorrecto: `{"secciones": "no-es-una-lista"}`,
			sinCampoDesconocido: true,
		},
		{
			nombre: "POST /api/plantillas/bloques/{bloque_id}/orden", metodo: http.MethodPost,
			patron:  "/api/plantillas/bloques/{bloque_id}/orden",
			ruta:    "/api/plantillas/bloques/" + errIDInexistente + "/orden",
			handler: estructura.OrdenarBloques, tipoIncorrecto: `{"bloques": "no-es-una-lista"}`,
			sinCampoDesconocido: true,
		},
		{
			nombre: "PATCH /api/plantillas/{id}/estilo", metodo: http.MethodPatch,
			patron: "/api/plantillas/{id}/estilo", ruta: "/api/plantillas/" + errIDInexistente + "/estilo",
			handler: estilo.Actualizar, tipoIncorrecto: `{"tema": 123}`,
		},
		{
			nombre: "POST /api/plantillas/bloques/{bloque_id}/vinculacion", metodo: http.MethodPost,
			patron:  "/api/plantillas/bloques/{bloque_id}/vinculacion",
			ruta:    "/api/plantillas/bloques/" + errIDInexistente + "/vinculacion",
			handler: vinculaciones.Guardar, tipoIncorrecto: `{"fuente_tipo": 123}`,
		},
		{
			nombre: "POST /api/cotizaciones/{id}/enlace", metodo: http.MethodPost,
			patron: "/api/cotizaciones/{id}/enlace", ruta: "/api/cotizaciones/" + errIDInexistente + "/enlace",
			actorID: admin, handler: enlaces.GenerarEnlace, tipoIncorrecto: `{"version": "abc"}`,
		},
	}
}

func TestErroresEntrada_CuerpoJSONInvalidoDa400(t *testing.T) {
	pool := setupTestDB(t)
	for _, caso := range errEndpointsConCuerpo(t, pool) {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := errPeticion(t, caso.metodo, caso.patron, caso.ruta, errCuerpoRoto, caso.actorID, caso.handler, caso.decorar)
			errAfirmarError(t, rec, http.StatusBadRequest, caso.nombre+" con JSON roto")
		})
	}
}

func TestErroresEntrada_TipoIncorrectoDa400(t *testing.T) {
	pool := setupTestDB(t)
	for _, caso := range errEndpointsConCuerpo(t, pool) {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := errPeticion(t, caso.metodo, caso.patron, caso.ruta, caso.tipoIncorrecto, caso.actorID, caso.handler, caso.decorar)
			errAfirmarError(t, rec, http.StatusBadRequest, caso.nombre+" con "+caso.tipoIncorrecto)
		})
	}
}

func TestErroresEntrada_CampoDesconocidoDa400(t *testing.T) {
	pool := setupTestDB(t)
	for _, caso := range errEndpointsConCuerpo(t, pool) {
		if caso.sinCampoDesconocido {
			continue
		}
		t.Run(caso.nombre, func(t *testing.T) {
			rec := errPeticion(t, caso.metodo, caso.patron, caso.ruta, errCampoDesconocido, caso.actorID, caso.handler, caso.decorar)
			errAfirmarError(t, rec, http.StatusBadRequest, caso.nombre+" con un campo que no existe en el struct")
		})
	}
}

// ---------------------------------------------------------------------
// 404 por id inexistente.
// ---------------------------------------------------------------------

func TestErroresRecursoInexistente_Da404(t *testing.T) {
	pool := setupTestDB(t)
	admin := crearAdminActorPrueba(t, pool)

	catalogos := &CatalogosHandler{DB: pool}
	tabs := &CotizadorTabsHandler{DB: pool}
	integraciones := &IntegracionesHandler{DB: pool}
	plantillas := &PlantillasHandler{DB: pool}
	estructura := &PlantillaEstructuraHandler{DB: pool}
	estilo := &PlantillaEstiloHandler{DB: pool}
	vinculaciones := &PlantillaVinculacionesHandler{DB: pool}
	solicitudes := &SolicitudesHandler{DB: pool, Cotizaciones: &CotizacionesHandler{DB: pool}}
	runtime := &CotizadorRuntimeHandler{DB: pool}
	enlaces := &EnlacesPublicosHandler{DB: pool}
	cotizaciones := &CotizacionesHandler{DB: pool}

	casos := []struct {
		nombre  string
		metodo  string
		patron  string
		ruta    string
		cuerpo  string
		actorID string
		handler http.HandlerFunc
	}{
		{"DELETE /api/catalogos/{id}", http.MethodDelete, "/api/catalogos/{id}", "/api/catalogos/" + errIDInexistente, "", "", catalogos.EliminarCatalogo},
		{"DELETE /api/catalogos/valores/{id}", http.MethodDelete, "/api/catalogos/valores/{id}", "/api/catalogos/valores/" + errIDInexistente, "", "", catalogos.EliminarValor},
		{"DELETE /api/catalogos/relaciones/{id}", http.MethodDelete, "/api/catalogos/relaciones/{id}", "/api/catalogos/relaciones/" + errIDInexistente, "", "", catalogos.EliminarRelacion},
		{"DELETE /api/cotizador/tabs/{id}", http.MethodDelete, "/api/cotizador/tabs/{id}", "/api/cotizador/tabs/" + errIDInexistente, "", "", tabs.EliminarTab},
		{"DELETE /api/cotizador/elementos/{id}", http.MethodDelete, "/api/cotizador/elementos/{id}", "/api/cotizador/elementos/" + errIDInexistente, "", "", tabs.EliminarElemento},
		{"GET /api/plantillas/{id}", http.MethodGet, "/api/plantillas/{id}", "/api/plantillas/" + errIDInexistente, "", "", plantillas.Detalle},
		{"PATCH /api/plantillas/{id}", http.MethodPatch, "/api/plantillas/{id}", "/api/plantillas/" + errIDInexistente, `{"nombre":"Auditoría QA"}`, "", plantillas.Editar},
		{"POST /api/plantillas/{id}/publicar", http.MethodPost, "/api/plantillas/{id}/publicar", "/api/plantillas/" + errIDInexistente + "/publicar", "", "", plantillas.Publicar},
		{"DELETE /api/plantillas/{id}", http.MethodDelete, "/api/plantillas/{id}", "/api/plantillas/" + errIDInexistente, "", "", plantillas.Eliminar},
		{"GET /api/plantillas/{id}/fuentes", http.MethodGet, "/api/plantillas/{id}/fuentes", "/api/plantillas/" + errIDInexistente + "/fuentes?calculadora_id=TEST-QA", "", "", vinculaciones.Fuentes},
		{"POST /api/plantillas/{id}/secciones", http.MethodPost, "/api/plantillas/{id}/secciones", "/api/plantillas/" + errIDInexistente + "/secciones", `{"nombre":"Sección QA"}`, "", estructura.CrearSeccion},
		{"PATCH /api/plantillas/secciones/{seccion_id}", http.MethodPatch, "/api/plantillas/secciones/{seccion_id}", "/api/plantillas/secciones/" + errIDInexistente, `{"titulo":"QA"}`, "", estructura.EditarSeccion},
		{"DELETE /api/plantillas/secciones/{seccion_id}", http.MethodDelete, "/api/plantillas/secciones/{seccion_id}", "/api/plantillas/secciones/" + errIDInexistente, "", "", estructura.EliminarSeccion},
		{"POST /api/plantillas/secciones/{seccion_id}/bloques", http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques", "/api/plantillas/secciones/" + errIDInexistente + "/bloques", `{"tipo_bloque":"TEXTO","nombre_interno":"bloque-qa"}`, "", estructura.CrearBloque},
		{"PATCH /api/plantillas/bloques/{bloque_id}", http.MethodPatch, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/" + errIDInexistente, `{"titulo":"QA"}`, "", estructura.EditarBloque},
		{"DELETE /api/plantillas/bloques/{bloque_id}", http.MethodDelete, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/" + errIDInexistente, "", "", estructura.EliminarBloque},
		{"PATCH /api/plantillas/{id}/estilo", http.MethodPatch, "/api/plantillas/{id}/estilo", "/api/plantillas/" + errIDInexistente + "/estilo", `{"tema":"MODERNO"}`, "", estilo.Actualizar},
		{"DELETE /api/plantillas/bloques/{bloque_id}/vinculacion", http.MethodDelete, "/api/plantillas/bloques/{bloque_id}/vinculacion", "/api/plantillas/bloques/" + errIDInexistente + "/vinculacion?calculadora_id=TEST-QA", "", "", vinculaciones.Eliminar},
		// El 404 de integraciones solo es alcanzable con estado=Inactivo:
		// cualquier otro estado se rechaza con 400 antes de tocar la tabla
		// (revocar es la única edición permitida).
		{"PATCH /api/integraciones/{id}", http.MethodPatch, "/api/integraciones/{id}", "/api/integraciones/" + errIDInexistente, `{"estado":"Inactivo"}`, admin, integraciones.Editar},
		{"GET /api/solicitudes/{id}", http.MethodGet, "/api/solicitudes/{id}", "/api/solicitudes/" + errIDInexistente, "", "", solicitudes.Detalle},
		{"PATCH /api/solicitudes/{id}", http.MethodPatch, "/api/solicitudes/{id}", "/api/solicitudes/" + errIDInexistente, `{"estado":"En revisión"}`, "", solicitudes.CambiarEstado},
		{"POST /api/solicitudes/{id}/convertir", http.MethodPost, "/api/solicitudes/{id}/convertir", "/api/solicitudes/" + errIDInexistente + "/convertir", "", admin, solicitudes.Convertir},
		{"GET /api/cotizador/runtime/{cotizacion_id}", http.MethodGet, "/api/cotizador/runtime/{cotizacion_id}", "/api/cotizador/runtime/" + errIDInexistente, "", admin, runtime.Obtener},
		{"POST /api/cotizaciones/{id}/enlace", http.MethodPost, "/api/cotizaciones/{id}/enlace", "/api/cotizaciones/" + errIDInexistente + "/enlace", "", admin, enlaces.GenerarEnlace},
		{"GET /api/cotizaciones/{id}", http.MethodGet, "/api/cotizaciones/{id}", "/api/cotizaciones/" + errIDInexistente, "", admin, cotizaciones.Detalle},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := errPeticion(t, caso.metodo, caso.patron, caso.ruta, caso.cuerpo, caso.actorID, caso.handler)
			errAfirmarError(t, rec, http.StatusNotFound, caso.nombre+" con un id inexistente")
		})
	}
}

// TestErroresEnlacePublico_VersionInexistenteDa404 va aparte porque
// necesita una cotización real: el 404 tiene que venir de la versión,
// no de la cotización.
func TestErroresEnlacePublico_VersionInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	admin := crearAdminActorPrueba(t, pool)
	cotizacionID, _ := errFixtureCotizacion(t, pool)
	enlaces := &EnlacesPublicosHandler{DB: pool}

	rec := errPeticion(t, http.MethodPost, "/api/cotizaciones/{id}/enlace",
		"/api/cotizaciones/"+cotizacionID+"/enlace", `{"version": 99}`, admin, enlaces.GenerarEnlace)
	errAfirmarError(t, rec, http.StatusNotFound, "POST /api/cotizaciones/{id}/enlace con una versión que no existe")
}

// ---------------------------------------------------------------------
// 400 por parámetro de filtro inválido.
// ---------------------------------------------------------------------

func TestErroresFiltros_ParametrosInvalidosDan400(t *testing.T) {
	pool := setupTestDB(t)
	admin := crearAdminActorPrueba(t, pool)

	dashboard := &DashboardHandler{DB: pool}
	plantillas := &PlantillasHandler{DB: pool}
	reportes := &ReportesHandler{DB: pool}
	tabs := &CotizadorTabsHandler{DB: pool}
	clientes := &ClientesHandler{DB: pool}
	solicitudes := &SolicitudesHandler{DB: pool, Cotizaciones: &CotizacionesHandler{DB: pool}}

	casos := []struct {
		nombre  string
		ruta    string
		actorID string
		handler http.HandlerFunc
	}{
		{"dashboard con tipo_cliente inválido", "/api/dashboard?tipo_cliente=basura", admin, dashboard.Obtener},
		{"plantillas con estado inválido", "/api/plantillas?estado=basura", "", plantillas.Listar},
		{"reportes con fecha_desde posterior a fecha_hasta", "/api/reportes/cotizaciones?fecha_desde=2026-09-10&fecha_hasta=2026-01-01", admin, reportes.Listar},
		{"reportes con fecha_desde inválida", "/api/reportes/cotizaciones?fecha_desde=basura", admin, reportes.Listar},
		{"reportes con estado inválido", "/api/reportes/cotizaciones?estado=basura", admin, reportes.Listar},
		{"exportar CSV con fecha inválida", "/api/reportes/cotizaciones/exportar?fecha_hasta=basura", admin, reportes.Exportar},
		{"tabs sin calculadora_id", "/api/cotizador/tabs", "", tabs.ListarTabs},
		{"elementos sin tab_id", "/api/cotizador/elementos", "", tabs.ListarElementos},
		{"clientes/gestion con estado inválido", "/api/clientes/gestion?estado=basura", "", clientes.Listar},
		{"solicitudes con estado inválido", "/api/solicitudes?estado=basura", "", solicitudes.Listar},
		{"solicitudes con prioridad inválida", "/api/solicitudes?prioridad=basura", "", solicitudes.Listar},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := errPeticion(t, http.MethodGet, "", caso.ruta, "", caso.actorID, caso.handler)
			errAfirmarError(t, rec, http.StatusBadRequest, caso.nombre)
		})
	}
}

// TestErroresSinSesion_HandlersQueExigenActorDan401 cubre el otro
// borde de entrada: el handler se invoca sin usuario en el contexto
// (lo que pasaría si alguien lo montara fuera del grupo protegido de
// main.go). Los que leen el actor de forma estricta responden 401.
func TestErroresSinSesion_HandlersQueExigenActorDan401(t *testing.T) {
	pool := setupTestDB(t)

	clientes := &ClientesHandler{DB: pool}
	solicitudes := &SolicitudesHandler{DB: pool, Cotizaciones: &CotizacionesHandler{DB: pool}}
	cotizaciones := &CotizacionesHandler{DB: pool}
	dashboard := &DashboardHandler{DB: pool}

	casos := []struct {
		nombre  string
		metodo  string
		patron  string
		ruta    string
		cuerpo  string
		handler http.HandlerFunc
	}{
		{"POST /api/clientes", http.MethodPost, "", "/api/clientes", `{"nombre_comercial":"QA"}`, clientes.Crear},
		{"POST /api/solicitudes", http.MethodPost, "", "/api/solicitudes", `{"titulo":"QA"}`, solicitudes.Crear},
		{"POST /api/solicitudes/{id}/convertir", http.MethodPost, "/api/solicitudes/{id}/convertir", "/api/solicitudes/" + errIDInexistente + "/convertir", "", solicitudes.Convertir},
		{"POST /api/cotizaciones", http.MethodPost, "", "/api/cotizaciones", `{"cliente_nombre_nuevo":"QA","calculadora_id":"TEST-QA"}`, cotizaciones.Crear},
		{"GET /api/dashboard", http.MethodGet, "", "/api/dashboard", "", dashboard.Obtener},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := errPeticion(t, caso.metodo, caso.patron, caso.ruta, caso.cuerpo, "", caso.handler)
			errAfirmarError(t, rec, http.StatusUnauthorized, caso.nombre+" sin usuario en el contexto")
		})
	}
}
