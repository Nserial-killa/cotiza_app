package handlers

// Auditoría de QA — dimensión 5 (Business Rules).
//
// Este archivo cubre reglas del DOMINIO que el resto de los _test.go
// no probaba: no repite forma de respuesta ni validaciones de campos
// (eso ya está en los tests por handler), sino las decisiones de
// negocio que se rompen en silencio si alguien toca el SQL o el orden
// de los pasos. Cada test nombra la regla en su comentario y en el
// mensaje de error, para que un fallo se lea como "se rompió esta
// regla" y no como "un número no coincidió".
//
// Convención de este archivo: helpers propios con prefijo "rn" para no
// chocar con los helpers de los demás archivos del paquete.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------
// Helpers propios
// ---------------------------------------------------------------

// rnCalculadoraConEstado crea un cotizador descartable en el estado
// pedido (para probar qué estados son publicables y cuáles no).
func rnCalculadoraConEstado(t *testing.T, pool *pgxpool.Pool, estado string) string {
	t.Helper()
	id := "TEST-RN-CALC-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1,'Cotizador reglas',$2)`,
		id, estado); err != nil {
		t.Fatalf("no se pudo crear el cotizador de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id)
	})
	return id
}

// rnClienteConEstado crea un cliente descartable en el estado pedido.
func rnClienteConEstado(t *testing.T, pool *pgxpool.Pool, estado string) string {
	t.Helper()
	id := "TEST-RN-CLI-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO clientes (cliente_id, nombre_comercial, estado) VALUES ($1,'Cliente reglas',$2)`,
		id, estado); err != nil {
		t.Fatalf("no se pudo crear el cliente de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=$1`, id)
	})
	return id
}

// rnLimpiarCotizacionYCliente borra la cotización creada por un
// endpoint y, si nació con cliente nuevo, también ese cliente.
func rnLimpiarCotizacionYCliente(t *testing.T, pool *pgxpool.Pool, cotizacionID string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		var clienteID *string
		pool.QueryRow(ctx, `SELECT cliente_id FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&clienteID)
		pool.Exec(ctx, `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID)
		if clienteID != nil {
			pool.Exec(ctx, `DELETE FROM clientes WHERE cliente_id=$1`, *clienteID)
		}
	})
}

// rnClienteDeCotizacion devuelve nombre comercial y razón social del
// cliente asociado a una cotización.
func rnClienteDeCotizacion(t *testing.T, pool *pgxpool.Pool, cotizacionID string) (nombreComercial string, razonSocial *string) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT cl.nombre_comercial, cl.razon_social
		  FROM cotizaciones c JOIN clientes cl ON cl.cliente_id = c.cliente_id
		 WHERE c.cotizacion_id = $1`, cotizacionID).Scan(&nombreComercial, &razonSocial)
	if err != nil {
		t.Fatalf("no se pudo leer el cliente de la cotización %s: %v", cotizacionID, err)
	}
	return nombreComercial, razonSocial
}

// rnGetRuntime pide el runtime de una cotización arbitraria (el
// getRuntime de cotizador_runtime_test.go está atado a su fixture).
func rnGetRuntime(t *testing.T, handler *CotizadorRuntimeHandler, cotizacionID, query string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/api/cotizador/runtime/{cotizacion_id}", handler.Obtener)
	ruta := "/api/cotizador/runtime/" + cotizacionID
	if query != "" {
		ruta += "?" + query
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))
	return rec
}

// rnCrearVersionExtra agrega una versión más a una cotización
// existente usando el endpoint real, para que version_actual y el
// historial queden como en producción.
func rnCrearVersionExtra(t *testing.T, handler *CotizacionesHandler, cotizacionID string) int {
	t.Helper()
	rec := postCotizacionSubruta(t, handler.CrearVersion, "/api/cotizaciones/{id}/version", cotizacionID, map[string]any{
		"nombre_version": "Versión extra " + sufijoUnico(),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("no se pudo crear la versión extra: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Version int `json:"version"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	return res.Version
}

// ---------------------------------------------------------------
// Cotizaciones
// ---------------------------------------------------------------

