package handlers

// Pruebas de integración del catálogo de reglas globales:
// GET/POST /api/reglas, DELETE /api/reglas/{id}. Mismo criterio que
// catalogos_test.go: contra Postgres real, con fixtures propias que
// se limpian solas.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func postRegla(t *testing.T, handler *ReglasHandler, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/reglas", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Guardar(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func getReglas(t *testing.T, handler *ReglasHandler, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	url := "/api/reglas"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

// deleteRegla monta un router de chi real (en vez de llamar a
// handler.Eliminar directo) porque Eliminar lee el {id} de la ruta
// con chi.URLParam — sin un router de por medio ese valor viene vacío.
func deleteRegla(t *testing.T, handler *ReglasHandler, reglaID string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	router := chi.NewRouter()
	router.Delete("/api/reglas/{id}", handler.Eliminar)

	req := httptest.NewRequest(http.MethodDelete, "/api/reglas/"+reglaID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func TestReglasGuardar_CrearYAparecEnListado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id) })

	rec, res := postRegla(t, handler, map[string]any{
		"regla_id": id, "nombre": "Valor mínimo", "categoria": "validacion", "tipo": "minimo",
		"severidad": "ADVERTENCIA", "momento": "AL_CAMBIAR_CAMPO", "mensaje": "El valor es muy bajo.",
		"orden": 5, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear regla: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != true {
		t.Fatalf("esperaba ok:true, dio %+v", res)
	}

	_, listado := getReglas(t, handler, "")
	reglas, _ := listado["reglas"].([]any)
	encontrada := false
	for _, r := range reglas {
		item, _ := r.(map[string]any)
		if item["regla_id"] == id {
			encontrada = true
			// El handler sube categoria/tipo a mayúsculas, igual que
			// hace GuardarCatalogo con alcance en catalogos.go.
			if item["categoria"] != "VALIDACION" {
				t.Errorf("categoria = %v, esperaba VALIDACION", item["categoria"])
			}
		}
	}
	if !encontrada {
		t.Errorf("la regla %q no apareció en el listado", id)
	}
}

func TestReglasListar_FiltraPorSeveridad(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	idAdvertencia := "TEST-REGLA-SEV-A-" + sufijoUnico()
	idBloqueante := "TEST-REGLA-SEV-B-" + sufijoUnico()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id IN ($1, $2)`, idAdvertencia, idBloqueante)
	})

	postRegla(t, handler, map[string]any{"regla_id": idAdvertencia, "nombre": "Regla advertencia", "severidad": "ADVERTENCIA", "activo": true})
	postRegla(t, handler, map[string]any{"regla_id": idBloqueante, "nombre": "Regla bloqueante", "severidad": "BLOQUEANTE", "activo": true})

	_, res := getReglas(t, handler, "severidad=BLOQUEANTE")
	reglas, _ := res["reglas"].([]any)

	encontradaBloqueante := false
	for _, r := range reglas {
		item, _ := r.(map[string]any)
		if item["regla_id"] == idAdvertencia {
			t.Errorf("el filtro severidad=BLOQUEANTE no debería incluir la regla ADVERTENCIA %q", idAdvertencia)
		}
		if item["regla_id"] == idBloqueante {
			encontradaBloqueante = true
		}
	}
	if !encontradaBloqueante {
		t.Errorf("el filtro severidad=BLOQUEANTE debería incluir %q", idBloqueante)
	}
}

func TestReglasGuardar_ActualizaConMismoID(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-EDIT-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id) })

	postRegla(t, handler, map[string]any{"regla_id": id, "nombre": "Original", "severidad": "INFORMATIVA", "orden": 1, "activo": true})
	postRegla(t, handler, map[string]any{"regla_id": id, "nombre": "Editada", "severidad": "BLOQUEANTE", "orden": 2, "activo": false})

	var nombre, severidad string
	var orden int
	var activo bool
	err := pool.QueryRow(context.Background(),
		`SELECT nombre, severidad, orden, activo FROM reglas WHERE regla_id = $1`, id,
	).Scan(&nombre, &severidad, &orden, &activo)
	if err != nil {
		t.Fatalf("no se pudo leer la regla editada: %v", err)
	}
	if nombre != "Editada" || severidad != "BLOQUEANTE" || orden != 2 || activo {
		t.Errorf("upsert no persistió la edición: nombre=%q severidad=%q orden=%d activo=%v", nombre, severidad, orden, activo)
	}
}

func TestReglasEliminar_QuitaDeLaBase(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-DEL-" + sufijoUnico()

	postRegla(t, handler, map[string]any{"regla_id": id, "nombre": "Para eliminar", "activo": true})

	rec, _ := deleteRegla(t, handler, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var existe bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM reglas WHERE regla_id = $1)`, id,
	).Scan(&existe); err != nil {
		t.Fatalf("no se pudo verificar el borrado: %v", err)
	}
	if existe {
		t.Errorf("la regla %q debería haberse eliminado", id)
	}
}

func TestReglasEliminar_NoExistente(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}

	rec, res := deleteRegla(t, handler, "TEST-REGLA-NO-EXISTE-"+sufijoUnico())
	if rec.Code != http.StatusNotFound {
		t.Errorf("esperaba 404 eliminando una regla inexistente, dio %d: %+v", rec.Code, res)
	}
}

