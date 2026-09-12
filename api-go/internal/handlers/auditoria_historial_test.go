package handlers

// Auditoría de QA — dimensión 9 (Audit Trail).
//
// cotizacion_historial es el rastro que la pantalla de Actividad del
// detalle le muestra al equipo comercial: qué pasó con una cotización,
// cuándo y quién lo hizo. Estas pruebas no se conforman con "existe
// una fila": comprueban el CONTENIDO de cada evento (accion exacta,
// versión, transición de estados, comentario y autor), porque una fila
// con la acción correcta pero sin autor o sin la transición es un
// rastro inútil para auditar después.
//
// Cubre los seis valores de `accion` que el sistema escribe hoy:
//   'creada'               → crearCotizacionEnTx (cotizaciones.go)
//   'CREAR_VERSION'        → CotizacionesHandler.CrearVersion
//   'CAMBIO_ESTADO'        → CotizacionesHandler.CambiarEstado
//   'valores_actualizados' → CotizadorRuntimeHandler.GuardarValores
//   'ENLACE_GENERADO'      → EnlacesPublicosHandler.GenerarEnlace
//   'VISTA_POR_CLIENTE'    → marcarVistaPorElCliente (enlaces_publicos.go)
//
// Los helpers de este archivo llevan prefijo `aud` y existen por una
// razón concreta: los helpers de los otros _test.go (postCotizacionSubruta,
// postEnlace, postValoresRuntime) NO inyectan actor de sesión, así que
// con ellos el usuario_id del historial siempre queda NULL y el "quién"
// no se puede verificar.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// audEvento es una fila de cotizacion_historial con todas las columnas
// que hacen al rastro de auditoría.
type audEvento struct {
	NumeroVersion  *int
	Accion         string
	EstadoAnterior *string
	EstadoNuevo    *string
	Comentario     *string
	UsuarioID      *string
	Fecha          time.Time
}

// audEventos devuelve los eventos de una cotización para una acción
// puntual, del más viejo al más nuevo.
func audEventos(t *testing.T, pool *pgxpool.Pool, cotizacionID, accion string) []audEvento {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT numero_version, accion, estado_anterior, estado_nuevo, comentario, usuario_id, fecha
		  FROM cotizacion_historial
		 WHERE cotizacion_id = $1 AND accion = $2
		 ORDER BY fecha, historial_id`, cotizacionID, accion)
	if err != nil {
		t.Fatalf("no se pudo leer el historial de %s: %v", cotizacionID, err)
	}
	defer rows.Close()

	eventos := make([]audEvento, 0)
	for rows.Next() {
		var ev audEvento
		if err := rows.Scan(&ev.NumeroVersion, &ev.Accion, &ev.EstadoAnterior, &ev.EstadoNuevo,
			&ev.Comentario, &ev.UsuarioID, &ev.Fecha); err != nil {
			t.Fatalf("no se pudo leer una fila de historial: %v", err)
		}
		eventos = append(eventos, ev)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("error recorriendo el historial: %v", err)
	}
	return eventos
}

// audEventoUnico exige que exista exactamente un evento de esa acción
// y lo devuelve. Que haya cero significa que la acción no se está
// auditando; que haya más de uno, que se está duplicando el rastro.
func audEventoUnico(t *testing.T, pool *pgxpool.Pool, cotizacionID, accion string) audEvento {
	t.Helper()
	eventos := audEventos(t, pool, cotizacionID, accion)
	if len(eventos) != 1 {
		t.Fatalf("se esperaba exactamente 1 evento %q en el historial de %s y hay %d: "+
			"sin esa fila la acción no queda auditada (o quedó duplicada)", accion, cotizacionID, len(eventos))
	}
	return eventos[0]
}

func audContarHistorial(t *testing.T, pool *pgxpool.Pool, cotizacionID, accion string) int {
	t.Helper()
	var cantidad int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM cotizacion_historial WHERE cotizacion_id = $1 AND accion = $2`,
		cotizacionID, accion,
	).Scan(&cantidad); err != nil {
		t.Fatalf("no se pudo contar el historial %q: %v", accion, err)
	}
	return cantidad
}

// audTexto describe un *string para los mensajes de error, sin
// confundir NULL con cadena vacía (la distinción importa: insertarHistorial
// usa NULLIF para dejar NULL a propósito).
func audTexto(valor *string) string {
	if valor == nil {
		return "NULL"
	}
	return "\"" + *valor + "\""
}

func audVersion(valor *int) string {
	if valor == nil {
		return "NULL"
	}
	return strconv.Itoa(*valor)
}