// Regla: al crear una cotización con cliente nuevo, la razón social es
// un dato APARTE del nombre comercial. Antes se guardaba el mismo
// texto en las dos columnas, y el detalle mostraba "Cliente" y
// "Empresa" idénticos.
func TestReglaCotizaciones_RazonSocialSeparadaDelNombreComercial(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)

	nombre := "Comercial " + sufijoUnico()
	razon := "Razón Social Distinta S.A. " + sufijoUnico()

	rec, res := postCrearCotizacion(t, handler, actorID, map[string]any{
		"cliente_nombre_nuevo":       nombre,
		"cliente_razon_social_nueva": razon,
		"calculadora_id":             calculadoraID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("esperaba 201 al crear con razón social propia, dio %d: %s", rec.Code, rec.Body.String())
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	rnLimpiarCotizacionYCliente(t, pool, cotizacionID)

	nombreGuardado, razonGuardada := rnClienteDeCotizacion(t, pool, cotizacionID)
	if nombreGuardado != nombre {
		t.Errorf("se rompió la regla: nombre_comercial debía guardarse tal cual (%q), quedó %q", nombre, nombreGuardado)
	}
	if razonGuardada == nil || *razonGuardada != razon {
		t.Errorf("se rompió la regla: la razón social enviada debía guardarse aparte (%q), quedó %v", razon, razonGuardada)
	}
	if razonGuardada != nil && *razonGuardada == nombreGuardado {
		t.Error("se rompió la regla: razón social y nombre comercial quedaron iguales, que es justo el bug que esta feature vino a arreglar")
	}
}

// Regla (compatibilidad): si NO se manda razón social, se sigue
// copiando el nombre comercial, como antes de la feature.
func TestReglaCotizaciones_SinRazonSocialCaeAlNombreComercial(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)
	nombre := "Solo comercial " + sufijoUnico()

	rec, res := postCrearCotizacion(t, handler, actorID, map[string]any{
		"cliente_nombre_nuevo": nombre,
		"calculadora_id":       calculadoraID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	rnLimpiarCotizacionYCliente(t, pool, cotizacionID)

	nombreGuardado, razonGuardada := rnClienteDeCotizacion(t, pool, cotizacionID)
	if razonGuardada == nil || *razonGuardada != nombreGuardado {
		t.Errorf("se rompió la compatibilidad: sin razón social explícita debía copiarse el nombre (%q), quedó %v", nombreGuardado, razonGuardada)
	}
}

// Regla: solo se puede cotizar contra un cliente Activo.
func TestReglaCotizaciones_ClienteInactivoNoSePuedeCotizar(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)
	calculadoraID := rnCalculadoraConEstado(t, pool, "Activo")
	clienteID := rnClienteConEstado(t, pool, "Inactivo")

	rec, _ := postCrearCotizacion(t, handler, actorID, map[string]any{
		"cliente_id": clienteID, "calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("se rompió la regla: un cliente Inactivo no debería poder cotizarse, respondió %d: %s", rec.Code, rec.Body.String())
	}

	var cotizaciones int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM cotizaciones WHERE cliente_id=$1`, clienteID).Scan(&cotizaciones); err != nil {
		t.Fatalf("no se pudo contar cotizaciones del cliente inactivo: %v", err)
	}
	if cotizaciones != 0 {
		t.Errorf("se rompió la regla: el rechazo dejó %d cotización(es) creadas para un cliente inactivo", cotizaciones)
	}
}

// Regla: solo se puede cotizar con un cotizador que exista y esté en
// un estado publicable (Activo o Publicado).
func TestReglaCotizaciones_CotizadorInexistenteONoPublicableSeRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)
	clienteID := rnClienteConEstado(t, pool, "Activo")

	casos := []struct {
		nombre        string
		calculadoraID string
	}{
		{"cotizador inexistente", "TEST-RN-CALC-QUE-NO-EXISTE"},
		{"cotizador Inactivo", rnCalculadoraConEstado(t, pool, "Inactivo")},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, _ := postCrearCotizacion(t, handler, actorID, map[string]any{
				"cliente_id": clienteID, "calculadora_id": caso.calculadoraID,
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("se rompió la regla: %s no debería poder usarse para cotizar, respondió %d: %s",
					caso.nombre, rec.Code, rec.Body.String())
			}
		})
	}
}

// Regla: puede_editar del detalle es la negación de "estado terminal".
// Una versión en estado terminal ya no se edita; el frontend usa esta
// bandera para bloquear la pantalla.
func TestReglaCotizaciones_PuedeEditarSoloFueraDeEstadosTerminales(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)

	casos := []struct {
		estado         string
		esperaEditable bool
	}{
		{"Borrador", true},
		{"Revisión Comercial", true},
		{"Enviada al Cliente", true},
		{"Cambios solicitados", true},
		{"Aceptada", false},
		{"Ganada", false},
		{"Perdida", false},
		{"Vencida", false},
		{"Cancelada", false},
	}
	for _, caso := range casos {
		t.Run(caso.estado, func(t *testing.T) {
			cotizacionID, _, _ := crearCotizacionPrueba(t, pool, caso.estado, "", "")
			rec := getDetalleCotizacion(t, handler, actorID, cotizacionID, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
			}
			var res struct {
				Cotizacion struct {
					Estado      string `json:"estado"`
					PuedeEditar bool   `json:"puede_editar"`
				} `json:"cotizacion"`
			}
			assertJSON(t, rec.Body.Bytes(), &res)
			if res.Cotizacion.PuedeEditar != caso.esperaEditable {
				t.Errorf("se rompió la regla de estados terminales: en %q puede_editar debía ser %v y fue %v",
					caso.estado, caso.esperaEditable, res.Cotizacion.PuedeEditar)
			}
		})
	}
}

// Regla: al marcar Ganada, la versión aceptada es la que indica el
// cliente en el body (a diferencia de Aceptada, donde la fija el
// servidor), y tiene que existir en esa cotización.
func TestReglaCotizaciones_GanadaUsaLaVersionAceptadaDelBody(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")
	segunda := rnCrearVersionExtra(t, handler, cotizacionID)
	if segunda != 2 {
		t.Fatalf("la versión extra debía ser la 2, dio %d", segunda)
	}

	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 2, "estado": "Ganada", "version_aceptada": 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200 marcando Ganada con version_aceptada=1, dio %d: %s", rec.Code, rec.Body.String())
	}

	var versionAceptada *int
	if err := pool.QueryRow(context.Background(),
		`SELECT version_aceptada FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&versionAceptada); err != nil {
		t.Fatalf("no se pudo leer version_aceptada: %v", err)
	}
	if versionAceptada == nil || *versionAceptada != 1 {
		t.Errorf("se rompió la regla: Ganada debía fijar la version_aceptada indicada en el body (1), quedó %v", versionAceptada)
	}
}

// Regla: una version_aceptada que no existe en la cotización se
// rechaza con 400 en vez de guardarse.
func TestReglaCotizaciones_GanadaConVersionAceptadaInexistenteSeRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")

	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 1, "estado": "Ganada", "version_aceptada": 99,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("se rompió la regla: una version_aceptada inexistente debía dar 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	var versionAceptada *int
	var estado string
	if err := pool.QueryRow(context.Background(),
		`SELECT version_aceptada, estado FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&versionAceptada, &estado); err != nil {
		t.Fatalf("no se pudo leer la cotización: %v", err)
	}
	if versionAceptada != nil {
		t.Errorf("se rompió la regla: el rechazo no debía dejar version_aceptada escrita, quedó %v", *versionAceptada)
	}
	if estado != "Enviada al Cliente" {
		t.Errorf("se rompió la regla: el rechazo no debía cambiar el estado de la cotización, quedó %q", estado)
	}
}

// Regla: cotizaciones.estado es una copia del estado de la versión
// ACTIVA. Cambiar el estado de una versión vieja actualiza esa
// versión pero NO el estado de la cotización.
func TestReglaCotizaciones_EstadoDeCabeceraSoloSigueALaVersionActual(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")
	if v := rnCrearVersionExtra(t, handler, cotizacionID); v != 2 {
		t.Fatalf("la versión extra debía ser la 2, dio %d", v)
	}

	// Tras crear la v2, la cabecera quedó en Borrador (estado de la v2).
	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 1, "estado": "Perdida",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200 cambiando el estado de la v1, dio %d: %s", rec.Code, rec.Body.String())
	}

	var estadoV1, estadoCabecera string
	var versionActual int
	if err := pool.QueryRow(context.Background(), `
		SELECT cv.estado, c.estado, c.version_actual
		  FROM cotizaciones c
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id=c.cotizacion_id AND cv.numero_version=1
		 WHERE c.cotizacion_id=$1`, cotizacionID).Scan(&estadoV1, &estadoCabecera, &versionActual); err != nil {
		t.Fatalf("no se pudo leer los estados: %v", err)
	}
	if estadoV1 != "Perdida" {
		t.Errorf("la versión vieja debía quedar en Perdida, quedó %q", estadoV1)
	}
	if versionActual != 2 {
		t.Fatalf("version_actual debía seguir en 2, quedó %d", versionActual)
	}
	if estadoCabecera == "Perdida" {
		t.Error("se rompió la regla: cambiar una versión que NO es la actual no debe arrastrar el estado de la cotización")
	}
	if estadoCabecera != "Borrador" {
		t.Errorf("la cabecera debía seguir con el estado de la v2 (Borrador), quedó %q", estadoCabecera)
	}
}

// Regla: el cambio de estado siempre es sobre una versión concreta;
// una versión no positiva no es una versión.
func TestReglaCotizaciones_CambiarEstadoExigeVersionPositiva(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	for _, version := range []int{0, -1} {
		rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
			"version": version, "estado": "Aceptada",
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("se rompió la regla: version=%d debía dar 400, dio %d: %s", version, rec.Code, rec.Body.String())
		}
	}

	var estado string
	if err := pool.QueryRow(context.Background(),
		`SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&estado); err != nil {
		t.Fatalf("no se pudo leer el estado: %v", err)
	}
	if estado != "Borrador" {
		t.Errorf("un pedido inválido no debía cambiar nada, la cotización quedó en %q", estado)
	}
}

// ---------------------------------------------------------------
// Solicitudes
// ---------------------------------------------------------------

// Regla: una solicitud Descartada salió del flujo activo y ya no se
// convierte (hermana de la regla de "Convertida", que sí estaba
// probada).
func TestReglaSolicitudes_DescartadaNoSeConvierte(t *testing.T) {
	pool := setupTestDB(t)
	cotizaciones := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizaciones}
	actorID := crearAdminActorPrueba(t, pool)
	calculadoraID := rnCalculadoraConEstado(t, pool, "Activo")
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente descartado", "", calculadoraID, "Descartada")

	rec, res := postConvertirSolicitud(t, handler, actorID, solicitudID, map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("se rompió la regla: una solicitud Descartada no se puede convertir, respondió %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Errorf("el rechazo debía venir con ok:false, dio %v", res["ok"])
	}

	var cotizacionGenerada *string
	if err := pool.QueryRow(context.Background(),
		`SELECT cotizacion_id_generada FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID).Scan(&cotizacionGenerada); err != nil {
		t.Fatalf("no se pudo leer la solicitud: %v", err)
	}
	if cotizacionGenerada != nil {
		t.Errorf("se rompió la regla: la solicitud descartada quedó con cotización generada %q", *cotizacionGenerada)
	}
}

// Regla: la razón social capturada en el alta de la solicitud viaja
// hasta el cliente que se crea al convertirla — si no, se perdería el
// dato y el cliente nacería con nombre y empresa iguales.
func TestReglaSolicitudes_RazonSocialLlegaAlClienteAlConvertir(t *testing.T) {
	pool := setupTestDB(t)
	cotizaciones := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizaciones}
	actorID := crearAdminActorPrueba(t, pool)
	vendedorID := crearUsuarioPrueba(t, pool, "rn.vendedor."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	calculadoraID := rnCalculadoraConEstado(t, pool, "Activo")

	nombre := "Empresa Solicitud " + sufijoUnico()
	razon := "Razón Social Solicitud S.A. " + sufijoUnico()

	recAlta, resAlta := postSolicitudManual(t, handler, actorID, map[string]any{
		"titulo":               "Solicitud con razón social",
		"crm_id":               "RN-" + sufijoUnico(),
		"cliente_nombre":       nombre,
		"cliente_razon_social": razon,
		"calculadora_id":       calculadoraID,
		"vendedor_id":          vendedorID,
		"descripcion":          "Alcance de prueba para la auditoría de reglas.",
	})
	if recAlta.Code != http.StatusCreated {
		t.Fatalf("no se pudo crear la solicitud: %d %s", recAlta.Code, recAlta.Body.String())
	}
	solicitudID, _ := resAlta["solicitud_id"].(string)
	// solicitudes.cotizacion_id_generada referencia cotizaciones SIN
	// ON DELETE, así que la solicitud tiene que borrarse ANTES que la
	// cotización que generó. Por eso la limpieza va toda junta acá y
	// resuelve los ids adentro, en vez de usar
	// rnLimpiarCotizacionYCliente (que borraría en el orden inverso).
	t.Cleanup(func() {
		ctx := context.Background()
		var cotizacionGenerada, clienteID *string
		pool.QueryRow(ctx, `SELECT cotizacion_id_generada FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID).Scan(&cotizacionGenerada)
		if cotizacionGenerada != nil {
			pool.QueryRow(ctx, `SELECT cliente_id FROM cotizaciones WHERE cotizacion_id=$1`, *cotizacionGenerada).Scan(&clienteID)
		}
		pool.Exec(ctx, `DELETE FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID)
		if cotizacionGenerada != nil {
			pool.Exec(ctx, `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, *cotizacionGenerada)
		}
		if clienteID != nil {
			pool.Exec(ctx, `DELETE FROM clientes WHERE cliente_id=$1`, *clienteID)
		}
	})

	recConv, resConv := postConvertirSolicitud(t, handler, actorID, solicitudID, map[string]any{})
	if recConv.Code != http.StatusOK {
		t.Fatalf("no se pudo convertir la solicitud: %d %s", recConv.Code, recConv.Body.String())
	}
	cotizacionID, _ := resConv["cotizacion_id"].(string)

	nombreGuardado, razonGuardada := rnClienteDeCotizacion(t, pool, cotizacionID)
	if nombreGuardado != nombre {
		t.Errorf("el cliente creado al convertir debía tomar el nombre de la solicitud (%q), quedó %q", nombre, nombreGuardado)
	}
	if razonGuardada == nil || *razonGuardada != razon {
		t.Errorf("se rompió la regla: la razón social de la solicitud (%q) debía llegar al cliente, quedó %v", razon, razonGuardada)
	}
}

