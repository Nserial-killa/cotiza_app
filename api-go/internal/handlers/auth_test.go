package handlers

// Pruebas de integración de POST /api/auth/login. Requieren Postgres
// corriendo con DATABASE_URL seteada — ver setupTestDB en
// helpers_test.go. Se saltan solas si no está disponible.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"cotiza/api/internal/middleware"
)

func postLogin(t *testing.T, handler *AuthHandler, body map[string]string) (*httptest.ResponseRecorder, loginResponse) {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body de la petición: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Login(rec, req)

	var res loginResponse
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func TestLogin_CredencialesValidas(t *testing.T) {
	pool := setupTestDB(t)
	correo := "prueba.login.ok@exceltecgroup.com"
	crearUsuarioPrueba(t, pool, correo, "9999", "Vendedor", "Activo")

	handler := &AuthHandler{DB: pool}
	rec, res := postLogin(t, handler, map[string]string{"correo": correo, "pin": "9999"})

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d", rec.Code)
	}
	if !res.OK || res.Usuario == nil {
		t.Fatalf("esperaba ok:true con usuario, dio %+v", res)
	}
	if res.Usuario.Correo != correo {
		t.Errorf("correo devuelto = %q, esperaba %q", res.Usuario.Correo, correo)
	}
	if res.Usuario.Rol != "Vendedor" {
		t.Errorf("rol devuelto = %q, esperaba %q", res.Usuario.Rol, "Vendedor")
	}
	if res.Usuario.UsuarioID == "" {
		t.Error("usuario_id vino vacío en la respuesta")
	}
}

