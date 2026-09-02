package handlers

// Pruebas de integración de la pantalla "Usuarios y Permisos":
// GET/POST /api/usuarios, PATCH /api/usuarios/{id}, GET /api/roles.
// Mismo criterio que auth_test.go: contra Postgres real, con
// fixtures propios que se limpian solos.
//
// Crear/Editar exigen sesión y, desde el cierre del Sprint 4,
// permisos por rol — los handlers leen el actor desde
// middleware.UsuarioIDKey en el contexto de la petición (lo que deja
// el middleware real en producción). Como estas pruebas llaman a los
// handlers directo, sin pasar por middleware.RequiereSesion, hay que
// simular esa parte: postUsuario/patchUsuario reciben el usuario_id
// del actor y lo inyectan en el contexto ellos mismos.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

// crearAdminActorPrueba crea un usuario Administrador descartable
// para usar como actor de sesión en las pruebas que necesitan
// permisos completos.
func crearAdminActorPrueba(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	correo := "admin.actor." + sufijoUnico() + "@exceltecgroup.com"
	return crearUsuarioPrueba(t, pool, correo, "0000", "Administrador", "Activo")
}

func conActor(req *http.Request, actorID string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), middleware.UsuarioIDKey, actorID))
}

func postUsuario(t *testing.T, handler *UsuariosHandler, actorID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/usuarios", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	handler.Crear(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

// patchUsuario monta un router de chi real (en vez de llamar a
// handler.Editar directo) porque Editar lee el {id} de la ruta con
// chi.URLParam — sin un router de por medio ese valor viene vacío.
func patchUsuario(t *testing.T, handler *UsuariosHandler, actorID, usuarioID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}

	router := chi.NewRouter()
	router.Patch("/api/usuarios/{id}", handler.Editar)

	req := httptest.NewRequest(http.MethodPatch, "/api/usuarios/"+usuarioID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func getUsuarios(t *testing.T, handler *UsuariosHandler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/usuarios", nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func TestUsuariosCrear_AparecceEnElListado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	correo := "prueba.crear." + sufijoUnico() + "@exceltecgroup.com"

	rec, res := postUsuario(t, handler, admin, map[string]any{
		"nombre": "Usuario Nuevo",
		"correo": correo,
		"pin":    "4321",
		"rol":    "Vendedor",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	usuario, ok := res["usuario"].(map[string]any)
	if !ok {
		t.Fatalf("esperaba un objeto 'usuario' en la respuesta, dio: %+v", res)
	}
	usuarioID, _ := usuario["usuario_id"].(string)
	if usuarioID == "" {
		t.Fatal("usuario_id vino vacío en la respuesta de creación")
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM usuarios WHERE usuario_id = $1`, usuarioID)
	})

	if usuario["estado"] != "Activo" {
		t.Errorf("estado por defecto = %v, esperaba Activo", usuario["estado"])
	}

	_, listado := getUsuarios(t, handler)
	usuarios, _ := listado["usuarios"].([]any)
	encontrado := false
	for _, u := range usuarios {
		fila, _ := u.(map[string]any)
		if fila["usuario_id"] == usuarioID {
			encontrado = true
			if fila["correo"] != correo {
				t.Errorf("correo en el listado = %v, esperaba %q", fila["correo"], correo)
			}
			if fila["rol"] != "Vendedor" {
				t.Errorf("rol en el listado = %v, esperaba Vendedor", fila["rol"])
			}
		}
	}
	if !encontrado {
		t.Errorf("el usuario creado %q no apareció en GET /api/usuarios", usuarioID)
	}
}

// El PIN (ni su hash) debe aparecer nunca en la respuesta de creación
// ni en el listado — la pantalla no tiene por qué verlo jamás.
func TestUsuarios_NuncaExponeElPin(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	correo := "prueba.pin." + sufijoUnico() + "@exceltecgroup.com"

	rec, res := postUsuario(t, handler, admin, map[string]any{
		"nombre": "Usuario Pin",
		"correo": correo,
		"pin":    "secreto999",
		"rol":    "Vendedor",
	})
	usuario, _ := res["usuario"].(map[string]any)
	usuarioID, _ := usuario["usuario_id"].(string)
	if usuarioID != "" {
		t.Cleanup(func() {
			pool.Exec(context.Background(), `DELETE FROM usuarios WHERE usuario_id = $1`, usuarioID)
		})
	}

	if strings.Contains(rec.Body.String(), "secreto999") {
		t.Fatal("la respuesta de creación contiene el PIN en texto plano")
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "pin_hash") {
		t.Fatal("la respuesta de creación expone el campo pin_hash")
	}

	recListado, _ := getUsuarios(t, handler)
	if strings.Contains(strings.ToLower(recListado.Body.String()), "pin_hash") || strings.Contains(recListado.Body.String(), "$2") {
		t.Fatal("el listado de usuarios expone pin_hash o algo con forma de hash bcrypt")
	}
}

func TestUsuariosCrear_RolInexistente(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	correo := "prueba.rol.malo." + sufijoUnico() + "@exceltecgroup.com"

	rec, res := postUsuario(t, handler, admin, map[string]any{
		"nombre": "Usuario Rol Malo",
		"correo": correo,
		"pin":    "1234",
		"rol":    "Superadministrador Inventado",
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con rol inexistente, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	// No debería haber quedado un usuario a medias en la base.
	var existe bool
	err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM usuarios WHERE correo = $1)`, correo).Scan(&existe)
	if err != nil {
		t.Fatalf("no se pudo verificar que el usuario no se creó: %v", err)
	}
	if existe {
		t.Error("se creó un usuario con un rol que no existe en la tabla roles")
	}
}

func TestUsuariosCrear_CamposFaltantes(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	casos := map[string]map[string]any{
		"sin nombre": {"correo": "algo@exceltecgroup.com", "pin": "1234", "rol": "Vendedor"},
		"sin correo": {"nombre": "Alguien", "pin": "1234", "rol": "Vendedor"},
		"sin pin":    {"nombre": "Alguien", "correo": "algo2@exceltecgroup.com", "rol": "Vendedor"},
		"sin rol":    {"nombre": "Alguien", "correo": "algo3@exceltecgroup.com", "pin": "1234"},
	}
	for nombre, body := range casos {
		t.Run(nombre, func(t *testing.T) {
			rec, res := postUsuario(t, handler, admin, body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
			}
			if res["ok"] != false {
				t.Error("esperaba ok:false")
			}
		})
	}
}

func TestUsuariosCrear_CorreoFormatoInvalido(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	casos := []string{"sin-arroba.com", "@sinlocal.com", "sindominio@", "sinpunto@exceltecgroup", "con espacio@exceltecgroup.com"}
	for _, correo := range casos {
		t.Run(correo, func(t *testing.T) {
			rec, res := postUsuario(t, handler, admin, map[string]any{
				"nombre": "Correo Malo", "correo": correo, "pin": "1234", "rol": "Vendedor",
			})
			if rec.Code != http.StatusBadRequest {
				t.Errorf("esperaba 400 con correo %q, dio %d: %s", correo, rec.Code, rec.Body.String())
			}
			if res["ok"] != false {
				t.Error("esperaba ok:false")
			}
		})
	}
}

func TestUsuariosCrear_CorreoDuplicado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	correo := "prueba.duplicado." + sufijoUnico() + "@exceltecgroup.com"

	rec1, res1 := postUsuario(t, handler, admin, map[string]any{
		"nombre": "Primero", "correo": correo, "pin": "1234", "rol": "Vendedor",
	})
	if rec1.Code != http.StatusCreated {
		t.Fatalf("la primera creación debió ser 201, dio %d: %s", rec1.Code, rec1.Body.String())
	}
	usuario1, _ := res1["usuario"].(map[string]any)
	id1, _ := usuario1["usuario_id"].(string)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM usuarios WHERE usuario_id = $1`, id1) })

	rec2, res2 := postUsuario(t, handler, admin, map[string]any{
		"nombre": "Segundo", "correo": correo, "pin": "5678", "rol": "Vendedor",
	})
	if rec2.Code != http.StatusConflict {
		t.Fatalf("esperaba 409 con correo duplicado, dio %d: %s", rec2.Code, rec2.Body.String())
	}
	if res2["ok"] != false {
		t.Error("esperaba ok:false con correo duplicado")
	}
}

// CRÍTICO: un usuario sin rol Administrador no debe poder crear
// usuarios, sin importar qué mande en el cuerpo.
func TestUsuariosCrear_VendedorNoPuedeCrear(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	vendedor := crearUsuarioPrueba(t, pool, "vendedor.crea."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")

	correo := "prueba.vendedor.crea." + sufijoUnico() + "@exceltecgroup.com"
	rec, res := postUsuario(t, handler, vendedor, map[string]any{
		"nombre": "Intento De Vendedor", "correo": correo, "pin": "1234", "rol": "Vendedor",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var existe bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM usuarios WHERE correo = $1)`, correo).Scan(&existe); err != nil {
		t.Fatalf("no se pudo verificar que el usuario no se creó: %v", err)
	}
	if existe {
		t.Error("un Vendedor no debería poder crear usuarios, pero el registro quedó en la base")
	}
}

func TestUsuariosEditar_CambiaRolYEstado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	correo := "prueba.editar." + sufijoUnico() + "@exceltecgroup.com"
	usuarioID := crearUsuarioPrueba(t, pool, correo, "1111", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, admin, usuarioID, map[string]any{
		"nombre": "Usuario Editado",
		"correo": correo,
		"rol":    "Gerente Comercial",
		"estado": "Inactivo",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	usuario, ok := res["usuario"].(map[string]any)
	if !ok {
		t.Fatalf("esperaba un objeto 'usuario' en la respuesta, dio: %+v", res)
	}
	if usuario["nombre"] != "Usuario Editado" {
		t.Errorf("nombre = %v, esperaba 'Usuario Editado'", usuario["nombre"])
	}
	if usuario["rol"] != "Gerente Comercial" {
		t.Errorf("rol = %v, esperaba 'Gerente Comercial'", usuario["rol"])
	}
	if usuario["estado"] != "Inactivo" {
		t.Errorf("estado = %v, esperaba 'Inactivo'", usuario["estado"])
	}
}

// Un Administrador SÍ puede cambiar el correo desde este sprint —
// capacidad nueva que antes no existía.
func TestUsuariosEditar_AdministradorPuedeCambiarCorreo(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	usuarioID := crearUsuarioPrueba(t, pool, "prueba.correo.viejo."+sufijoUnico()+"@exceltecgroup.com", "1111", "Vendedor", "Activo")

	correoNuevo := "prueba.correo.nuevo." + sufijoUnico() + "@exceltecgroup.com"
	rec, res := patchUsuario(t, handler, admin, usuarioID, map[string]any{
		"nombre": "Con Correo Nuevo", "correo": correoNuevo, "rol": "Vendedor", "estado": "Activo",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	usuario, _ := res["usuario"].(map[string]any)
	if usuario["correo"] != correoNuevo {
		t.Errorf("correo = %v, esperaba %q", usuario["correo"], correoNuevo)
	}
}

func TestUsuariosEditar_CorreoFormatoInvalido(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	usuarioID := crearUsuarioPrueba(t, pool, "prueba.editar.correo.malo."+sufijoUnico()+"@exceltecgroup.com", "1111", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, admin, usuarioID, map[string]any{
		"nombre": "Usuario", "correo": "esto-no-es-un-correo", "rol": "Vendedor", "estado": "Activo",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con correo mal formado, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestUsuariosEditar_RolInexistente(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)
	correo := "prueba.editar.rol.malo." + sufijoUnico() + "@exceltecgroup.com"
	usuarioID := crearUsuarioPrueba(t, pool, correo, "1111", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, admin, usuarioID, map[string]any{
		"nombre": "Usuario",
		"correo": correo,
		"rol":    "Rol Que No Existe",
		"estado": "Activo",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con rol inexistente, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var rolActual string
	if err := pool.QueryRow(context.Background(), `SELECT rol FROM usuarios WHERE usuario_id = $1`, usuarioID).Scan(&rolActual); err != nil {
		t.Fatalf("no se pudo releer el usuario: %v", err)
	}
	if rolActual != "Vendedor" {
		t.Errorf("el rol quedó en %q; un rol inválido no debió modificar el registro", rolActual)
	}
}

func TestUsuariosEditar_UsuarioInexistente(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	admin := crearAdminActorPrueba(t, pool)

	rec, res := patchUsuario(t, handler, admin, "usr-no-existe-"+sufijoUnico(), map[string]any{
		"nombre": "Nadie",
		"correo": "nadie@exceltecgroup.com",
		"rol":    "Vendedor",
		"estado": "Activo",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

// CRÍTICO: un Vendedor no puede editar a OTRO usuario, ni siquiera un
// campo tan inocuo como el nombre.
func TestUsuariosEditar_VendedorNoPuedeEditarAOtroUsuario(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	vendedor := crearUsuarioPrueba(t, pool, "vendedor.ajeno."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	otroCorreo := "otro.usuario." + sufijoUnico() + "@exceltecgroup.com"
	otroID := crearUsuarioPrueba(t, pool, otroCorreo, "5678", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, vendedor, otroID, map[string]any{
		"nombre": "Nombre Cambiado Sin Permiso", "correo": otroCorreo,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var nombreActual string
	if err := pool.QueryRow(context.Background(), `SELECT nombre FROM usuarios WHERE usuario_id = $1`, otroID).Scan(&nombreActual); err != nil {
		t.Fatalf("no se pudo releer el usuario: %v", err)
	}
	if nombreActual == "Nombre Cambiado Sin Permiso" {
		t.Error("un Vendedor no debería poder editar a otro usuario, pero el cambio se aplicó")
	}
}

// CRÍTICO: un Vendedor puede editar su propio nombre/correo.
func TestUsuariosEditar_VendedorPuedeEditarSuPropioNombre(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	correo := "vendedor.propio." + sufijoUnico() + "@exceltecgroup.com"
	vendedor := crearUsuarioPrueba(t, pool, correo, "1234", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, vendedor, vendedor, map[string]any{
		"nombre": "Mi Nombre Actualizado", "correo": correo,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	usuario, _ := res["usuario"].(map[string]any)
	if usuario["nombre"] != "Mi Nombre Actualizado" {
		t.Errorf("nombre = %v, esperaba 'Mi Nombre Actualizado'", usuario["nombre"])
	}
	if usuario["rol"] != "Vendedor" {
		t.Errorf("el rol no debería haber cambiado, dio %v", usuario["rol"])
	}
}

// CRÍTICO: un Vendedor no puede cambiar su propio rol, ni aunque
// mande el mismo valor que ya tiene — se rechaza en vez de aplicarlo
// o ignorarlo en silencio.
func TestUsuariosEditar_VendedorNoPuedeCambiarSuPropioRol(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	correo := "vendedor.rol.propio." + sufijoUnico() + "@exceltecgroup.com"
	vendedor := crearUsuarioPrueba(t, pool, correo, "1234", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, vendedor, vendedor, map[string]any{
		"nombre": "Intento De Ascenso", "correo": correo, "rol": "Administrador",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}

	var rolActual string
	if err := pool.QueryRow(context.Background(), `SELECT rol FROM usuarios WHERE usuario_id = $1`, vendedor).Scan(&rolActual); err != nil {
		t.Fatalf("no se pudo releer el usuario: %v", err)
	}
	if rolActual != "Vendedor" {
		t.Errorf("el rol quedó en %q — un Vendedor no debería poder cambiar su propio rol a %q", rolActual, "Administrador")
	}
}

// Mismo criterio que el rol: un Vendedor tampoco puede cambiar su
// propio estado (ej. reactivarse si un Administrador lo desactivó).
func TestUsuariosEditar_VendedorNoPuedeCambiarSuPropioEstado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	correo := "vendedor.estado.propio." + sufijoUnico() + "@exceltecgroup.com"
	vendedor := crearUsuarioPrueba(t, pool, correo, "1234", "Vendedor", "Activo")

	rec, res := patchUsuario(t, handler, vendedor, vendedor, map[string]any{
		"nombre": "Vendedor", "correo": correo, "estado": "Inactivo",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("esperaba 403, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestRoles_ListaLosSembrados(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}

	req := httptest.NewRequest(http.MethodGet, "/api/roles", nil)
	rec := httptest.NewRecorder()
	handler.ListarRoles(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	roles, _ := res["roles"].([]any)
	if len(roles) != 5 {
		t.Fatalf("esperaba los 5 roles sembrados en 0001_init_schema.sql, dio %d", len(roles))
	}

	nombres := make(map[string]bool, len(roles))
	for _, r := range roles {
		fila, _ := r.(map[string]any)
		nombres[fmt.Sprintf("%v", fila["rol"])] = true
	}
	for _, esperado := range []string{"Administrador", "Gerente Comercial", "Vendedor", "Consultor", "Solo Consulta"} {
		if !nombres[esperado] {
			t.Errorf("el rol sembrado %q no apareció en GET /api/roles", esperado)
		}
	}
}
