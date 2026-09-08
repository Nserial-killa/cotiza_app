package handlers

// Pruebas de integración de GET/PATCH /api/solicitudes y POST
// /api/solicitudes/{id}/convertir — la pantalla del equipo de Cotiza
// sobre las solicitudes que llegaron por API externo. Mismo criterio
// que el resto del paquete: Postgres real, fixtures propias.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// crearSolicitudPrueba inserta una solicitud descartable directo en
// la base (equivalente a lo que dejaría POST /api/externo/solicitudes),
// sin depender de ese endpoint para no acoplar ambos conjuntos de
// pruebas entre sí.
func crearSolicitudPrueba(t *testing.T, pool *pgxpool.Pool, clienteNombre, clienteID, calculadoraID, estado string) string {
	t.Helper()
	var clienteIDValor, calculadoraIDValor any
	if clienteID != "" {
		clienteIDValor = clienteID
	}
	if calculadoraID != "" {
		calculadoraIDValor = calculadoraID
	}
	var solicitudID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO solicitudes (origen, cliente_id, cliente_nombre, calculadora_id, estado)
		VALUES ('API_EXTERNA', $1, $2, $3, $4)
		RETURNING solicitud_id::text`,
		clienteIDValor, clienteNombre, calculadoraIDValor, estado,
	).Scan(&solicitudID)
	if err != nil {
		t.Fatalf("no se pudo crear la solicitud de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID)
	})
	return solicitudID
}

func getSolicitudes(t *testing.T, handler *SolicitudesHandler, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/solicitudes"+query, nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func patchSolicitud(t *testing.T, handler *SolicitudesHandler, solicitudID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Patch("/api/solicitudes/{id}", handler.CambiarEstado)

	req := httptest.NewRequest(http.MethodPatch, "/api/solicitudes/"+solicitudID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func postConvertirSolicitud(t *testing.T, handler *SolicitudesHandler, actorID, solicitudID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Post("/api/solicitudes/{id}/convertir", handler.Convertir)

	req := httptest.NewRequest(http.MethodPost, "/api/solicitudes/"+solicitudID+"/convertir", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func TestSolicitudesListar_FiltraPorEstado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &SolicitudesHandler{DB: pool}
	nueva := crearSolicitudPrueba(t, pool, "Cliente Nueva", "", "", "Nueva")
	descartada := crearSolicitudPrueba(t, pool, "Cliente Descartada", "", "", "Descartada")

	rec, res := getSolicitudes(t, handler, "?estado=Nueva")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	solicitudes, _ := res["solicitudes"].([]any)
	ids := map[string]bool{}
	for _, item := range solicitudes {
		fila, _ := item.(map[string]any)
		ids[fila["solicitud_id"].(string)] = true
	}
	if !ids[nueva] {
		t.Error("la solicitud en estado Nueva debió aparecer en el filtro estado=Nueva")
	}
	if ids[descartada] {
		t.Error("la solicitud Descartada no debió aparecer en el filtro estado=Nueva")
	}
}

func TestSolicitudesListar_EstadoInvalidoRechaza(t *testing.T) {
	pool := setupTestDB(t)
	handler := &SolicitudesHandler{DB: pool}

	rec, res := getSolicitudes(t, handler, "?estado=Inventado")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestSolicitudesCambiarEstado_AEnRevisionYDescartada(t *testing.T) {
	pool := setupTestDB(t)
	handler := &SolicitudesHandler{DB: pool}
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente Revisión", "", "", "Nueva")

	rec, res := patchSolicitud(t, handler, solicitudID, map[string]any{"estado": "En revisión"})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["estado"] != "En revisión" {
		t.Errorf("estado = %v, esperaba 'En revisión'", res["estado"])
	}

	rec2, _ := patchSolicitud(t, handler, solicitudID, map[string]any{"estado": "Descartada"})
	if rec2.Code != http.StatusOK {
		t.Fatalf("esperaba 200 al descartar, dio %d: %s", rec2.Code, rec2.Body.String())
	}

	var estadoGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID).Scan(&estadoGuardado); err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if estadoGuardado != "Descartada" {
		t.Errorf("estado guardado = %q, esperaba Descartada", estadoGuardado)
	}
}

// CRÍTICO: no se puede mover una solicitud a 'Convertida' a mano —
// eso es exclusivo de Convertir.
func TestSolicitudesCambiarEstado_NoPermiteConvertidaAMano(t *testing.T) {
	pool := setupTestDB(t)
	handler := &SolicitudesHandler{DB: pool}
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente Intento Convertida", "", "", "Nueva")

	rec, res := patchSolicitud(t, handler, solicitudID, map[string]any{"estado": "Convertida"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var estadoGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID).Scan(&estadoGuardado); err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if estadoGuardado != "Nueva" {
		t.Errorf("estado guardado = %q, no debió cambiar", estadoGuardado)
	}
}

func TestSolicitudesCambiarEstado_NoSePuedeCambiarUnaYaConvertida(t *testing.T) {
	pool := setupTestDB(t)
	handler := &SolicitudesHandler{DB: pool}
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente Ya Convertida", "", "", "Convertida")

	rec, res := patchSolicitud(t, handler, solicitudID, map[string]any{"estado": "En revisión"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestSolicitudesConvertir_CreaClienteNuevoYCotizacionConCalculadoraCorrecta(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionesHandler := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizacionesHandler}
	actor := crearAdminActorPrueba(t, pool)
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)
	nombreCliente := "Cliente Nuevo Desde Solicitud " + sufijoUnico()
	solicitudID := crearSolicitudPrueba(t, pool, nombreCliente, "", calculadoraID, "Nueva")

	rec, res := postConvertirSolicitud(t, handler, actor, solicitudID, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != true {
		t.Fatal("esperaba ok:true")
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	if cotizacionID == "" {
		t.Fatal("cotizacion_id vino vacío en la respuesta")
	}
	limpiarCotizacionCreada(t, pool, cotizacionID)

	var clienteIDGuardado, calculadoraGuardada string
	err := pool.QueryRow(context.Background(), `SELECT cliente_id, calculadora_id FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&clienteIDGuardado, &calculadoraGuardada)
	if err != nil {
		t.Fatalf("no se pudo releer la cotización generada: %v", err)
	}
	if calculadoraGuardada != calculadoraID {
		t.Errorf("calculadora_id de la cotización = %q, esperaba %q", calculadoraGuardada, calculadoraID)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=$1`, clienteIDGuardado) })

	var nombreClienteGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT nombre_comercial FROM clientes WHERE cliente_id = $1`, clienteIDGuardado).Scan(&nombreClienteGuardado); err != nil {
		t.Fatalf("no se pudo releer el cliente generado: %v", err)
	}
	if nombreClienteGuardado != nombreCliente {
		t.Errorf("el cliente creado tiene nombre %q, esperaba %q", nombreClienteGuardado, nombreCliente)
	}

	var estadoSolicitud, cotizacionGeneradaGuardada string
	err = pool.QueryRow(context.Background(), `SELECT estado, cotizacion_id_generada FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID).Scan(&estadoSolicitud, &cotizacionGeneradaGuardada)
	if err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if estadoSolicitud != "Convertida" {
		t.Errorf("estado de la solicitud = %q, esperaba Convertida", estadoSolicitud)
	}
	if cotizacionGeneradaGuardada != cotizacionID {
		t.Errorf("cotizacion_id_generada = %q, esperaba %q", cotizacionGeneradaGuardada, cotizacionID)
	}
}

func TestSolicitudesConvertir_UsaClienteExistenteSiLaSolicitudYaLoTraia(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionesHandler := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizacionesHandler}
	actor := crearAdminActorPrueba(t, pool)
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)
	solicitudID := crearSolicitudPrueba(t, pool, "Nombre que no debería usarse", clienteID, calculadoraID, "Nueva")

	rec, res := postConvertirSolicitud(t, handler, actor, solicitudID, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	limpiarCotizacionCreada(t, pool, cotizacionID)

	var clienteGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT cliente_id FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&clienteGuardado); err != nil {
		t.Fatalf("no se pudo releer la cotización: %v", err)
	}
	if clienteGuardado != clienteID {
		t.Errorf("cliente_id de la cotización = %q, esperaba reusar el existente %q", clienteGuardado, clienteID)
	}
}

func TestSolicitudesConvertir_RechazaSinCalculadoraIDNiEnLaSolicitudNiEnElBody(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionesHandler := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizacionesHandler}
	actor := crearAdminActorPrueba(t, pool)
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente Sin Cotizador", "", "", "Nueva")

	rec, res := postConvertirSolicitud(t, handler, actor, solicitudID, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var estadoGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID).Scan(&estadoGuardado); err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if estadoGuardado != "Nueva" {
		t.Errorf("estado guardado = %q, no debió convertirse sin cotizador", estadoGuardado)
	}
}

func TestSolicitudesConvertir_CalculadoraIDAlternativaEnElBodyFunciona(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionesHandler := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizacionesHandler}
	actor := crearAdminActorPrueba(t, pool)
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente Con Cotizador Alternativo", "", "", "Nueva")

	rec, res := postConvertirSolicitud(t, handler, actor, solicitudID, map[string]any{"calculadora_id": calculadoraID})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	cotizacionID, _ := res["cotizacion_id"].(string)
	limpiarCotizacionCreada(t, pool, cotizacionID)

	var clienteGuardado, calculadoraGuardada string
	err := pool.QueryRow(context.Background(), `SELECT cliente_id, calculadora_id FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&clienteGuardado, &calculadoraGuardada)
	if err != nil {
		t.Fatalf("no se pudo releer la cotización: %v", err)
	}
	if calculadoraGuardada != calculadoraID {
		t.Errorf("calculadora_id = %q, esperaba %q", calculadoraGuardada, calculadoraID)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=$1`, clienteGuardado) })
}

func TestSolicitudesConvertir_NoSePuedeConvertirDosVeces(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionesHandler := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizacionesHandler}
	actor := crearAdminActorPrueba(t, pool)
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente Doble Conversión", "", calculadoraID, "Nueva")

	rec1, res1 := postConvertirSolicitud(t, handler, actor, solicitudID, map[string]any{})
	if rec1.Code != http.StatusOK {
		t.Fatalf("esperaba 200 en la primera conversión, dio %d: %s", rec1.Code, rec1.Body.String())
	}
	cotizacionID, _ := res1["cotizacion_id"].(string)
	limpiarCotizacionCreada(t, pool, cotizacionID)
	var clienteGuardado string
	pool.QueryRow(context.Background(), `SELECT cliente_id FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&clienteGuardado)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=$1`, clienteGuardado) })

	rec2, res2 := postConvertirSolicitud(t, handler, actor, solicitudID, map[string]any{})
	if rec2.Code != http.StatusConflict {
		t.Fatalf("esperaba 409 al intentar convertir de nuevo, dio %d: %s", rec2.Code, rec2.Body.String())
	}
	if res2["ok"] != false {
		t.Error("esperaba ok:false")
	}
}
