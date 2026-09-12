package handlers

// Pruebas de integración de la pantalla de gestión de Clientes:
// GET /api/clientes/gestion, POST /api/clientes y
// PATCH /api/clientes/{id} (ClientesHandler), más una prueba de
// regresión del selector viejo GET /api/clientes
// (CotizacionesHandler.ListarClientes), que alimenta "Nueva
// cotización" y no debe romperse.
//
// Mismo criterio que el resto del paquete: contra Postgres real, con
// fixtures propios que se limpian solos. Igual que usuarios_test.go,
// estas pruebas llaman a los handlers directo (sin
// middleware.RequiereSesion), así que el actor de la sesión se inyecta
// a mano en el contexto con conActor.
//
// Para no depender de qué más haya en la base, cada prueba etiqueta
// sus fixtures con un token único (cliTokenUnico) y los aísla con el
// filtro ?busqueda=<token>. Así el listado puede traer miles de
// clientes reales y las afirmaciones siguen siendo deterministas.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cliContador desempata los ids cuando una misma prueba crea varios
// fixtures seguidos: sufijoUnico() usa la hora y, dentro de un bucle,
// dos llamadas podrían caer en el mismo instante.
var cliContador int64

func cliIDUnico(prefijo string) string {
	return prefijo + "-" + sufijoUnico() + "-" + strconv.FormatInt(atomic.AddInt64(&cliContador, 1), 10)
}

// cliTokenUnico devuelve un texto que solo aparece en los fixtures de
// una prueba. Lleva letras mayúsculas a propósito, para que las
// pruebas de búsqueda insensible a mayúsculas tengan algo real que
// comprobar.
func cliTokenUnico() string {
	return "ZZQA" + strings.ReplaceAll(sufijoUnico(), ".", "") + strconv.FormatInt(atomic.AddInt64(&cliContador, 1), 10)
}

// cliCrearClientePrueba inserta un cliente descartable. razonSocial y
// origen son `any` para poder pasar nil y cubrir el caso de columnas
// NULL, que es justamente lo que podría romper el Scan del listado.
func cliCrearClientePrueba(t *testing.T, pool *pgxpool.Pool, nombre string, razonSocial, origen any, estado string) string {
	t.Helper()
	id := cliIDUnico("test-cli")
	_, err := pool.Exec(context.Background(), `
		INSERT INTO clientes (cliente_id, nombre_comercial, razon_social, origen, estado)
		VALUES ($1, $2, $3, $4, $5)`,
		id, nombre, razonSocial, origen, estado,
	)
	if err != nil {
		t.Fatalf("no se pudo crear el cliente de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id = $1`, id)
	})
	return id
}

// cliLimpiarClienteCreado registra la limpieza de un cliente que creó
// el propio handler (su id lo elige el handler, no la prueba).
func cliLimpiarClienteCreado(t *testing.T, pool *pgxpool.Pool, clienteID string) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id = $1`, clienteID)
	})
}

func cliCrearCalculadoraPrueba(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := cliIDUnico("TEST-CALC-CLI")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado)
		VALUES ($1, 'Cotizador de pruebas de clientes', 'Activo')`, id); err != nil {
		t.Fatalf("no se pudo crear el cotizador de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id = $1`, id)
	})
	return id
}

