package handlers

// Pruebas de integración de POST /api/externo/solicitudes — el único
// endpoint protegido por middleware.RequiereApiKey en vez de una
// sesión. A diferencia del resto del paquete, acá SÍ se monta el
// middleware real delante del handler (con chi.With), porque el
// contrato completo que hay que probar es justamente "la clave real
// funciona contra este endpoint" — no alcanza con inyectar el
// integracion_id a mano en el contexto.

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

	"cotiza/api/internal/middleware"
)

// crearIntegracionPruebaExterna inserta una integración descartable
// (estado indicado) y devuelve su clave real en texto plano.
func crearIntegracionPruebaExterna(t *testing.T, pool *pgxpool.Pool, estado string) (integracionID, clave string) {
	t.Helper()
	clave = "test-externa-key-" + sufijoUnico()
	hash, err := bcrypt.GenerateFromPassword([]byte(clave), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("no se pudo hashear la clave de prueba: %v", err)
	}
	err = pool.QueryRow(context.Background(), `
		INSERT INTO integraciones_api (nombre, api_key_hash, estado)
		VALUES ('Integración externa de prueba', $1, $2)
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

func montarRouterSolicitudesExternas(pool *pgxpool.Pool) *chi.Mux {
	handler := &SolicitudesExternasHandler{DB: pool}
	r := chi.NewRouter()
	r.With(middleware.RequiereApiKey(pool)).Post("/api/externo/solicitudes", handler.Crear)
	return r
}

func postSolicitudExterna(t *testing.T, router http.Handler, clave string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/externo/solicitudes", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if clave != "" {
		req.Header.Set("X-Api-Key", clave)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func limpiarSolicitud(t *testing.T, pool *pgxpool.Pool, solicitudID string) {
	t.Helper()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID)
	})
}

func TestSolicitudesExternas_ClaveValidaCreaSolicitud(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterSolicitudesExternas(pool)
	integracionID, clave := crearIntegracionPruebaExterna(t, pool, "Activo")

	rec, res := postSolicitudExterna(t, router, clave, map[string]any{
		"cliente_nombre":    "Cliente Bitrix S.A.",
		"contacto_nombre":   "Juan Pérez",
		"contacto_correo":   "juan@clientebitrix.com",
		"contacto_telefono": "8888-8888",
		"descripcion":       "Solicitud de prueba desde Bitrix24.",
	})
	if rec.Code != http.StatusCreated || res["ok"] != true {
		t.Fatalf("esperaba 201/ok con clave válida, dio %d: %s", rec.Code, rec.Body.String())
	}
	solicitudID, _ := res["solicitud_id"].(string)
	if solicitudID == "" {
		t.Fatal("solicitud_id vino vacío en la respuesta")
	}
	limpiarSolicitud(t, pool, solicitudID)

	var origen, clienteNombre, estado string
	var integracionGuardada string
	err := pool.QueryRow(context.Background(), `
		SELECT origen, cliente_nombre, estado, integracion_id::text
		  FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID,
	).Scan(&origen, &clienteNombre, &estado, &integracionGuardada)
	if err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if origen != "API_EXTERNA" || clienteNombre != "Cliente Bitrix S.A." || estado != "Nueva" || integracionGuardada != integracionID {
		t.Fatalf("solicitud inconsistente: origen=%s cliente=%s estado=%s integracion=%s", origen, clienteNombre, estado, integracionGuardada)
	}
}

func TestSolicitudesExternas_ClaveInventadaRechaza(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterSolicitudesExternas(pool)

	rec, _ := postSolicitudExterna(t, router, "clave-que-no-existe", map[string]any{"cliente_nombre": "Cliente Cualquiera"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 con clave inventada, dio %d: %s", rec.Code, rec.Body.String())
	}

	var existe bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM solicitudes WHERE cliente_nombre = 'Cliente Cualquiera')`).Scan(&existe); err != nil {
		t.Fatalf("no se pudo verificar que no se creó la solicitud: %v", err)
	}
	if existe {
		t.Error("una clave inventada no debería poder crear una solicitud")
	}
}

func TestSolicitudesExternas_IntegracionInactivaRechaza(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterSolicitudesExternas(pool)
	_, clave := crearIntegracionPruebaExterna(t, pool, "Inactivo")

	rec, _ := postSolicitudExterna(t, router, clave, map[string]any{"cliente_nombre": "Cliente Con Clave Revocada"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401 con integración inactiva, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSolicitudesExternas_SinClienteNombreRechaza(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterSolicitudesExternas(pool)
	_, clave := crearIntegracionPruebaExterna(t, pool, "Activo")

	rec, res := postSolicitudExterna(t, router, clave, map[string]any{"descripcion": "Sin nombre de cliente"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 sin cliente_nombre, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestSolicitudesExternas_CalculadoraIDInexistenteRechaza(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterSolicitudesExternas(pool)
	_, clave := crearIntegracionPruebaExterna(t, pool, "Activo")

	rec, res := postSolicitudExterna(t, router, clave, map[string]any{
		"cliente_nombre": "Cliente Con Cotizador Inventado",
		"calculadora_id": "COTIZADOR-QUE-NO-EXISTE",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con calculadora_id inexistente, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Error("esperaba ok:false")
	}
}

func TestSolicitudesExternas_CalculadoraIDValidoSeGuarda(t *testing.T) {
	pool := setupTestDB(t)
	router := montarRouterSolicitudesExternas(pool)
	_, clave := crearIntegracionPruebaExterna(t, pool, "Activo")
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)

	rec, res := postSolicitudExterna(t, router, clave, map[string]any{
		"cliente_nombre": "Cliente Con Cotizador Real",
		"calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	solicitudID, _ := res["solicitud_id"].(string)
	limpiarSolicitud(t, pool, solicitudID)

	var calculadoraGuardada string
	if err := pool.QueryRow(context.Background(), `SELECT calculadora_id FROM solicitudes WHERE solicitud_id::text = $1`, solicitudID).Scan(&calculadoraGuardada); err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if calculadoraGuardada != calculadoraID {
		t.Errorf("calculadora_id guardado = %q, esperaba %q", calculadoraGuardada, calculadoraID)
	}
}
