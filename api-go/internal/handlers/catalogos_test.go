package handlers

// Pruebas de integración de GET /api/catalogos/designer. Mismo
// criterio que auth_test.go: contra Postgres real, con fixtures
// propios que se limpian solos.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postCatalogos(t *testing.T, metodo http.HandlerFunc, ruta string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, ruta, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	metodo(rec, req)
	return rec
}

func getDesigner(t *testing.T, handler *CatalogosHandler, query string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/catalogos/designer"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler.ListarDesigner(rec, req)
	return rec
}

func TestCatalogosDesigner_ModoInvalido(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}

	rec := getDesigner(t, handler, "modo=esto_no_existe")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con modo inválido, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogosDesigner_SoloCatalogos(t *testing.T) {
	pool := setupTestDB(t)
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo de prueba (solo_catalogos)", "")

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler, "") // modo por defecto = solo_catalogos

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK        bool               `json:"ok"`
		Catalogos []catalogoDesigner `json:"catalogos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	encontrado := false
	for _, c := range res.Catalogos {
		if c.CatalogoID == catalogoID {
			encontrado = true
			break
		}
	}
	if !encontrado {
		t.Errorf("el catálogo de prueba %q no apareció en la lista de %d catálogos", catalogoID, len(res.Catalogos))
	}
}

// Un catálogo con activo=false no debería listarse — es el filtro
// que usa la pantalla para "ocultar" en vez de borrar.
func TestCatalogosDesigner_NoListaInactivos(t *testing.T) {
	pool := setupTestDB(t)
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo inactivo de prueba", "")

	// lo desactivamos después de crearlo (el helper siempre crea activo=true)
	_, err := pool.Exec(context.Background(), `UPDATE catalogos SET activo = false WHERE catalogo_id = $1`, catalogoID)
	if err != nil {
		t.Fatalf("no se pudo desactivar el catálogo de prueba: %v", err)
	}

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler, "")

	var res struct {
		Catalogos []catalogoDesigner `json:"catalogos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	for _, c := range res.Catalogos {
		if c.CatalogoID == catalogoID {
			t.Errorf("el catálogo inactivo %q no debería aparecer en la lista", catalogoID)
		}
	}
}

func TestCatalogosDesigner_ValoresDeCatalogo(t *testing.T) {
	pool := setupTestDB(t)
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo con valores", "")
	crearValorCatalogoPrueba(t, pool, catalogoID, "Valor Uno", "")
	crearValorCatalogoPrueba(t, pool, catalogoID, "Valor Dos", "")

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler, "modo=valores_catalogo&catalogo_id="+catalogoID)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK      bool                    `json:"ok"`
		Valores []valorCatalogoDesigner `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	if len(res.Valores) != 2 {
		t.Fatalf("esperaba 2 valores, dio %d", len(res.Valores))
	}
}

// Sin catalogo_id, la pantalla espera una lista vacía y ok:true —
// NO un error — porque "todavía no se seleccionó ningún catálogo" es
// un estado normal de la UI, no una falla.
func TestCatalogosDesigner_ValoresSinCatalogoID(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}

	rec := getDesigner(t, handler, "modo=valores_catalogo")

	var res struct {
		OK      bool                    `json:"ok"`
		Valores []valorCatalogoDesigner `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Error("sin catalogo_id esperaba ok:true (lista vacía), no un error")
	}
	if len(res.Valores) != 0 {
		t.Errorf("esperaba 0 valores sin catalogo_id, dio %d", len(res.Valores))
	}
}