// ---------------------------------------------------------------
// Motor de ejecución (Sprint 4)
// ---------------------------------------------------------------

// Regla central del motor de ejecución: el compilado se FIJA en la
// primera apertura. Si después alguien recompila la calculadora, esa
// cotización sigue trabajando con la versión con la que se abrió —
// recompilar no puede cambiarle la estructura por debajo.
func TestReglaRuntime_ElCompiladoFijadoNoCambiaSiSeRecompila(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	pool := fixture.Handler.DB
	ctx := context.Background()

	// Primera apertura: fija el compilado v1.
	if rec := rnGetRuntime(t, fixture.Handler, fixture.CotizacionID, ""); rec.Code != http.StatusOK {
		t.Fatalf("primera apertura: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var fijadoInicial string
	if err := pool.QueryRow(ctx, `SELECT compilado_id_usado::text FROM cotizaciones WHERE cotizacion_id=$1`, fixture.CotizacionID).Scan(&fijadoInicial); err != nil {
		t.Fatalf("no se pudo leer el compilado fijado: %v", err)
	}
	if fijadoInicial != fixture.CompiladoID {
		t.Fatalf("la primera apertura debía fijar el compilado ACTIVA %q, fijó %q", fixture.CompiladoID, fijadoInicial)
	}

	// Se publica una versión nueva del cotizador. Se hace por SQL a
	// propósito: lo que se prueba acá es el comportamiento del runtime
	// ante un compilado nuevo, no el compilador (que tiene sus propias
	// pruebas). El índice único parcial obliga a degradar la anterior.
	var calculadoraID string
	if err := pool.QueryRow(ctx, `SELECT calculadora_id FROM cotizaciones WHERE cotizacion_id=$1`, fixture.CotizacionID).Scan(&calculadoraID); err != nil {
		t.Fatalf("no se pudo leer la calculadora: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE cotizadores_compilados SET estado='ANTERIOR' WHERE compilado_id::text=$1`, fixture.CompiladoID); err != nil {
		t.Fatalf("no se pudo degradar el compilado viejo: %v", err)
	}
	estructuraNueva := `{"calculadora_id":"` + calculadoraID + `","version":2,"tabs":[{"tab_id":"TEST-RN-TAB-V2","nombre":"Estructura recompilada","alcance":"PROPIO","orden":1,"elementos":[]}]}`
	var compiladoNuevoID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
		VALUES ($1, 2, 'ACTIVA', $2) RETURNING compilado_id::text`, calculadoraID, estructuraNueva).Scan(&compiladoNuevoID); err != nil {
		t.Fatalf("no se pudo crear el compilado v2: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoNuevoID)
	})

	// Segunda apertura: tiene que seguir con el compilado viejo.
	rec := rnGetRuntime(t, fixture.Handler, fixture.CotizacionID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("segunda apertura: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	var fijadoDespues string
	if err := pool.QueryRow(ctx, `SELECT compilado_id_usado::text FROM cotizaciones WHERE cotizacion_id=$1`, fixture.CotizacionID).Scan(&fijadoDespues); err != nil {
		t.Fatalf("no se pudo releer el compilado fijado: %v", err)
	}
	if fijadoDespues != fijadoInicial {
		t.Errorf("se rompió la regla: recompilar cambió el compilado fijado de %q a %q", fijadoInicial, fijadoDespues)
	}
	if fijadoDespues == compiladoNuevoID {
		t.Error("se rompió la regla: la cotización se pasó sola al compilado recién publicado")
	}
	tabs, _ := res.Estructura["tabs"].([]any)
	if len(tabs) != 1 {
		t.Fatalf("estructura inesperada tras recompilar: %+v", res.Estructura)
	}
	nombreTab, _ := tabs[0].(map[string]any)["nombre"].(string)
	if nombreTab == "Estructura recompilada" {
		t.Error("se rompió la regla: el runtime devolvió la estructura NUEVA en una cotización que ya tenía compilado fijado")
	}
	if nombreTab != "Datos" {
		t.Errorf("el runtime debía seguir devolviendo la estructura fijada (tab \"Datos\"), devolvió %q", nombreTab)
	}
}

// Regla: sin una versión compilada ACTIVA no hay cotizador que
// ejecutar — es un conflicto de estado (409), no un 404 ni un 500.
func TestReglaRuntime_SinCompiladoActivoDa409(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizadorRuntimeHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := rnGetRuntime(t, handler, cotizacionID, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("se rompió la regla: sin compilado ACTIVA se espera 409, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// Regla: el runtime de una cotización que no existe es 404.
func TestReglaRuntime_CotizacionInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizadorRuntimeHandler{DB: pool}

	rec := rnGetRuntime(t, handler, "TEST-RN-COT-QUE-NO-EXISTE", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("se rompió la regla: una cotización inexistente debía dar 404, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------
// Compilador
// ---------------------------------------------------------------

// Regla: compilar es publicar. Si la validación falla, no se publica
// nada — no puede quedar una versión compilada de una configuración
// inválida.
func TestReglaCompilador_NoPublicaSiLaValidacionFalla(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	handler := &CompiladorHandler{DB: tabsHandler.DB}

	res := postCompilador(t, handler.Compilar, calculadoraID)
	if res.Valido || res.Compilado {
		t.Fatalf("se rompió la regla: un cotizador sin secciones no debía compilar, dio valido=%v compilado=%v", res.Valido, res.Compilado)
	}

	var compilados int
	if err := tabsHandler.DB.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM cotizadores_compilados WHERE calculadora_id=$1`, calculadoraID).Scan(&compilados); err != nil {
		t.Fatalf("no se pudo contar compilados: %v", err)
	}
	if compilados != 0 {
		t.Errorf("se rompió la regla: la compilación rechazada dejó %d fila(s) en cotizadores_compilados", compilados)
	}

	var estado string
	if err := tabsHandler.DB.QueryRow(context.Background(),
		`SELECT estado FROM calculadoras WHERE calculadora_id=$1`, calculadoraID).Scan(&estado); err != nil {
		t.Fatalf("no se pudo leer el estado de la calculadora: %v", err)
	}
	if estado == "Publicado" {
		t.Error("se rompió la regla: una compilación inválida no debe dejar la calculadora como Publicado")
	}
}

// Regla: por cotizador hay UNA sola versión compilada ACTIVA, pase lo
// que pase. Con tres publicaciones tienen que quedar dos ANTERIOR y
// una ACTIVA, y la calculadora apuntando a la última.
func TestReglaCompilador_TresPublicacionesDejanUnaSolaActiva(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-RN-COMP-TAB-" + sufijoUnico()
	if rec := postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Publicable", "activo": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("no se pudo crear el tab: %s", rec.Body.String())
	}
	if rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-RN-COMP-EL-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Nombre", "orden": 1, "activo": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("no se pudo crear el elemento: %s", rec.Body.String())
	}

	handler := &CompiladorHandler{DB: tabsHandler.DB}
	versiones := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		res := postCompilador(t, handler.Compilar, calculadoraID)
		if !res.Compilado {
			t.Fatalf("la publicación %d no compiló: %+v", i+1, res)
		}
		versiones = append(versiones, res.VersionConfiguracion)
	}
	if versiones[0] != "1" || versiones[1] != "2" || versiones[2] != "3" {
		t.Errorf("se rompió la regla de versionado incremental: %v", versiones)
	}

	var activas, anteriores int
	if err := tabsHandler.DB.QueryRow(context.Background(), `
		SELECT COUNT(*) FILTER (WHERE estado='ACTIVA'), COUNT(*) FILTER (WHERE estado='ANTERIOR')
		  FROM cotizadores_compilados WHERE calculadora_id=$1`, calculadoraID).Scan(&activas, &anteriores); err != nil {
		t.Fatalf("no se pudo contar compilados: %v", err)
	}
	if activas != 1 {
		t.Errorf("se rompió la regla: debe haber exactamente una versión ACTIVA por cotizador, hay %d", activas)
	}
	if anteriores != 2 {
		t.Errorf("tras tres publicaciones debían quedar 2 ANTERIOR, hay %d", anteriores)
	}

	var versionActual, estado string
	if err := tabsHandler.DB.QueryRow(context.Background(),
		`SELECT version_actual, estado FROM calculadoras WHERE calculadora_id=$1`, calculadoraID).Scan(&versionActual, &estado); err != nil {
		t.Fatalf("no se pudo leer la calculadora: %v", err)
	}
	if versionActual != "3" || estado != "Publicado" {
		t.Errorf("la calculadora debía quedar en versión 3 y Publicado, quedó versión=%q estado=%q", versionActual, estado)
	}
}

// ---------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------

// Regla: el dashboard depende del rol del usuario de la sesión (para
// decidir si muestra márgenes), así que sin usuario en el contexto
// responde 401 en vez de asumir un rol.
func TestReglaDashboard_SinUsuarioEnLaSesionDa401(t *testing.T) {
	pool := setupTestDB(t)
	handler := &DashboardHandler{DB: pool}

	rec := httptest.NewRecorder()
	handler.Obtener(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("se rompió la regla: sin usuario en la sesión el dashboard debía dar 401, dio %d: %s", rec.Code, rec.Body.String())
	}
}
