package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func crearCalculadoraTabsPrueba(t *testing.T) (*CotizadorTabsHandler, string) {
	t.Helper()
	pool := setupTestDB(t)
	id := "TEST-CALC-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora) VALUES ($1, 'Cotizador de prueba')`, id); err != nil {
		t.Fatalf("no se pudo crear la calculadora: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id)
	})
	return &CotizadorTabsHandler{DB: pool}, id
}

func TestCotizadorTabs_CrearYListar(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Datos generales",
		"alcance": "PROPIO", "orden": 2, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/tabs?calculadora_id="+url.QueryEscape(calculadoraID), nil)
	rec = httptest.NewRecorder()
	handler.ListarTabs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar tabs: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK   bool           `json:"ok"`
		Tabs []tabCotizador `json:"tabs"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || len(res.Tabs) != 1 || res.Tabs[0].TabID != tabID || res.Tabs[0].Nombre != "Datos generales" {
		t.Fatalf("tab guardado no apareció correctamente: %+v", res)
	}
}

func TestCotizadorElementos_CreaLosCuatroTiposSimples(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-EL-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Elementos", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %d: %s", rec.Code, rec.Body.String())
	}
	catalogoID := crearCatalogoPrueba(t, handler.DB, "Catálogo para elemento", "")
	tipos := []string{"CAMPO", "CAMPO_CATALOGO", "LEYENDA", "TEXTO_INFORMATIVO"}
	for i, tipo := range tipos {
		body := map[string]any{
			"elemento_id": "TEST-EL-" + tipo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": tipo, "etiqueta": "Elemento " + tipo, "columnas_ancho": 1,
			"orden": i + 1, "requerido": tipo == "CAMPO", "configuracion": map[string]any{"prueba": true}, "activo": true,
		}
		if tipo == "CAMPO_CATALOGO" {
			body["catalogo_id"] = catalogoID
		}
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("crear %s: esperaba 200, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	rec = httptest.NewRecorder()
	handler.ListarElementos(rec, req)
	var res struct {
		OK        bool                   `json:"ok"`
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || !res.OK || len(res.Elementos) != len(tipos) {
		t.Fatalf("esperaba %d elementos persistidos, obtuvo %d: %s", len(tipos), len(res.Elementos), rec.Body.String())
	}
}

func TestCotizadorElementos_ValidaCatalogoSegunTipo(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-VALID-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Validaciones", "activo": true,
	})
	catalogoID := crearCatalogoPrueba(t, handler.DB, "Catálogo validación", "")

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-SIN-CAT-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO_CATALOGO", "etiqueta": "Sin catálogo", "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CAMPO_CATALOGO sin catalogo_id: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	for _, tipo := range []string{"CAMPO", "LEYENDA", "TEXTO_INFORMATIVO"} {
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": "TEST-EL-CAT-INVALIDO-" + tipo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": tipo, "etiqueta": "Catálogo inválido", "catalogo_id": catalogoID, "activo": true,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s con catalogo_id: esperaba 400, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}
}
