package handlers

// Auditoría de QA — health.go no tenía ninguna prueba. Es el endpoint
// que usa el healthcheck de Docker y el frontend para mostrar "sin
// conexión", así que su contrato (status HTTP + las dos claves del
// cuerpo) es lo único que hay que fijar.
//
// El caso interesante es el degradado: se construye un pool contra un
// DSN que apunta a un puerto donde no hay nada escuchando, así que el
// Ping falla por conexión rechazada, no por timeout — determinista y
// sin sleeps.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func healthPedirCheck(t *testing.T, pool *pgxpool.Pool) (*httptest.ResponseRecorder, healthResponse) {
	t.Helper()
	handler := &HealthHandler{DB: pool}
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.Check(rec, req)

	var cuerpo healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cuerpo); err != nil {
		t.Fatalf("la respuesta de /api/health no es el JSON esperado: %v\nbody crudo: %s", err, rec.Body.String())
	}
	return rec, cuerpo
}

func TestHealthCheck_ConBaseSanaResponde200YOk(t *testing.T) {
	pool := setupTestDB(t)

	rec, cuerpo := healthPedirCheck(t, pool)

	if rec.Code != http.StatusOK {
		t.Errorf("con Postgres sano se esperaba 200 y respondió %d: el healthcheck de Docker marcaría el contenedor como no sano", rec.Code)
	}
	if cuerpo.Status != "ok" || cuerpo.Database != "ok" {
		t.Errorf("con Postgres sano se esperaba status=ok database=ok y llegó status=%q database=%q", cuerpo.Status, cuerpo.Database)
	}
	if tipo := rec.Header().Get("Content-Type"); tipo != "application/json" {
		t.Errorf("Content-Type inesperado: %q", tipo)
	}
}

func TestHealthCheck_SinBaseResponde503YDegraded(t *testing.T) {
	// Puerto reservado por IANA para "discard" y que en la práctica no
	// tiene nada escuchando: la conexión se rechaza de inmediato.
	pool, err := pgxpool.New(context.Background(),
		"postgres://nadie:nada@127.0.0.1:9/basequenoexiste?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("no se pudo construir el pool de prueba: %v", err)
	}
	t.Cleanup(pool.Close)

	rec, cuerpo := healthPedirCheck(t, pool)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("con Postgres inalcanzable se esperaba 503 y respondió %d: un contenedor sin base se reportaría como sano", rec.Code)
	}
	if cuerpo.Status != "degraded" || cuerpo.Database != "unreachable" {
		t.Errorf("con Postgres inalcanzable se esperaba status=degraded database=unreachable y llegó status=%q database=%q", cuerpo.Status, cuerpo.Database)
	}
}