func TestCatalogosDesigner_RelacionesPadreHijo(t *testing.T) {
	pool := setupTestDB(t)
	padreID := crearCatalogoPrueba(t, pool, "Catálogo padre", "")
	hijoID := crearCatalogoPrueba(t, pool, "Catálogo hijo", padreID)
	valorPadreID := crearValorCatalogoPrueba(t, pool, padreID, "Valor Padre", "")
	valorHijoID := crearValorCatalogoPrueba(t, pool, hijoID, "Valor Hijo", "")
	crearRelacionPrueba(t, pool, padreID, valorPadreID, hijoID, valorHijoID)

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler,
		"modo=relaciones_valor&catalogo_hijo_id="+hijoID+
			"&valor_hijo_id="+valorHijoID+
			"&catalogo_padre_id="+padreID)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK           bool                       `json:"ok"`
		Relaciones   []relacionCatalogoDesigner `json:"relaciones"`
		ValoresPadre []valorCatalogoDesigner    `json:"valores_padre"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	if len(res.Relaciones) != 1 {
		t.Fatalf("esperaba 1 relación, dio %d", len(res.Relaciones))
	}
	if res.Relaciones[0].ValorHijoID != valorHijoID {
		t.Errorf("valor_hijo_id de la relación = %q, esperaba %q", res.Relaciones[0].ValorHijoID, valorHijoID)
	}
	// El modo relaciones_valor también debería traer los valores
	// disponibles del catálogo padre, para poblar el selector.
	if len(res.ValoresPadre) != 1 {
		t.Errorf("esperaba 1 valor del catálogo padre para el selector, dio %d", len(res.ValoresPadre))
	}
}

func TestCatalogosGuardar_UpsertCatalogo(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}
	id := "TEST-CAT-WRITE-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM catalogos WHERE catalogo_id = $1`, id) })

	rec := postCatalogos(t, handler.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": id, "nombre_catalogo": "Catálogo creado", "alcance": "COTIZADOR",
		"descripcion": "Primera versión", "activo": true, "orden": "7",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear catálogo: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": id, "nombre_catalogo": "Catálogo editado", "alcance": "GLOBAL",
		"descripcion": "Segunda versión", "activo": true, "orden": 9,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar catálogo: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var nombre, alcance string
	var orden int
	if err := pool.QueryRow(context.Background(), `SELECT nombre_catalogo, alcance, orden FROM catalogos WHERE catalogo_id=$1`, id).Scan(&nombre, &alcance, &orden); err != nil {
		t.Fatalf("no se pudo leer el catálogo guardado: %v", err)
	}
	if nombre != "Catálogo editado" || alcance != "GLOBAL" || orden != 9 {
		t.Errorf("upsert no persistió la edición: nombre=%q alcance=%q orden=%d", nombre, alcance, orden)
	}
}

func TestCatalogosGuardar_UpsertValor(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo para escritura", "")
	valorID := "TEST-VAL-WRITE-" + sufijoUnico()

	body := map[string]any{
		"valor_id": valorID, "catalogo_id": catalogoID, "clave": "CLAVE",
		"texto_visible": "Texto inicial", "valor_sistema": "VALOR", "descripcion": "Demo",
		"orden": "3", "activo": true, "valor_padre_id": "",
	}
	rec := postCatalogos(t, handler.GuardarValor, "/api/catalogos/valores", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("crear valor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	body["texto_visible"] = "Texto editado"
	body["orden"] = 5
	rec = postCatalogos(t, handler.GuardarValor, "/api/catalogos/valores", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("editar valor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var texto string
	var orden int
	if err := pool.QueryRow(context.Background(), `SELECT texto_visible, orden FROM catalogo_valores WHERE valor_id=$1`, valorID).Scan(&texto, &orden); err != nil {
		t.Fatalf("no se pudo leer el valor guardado: %v", err)
	}
	if texto != "Texto editado" || orden != 5 {
		t.Errorf("upsert no persistió la edición: texto=%q orden=%d", texto, orden)
	}
}

func TestCatalogosGuardar_ReemplazaRelacionesEnTransaccion(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}
	padreID := crearCatalogoPrueba(t, pool, "Padre para reemplazo", "")
	hijoID := crearCatalogoPrueba(t, pool, "Hijo para reemplazo", padreID)
	padreValor1 := crearValorCatalogoPrueba(t, pool, padreID, "Padre Uno", "")
	padreValor2 := crearValorCatalogoPrueba(t, pool, padreID, "Padre Dos", "")
	hijoValorID := "TEST-VAL-CHILD-" + sufijoUnico()

	base := map[string]any{
		"valor_id": hijoValorID, "catalogo_id": hijoID, "clave": "HIJO",
		"texto_visible": "Valor hijo", "valor_sistema": "HIJO", "descripcion": "",
		"orden": "1", "activo": true, "_actualizar_relaciones": true,
		"_catalogo_padre_id": padreID,
		"_valor_padre_ids":   []string{padreValor1, padreValor2},
	}
	rec := postCatalogos(t, handler.GuardarRelaciones, "/api/catalogos/relaciones", base)
	if rec.Code != http.StatusOK {
		t.Fatalf("crear relaciones: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	base["_valor_padre_ids"] = []string{padreValor2}
	rec = postCatalogos(t, handler.GuardarRelaciones, "/api/catalogos/relaciones", base)
	if rec.Code != http.StatusOK {
		t.Fatalf("reemplazar relaciones: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rows, err := pool.Query(context.Background(), `SELECT valor_padre_id FROM catalogo_relaciones WHERE valor_hijo_id=$1`, hijoValorID)
	if err != nil {
		t.Fatalf("no se pudieron consultar las relaciones: %v", err)
	}
	defer rows.Close()
	var encontrados []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		encontrados = append(encontrados, id)
	}
	if len(encontrados) != 1 || encontrados[0] != padreValor2 {
		t.Fatalf("esperaba solo la relación reemplazada %q, obtuvo %v", padreValor2, encontrados)
	}
}
