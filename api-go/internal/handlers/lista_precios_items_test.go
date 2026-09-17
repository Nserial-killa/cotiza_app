package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// crearElementoListaPreciosPrueba crea un elemento LISTA_PRECIOS válido
// (UNICA, sin ítems todavía) y devuelve su elemento_id.
func crearElementoListaPreciosPrueba(t *testing.T, tabsHandler *CotizadorTabsHandler, tabID string) string {
	t.Helper()
	elementoID := "TEST-EL-LP-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "LISTA_PRECIOS", "etiqueta": "Servicios",
		"configuracion": map[string]any{"tipo_lista_precios": "UNICA"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear elemento LISTA_PRECIOS: %d: %s", rec.Code, rec.Body.String())
	}
	return elementoID
}

func crearItemListaPrecios(t *testing.T, handler *ListaPreciosItemsHandler, elementoID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Post("/api/cotizador/elementos/{elemento_id}/items", handler.Crear)
	req := httptest.NewRequest(http.MethodPost, "/api/cotizador/elementos/"+elementoID+"/items", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func patchItemListaPrecios(t *testing.T, handler *ListaPreciosItemsHandler, itemID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Patch("/api/cotizador/items/{item_id}", handler.Editar)
	req := httptest.NewRequest(http.MethodPatch, "/api/cotizador/items/"+itemID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func deleteItemListaPrecios(t *testing.T, handler *ListaPreciosItemsHandler, itemID string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Delete("/api/cotizador/items/{item_id}", handler.Eliminar)
	req := httptest.NewRequest(http.MethodDelete, "/api/cotizador/items/"+itemID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestListaPreciosItems_CrearEditarEliminar(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-LP-CRUD-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Precios", "activo": true,
	})
	elementoID := crearElementoListaPreciosPrueba(t, tabsHandler, tabID)
	itemsHandler := &ListaPreciosItemsHandler{DB: tabsHandler.DB}

	rec, res := crearItemListaPrecios(t, itemsHandler, elementoID, map[string]any{
		"codigo": "OUT-MT", "nombre": "Outsourcing Medio Tiempo", "precio": 2000, "moneda": "USD",
		"unidad_cobro": "Mes", "costo_interno": 1400, "margen_porcentaje": 30,
	})
	if rec.Code != http.StatusOK || !res["ok"].(bool) {
		t.Fatalf("crear ítem: %d: %+v", rec.Code, res)
	}
	itemID, _ := res["item_id"].(string)
	if itemID == "" {
		t.Fatalf("esperaba item_id en la respuesta: %+v", res)
	}

	// código duplicado dentro del mismo elemento: rechaza.
	rec, _ = crearItemListaPrecios(t, itemsHandler, elementoID, map[string]any{
		"codigo": "OUT-MT", "nombre": "Otro nombre", "precio": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("código duplicado: esperaba 400, dio %d", rec.Code)
	}

	// elemento que no es LISTA_PRECIOS: rechaza.
	campoID := "TEST-EL-NOLISTA-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Campo", "activo": true,
	})
	rec, _ = crearItemListaPrecios(t, itemsHandler, campoID, map[string]any{"codigo": "X", "nombre": "X", "precio": 1})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("elemento no es LISTA_PRECIOS: esperaba 400, dio %d", rec.Code)
	}

	// editar: actualización parcial (solo precio).
	rec = patchItemListaPrecios(t, itemsHandler, itemID, map[string]any{"precio": 2500})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar ítem: %d: %s", rec.Code, rec.Body.String())
	}
	var precio float64
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT precio FROM lista_precios_items WHERE item_id::text=$1`, itemID).Scan(&precio); err != nil {
		t.Fatal(err)
	}
	if precio != 2500 {
		t.Fatalf("esperaba precio=2500 tras editar, obtuvo %v", precio)
	}

	// eliminar: soft delete (activo=false, la fila sigue existiendo).
	rec = deleteItemListaPrecios(t, itemsHandler, itemID)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar ítem: %d: %s", rec.Code, rec.Body.String())
	}
	var activo bool
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT activo FROM lista_precios_items WHERE item_id::text=$1`, itemID).Scan(&activo); err != nil {
		t.Fatal(err)
	}
	if activo {
		t.Fatal("esperaba activo=false tras eliminar, la fila no debía borrarse físicamente")
	}
}

func TestListaPreciosItems_EliminarInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	handler := &ListaPreciosItemsHandler{DB: pool}
	rec := deleteItemListaPrecios(t, handler, "00000000-0000-0000-0000-000000000000")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}
}