func TestLogin_PinIncorrecto(t *testing.T) {
	pool := setupTestDB(t)
	correo := "prueba.login.pin.malo@exceltecgroup.com"
	crearUsuarioPrueba(t, pool, correo, "9999", "Vendedor", "Activo")

	handler := &AuthHandler{DB: pool}
	rec, res := postLogin(t, handler, map[string]string{"correo": correo, "pin": "0000"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, dio %d", rec.Code)
	}
	if res.OK {
		t.Fatal("esperaba ok:false con PIN incorrecto")
	}
	if res.Error != mensajeCredencialesInvalidas {
		t.Errorf("mensaje = %q, esperaba el genérico %q", res.Error, mensajeCredencialesInvalidas)
	}
}

// El mensaje de correo inexistente debe ser IDÉNTICO al de PIN
// incorrecto — es la mitigación contra enumeración de usuarios que
// CLAUDE.md documenta explícitamente. Si este test falla, alguien
// rompió esa garantía de seguridad, no solo un detalle cosmético.
func TestLogin_CorreoInexistente(t *testing.T) {
	pool := setupTestDB(t)
	handler := &AuthHandler{DB: pool}
	rec, res := postLogin(t, handler, map[string]string{
		"correo": "no.existe.nadie.con.este.correo@exceltecgroup.com",
		"pin":    "1234",
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, dio %d", rec.Code)
	}
	if res.Error != mensajeCredencialesInvalidas {
		t.Errorf("mensaje = %q, esperaba el mismo genérico que PIN incorrecto (%q)", res.Error, mensajeCredencialesInvalidas)
	}
}

// Mismo criterio que el test anterior: un usuario inactivo no debe
// distinguirse de un PIN incorrecto en la respuesta.
func TestLogin_UsuarioInactivo(t *testing.T) {
	pool := setupTestDB(t)
	correo := "prueba.login.inactivo@exceltecgroup.com"
	crearUsuarioPrueba(t, pool, correo, "9999", "Vendedor", "Inactivo")

	handler := &AuthHandler{DB: pool}
	rec, res := postLogin(t, handler, map[string]string{"correo": correo, "pin": "9999"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, dio %d", rec.Code)
	}
	if res.Error != mensajeCredencialesInvalidas {
		t.Errorf("mensaje = %q, esperaba el genérico — no debe delatar que la cuenta existe pero está inactiva", res.Error)
	}
}

// El handler normaliza el correo a minúsculas antes de consultar; el
// usuario debería poder escribir su correo con cualquier capitalización.
func TestLogin_CorreoInsensibleAMayusculas(t *testing.T) {
	pool := setupTestDB(t)
	correoGuardado := "prueba.mayus@exceltecgroup.com"
	crearUsuarioPrueba(t, pool, correoGuardado, "9999", "Vendedor", "Activo")

	handler := &AuthHandler{DB: pool}
	_, res := postLogin(t, handler, map[string]string{
		"correo": "Prueba.Mayus@ExcelTecGroup.COM",
		"pin":    "9999",
	})

	if !res.OK {
		t.Errorf("el login debería ser insensible a mayúsculas en el correo, dio: %+v", res)
	}
}

func TestLogin_CamposFaltantes(t *testing.T) {
	pool := setupTestDB(t)
	handler := &AuthHandler{DB: pool}

	casos := map[string]map[string]string{
		"sin correo":              {"correo": "", "pin": "1234"},
		"sin pin":                 {"correo": "algo@exceltecgroup.com", "pin": ""},
		"vacio":                   {},
		"solo espacios en correo": {"correo": "   ", "pin": "1234"},
	}
	for nombre, body := range casos {
		t.Run(nombre, func(t *testing.T) {
			rec, res := postLogin(t, handler, body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("esperaba 400, dio %d", rec.Code)
			}
			if res.OK {
				t.Error("esperaba ok:false")
			}
		})
	}
}

func TestLogin_CuerpoJSONInvalido(t *testing.T) {
	pool := setupTestDB(t)
	handler := &AuthHandler{DB: pool}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader([]byte("esto no es json")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.Login(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con cuerpo no-JSON, dio %d", rec.Code)
	}
}

// El PIN nunca debe aparecer en la respuesta, ni el hash. Es un
// chequeo barato que vale la pena tener como red de seguridad si
// alguien agrega un campo nuevo a usuarioSesion sin querer.
func TestLogin_RespuestaNuncaExponeElPin(t *testing.T) {
	pool := setupTestDB(t)
	correo := "prueba.no.filtrar.pin@exceltecgroup.com"
	crearUsuarioPrueba(t, pool, correo, "secreto123", "Vendedor", "Activo")

	handler := &AuthHandler{DB: pool}
	rec, _ := postLogin(t, handler, map[string]string{"correo": correo, "pin": "secreto123"})

	if strings.Contains(rec.Body.String(), "secreto123") {
		t.Fatal("la respuesta del login contiene el PIN en texto plano — no debería aparecer nunca")
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "pin_hash") || strings.Contains(rec.Body.String(), "$2") {
		t.Fatal("la respuesta del login parece incluir el hash del PIN — no debería exponerse")
	}
}

// El login exitoso debe devolver un token, y ese token debe existir
// de verdad en la tabla sesiones, apuntando al usuario correcto y con
// vencimiento futuro (Sprint 3: sesión + autorización).
func TestLogin_DevuelveTokenValido(t *testing.T) {
	pool := setupTestDB(t)
	correo := "prueba.login.token@exceltecgroup.com"
	usuarioID := crearUsuarioPrueba(t, pool, correo, "9999", "Vendedor", "Activo")

	handler := &AuthHandler{DB: pool}
	rec, res := postLogin(t, handler, map[string]string{"correo": correo, "pin": "9999"})

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d", rec.Code)
	}
	if res.Token == "" {
		t.Fatal("el login no devolvió token")
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM sesiones WHERE token = $1`, res.Token)
	})

	var usuarioSesionID string
	var fechaExpiracion time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT usuario_id, fecha_expiracion FROM sesiones WHERE token = $1`, res.Token,
	).Scan(&usuarioSesionID, &fechaExpiracion)
	if err != nil {
		t.Fatalf("el token del login no existe en la tabla sesiones: %v", err)
	}
	if usuarioSesionID != usuarioID {
		t.Errorf("usuario_id de la sesión = %q, esperaba %q", usuarioSesionID, usuarioID)
	}
	if !fechaExpiracion.After(time.Now()) {
		t.Errorf("fecha_expiracion = %v, esperaba un vencimiento futuro", fechaExpiracion)
	}
}

// Logout debe borrar la sesión de la base; un pedido posterior con
// ese mismo token, contra un endpoint protegido de verdad (con
// middleware.RequiereSesion de por medio, no solo el handler a pelo),
// debe volver a dar 401.
func TestLogout_InvalidaElToken(t *testing.T) {
	pool := setupTestDB(t)
	correo := "prueba.logout@exceltecgroup.com"
	crearUsuarioPrueba(t, pool, correo, "9999", "Vendedor", "Activo")

	handler := &AuthHandler{DB: pool}
	_, res := postLogin(t, handler, map[string]string{"correo": correo, "pin": "9999"})
	if res.Token == "" {
		t.Fatal("el login no devolvió token")
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM sesiones WHERE token = $1`, res.Token)
	})

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.RequiereSesion(pool))
		r.Delete("/api/auth/logout", handler.Logout)
		r.Get("/api/protegido", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	})

	// Antes de logout, el token todavía funciona.
	req := httptest.NewRequest(http.MethodGet, "/api/protegido", nil)
	req.Header.Set("Authorization", "Bearer "+res.Token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("antes de logout esperaba 200, dio %d", rec.Code)
	}

	// Logout.
	req = httptest.NewRequest(http.MethodDelete, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+res.Token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var existe bool
	pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM sesiones WHERE token = $1)`, res.Token).Scan(&existe)
	if existe {
		t.Error("la sesión debería haberse borrado de la base tras el logout")
	}

	// El mismo token, después de logout, ya no debe servir.
	req = httptest.NewRequest(http.MethodGet, "/api/protegido", nil)
	req.Header.Set("Authorization", "Bearer "+res.Token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("después de logout esperaba 401, dio %d", rec.Code)
	}
}
