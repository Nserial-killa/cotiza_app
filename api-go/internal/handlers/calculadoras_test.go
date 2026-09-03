package handlers

// Pruebas de integración de GET /api/calculadoras. Mismo criterio que
// el resto del paquete: contra Postgres real, con fixtures propias
// que se limpian solas.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCalculadoras_ListaSoloActivas(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CalculadorasHandler{DB: pool}

	activaID := "TEST-CALC-ACTIVA-" + sufijoUnico()
	inactivaID := "TEST-CALC-INACTIVA-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1, 'Activa de prueba', 'Activo')`, activaID); err != nil {
		t.Fatalf("no se pudo crear la calculadora activa: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id = $1`, activaID)
	})
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1, 'Inactiva de prueba', 'Inactivo')`, inactivaID); err != nil {
		t.Fatalf("no se pudo crear la calculadora inactiva: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id = $1`, inactivaID)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/calculadoras", nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK           bool                `json:"ok"`
		Calculadoras []calculadoraSimple `json:"calculadoras"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK {
		t.Fatal("esperaba ok:true")
	}

	encontradaActiva, encontradaInactiva := false, false
	for _, c := range res.Calculadoras {
		if c.CalculadoraID == activaID {
			encontradaActiva = true
		}
		if c.CalculadoraID == inactivaID {
			encontradaInactiva = true
		}
	}
	if !encontradaActiva {
		t.Error("la calculadora activa de prueba no apareció en el listado")
	}
	if encontradaInactiva {
		t.Error("una calculadora inactiva no debería aparecer en el listado")
	}
}

func TestCalculadoras_IncluyePublicadas(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CalculadorasHandler{DB: pool}
	id := "TEST-CALC-PUBLICADA-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id,nombre_calculadora,estado) VALUES ($1,'Publicada de prueba','Publicado')`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id) })
	rec := httptest.NewRecorder()
	handler.Listar(rec, httptest.NewRequest(http.MethodGet, "/api/calculadoras", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200: %s", rec.Body.String())
	}
	var res struct {
		Calculadoras []calculadoraSimple `json:"calculadoras"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, calculadora := range res.Calculadoras {
		if calculadora.CalculadoraID == id {
			return
		}
	}
	t.Fatal("la calculadora publicada no apareció en el selector")
}
