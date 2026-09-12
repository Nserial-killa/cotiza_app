package handlers

// Pruebas de integración de GET /api/calculadoras. Mismo criterio que
// el resto del paquete: contra Postgres real, con fixtures propias
// que se limpian solas.

import (
	"bytes"
	"context"
	"encoding/json"
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

func postCalculadora(t *testing.T, handler *CalculadorasHandler, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/calculadoras", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Crear(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func TestCalculadorasCrear_QuedaDisponibleParaListar(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CalculadorasHandler{DB: pool}
	id := "TEST-CALC-CREAR-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id) })

	rec, res := postCalculadora(t, handler, map[string]any{
		"calculadora_id":     id,
		"nombre_calculadora": "Cotizador de alta",
		"linea_negocio":      "Servicios Profesionales",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != true {
		t.Fatalf("esperaba ok:true, dio: %+v", res)
	}

	var nombre string
	if err := pool.QueryRow(context.Background(), `SELECT nombre_calculadora FROM calculadoras WHERE calculadora_id=$1`, id).Scan(&nombre); err != nil {
		t.Fatalf("el cotizador no quedó en la base: %v", err)
	}
	if nombre != "Cotizador de alta" {
		t.Errorf("nombre_calculadora = %q, esperaba %q", nombre, "Cotizador de alta")
	}
}

// Llamar dos veces con el mismo calculadora_id (por ejemplo, un
// script de datos de demostración que se corre más de una vez) debe
// actualizar la misma fila, nunca fallar por duplicado ni crear una
// segunda.
func TestCalculadorasCrear_EsIdempotentePorCalculadoraID(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CalculadorasHandler{DB: pool}
	id := "TEST-CALC-IDEMP-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id) })

	rec1, _ := postCalculadora(t, handler, map[string]any{"calculadora_id": id, "nombre_calculadora": "Primer nombre"})
	if rec1.Code != http.StatusOK {
		t.Fatalf("primera llamada: esperaba 200, dio %d: %s", rec1.Code, rec1.Body.String())
	}
	rec2, _ := postCalculadora(t, handler, map[string]any{"calculadora_id": id, "nombre_calculadora": "Nombre actualizado"})
	if rec2.Code != http.StatusOK {
		t.Fatalf("segunda llamada: esperaba 200, dio %d: %s", rec2.Code, rec2.Body.String())
	}

	var total int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM calculadoras WHERE calculadora_id=$1`, id).Scan(&total); err != nil {
		t.Fatalf("no se pudo contar filas: %v", err)
	}
	if total != 1 {
		t.Fatalf("esperaba una sola fila tras llamar dos veces, hay %d", total)
	}
	var nombre string
	pool.QueryRow(context.Background(), `SELECT nombre_calculadora FROM calculadoras WHERE calculadora_id=$1`, id).Scan(&nombre)
	if nombre != "Nombre actualizado" {
		t.Errorf("nombre_calculadora = %q, esperaba que la segunda llamada actualizara el valor", nombre)
	}
}

func TestCalculadorasCrear_CamposFaltantes(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CalculadorasHandler{DB: pool}

	casos := []map[string]any{
		{"calculadora_id": "", "nombre_calculadora": "Nombre"},
		{"calculadora_id": "TEST-CALC-SIN-NOMBRE", "nombre_calculadora": ""},
	}
	for _, body := range casos {
		rec, res := postCalculadora(t, handler, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %+v: esperaba 400, dio %d: %s", body, rec.Code, rec.Body.String())
		}
		if res["ok"] != false {
			t.Errorf("body %+v: esperaba ok:false", body)
		}
	}
}

// Recompilar no debe ser necesario para que el alta idempotente
// funcione: si el cotizador ya está Publicado (compilador.Compilar lo
// dejó así), volver a llamar Crear con el mismo calculadora_id no
// debe regresarlo a 'Activo' — Crear no toca la columna estado.
func TestCalculadorasCrear_NoPisaEstadoPublicado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CalculadorasHandler{DB: pool}
	id := "TEST-CALC-PUBLICADO-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora, estado) VALUES ($1, 'Publicado ya', 'Publicado')`, id); err != nil {
		t.Fatalf("no se pudo sembrar la calculadora publicada: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id) })

	rec, _ := postCalculadora(t, handler, map[string]any{"calculadora_id": id, "nombre_calculadora": "Publicado ya"})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var estado string
	pool.QueryRow(context.Background(), `SELECT estado FROM calculadoras WHERE calculadora_id=$1`, id).Scan(&estado)
	if estado != "Publicado" {
		t.Errorf("estado = %q, esperaba que siguiera Publicado tras volver a llamar Crear", estado)
	}
}
