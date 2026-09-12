package middleware

// Pruebas de integración de RequiereSesion: contra Postgres real
// (mismo criterio que api-go/internal/handlers/helpers_test.go), con
// fixtures propias que se limpian solas. El handler protegido es uno
// mínimo definido acá mismo — el contrato bajo prueba es el del
// middleware (rechaza/acepta, deja usuario_id en el contexto), no el
// de ningún endpoint real en particular.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL no está seteada — ver el comentario de setupTestDB en handlers/helpers_test.go")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("no se pudo conectar a Postgres: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Postgres no respondió al ping: %v (¿está corriendo 'docker compose up -d postgres'?)", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func sufijoUnico() string {
	return time.Now().Format("150405.000000000")
}

func tokenAlAzar(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("no se pudo generar un token de prueba: %v", err)
	}
	return hex.EncodeToString(buf)
}

// crearUsuarioYSesion inserta un usuario y una sesión descartables
// (con el vencimiento indicado) y los borra al terminar el test.
func crearUsuarioYSesion(t *testing.T, pool *pgxpool.Pool, fechaExpiracion time.Time) (usuarioID, token string) {
	t.Helper()
	usuarioID = "test-usr-mw-" + sufijoUnico()
	token = tokenAlAzar(t)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO usuarios (usuario_id, nombre, correo, pin_hash, rol, estado, puede_ver_gestor)
		VALUES ($1, 'Usuario de prueba', $2, crypt('0000', gen_salt('bf')), 'Vendedor', 'Activo', true)`,
		usuarioID, usuarioID+"@exceltecgroup.com",
	)
	if err != nil {
		t.Fatalf("no se pudo crear el usuario de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM usuarios WHERE usuario_id = $1`, usuarioID)
	})

	_, err = pool.Exec(context.Background(), `
		INSERT INTO sesiones (token, usuario_id, fecha_expiracion) VALUES ($1, $2, $3)`,
		token, usuarioID, fechaExpiracion,
	)
	if err != nil {
		t.Fatalf("no se pudo crear la sesión de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM sesiones WHERE token = $1`, token)
	})

	return usuarioID, token
}

// handlerDeEco responde 200 con el usuario_id que el middleware dejó
// en el contexto — así la prueba puede verificar no solo que dejó
// pasar la petición, sino que el usuario_id es el correcto.
func handlerDeEco() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		usuarioID, _ := r.Context().Value(UsuarioIDKey).(string)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(usuarioID))
	}
}

func montarRouterProtegido(pool *pgxpool.Pool) *chi.Mux {
	r := chi.NewRouter()
	r.With(RequiereSesion(pool)).Get("/protegido", handlerDeEco())
	return r
}

func pedirConToken(router http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protegido", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestRequiereSesion_SinHeader(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterProtegido(pool)

	rec := pedirConToken(router, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 sin header Authorization, dio %d", rec.Code)
	}
}

func TestRequiereSesion_TokenInventado(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterProtegido(pool)

	rec := pedirConToken(router, "esto-no-es-un-token-real")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 con token inventado, dio %d", rec.Code)
	}
}

func TestRequiereSesion_TokenVencido(t *testing.T) {
	pool := setupTestDB(t)
	_, token := crearUsuarioYSesion(t, pool, time.Now().Add(-1*time.Hour))
	router := montarRouterProtegido(pool)

	rec := pedirConToken(router, token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 con token vencido, dio %d", rec.Code)
	}
}

func TestRequiereSesion_TokenValidoDejaPasarYPoneUsuarioIDEnElContexto(t *testing.T) {
	pool := setupTestDB(t)
	usuarioID, token := crearUsuarioYSesion(t, pool, time.Now().Add(1*time.Hour))
	router := montarRouterProtegido(pool)

	rec := pedirConToken(router, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200 con token válido, dio %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != usuarioID {
		t.Errorf("usuario_id en el contexto = %q, esperaba %q", rec.Body.String(), usuarioID)
	}
}

func TestRequiereSesion_ActualizaFechaUltimoUso(t *testing.T) {
	pool := setupTestDB(t)
	_, token := crearUsuarioYSesion(t, pool, time.Now().Add(1*time.Hour))
	router := montarRouterProtegido(pool)

	var antes *time.Time
	pool.QueryRow(context.Background(), `SELECT fecha_ultimo_uso FROM sesiones WHERE token = $1`, token).Scan(&antes)
	if antes != nil {
		t.Fatalf("fecha_ultimo_uso debería empezar en null, dio %v", antes)
	}

	rec := pedirConToken(router, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d", rec.Code)
	}

	var despues *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT fecha_ultimo_uso FROM sesiones WHERE token = $1`, token).Scan(&despues); err != nil {
		t.Fatalf("no se pudo leer fecha_ultimo_uso: %v", err)
	}
	if despues == nil {
		t.Error("fecha_ultimo_uso debería haberse actualizado tras una petición válida")
	}
}
