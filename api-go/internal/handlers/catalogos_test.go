package handlers

// Pruebas de integración de GET /api/catalogos/designer. Mismo
// criterio que auth_test.go: contra Postgres real, con fixtures
// propios que se limpian solos.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