func cliCrearCotizacionPrueba(t *testing.T, pool *pgxpool.Pool, calculadoraID, clienteID, estado string) string {
	t.Helper()
	id := cliIDUnico("test-cot-cli")
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO cotizaciones (cotizacion_id, calculadora_id, cliente_id, estado, version_actual)
		VALUES ($1, $2, $3, $4, 1)`, id, calculadoraID, clienteID, estado); err != nil {
		t.Fatalf("no se pudo crear la cotización de prueba en estado %q: %v", estado, err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, id)
	})
	return id
}

func cliGetGestion(t *testing.T, handler *ClientesHandler, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	url := "/api/clientes/gestion"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

// cliPostClienteCrudo manda el body tal cual, para poder probar JSON
// malformado. actorID vacío = petición sin sesión.
func cliPostClienteCrudo(t *testing.T, handler *ClientesHandler, actorID, cuerpo string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/clientes", strings.NewReader(cuerpo))
	req.Header.Set("Content-Type", "application/json")
	if actorID != "" {
		req = conActor(req, actorID)
	}
	rec := httptest.NewRecorder()
	handler.Crear(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func cliPostCliente(t *testing.T, handler *ClientesHandler, actorID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	return cliPostClienteCrudo(t, handler, actorID, string(raw))
}

// cliPatchClienteCrudo monta un chi.Router real porque Editar lee el
// {id} con chi.URLParam: sin router de por medio ese valor viene vacío.
func cliPatchClienteCrudo(t *testing.T, handler *ClientesHandler, actorID, clienteID, cuerpo string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	router := chi.NewRouter()
	router.Patch("/api/clientes/{id}", handler.Editar)

	req := httptest.NewRequest(http.MethodPatch, "/api/clientes/"+clienteID, strings.NewReader(cuerpo))
	req.Header.Set("Content-Type", "application/json")
	if actorID != "" {
		req = conActor(req, actorID)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func cliPatchCliente(t *testing.T, handler *ClientesHandler, actorID, clienteID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	return cliPatchClienteCrudo(t, handler, actorID, clienteID, string(raw))
}

type cliFilaBD struct {
	Nombre  string
	Razon   *string
	Origen  *string
	Estado  string
	Creador *string
}

func cliLeerEnBD(t *testing.T, pool *pgxpool.Pool, clienteID string) cliFilaBD {
	t.Helper()
	var fila cliFilaBD
	err := pool.QueryRow(context.Background(), `
		SELECT nombre_comercial, razon_social, origen, estado, usuario_creador_id
		  FROM clientes WHERE cliente_id = $1`, clienteID,
	).Scan(&fila.Nombre, &fila.Razon, &fila.Origen, &fila.Estado, &fila.Creador)
	if err != nil {
		t.Fatalf("no se pudo leer el cliente %s de la base: %v", clienteID, err)
	}
	return fila
}

// cliListadoPorID indexa la respuesta por cliente_id para poder
// afirmar sobre un fixture puntual sin depender del resto de la base.
func cliListadoPorID(t *testing.T, res map[string]any) map[string]map[string]any {
	t.Helper()
	crudos, ok := res["clientes"].([]any)
	if !ok {
		t.Fatalf("la respuesta no trae el arreglo clientes: %#v", res)
	}
	porID := make(map[string]map[string]any, len(crudos))
	for _, crudo := range crudos {
		fila, ok := crudo.(map[string]any)
		if !ok {
			t.Fatalf("fila de cliente con forma inesperada: %#v", crudo)
		}
		id, _ := fila["cliente_id"].(string)
		porID[id] = fila
	}
	return porID
}

// cliOrdenDelListado devuelve los cliente_id en el orden en que
// llegaron, para poder comprobar el ORDER BY del handler.
func cliOrdenDelListado(t *testing.T, res map[string]any) []string {
	t.Helper()
	crudos, ok := res["clientes"].([]any)
	if !ok {
		t.Fatalf("la respuesta no trae el arreglo clientes: %#v", res)
	}
	orden := make([]string, 0, len(crudos))
	for _, crudo := range crudos {
		fila, _ := crudo.(map[string]any)
		id, _ := fila["cliente_id"].(string)
		orden = append(orden, id)
	}
	return orden
}

// ------------------------------------------------------------------
// Listar — GET /api/clientes/gestion
// ------------------------------------------------------------------

func TestClientesListar_FormaYConteoDeCotizaciones(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	token := cliTokenUnico()

	conCotizaciones := cliCrearClientePrueba(t, pool, "Cliente "+token+" con cotizaciones", "Razón "+token+" S.A.", "COTIZA", "Activo")
	sinCotizaciones := cliCrearClientePrueba(t, pool, "Cliente "+token+" sin cotizaciones", "Razón "+token+" Ltda.", "COTIZA", "Activo")
	calculadora := cliCrearCalculadoraPrueba(t, pool)
	// El total es histórico: incluye estados terminales. Una Perdida
	// cuenta acá aunque NO cuente para el guard de desactivación.
	cliCrearCotizacionPrueba(t, pool, calculadora, conCotizaciones, "Borrador")
	cliCrearCotizacionPrueba(t, pool, calculadora, conCotizaciones, "Ganada")
	cliCrearCotizacionPrueba(t, pool, calculadora, conCotizaciones, "Perdida")

	rec, res := cliGetGestion(t, handler, "busqueda="+token)
	if rec.Code != http.StatusOK {
		t.Fatalf("el listado de gestión respondió %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != true {
		t.Fatalf("la respuesta debería traer ok:true, trajo %#v", res)
	}

	porID := cliListadoPorID(t, res)
	fila := porID[conCotizaciones]
	if fila == nil {
		t.Fatalf("el cliente %s no apareció en el listado filtrado por su propio token", conCotizaciones)
	}
	for _, campo := range []string{"cliente_id", "nombre_comercial", "razon_social", "estado", "origen", "fecha_creacion", "total_cotizaciones"} {
		if _, existe := fila[campo]; !existe {
			t.Errorf("la fila del listado no trae el campo %q que la pantalla necesita: %#v", campo, fila)
		}
	}
	if fila["total_cotizaciones"] != float64(3) {
		t.Errorf("total_cotizaciones debería ser 3 (Borrador + Ganada + Perdida: el total es histórico), fue %v", fila["total_cotizaciones"])
	}

	filaSin := porID[sinCotizaciones]
	if filaSin == nil {
		t.Fatalf("el cliente sin cotizaciones %s no apareció en el listado", sinCotizaciones)
	}
	if filaSin["total_cotizaciones"] != float64(0) {
		t.Errorf("un cliente sin cotizaciones debería traer total_cotizaciones 0, trajo %v", filaSin["total_cotizaciones"])
	}
}

func TestClientesListar_FiltroEstado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	token := cliTokenUnico()

	activo := cliCrearClientePrueba(t, pool, "Cliente "+token+" activo", nil, "COTIZA", "Activo")
	inactivo := cliCrearClientePrueba(t, pool, "Cliente "+token+" inactivo", nil, "COTIZA", "Inactivo")

	_, res := cliGetGestion(t, handler, "busqueda="+token)
	porID := cliListadoPorID(t, res)
	if len(porID) != 2 {
		t.Fatalf("sin filtro de estado deberían venir los 2 clientes del token, vinieron %d", len(porID))
	}

	_, res = cliGetGestion(t, handler, "busqueda="+token+"&estado=Activo")
	porID = cliListadoPorID(t, res)
	if porID[activo] == nil {
		t.Errorf("estado=Activo debería incluir al cliente activo %s", activo)
	}
	if porID[inactivo] != nil {
		t.Errorf("estado=Activo NO debería incluir al cliente inactivo %s", inactivo)
	}

	_, res = cliGetGestion(t, handler, "busqueda="+token+"&estado=Inactivo")
	porID = cliListadoPorID(t, res)
	if porID[inactivo] == nil {
		t.Errorf("estado=Inactivo debería incluir al cliente inactivo %s", inactivo)
	}
	if porID[activo] != nil {
		t.Errorf("estado=Inactivo NO debería incluir al cliente activo %s", activo)
	}
}

func TestClientesListar_EstadoInvalidoRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}

	rec, res := cliGetGestion(t, handler, "estado=Basura")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("un estado fuera de Activo/Inactivo debería dar 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["error"] != "estado no es válido." {
		t.Errorf("el mensaje de error debería explicar que el estado no es válido, fue %#v", res["error"])
	}
}

func TestClientesListar_BusquedaPorNombreYRazonSocial(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	tokenNombre := cliTokenUnico()
	tokenRazon := cliTokenUnico()

	// Tokens distintos en cada columna: así se comprueba que la
	// búsqueda mira nombre_comercial Y razon_social, no solo una.
	cliente := cliCrearClientePrueba(t, pool, "Ferretería "+tokenNombre+" Central", "Razón "+tokenRazon+" S.A.", "COTIZA", "Activo")

	casos := []struct {
		nombre   string
		busqueda string
	}{
		{"por nombre comercial parcial", tokenNombre},
		{"por razón social parcial", tokenRazon},
		{"insensible a mayúsculas", strings.ToLower(tokenNombre)},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, res := cliGetGestion(t, handler, "busqueda="+caso.busqueda)
			if rec.Code != http.StatusOK {
				t.Fatalf("la búsqueda respondió %d: %s", rec.Code, rec.Body.String())
			}
			if cliListadoPorID(t, res)[cliente] == nil {
				t.Errorf("la búsqueda %q debería encontrar al cliente %s", caso.busqueda, cliente)
			}
		})
	}
}

func TestClientesListar_RazonSocialYOrigenNulosNoRompenElScan(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	token := cliTokenUnico()

	// razon_social y origen son nullable en el esquema: un cliente
	// importado a medias no debe reventar el listado.
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+token+" sin datos opcionales", nil, nil, "Activo")

	rec, res := cliGetGestion(t, handler, "busqueda="+token)
	if rec.Code != http.StatusOK {
		t.Fatalf("un cliente con razon_social y origen NULL rompió el listado (%d): %s", rec.Code, rec.Body.String())
	}
	fila := cliListadoPorID(t, res)[cliente]
	if fila == nil {
		t.Fatalf("el cliente con columnas NULL %s no apareció en el listado", cliente)
	}
	// Ambos campos llevan omitempty, así que directamente no vienen.
	if _, existe := fila["razon_social"]; existe {
		t.Errorf("razon_social NULL debería omitirse de la respuesta, vino %#v", fila["razon_social"])
	}
	if _, existe := fila["origen"]; existe {
		t.Errorf("origen NULL debería omitirse de la respuesta, vino %#v", fila["origen"])
	}
}

func TestClientesListar_OrdenPorNombreComercial(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	token := cliTokenUnico()

	// Se crean a propósito en orden alfabético inverso.
	tercero := cliCrearClientePrueba(t, pool, "C "+token, nil, "COTIZA", "Activo")
	primero := cliCrearClientePrueba(t, pool, "A "+token, nil, "COTIZA", "Activo")
	segundo := cliCrearClientePrueba(t, pool, "B "+token, nil, "COTIZA", "Activo")

	_, res := cliGetGestion(t, handler, "busqueda="+token)
	obtenido := cliOrdenDelListado(t, res)
	esperado := []string{primero, segundo, tercero}
	if len(obtenido) != len(esperado) {
		t.Fatalf("deberían venir %d clientes del token, vinieron %d", len(esperado), len(obtenido))
	}
	for i := range esperado {
		if obtenido[i] != esperado[i] {
			t.Fatalf("el listado debería venir ordenado por nombre_comercial (A, B, C); obtuvo %v", obtenido)
		}
	}
}

// ------------------------------------------------------------------
// Crear — POST /api/clientes
// ------------------------------------------------------------------

func TestClientesCrear_GuardaOrigenEstadoYCreador(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	token := cliTokenUnico()

	rec, res := cliPostCliente(t, handler, actor, map[string]any{
		"nombre_comercial": "Cliente Nuevo " + token,
		"razon_social":     "Razón Nueva " + token + " S.A.",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("el alta debería responder 201, respondió %d: %s", rec.Code, rec.Body.String())
	}
	clienteID, ok := res["cliente_id"].(string)
	if !ok || clienteID == "" {
		t.Fatalf("el alta debería devolver el cliente_id creado, devolvió %#v", res)
	}
	cliLimpiarClienteCreado(t, pool, clienteID)

	if !strings.HasPrefix(clienteID, "cli-") {
		t.Errorf("el id generado debería llevar el prefijo cli-, fue %q", clienteID)
	}

	fila := cliLeerEnBD(t, pool, clienteID)
	if fila.Origen == nil || *fila.Origen != "COTIZA" {
		t.Errorf("un cliente creado desde la pantalla debería nacer con origen COTIZA, quedó %v", fila.Origen)
	}
	if fila.Estado != "Activo" {
		t.Errorf("un cliente nuevo debería nacer Activo, quedó %q", fila.Estado)
	}
	if fila.Creador == nil || *fila.Creador != actor {
		t.Errorf("usuario_creador_id debería ser el actor de la sesión (%s), quedó %v", actor, fila.Creador)
	}
}

func TestClientesCrear_RazonSocialVaciaQuedaNula(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)

	casos := []struct {
		nombre string
		body   map[string]any
	}{
		{"razón social ausente", map[string]any{"nombre_comercial": "Cliente " + cliTokenUnico()}},
		{"razón social vacía", map[string]any{"nombre_comercial": "Cliente " + cliTokenUnico(), "razon_social": ""}},
		{"razón social solo espacios", map[string]any{"nombre_comercial": "Cliente " + cliTokenUnico(), "razon_social": "   "}},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, res := cliPostCliente(t, handler, actor, caso.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("el alta respondió %d: %s", rec.Code, rec.Body.String())
			}
			clienteID, _ := res["cliente_id"].(string)
			cliLimpiarClienteCreado(t, pool, clienteID)

			if fila := cliLeerEnBD(t, pool, clienteID); fila.Razon != nil {
				t.Errorf("sin razón social la columna debería quedar NULL (no cadena vacía), quedó %q", *fila.Razon)
			}
		})
	}
}

func TestClientesCrear_RazonSocialDistintaSeGuardaSeparada(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	token := cliTokenUnico()
	nombre := "Comercial " + token
	razon := "Razón Social Distinta " + token + " S.A."

	_, res := cliPostCliente(t, handler, actor, map[string]any{
		"nombre_comercial": nombre,
		"razon_social":     razon,
	})
	clienteID, _ := res["cliente_id"].(string)
	if clienteID == "" {
		t.Fatalf("no se creó el cliente: %#v", res)
	}
	cliLimpiarClienteCreado(t, pool, clienteID)

	fila := cliLeerEnBD(t, pool, clienteID)
	if fila.Nombre != nombre {
		t.Errorf("nombre_comercial debería ser %q, quedó %q", nombre, fila.Nombre)
	}
	if fila.Razon == nil || *fila.Razon != razon {
		t.Errorf("razon_social debería guardarse separada del nombre (%q), quedó %v", razon, fila.Razon)
	}
}

func TestClientesCrear_SinNombreComercialRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)

	casos := []struct {
		nombre string
		body   map[string]any
	}{
		{"ausente", map[string]any{"razon_social": "Solo razón social S.A."}},
		{"vacío", map[string]any{"nombre_comercial": ""}},
		{"solo espacios", map[string]any{"nombre_comercial": "   "}},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, res := cliPostCliente(t, handler, actor, caso.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("el nombre comercial es obligatorio: debería dar 400, dio %d: %s", rec.Code, rec.Body.String())
			}
			if res["ok"] != false {
				t.Errorf("la respuesta de error debería traer ok:false, trajo %#v", res)
			}
		})
	}
}

func TestClientesCrear_SinSesionRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}

	// Sin actor en el contexto: en producción eso significa que la
	// petición no pasó por middleware.RequiereSesion.
	rec, res := cliPostCliente(t, handler, "", map[string]any{"nombre_comercial": "Cliente sin sesión"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("crear un cliente sin sesión debería dar 401, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Errorf("la respuesta de error debería traer ok:false, trajo %#v", res)
	}
}

func TestClientesCrear_EntradaMalformadaRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)

	casos := []struct {
		nombre string
		cuerpo string
	}{
		{"JSON incompleto", `{"nombre_comercial":`},
		{"JSON que no es objeto", `["no", "soy", "un", "objeto"]`},
		{"campo desconocido", `{"nombre_comercial":"X","campo_inexistente":1}`},
		{"tipo incorrecto", `{"nombre_comercial":123}`},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, res := cliPostClienteCrudo(t, handler, actor, caso.cuerpo)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("una entrada malformada debe dar un 400 controlado (nunca 500), dio %d: %s", rec.Code, rec.Body.String())
			}
			if res["ok"] != false {
				t.Errorf("la respuesta de error debería traer ok:false, trajo %#v", res)
			}
		})
	}
}

// ------------------------------------------------------------------
// Editar — PATCH /api/clientes/{id}
// ------------------------------------------------------------------

func TestClientesEditar_PatchParcialNoPisaLosOtrosCampos(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	token := cliTokenUnico()
	nombreOriginal := "Nombre Original " + token
	cliente := cliCrearClientePrueba(t, pool, nombreOriginal, "Razón Original S.A.", "COTIZA", "Activo")

	rec, res := cliPatchCliente(t, handler, actor, cliente, map[string]any{"razon_social": "Razón Editada S.A."})
	if rec.Code != http.StatusOK {
		t.Fatalf("la edición parcial respondió %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != true {
		t.Fatalf("la respuesta debería traer ok:true, trajo %#v", res)
	}

	fila := cliLeerEnBD(t, pool, cliente)
	if fila.Nombre != nombreOriginal {
		t.Errorf("editar solo la razón social no debería tocar nombre_comercial (%q), quedó %q", nombreOriginal, fila.Nombre)
	}
	if fila.Razon == nil || *fila.Razon != "Razón Editada S.A." {
		t.Errorf("la razón social debería haber quedado editada, quedó %v", fila.Razon)
	}
	if fila.Estado != "Activo" {
		t.Errorf("editar solo la razón social no debería tocar el estado, quedó %q", fila.Estado)
	}
}

// TestClientesEditar_RazonSocialVaciaGuardaCadenaVacia fija el
// comportamiento ACTUAL, que es asimétrico respecto de Crear: al crear,
// una razón social vacía se normaliza a NULL; al editar, se guarda como
// cadena vacía. Si algún día se unifica, esta prueba es la que hay que
// cambiar a propósito (y no debería fallar por accidente).
func TestClientesEditar_RazonSocialVaciaGuardaCadenaVacia(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+cliTokenUnico(), "Razón Previa S.A.", "COTIZA", "Activo")

	rec, _ := cliPatchCliente(t, handler, actor, cliente, map[string]any{"razon_social": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("vaciar la razón social respondió %d: %s", rec.Code, rec.Body.String())
	}

	fila := cliLeerEnBD(t, pool, cliente)
	if fila.Razon == nil {
		t.Fatalf("comportamiento cambiado: Editar ahora normaliza la razón social vacía a NULL. " +
			"Si el cambio es deliberado, actualizar esta prueba; si no, es una regresión")
	}
	if *fila.Razon != "" {
		t.Errorf("al editar, una razón social vacía queda como cadena vacía, quedó %q", *fila.Razon)
	}
}

func TestClientesEditar_ValidacionesDeCampos(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+cliTokenUnico(), nil, "COTIZA", "Activo")

	casos := []struct {
		nombre string
		body   map[string]any
		porQue string
	}{
		{"nombre comercial vacío", map[string]any{"nombre_comercial": ""}, "el nombre comercial no puede quedar vacío"},
		{"nombre comercial solo espacios", map[string]any{"nombre_comercial": "   "}, "el nombre comercial no puede quedar vacío"},
		{"estado inválido", map[string]any{"estado": "Basura"}, "el estado solo puede ser Activo o Inactivo"},
		{"body sin ningún campo", map[string]any{}, "hay que indicar al menos un campo para editar"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, res := cliPatchCliente(t, handler, actor, cliente, caso.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: debería dar 400, dio %d: %s", caso.porQue, rec.Code, rec.Body.String())
			}
			if res["ok"] != false {
				t.Errorf("la respuesta de error debería traer ok:false, trajo %#v", res)
			}
		})
	}
}

func TestClientesEditar_ClienteInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)

	rec, res := cliPatchCliente(t, handler, actor, "cli-no-existe-"+sufijoUnico(), map[string]any{"nombre_comercial": "Nombre nuevo"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("editar un cliente inexistente debería dar 404, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Errorf("la respuesta de error debería traer ok:false, trajo %#v", res)
	}
}

func TestClientesEditar_EntradaMalformadaRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+cliTokenUnico(), nil, "COTIZA", "Activo")

	casos := []struct {
		nombre string
		cuerpo string
	}{
		{"JSON incompleto", `{"nombre_comercial":`},
		{"campo desconocido", `{"nombre_comercial":"X","otro_campo":true}`},
		{"tipo incorrecto", `{"estado":42}`},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, res := cliPatchClienteCrudo(t, handler, actor, cliente, caso.cuerpo)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("una entrada malformada debe dar un 400 controlado (nunca 500), dio %d: %s", rec.Code, rec.Body.String())
			}
			if res["ok"] != false {
				t.Errorf("la respuesta de error debería traer ok:false, trajo %#v", res)
			}
		})
	}
}

// ------------------------------------------------------------------
// Guard de desactivación — la regla de negocio propia de este handler
// ------------------------------------------------------------------

func TestClientesEditar_NoDesactivaConCotizacionesActivas(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+cliTokenUnico(), nil, "COTIZA", "Activo")
	calculadora := cliCrearCalculadoraPrueba(t, pool)
	cliCrearCotizacionPrueba(t, pool, calculadora, cliente, "Borrador")
	cliCrearCotizacionPrueba(t, pool, calculadora, cliente, "Enviada al Cliente")

	rec, res := cliPatchCliente(t, handler, actor, cliente, map[string]any{"estado": "Inactivo"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("desactivar un cliente con cotizaciones activas debería dar 409, dio %d: %s", rec.Code, rec.Body.String())
	}
	mensaje, _ := res["error"].(string)
	if !strings.Contains(mensaje, "2") {
		t.Errorf("el mensaje debería decir cuántas cotizaciones activas bloquean el cambio (2), fue %q", mensaje)
	}

	if fila := cliLeerEnBD(t, pool, cliente); fila.Estado != "Activo" {
		t.Errorf("un rechazo con 409 no debe haber cambiado el estado en la base, quedó %q", fila.Estado)
	}
}

func TestClientesEditar_DesactivaSiSusCotizacionesEstanPerdidasOCanceladas(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+cliTokenUnico(), nil, "COTIZA", "Activo")
	calculadora := cliCrearCalculadoraPrueba(t, pool)
	cliCrearCotizacionPrueba(t, pool, calculadora, cliente, "Perdida")
	cliCrearCotizacionPrueba(t, pool, calculadora, cliente, "Cancelada")

	rec, res := cliPatchCliente(t, handler, actor, cliente, map[string]any{"estado": "Inactivo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("con solo cotizaciones Perdida/Cancelada la desactivación debería pasar, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != true {
		t.Fatalf("la respuesta debería traer ok:true, trajo %#v", res)
	}
	if fila := cliLeerEnBD(t, pool, cliente); fila.Estado != "Inactivo" {
		t.Errorf("el cliente debería haber quedado Inactivo en la base, quedó %q", fila.Estado)
	}
}

// TestClientesEditar_EstadosQueBloqueanLaDesactivacion fija el criterio
// explícito de clienteConCotizacionesActivas: "activa" es todo lo que NO
// sea Perdida ni Cancelada. Es a propósito distinto de
// estadosCotizacionBloqueados (que incluye Aceptada, Ganada y Vencida
// como estados no editables): una cotización Ganada ya no se edita, pero
// sí impide desactivar al cliente.
func TestClientesEditar_EstadosQueBloqueanLaDesactivacion(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	calculadora := cliCrearCalculadoraPrueba(t, pool)

	estadosQueBloquean := []string{
		"Borrador", "Revisión Comercial", "Enviada al Cliente",
		"Vista por el Cliente", "Cambios solicitados", "Aceptada",
		"Ganada", "Vencida",
	}
	for _, estado := range estadosQueBloquean {
		t.Run(estado, func(t *testing.T) {
			cliente := cliCrearClientePrueba(t, pool, "Cliente "+cliTokenUnico(), nil, "COTIZA", "Activo")
			cliCrearCotizacionPrueba(t, pool, calculadora, cliente, estado)

			rec, _ := cliPatchCliente(t, handler, actor, cliente, map[string]any{"estado": "Inactivo"})
			if rec.Code != http.StatusConflict {
				t.Errorf("una cotización en estado %q debería impedir desactivar al cliente (409), respondió %d: %s",
					estado, rec.Code, rec.Body.String())
			}
			if fila := cliLeerEnBD(t, pool, cliente); fila.Estado != "Activo" {
				t.Errorf("con una cotización en %q el cliente debería seguir Activo, quedó %q", estado, fila.Estado)
			}
		})
	}
}

// ------------------------------------------------------------------
// Regresión del selector viejo — GET /api/clientes
// ------------------------------------------------------------------

// TestClientesSelector_SoloActivosYConvivenConGestion protege el
// contrato del selector que alimenta "Nueva cotización": la pantalla de
// gestión se agregó al lado, no en lugar de él. Un cliente Inactivo
// tiene que verse en /gestion y NO en el selector.
func TestClientesSelector_SoloActivosYConvivenConGestion(t *testing.T) {
	pool := setupTestDB(t)
	gestion := &ClientesHandler{DB: pool}
	selector := &CotizacionesHandler{DB: pool}
	token := cliTokenUnico()

	activo := cliCrearClientePrueba(t, pool, "Cliente "+token+" activo", "Razón "+token+" S.A.", "COTIZA", "Activo")
	inactivo := cliCrearClientePrueba(t, pool, "Cliente "+token+" inactivo", nil, "COTIZA", "Inactivo")

	req := httptest.NewRequest(http.MethodGet, "/api/clientes", nil)
	rec := httptest.NewRecorder()
	selector.ListarClientes(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("el selector respondió %d: %s", rec.Code, rec.Body.String())
	}
	var resSelector map[string]any
	assertJSON(t, rec.Body.Bytes(), &resSelector)

	porIDSelector := cliListadoPorID(t, resSelector)
	filaActivo := porIDSelector[activo]
	if filaActivo == nil {
		t.Fatalf("el cliente activo %s debería seguir apareciendo en el selector", activo)
	}
	if porIDSelector[inactivo] != nil {
		t.Errorf("el selector solo debe traer clientes Activo, pero trajo al inactivo %s", inactivo)
	}
	// El selector es deliberadamente reducido: 3 campos, sin conteos.
	for _, campoDeGestion := range []string{"estado", "origen", "total_cotizaciones", "fecha_creacion"} {
		if _, existe := filaActivo[campoDeGestion]; existe {
			t.Errorf("el selector no debería incluir %q (es un campo de la pantalla de gestión): %#v", campoDeGestion, filaActivo)
		}
	}
	for _, campoEsperado := range []string{"cliente_id", "nombre_comercial", "razon_social"} {
		if _, existe := filaActivo[campoEsperado]; !existe {
			t.Errorf("el selector debería seguir trayendo %q: %#v", campoEsperado, filaActivo)
		}
	}

	_, resGestion := cliGetGestion(t, gestion, "busqueda="+token)
	porIDGestion := cliListadoPorID(t, resGestion)
	if porIDGestion[inactivo] == nil {
		t.Errorf("la pantalla de gestión sí debe mostrar clientes Inactivo, faltó %s", inactivo)
	}
	if porIDGestion[activo] == nil {
		t.Errorf("la pantalla de gestión debe mostrar también los Activo, faltó %s", activo)
	}
}

// TestClientesEditar_ConteoHistoricoNoBloqueaLaDesactivacion deja
// constancia de por qué el guard y el conteo del listado usan criterios
// distintos, con un caso concreto: el mismo cliente puede tener
// total_cotizaciones > 0 y aun así poder desactivarse, si todas están
// Perdida/Cancelada.
func TestClientesEditar_ConteoHistoricoNoBloqueaLaDesactivacion(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ClientesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	token := cliTokenUnico()
	cliente := cliCrearClientePrueba(t, pool, "Cliente "+token, nil, "COTIZA", "Activo")
	calculadora := cliCrearCalculadoraPrueba(t, pool)
	cliCrearCotizacionPrueba(t, pool, calculadora, cliente, "Perdida")
	cliCrearCotizacionPrueba(t, pool, calculadora, cliente, "Cancelada")

	_, res := cliGetGestion(t, handler, "busqueda="+token)
	fila := cliListadoPorID(t, res)[cliente]
	if fila == nil {
		t.Fatalf("el cliente %s no apareció en el listado", cliente)
	}
	if fila["total_cotizaciones"] != float64(2) {
		t.Errorf("total_cotizaciones es histórico y debería contar las 2 terminales, fue %v", fila["total_cotizaciones"])
	}

	rec, _ := cliPatchCliente(t, handler, actor, cliente, map[string]any{"estado": "Inactivo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tener cotizaciones históricas terminales no debe bloquear la desactivación, respondió %d: %s",
			rec.Code, rec.Body.String())
	}
	if fila := cliLeerEnBD(t, pool, cliente); fila.Estado != "Inactivo" {
		t.Errorf("el cliente debería haber quedado Inactivo, quedó %q", fila.Estado)
	}
}