func TestReglasGuardar_CamposFaltantes(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}

	rec, res := postRegla(t, handler, map[string]any{"regla_id": "", "nombre": ""})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("esperaba 400 sin regla_id/nombre, dio %d: %+v", rec.Code, res)
	}
}

// El handler valida severidad/momento antes de tocar la base (para dar
// un mensaje claro), pero el esquema tiene el mismo CHECK como defensa
// de fondo — ver TestReglasEsquema_* más abajo, que confirman que la
// base también lo rechazaría si el frontend llegara a mandar algo mal
// y ese paso de validación en Go se rompiera.
func TestReglasGuardar_SeveridadInvalida(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-SEV-BAD-" + sufijoUnico()

	rec, res := postRegla(t, handler, map[string]any{"regla_id": id, "nombre": "Prueba", "severidad": "NO_EXISTE"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con severidad inválida, dio %d: %+v", rec.Code, res)
	}

	var existe bool
	pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM reglas WHERE regla_id = $1)`, id).Scan(&existe)
	if existe {
		t.Error("no debería haberse creado ninguna fila con severidad inválida")
		pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id)
	}
}

func TestReglasGuardar_MomentoInvalido(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-MOM-BAD-" + sufijoUnico()

	rec, res := postRegla(t, handler, map[string]any{"regla_id": id, "nombre": "Prueba", "momento": "NO_EXISTE"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con momento inválido, dio %d: %+v", rec.Code, res)
	}
}

func TestReglasGuardar_ParametrosSchemaDebeSerArreglo(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-PARAM-BAD-" + sufijoUnico()

	rec, res := postRegla(t, handler, map[string]any{
		"regla_id": id, "nombre": "Prueba", "parametros_schema": map[string]any{"no": "es un arreglo"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con parametros_schema que no es arreglo, dio %d: %+v", rec.Code, res)
	}
}

func TestReglasGuardar_ParametrosSchemaSeGuardaYSeLista(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-PARAM-OK-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id) })

	rec, _ := postRegla(t, handler, map[string]any{
		"regla_id": id, "nombre": "Con parámetros",
		"parametros_schema": []map[string]any{{"clave": "valor_minimo", "tipo": "numero", "requerido": true}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	_, listado := getReglas(t, handler, "")
	reglas, _ := listado["reglas"].([]any)
	for _, r := range reglas {
		item, _ := r.(map[string]any)
		if item["regla_id"] != id {
			continue
		}
		schema, _ := item["parametros_schema"].([]any)
		if len(schema) != 1 {
			t.Fatalf("esperaba 1 parámetro en parametros_schema, dio %d: %+v", len(schema), item["parametros_schema"])
		}
		primero, _ := schema[0].(map[string]any)
		if primero["clave"] != "valor_minimo" {
			t.Errorf("clave del parámetro = %v, esperaba valor_minimo", primero["clave"])
		}
		return
	}
	t.Fatalf("la regla %q no apareció en el listado", id)
}

func TestReglasGuardar_ParametrosSchemaPorDefectoEsArregloVacio(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ReglasHandler{DB: pool}
	id := "TEST-REGLA-PARAM-DEF-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id) })

	rec, _ := postRegla(t, handler, map[string]any{"regla_id": id, "nombre": "Sin parámetros"})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var schema []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT parametros_schema FROM reglas WHERE regla_id = $1`, id,
	).Scan(&schema); err != nil {
		t.Fatalf("no se pudo leer parametros_schema: %v", err)
	}
	if strings.TrimSpace(string(schema)) != "[]" {
		t.Errorf("parametros_schema por defecto = %s, esperaba []", schema)
	}
}

// El esquema (0003_disenador_esquema.sql) tiene un CHECK de severidad y
// otro de momento. El handler ya los valida antes de llegar a la base
// (ver TestReglasGuardar_SeveridadInvalida/_MomentoInvalido), pero
// estas dos pruebas confirman que la base también los rechazaría por
// su cuenta si ese paso de validación en Go llegara a romperse.
func TestReglasEsquema_CheckSeveridadRechazaValorInvalido(t *testing.T) {
	pool := setupTestDB(t)
	id := "TEST-REGLA-CHECK-SEV-" + sufijoUnico()

	_, err := pool.Exec(context.Background(),
		`INSERT INTO reglas (regla_id, nombre, severidad) VALUES ($1, 'Prueba CHECK', 'NO_EXISTE')`, id)
	if err == nil {
		pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id)
		t.Fatal("el CHECK de severidad debería haber rechazado un valor fuera de la lista permitida")
	}
}

func TestReglasEsquema_CheckMomentoRechazaValorInvalido(t *testing.T) {
	pool := setupTestDB(t)
	id := "TEST-REGLA-CHECK-MOM-" + sufijoUnico()

	_, err := pool.Exec(context.Background(),
		`INSERT INTO reglas (regla_id, nombre, momento) VALUES ($1, 'Prueba CHECK', 'NO_EXISTE')`, id)
	if err == nil {
		pool.Exec(context.Background(), `DELETE FROM reglas WHERE regla_id = $1`, id)
		t.Fatal("el CHECK de momento debería haber rechazado un valor fuera de la lista permitida")
	}
}
