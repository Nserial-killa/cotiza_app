package handlers

// Pruebas de integración de la pantalla de Integraciones de API
// externo: POST/GET/PATCH /api/integraciones. Mismo criterio que
// usuarios_test.go: contra Postgres real, actor inyectado a mano en
// el contexto (los handlers leen middleware.UsuarioIDKey, no pasan
// por middleware.RequiereSesion en la prueba).

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func postIntegracion(t *testing.T, handler *IntegracionesHandler, actorID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/integraciones", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	handler.Crear(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func getIntegraciones(t *testing.T, handler *IntegracionesHandler, actorID string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/integraciones", nil)
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

// patchIntegracion monta un router de chi real porque Editar lee el
// {id} de la ruta con chi.URLParam.
func patchIntegracion(t *testing.T, handler *IntegracionesHandler, actorID, integracionID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Patch("/api/integraciones/{id}", handler.Editar)

	req := httptest.NewRequest(http.MethodPatch, "/api/integraciones/"+integracionID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func limpiarIntegracion(t *testing.T, pool *pgxpool.Pool, integracionID string) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM integraciones_api WHERE integracion_id::text = $1`, integracionID)
	})
}

func TestIntegracionesCrear_DevuelveClaveYGuardaSoloElHash(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	rec, res := postIntegracion(t, handler, admin, map[string]any{"nombre": "Bitrix24 Prueba"})
	if rec.Code != http.StatusCreated || res["ok"] != true {
		t.Fatalf("esperaba 201/ok, dio %d: %s", rec.Code, rec.Body.String())
	}
	integracionID, _ := res["integracion_id"].(string)
	if integracionID == "" {
		t.Fatal("integracion_id vino vacío en la respuesta")
	}
	limpiarIntegracion(t, pool, integracionID)

	apiKey, _ := res["api_key"].(string)
	if apiKey == "" {
		t.Fatal("api_key vino vacío en la respuesta de creación")
	}

	var hashGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT api_key_hash FROM integraciones_api WHERE integracion_id::text = $1`, integracionID).Scan(&hashGuardado); err != nil {
		t.Fatalf("no se pudo leer el hash guardado: %v", err)
	}
	if hashGuardado == apiKey {
		t.Fatal("la clave se guardó en texto plano, debería estar hasheada")
	}
	if bcrypt.CompareHashAndPassword([]byte(hashGuardado), []byte(apiKey)) != nil {
		t.Fatal("el hash guardado no corresponde a la clave devuelta")
	}
}

func TestIntegracionesCrear_VendedorNoPuedeCrear(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	vendedor := crearUsuarioPrueba(t, pool, "vendedor.integraciones."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")

	rec, res := postIntegracion(t, handler, vendedor, map[string]any{"nombre": "Intento de vendedor"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestIntegracionesCrear_SinNombre(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	rec, res := postIntegracion(t, handler, admin, map[string]any{"nombre": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestIntegracionesListar_NuncaExponeLaClave(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	_, res := postIntegracion(t, handler, admin, map[string]any{"nombre": "Integración para listar"})
	integracionID, _ := res["integracion_id"].(string)
	limpiarIntegracion(t, pool, integracionID)
	apiKey, _ := res["api_key"].(string)

	recListado, resListado := getIntegraciones(t, handler, admin)
	if recListado.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", recListado.Code, recListado.Body.String())
	}
	if resListado["ok"] != true {
		t.Fatal("esperaba ok:true")
	}
	cuerpo := recListado.Body.String()
	if apiKey != "" && bytes.Contains([]byte(cuerpo), []byte(apiKey)) {
		t.Fatal("el listado expone la clave en texto plano")
	}
	if bytes.Contains([]byte(cuerpo), []byte("api_key_hash")) {
		t.Fatal("el listado expone el campo api_key_hash")
	}

	integraciones, _ := resListado["integraciones"].([]any)
	encontrada := false
	for _, item := range integraciones {
		fila, _ := item.(map[string]any)
		if fila["integracion_id"] == integracionID {
			encontrada = true
			if fila["nombre"] != "Integración para listar" {
				t.Errorf("nombre = %v, esperaba 'Integración para listar'", fila["nombre"])
			}
			if fila["estado"] != "Activo" {
				t.Errorf("estado = %v, esperaba Activo", fila["estado"])
			}
		}
	}
	if !encontrada {
		t.Error("la integración creada no apareció en el listado")
	}
}

func TestIntegracionesListar_VendedorNoPuedeVer(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	vendedor := crearUsuarioPrueba(t, pool, "vendedor.listar.integraciones."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")

	rec, res := getIntegraciones(t, handler, vendedor)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestIntegracionesEditar_RevocaYLaClaveDejaDeFuncionar(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	_, res := postIntegracion(t, handler, admin, map[string]any{"nombre": "Integración a revocar"})
	integracionID, _ := res["integracion_id"].(string)
	limpiarIntegracion(t, pool, integracionID)

	rec, resPatch := patchIntegracion(t, handler, admin, integracionID, map[string]any{"estado": "Inactivo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200 al revocar, dio %d: %s", rec.Code, rec.Body.String())
	}
	if resPatch["estado"] != "Inactivo" {
		t.Errorf("estado = %v, esperaba Inactivo", resPatch["estado"])
	}

	var estadoGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM integraciones_api WHERE integracion_id::text = $1`, integracionID).Scan(&estadoGuardado); err != nil {
		t.Fatalf("no se pudo releer la integración: %v", err)
	}
	if estadoGuardado != "Inactivo" {
		t.Errorf("estado guardado = %q, esperaba Inactivo", estadoGuardado)
	}
}

// CRÍTICO: no se puede "reactivar" una integración desde este
// endpoint (para eso hay que crear una nueva) — solo Inactivo es un
// valor aceptado.
func TestIntegracionesEditar_NoPermiteReactivar(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	_, res := postIntegracion(t, handler, admin, map[string]any{"nombre": "Integración activa"})
	integracionID, _ := res["integracion_id"].(string)
	limpiarIntegracion(t, pool, integracionID)

	patchIntegracion(t, handler, admin, integracionID, map[string]any{"estado": "Inactivo"})

	rec, resPatch := patchIntegracion(t, handler, admin, integracionID, map[string]any{"estado": "Activo"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 al intentar reactivar, dio %d: %s", rec.Code, rec.Body.String())
	}
	if resPatch["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var estadoGuardado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM integraciones_api WHERE integracion_id::text = $1`, integracionID).Scan(&estadoGuardado); err != nil {
		t.Fatalf("no se pudo releer la integración: %v", err)
	}
	if estadoGuardado != "Inactivo" {
		t.Errorf("estado guardado = %q, no debió reactivarse", estadoGuardado)
	}
}

func TestIntegracionesEditar_VendedorNoPuedeRevocar(t *testing.T) {
	pool := setupTestDB(t)
	handler := &IntegracionesHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	vendedor := crearUsuarioPrueba(t, pool, "vendedor.revocar.integraciones."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")

	_, res := postIntegracion(t, handler, admin, map[string]any{"nombre": "Integración protegida"})
	integracionID, _ := res["integracion_id"].(string)
	limpiarIntegracion(t, pool, integracionID)

	rec, resPatch := patchIntegracion(t, handler, vendedor, integracionID, map[string]any{"estado": "Inactivo"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if resPatch["ok"] != false {
		t.Error("esperaba ok:false")
	}
}