// audPostSubruta es postCotizacionSubruta + actor de sesión, para poder
// verificar el usuario_id que queda en el historial.
func audPostSubruta(t *testing.T, handler http.HandlerFunc, patron, cotizacionID, actorID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post(patron, handler)

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	ruta := "/api/cotizaciones/" + cotizacionID + "/" + patron[len("/api/cotizaciones/{id}/"):]
	req := httptest.NewRequest(http.MethodPost, ruta, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// audPostEnlace es postEnlace + actor de sesión. El de
// enlaces_publicos_test.go no inyecta actor, así que con ese helper
// nunca se puede comprobar quién generó el enlace.
func audPostEnlace(t *testing.T, handler *EnlacesPublicosHandler, cotizacionID, actorID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/cotizaciones/{id}/enlace", handler.GenerarEnlace)

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cotizaciones/"+cotizacionID+"/enlace", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// audPostValoresRuntime es postValoresRuntime + actor de sesión.
func audPostValoresRuntime(t *testing.T, fixture fixtureRuntime, actorID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/cotizador/runtime/{cotizacion_id}/valores", fixture.Handler.GuardarValores)

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cotizador/runtime/"+fixture.CotizacionID+"/valores", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// audCotizacionConActor crea el actor ANTES de la cotización a
// propósito: t.Cleanup corre en orden inverso, así la cotización (y su
// historial, por CASCADE) se borra antes que el usuario al que apunta
// la FK usuario_id.
func audCotizacionConActor(t *testing.T, estado string) (*pgxpool.Pool, string, string, *CotizacionesHandler) {
	t.Helper()
	pool := setupTestDB(t)
	actorID := crearAdminActorPrueba(t, pool)
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, estado, "", "")
	return pool, actorID, cotizacionID, &CotizacionesHandler{DB: pool}
}

func TestAuditoriaCreada_RegistraElAltaConSuActorYSinTransicion(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)

	rec, res := postCrearCotizacion(t, handler, actorID, map[string]any{
		"cliente_id": clienteID, "calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("alta: esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	limpiarCotizacionCreada(t, pool, cotizacionID)

	ev := audEventoUnico(t, pool, cotizacionID, "creada")
	if ev.NumeroVersion == nil || *ev.NumeroVersion != 1 {
		t.Errorf("el alta debe auditarse contra la versión 1 y quedó numero_version=%s", audVersion(ev.NumeroVersion))
	}
	// El alta no es una transición: no viene de ningún estado previo.
	if ev.EstadoAnterior != nil || ev.EstadoNuevo != nil {
		t.Errorf("el alta no debería registrar transición de estados y quedó anterior=%s nuevo=%s",
			audTexto(ev.EstadoAnterior), audTexto(ev.EstadoNuevo))
	}
	if ev.Comentario == nil || *ev.Comentario != "Cotización creada desde el Gestor." {
		t.Errorf("comentario del alta = %s, esperaba \"Cotización creada desde el Gestor.\" "+
			"(es lo que distingue un alta del Gestor de una que nace de una solicitud)", audTexto(ev.Comentario))
	}
	if ev.UsuarioID == nil || *ev.UsuarioID != actorID {
		t.Errorf("usuario_id del alta = %s, esperaba %q: sin autor no se puede auditar quién creó la cotización",
			audTexto(ev.UsuarioID), actorID)
	}
	if ev.Fecha.IsZero() {
		t.Error("el evento quedó sin fecha: la Actividad no podría ordenarlo")
	}
}

func TestAuditoriaCrearVersion_RegistraTransicionYResumenDeCambios(t *testing.T) {
	pool, actorID, cotizacionID, handler := audCotizacionConActor(t, "Enviada al Cliente")

	rec := audPostSubruta(t, handler.CrearVersion, "/api/cotizaciones/{id}/version", cotizacionID, actorID, map[string]any{
		"nombre_version":  "Versión con 10 licencias",
		"resumen_cambios": "Se agregaron 10 licencias",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear versión: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	ev := audEventoUnico(t, pool, cotizacionID, "CREAR_VERSION")
	if ev.NumeroVersion == nil || *ev.NumeroVersion != 2 {
		t.Errorf("la nueva versión debe auditarse como la 2 y quedó numero_version=%s", audVersion(ev.NumeroVersion))
	}
	if ev.EstadoAnterior == nil || *ev.EstadoAnterior != "Enviada al Cliente" {
		t.Errorf("estado_anterior = %s, esperaba \"Enviada al Cliente\": el rastro debe decir de qué estado se versionó",
			audTexto(ev.EstadoAnterior))
	}
	if ev.EstadoNuevo == nil || *ev.EstadoNuevo != "Borrador" {
		t.Errorf("estado_nuevo = %s, esperaba \"Borrador\": una versión nueva arranca en Borrador", audTexto(ev.EstadoNuevo))
	}
	if ev.Comentario == nil || *ev.Comentario != "Se agregaron 10 licencias" {
		t.Errorf("comentario = %s, esperaba el resumen de cambios que mandó el usuario", audTexto(ev.Comentario))
	}
	if ev.UsuarioID == nil || *ev.UsuarioID != actorID {
		t.Errorf("usuario_id = %s, esperaba %q: sin autor no se sabe quién versionó", audTexto(ev.UsuarioID), actorID)
	}
}

// TestAuditoriaCambioEstado_RegistraAccionEstadosComentarioYActor cierra
// un hueco puntual: el test que ya existía
// (TestCotizacionesCambiarEstado_ActualizaYRegistraHistorial) filtra por
// estado_nuevo y nunca comprueba que la accion sea 'CAMBIO_ESTADO', ni
// el estado de origen, ni el comentario, ni el autor.
func TestAuditoriaCambioEstado_RegistraAccionEstadosComentarioYActor(t *testing.T) {
	pool, actorID, cotizacionID, handler := audCotizacionConActor(t, "Enviada al Cliente")

	rec := audPostSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, actorID, map[string]any{
		"version": 1, "estado": "Vista por el Cliente", "comentario": "El cliente confirmó por teléfono",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("cambiar estado: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	ev := audEventoUnico(t, pool, cotizacionID, "CAMBIO_ESTADO")
	if ev.NumeroVersion == nil || *ev.NumeroVersion != 1 {
		t.Errorf("numero_version = %s, esperaba 1", audVersion(ev.NumeroVersion))
	}
	if ev.EstadoAnterior == nil || *ev.EstadoAnterior != "Enviada al Cliente" {
		t.Errorf("estado_anterior = %s, esperaba \"Enviada al Cliente\": sin el estado de origen no se puede reconstruir la transición",
			audTexto(ev.EstadoAnterior))
	}
	if ev.EstadoNuevo == nil || *ev.EstadoNuevo != "Vista por el Cliente" {
		t.Errorf("estado_nuevo = %s, esperaba \"Vista por el Cliente\"", audTexto(ev.EstadoNuevo))
	}
	if ev.Comentario == nil || *ev.Comentario != "El cliente confirmó por teléfono" {
		t.Errorf("comentario = %s, esperaba el que escribió el usuario: es la justificación del cambio", audTexto(ev.Comentario))
	}
	if ev.UsuarioID == nil || *ev.UsuarioID != actorID {
		t.Errorf("usuario_id = %s, esperaba %q: sin autor no se sabe quién movió el estado", audTexto(ev.UsuarioID), actorID)
	}
}

func TestAuditoriaValoresActualizados_RegistraCuantosValoresYQuienLosGuardo(t *testing.T) {
	pool := setupTestDB(t)
	actorID := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureRuntime(t)

	rec := audPostValoresRuntime(t, fixture, actorID, map[string]any{
		"version": 1,
		"valores": map[string]any{fixture.CampoID: "Daniel", fixture.CatalogoElID: fixture.ValorSistema},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar valores: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	ev := audEventoUnico(t, pool, fixture.CotizacionID, "valores_actualizados")
	if ev.NumeroVersion == nil || *ev.NumeroVersion != 1 {
		t.Errorf("numero_version = %s, esperaba 1", audVersion(ev.NumeroVersion))
	}
	if ev.EstadoAnterior != nil || ev.EstadoNuevo != nil {
		t.Errorf("guardar valores no cambia el estado, no debería registrar transición: anterior=%s nuevo=%s",
			audTexto(ev.EstadoAnterior), audTexto(ev.EstadoNuevo))
	}
	if ev.Comentario == nil || *ev.Comentario != "Se actualizaron 2 valor(es) del cotizador." {
		t.Errorf("comentario = %s, esperaba \"Se actualizaron 2 valor(es) del cotizador.\" "+
			"(la cantidad es lo único que distingue un guardado de otro en la Actividad)", audTexto(ev.Comentario))
	}
	if ev.UsuarioID == nil || *ev.UsuarioID != actorID {
		t.Errorf("usuario_id = %s, esperaba %q: sin autor no se sabe quién llenó el cotizador",
			audTexto(ev.UsuarioID), actorID)
	}
}

// TestAuditoriaEnlaceGenerado_RegistraQuienGeneroElEnlace cubre el
// evento que hasta ahora no tenía ninguna prueba: 'ENLACE_GENERADO'.
func TestAuditoriaEnlaceGenerado_RegistraQuienGeneroElEnlace(t *testing.T) {
	pool := setupTestDB(t)
	actorID := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureEnlace(t, pool)

	rec := audPostEnlace(t, fixture.Handler, fixture.CotizacionID, actorID, map[string]any{"version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("generar enlace: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	ev := audEventoUnico(t, pool, fixture.CotizacionID, "ENLACE_GENERADO")
	if ev.NumeroVersion == nil || *ev.NumeroVersion != 1 {
		t.Errorf("numero_version = %s, esperaba 1: el enlace se genera para una versión puntual", audVersion(ev.NumeroVersion))
	}
	if ev.EstadoAnterior != nil || ev.EstadoNuevo != nil {
		t.Errorf("generar el enlace no cambia el estado de la cotización: anterior=%s nuevo=%s",
			audTexto(ev.EstadoAnterior), audTexto(ev.EstadoNuevo))
	}
	if ev.Comentario == nil || *ev.Comentario != "Enlace público generado." {
		t.Errorf("comentario = %s, esperaba \"Enlace público generado.\"", audTexto(ev.Comentario))
	}
	if ev.UsuarioID == nil || *ev.UsuarioID != actorID {
		t.Errorf("usuario_id = %s, esperaba %q: compartir una cotización con el cliente es una acción "+
			"atribuible y tiene que quedar registrado quién la hizo", audTexto(ev.UsuarioID), actorID)
	}
}

// TestAuditoriaEnlaceGenerado_NoSeDuplicaAlReutilizarElEnlace: el
// handler reutiliza el enlace existente (ON CONFLICT) y solo audita
// cuando el token es nuevo. Sin esto, cada clic en "compartir" ensuciaría
// la Actividad con un evento que no representa nada nuevo.
func TestAuditoriaEnlaceGenerado_NoSeDuplicaAlReutilizarElEnlace(t *testing.T) {
	pool := setupTestDB(t)
	actorID := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureEnlace(t, pool)

	for intento := 1; intento <= 2; intento++ {
		rec := audPostEnlace(t, fixture.Handler, fixture.CotizacionID, actorID, map[string]any{"version": 1})
		if rec.Code != http.StatusOK {
			t.Fatalf("generar enlace (intento %d): esperaba 200, dio %d: %s", intento, rec.Code, rec.Body.String())
		}
	}

	if cantidad := audContarHistorial(t, pool, fixture.CotizacionID, "ENLACE_GENERADO"); cantidad != 1 {
		t.Errorf("hay %d eventos ENLACE_GENERADO y debería haber 1: reutilizar un enlace ya generado "+
			"no es un hecho nuevo que auditar", cantidad)
	}
}

// TestAuditoriaVistaPorCliente_RegistraLaTransicionSinAutor: el
// visitante del enlace público es anónimo, así que el evento tiene que
// quedar con usuario_id NULL (no cadena vacía) — es el comportamiento
// deliberado del NULLIF de insertarHistorial.
func TestAuditoriaVistaPorCliente_RegistraLaTransicionSinAutor(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	var token string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version)
		VALUES ($1, $2, 1) RETURNING token`,
		"tok-aud-"+sufijoUnico(), fixture.CotizacionID,
	).Scan(&token); err != nil {
		t.Fatalf("no se pudo crear el enlace de prueba: %v", err)
	}

	rec := getPublico(t, fixture.Handler, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("abrir el enlace público: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	ev := audEventoUnico(t, pool, fixture.CotizacionID, "VISTA_POR_CLIENTE")
	if ev.NumeroVersion == nil || *ev.NumeroVersion != 1 {
		t.Errorf("numero_version = %s, esperaba 1", audVersion(ev.NumeroVersion))
	}
	if ev.EstadoAnterior == nil || *ev.EstadoAnterior != "Enviada al Cliente" {
		t.Errorf("estado_anterior = %s, esperaba \"Enviada al Cliente\"", audTexto(ev.EstadoAnterior))
	}
	if ev.EstadoNuevo == nil || *ev.EstadoNuevo != "Vista por el Cliente" {
		t.Errorf("estado_nuevo = %s, esperaba \"Vista por el Cliente\"", audTexto(ev.EstadoNuevo))
	}
	if ev.Comentario == nil || *ev.Comentario != "Abierta desde el enlace público." {
		t.Errorf("comentario = %s, esperaba \"Abierta desde el enlace público.\"", audTexto(ev.Comentario))
	}
	if ev.UsuarioID != nil {
		t.Errorf("usuario_id = %s, esperaba NULL: quien abre el enlace es el cliente externo, no un usuario "+
			"de Cotiza, y atribuirle el evento a alguien sería un rastro falso", audTexto(ev.UsuarioID))
	}
}

// TestAuditoriaVistaPorCliente_SoloSeRegistraLaPrimeraApertura: la
// segunda visita ya no encuentra la versión en "Enviada al Cliente", así
// que no debe volver a auditar. Si se duplicara, la Actividad diría que
// el cliente "vio la propuesta por primera vez" varias veces.
func TestAuditoriaVistaPorCliente_SoloSeRegistraLaPrimeraApertura(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureEnlace(t, pool)

	var token string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO cotizacion_enlaces_publicos (token, cotizacion_id, version)
		VALUES ($1, $2, 1) RETURNING token`,
		"tok-aud2-"+sufijoUnico(), fixture.CotizacionID,
	).Scan(&token); err != nil {
		t.Fatalf("no se pudo crear el enlace de prueba: %v", err)
	}

	for intento := 1; intento <= 2; intento++ {
		rec := getPublico(t, fixture.Handler, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("abrir el enlace (intento %d): esperaba 200, dio %d: %s", intento, rec.Code, rec.Body.String())
		}
	}

	if cantidad := audContarHistorial(t, pool, fixture.CotizacionID, "VISTA_POR_CLIENTE"); cantidad != 1 {
		t.Errorf("hay %d eventos VISTA_POR_CLIENTE y debería haber 1: solo la primera apertura es un hecho nuevo", cantidad)
	}
}

// TestAuditoriaHistorial_ElDetalleDevuelveLaSecuenciaCompleta comprueba
// el otro extremo: que los eventos auditados lleguen efectivamente a la
// pantalla de Actividad (GET /api/cotizaciones/{id}).
//
// No se afirma un orden puntual entre eventos creados uno detrás del
// otro —dos transacciones consecutivas pueden compartir el valor de
// now() y eso volvería la prueba dependiente del timing—; se afirma lo
// que sí es determinista: que están los tres eventos, con su versión, y
// que la lista viene ordenada de más nueva a más vieja.
func TestAuditoriaHistorial_ElDetalleDevuelveLaSecuenciaCompleta(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actorID := crearAdminActorPrueba(t, pool)
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)

	rec, res := postCrearCotizacion(t, handler, actorID, map[string]any{
		"cliente_id": clienteID, "calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("alta: esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	limpiarCotizacionCreada(t, pool, cotizacionID)

	if rec := audPostSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, actorID, map[string]any{
		"version": 1, "estado": "Revisión Comercial", "comentario": "Pasa a revisión",
	}); rec.Code != http.StatusOK {
		t.Fatalf("cambiar estado: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if rec := audPostSubruta(t, handler.CrearVersion, "/api/cotizaciones/{id}/version", cotizacionID, actorID, map[string]any{
		"nombre_version": "Segunda propuesta",
	}); rec.Code != http.StatusOK {
		t.Fatalf("crear versión: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	detalle := getDetalleCotizacion(t, handler, actorID, cotizacionID, "")
	if detalle.Code != http.StatusOK {
		t.Fatalf("detalle: esperaba 200, dio %d: %s", detalle.Code, detalle.Body.String())
	}
	var respuesta struct {
		Historial []struct {
			Accion        string    `json:"accion"`
			NombreUsuario *string   `json:"nombre_usuario"`
			Fecha         time.Time `json:"fecha"`
		} `json:"historial"`
	}
	assertJSON(t, detalle.Body.Bytes(), &respuesta)

	vistos := map[string]bool{}
	for _, ev := range respuesta.Historial {
		vistos[ev.Accion] = true
		if ev.NombreUsuario == nil {
			t.Errorf("el evento %q llegó a la Actividad sin nombre de usuario: la pantalla no puede mostrar quién lo hizo", ev.Accion)
		}
	}
	for _, esperada := range []string{"creada", "CAMBIO_ESTADO", "CREAR_VERSION"} {
		if !vistos[esperada] {
			t.Errorf("la Actividad del detalle no incluye el evento %q (llegaron %d eventos): "+
				"el rastro existe en la tabla pero no se está mostrando", esperada, len(respuesta.Historial))
		}
	}
	for i := 1; i < len(respuesta.Historial); i++ {
		if respuesta.Historial[i].Fecha.After(respuesta.Historial[i-1].Fecha) {
			t.Errorf("la Actividad no viene ordenada de más nueva a más vieja: %q (%s) quedó antes de %q (%s)",
				respuesta.Historial[i-1].Accion, respuesta.Historial[i-1].Fecha,
				respuesta.Historial[i].Accion, respuesta.Historial[i].Fecha)
		}
	}
}
