package middleware

// Pruebas de integración de RequiereApiKey — mismo criterio que
// auth_test.go: contra Postgres real, con fixtures propias que se
// limpian solas, y un handler mínimo de eco para verificar tanto el
// código de estado como lo que el middleware deja en el contexto.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// crearIntegracionYClave inserta una integración descartable con el
// estado indicado y devuelve la clave real en texto plano (nunca se
// guarda; solo su hash bcrypt viaja a la base, igual que el handler
// real de internal/handlers/integraciones.go).
func crearIntegracionYClave(t *testing.T, pool *pgxpool.Pool, estado string) (integracionID, clave string) {
	t.Helper()
	clave = "test-api-key-" + sufijoUnico()
	hash, err := bcrypt.GenerateFromPassword([]byte(clave), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("no se pudo hashear la clave de prueba: %v", err)
	}
	err = pool.QueryRow(context.Background(), `
		INSERT INTO integraciones_api (nombre, api_key_hash, estado)
		VALUES ('Integración de prueba', $1, $2)
		RETURNING integracion_id::text`, string(hash), estado,
	).Scan(&integracionID)
	if err != nil {
		t.Fatalf("no se pudo crear la integración de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM integraciones_api WHERE integracion_id::text = $1`, integracionID)
	})
	return integracionID, clave
}

func handlerDeEcoIntegracion() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		integracionID, _ := r.Context().Value(IntegracionIDKey).(string)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(integracionID))
	}
}

func montarRouterConApiKey(pool *pgxpool.Pool) *chi.Mux {
	r := chi.NewRouter()
	r.With(RequiereApiKey(pool)).Get("/protegido", handlerDeEcoIntegracion())
	return r
}

func pedirConApiKey(router http.Handler, clave string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/protegido", nil)
	if clave != "" {
		req.Header.Set("X-Api-Key", clave)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestRequiereApiKey_SinHeader(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterConApiKey(pool)

	rec := pedirConApiKey(router, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 sin header X-Api-Key, dio %d", rec.Code)
	}
}

func TestRequiereApiKey_ClaveInventada(t *testing.T) {
	pool := setupTestDB(t)
	_, _ = crearIntegracionYClave(t, pool, "Activo")
	router := montarRouterConApiKey(pool)

	rec := pedirConApiKey(router, "esto-no-es-una-clave-real")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 con clave inventada, dio %d", rec.Code)
	}
}

func TestRequiereApiKey_ClaveValidaDejaPasarYPoneIntegracionIDEnElContexto(t *testing.T) {
	pool := setupTestDB(t)
	integracionID, clave := crearIntegracionYClave(t, pool, "Activo")
	router := montarRouterConApiKey(pool)

	rec := pedirConApiKey(router, clave)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200 con clave válida, dio %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != integracionID {
		t.Errorf("integracion_id en el contexto = %q, esperaba %q", rec.Body.String(), integracionID)
	}
}

// CRÍTICO: una integración revocada (Inactivo) debe rechazar aunque
// la clave en sí siga siendo la correcta — revocar tiene que servir
// de verdad para cortar el acceso.
func TestRequiereApiKey_IntegracionInactivaRechazaAunqueLaClaveSeaCorrecta(t *testing.T) {
	pool := setupTestDB(t)
	_, clave := crearIntegracionYClave(t, pool, "Inactivo")
	router := montarRouterConApiKey(pool)

	rec := pedirConApiKey(router, clave)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 con integración inactiva, dio %d", rec.Code)
	}
}

func TestRequiereApiKey_ActualizaUltimaUso(t *testing.T) {
	pool := setupTestDB(t)
	integracionID, clave := crearIntegracionYClave(t, pool, "Activo")
	router := montarRouterConApiKey(pool)

	var antes *string
	pool.QueryRow(context.Background(), `SELECT ultima_uso::text FROM integraciones_api WHERE integracion_id::text = $1`, integracionID).Scan(&antes)
	if antes != nil {
		t.Fatalf("ultima_uso debería empezar en null, dio %v", *antes)
	}

	rec := pedirConApiKey(router, clave)
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d", rec.Code)
	}

	var despues *string
	if err := pool.QueryRow(context.Background(), `SELECT ultima_uso::text FROM integraciones_api WHERE integracion_id::text = $1`, integracionID).Scan(&despues); err != nil {
		t.Fatalf("no se pudo leer ultima_uso: %v", err)
	}
	if despues == nil {
		t.Error("ultima_uso debería haberse actualizado tras una petición válida")
	}
}
