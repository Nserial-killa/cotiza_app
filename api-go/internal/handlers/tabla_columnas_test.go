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

// crearElementoTablaPrueba crea un elemento TABLA válido (sin columnas
// todavía) y devuelve su elemento_id.
func crearElementoTablaPrueba(t *testing.T, tabsHandler *CotizadorTabsHandler, tabID string) string {
	t.Helper()
	elementoID := "TEST-EL-TABLA-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "TABLA", "etiqueta": "Perfiles", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear elemento TABLA: %d: %s", rec.Code, rec.Body.String())
	}
	return elementoID
}

func crearColumnaTabla(t *testing.T, handler *TablaColumnasHandler, elementoID string, body map[string]any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Post("/api/cotizador/elementos/{elemento_id}/columnas", handler.Crear)
	req := httptest.NewRequest(http.MethodPost, "/api/cotizador/elementos/"+elementoID+"/columnas", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var res map[string]any
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

func patchColumnaTabla(t *testing.T, handler *TablaColumnasHandler, columnaID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	router := chi.NewRouter()
	router.Patch("/api/cotizador/columnas/{columna_id}", handler.Editar)
	req := httptest.NewRequest(http.MethodPatch, "/api/cotizador/columnas/"+columnaID, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func deleteColumnaTabla(t *testing.T, handler *TablaColumnasHandler, columnaID string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Delete("/api/cotizador/columnas/{columna_id}", handler.Eliminar)
	req := httptest.NewRequest(http.MethodDelete, "/api/cotizador/columnas/"+columnaID, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestTablaColumnas_CrearPropiaYCampoExistente(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-COL-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Tabla", "activo": true,
	})
	tablaID := crearElementoTablaPrueba(t, tabsHandler, tabID)
	columnasHandler := &TablaColumnasHandler{DB: tabsHandler.DB}

	rec, res := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{
		"origen": "PROPIA", "tipo_dato": "NUMERO", "etiqueta": "Horas", "orden": 1,
	})
	if rec.Code != http.StatusOK || res["ok"] != true {
		t.Fatalf("crear columna propia: %d: %+v", rec.Code, res)
	}

	// PROPIA sin tipo_dato: rechaza.
	rec, _ = crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "PROPIA", "etiqueta": "Sin tipo"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PROPIA sin tipo_dato: esperaba 400, dio %d", rec.Code)
	}

	// PROPIA con campo_existente_id: rechaza (mezcla de formas).
	campoID := "TEST-EL-COL-CAMPO-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Perfil", "activo": true,
	})
	rec, _ = crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{
		"origen": "PROPIA", "tipo_dato": "TEXTO", "etiqueta": "Perfil", "campo_existente_id": campoID,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PROPIA con campo_existente_id: esperaba 400, dio %d", rec.Code)
	}

	// CAMPO_EXISTENTE válido.
	rec, res = crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "CAMPO_EXISTENTE", "campo_existente_id": campoID, "orden": 2})
	if rec.Code != http.StatusOK || res["ok"] != true {
		t.Fatalf("crear columna CAMPO_EXISTENTE: %d: %+v", rec.Code, res)
	}

	// CAMPO_EXISTENTE apuntando a otra TABLA: rechaza (no anidar tablas).
	otraTablaID := crearElementoTablaPrueba(t, tabsHandler, tabID)
	rec, _ = crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "CAMPO_EXISTENTE", "campo_existente_id": otraTablaID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CAMPO_EXISTENTE apuntando a otra TABLA: esperaba 400, dio %d", rec.Code)
	}

	// CAMPO_EXISTENTE de otro tab: rechaza.
	otroTabID := "TEST-TAB-COL-OTRO-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": calculadoraID, "nombre": "Otro tab", "activo": true,
	})
	campoOtroTabID := "TEST-EL-COL-CAMPO-OTRO-TAB-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoOtroTabID, "tab_id": otroTabID, "tipo": "CAMPO", "etiqueta": "De otro tab", "activo": true,
	})
	rec, _ = crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "CAMPO_EXISTENTE", "campo_existente_id": campoOtroTabID})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CAMPO_EXISTENTE de otro tab: esperaba 400, dio %d", rec.Code)
	}
}

func TestTablaColumnas_EditarYEliminar(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-COL-EDIT-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Tabla", "activo": true,
	})
	tablaID := crearElementoTablaPrueba(t, tabsHandler, tabID)
	columnasHandler := &TablaColumnasHandler{DB: tabsHandler.DB}

	_, res := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "PROPIA", "tipo_dato": "TEXTO", "etiqueta": "Perfil"})
	columnaID, _ := res["columna_id"].(string)

	rec := patchColumnaTabla(t, columnasHandler, columnaID, map[string]any{"etiqueta": "Rol", "orden": 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar columna: %d: %s", rec.Code, rec.Body.String())
	}
	var etiqueta string
	var orden int
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT etiqueta, orden FROM tabla_columnas WHERE columna_id::text=$1`, columnaID).Scan(&etiqueta, &orden); err != nil {
		t.Fatal(err)
	}
	if etiqueta != "Rol" || orden != 5 {
		t.Fatalf("esperaba etiqueta=Rol orden=5, obtuvo etiqueta=%q orden=%d", etiqueta, orden)
	}

	rec = deleteColumnaTabla(t, columnasHandler, columnaID)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar columna: %d: %s", rec.Code, rec.Body.String())
	}
	var existe bool
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM tabla_columnas WHERE columna_id::text=$1)`, columnaID).Scan(&existe); err != nil {
		t.Fatal(err)
	}
	if existe {
		t.Fatal("esperaba que la columna se borrara físicamente")
	}
}

func TestTablaColumnas_EditarEtiquetaDeCampoExistenteRechaza(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-COL-CAMPOEX-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Tabla", "activo": true,
	})
	tablaID := crearElementoTablaPrueba(t, tabsHandler, tabID)
	campoID := "TEST-EL-COL-CAMPOEX-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Perfil", "activo": true,
	})
	columnasHandler := &TablaColumnasHandler{DB: tabsHandler.DB}
	_, res := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{"origen": "CAMPO_EXISTENTE", "campo_existente_id": campoID})
	columnaID, _ := res["columna_id"].(string)

	rec := patchColumnaTabla(t, columnasHandler, columnaID, map[string]any{"etiqueta": "Otra cosa"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("editar etiqueta de columna CAMPO_EXISTENTE: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	// pero reordenar sí es válido.
	rec = patchColumnaTabla(t, columnasHandler, columnaID, map[string]any{"orden": 9})
	if rec.Code != http.StatusOK {
		t.Fatalf("reordenar columna CAMPO_EXISTENTE: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestTablaColumnas_EliminarInexistenteDa404(t *testing.T) {
	pool := setupTestDB(t)
	handler := &TablaColumnasHandler{DB: pool}
	rec := deleteColumnaTabla(t, handler, "00000000-0000-0000-0000-000000000000")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}
}
