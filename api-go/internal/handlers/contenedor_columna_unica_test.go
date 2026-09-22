package handlers

// Columna 1 en Contenedor (DIS-003, Ronda D, Crítica). Antes solo se
// aceptaba columnas: 2 o 3; ahora también 1 (ancho completo, sin dividir
// en grid) — mismo criterio de validación en cotizador_tabs.go que ya
// rechazaba columnas=5 en TestCotizadorElementos_ContenedorValidaColumnasYSinPadre.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCotizadorElementos_ContenedorAceptaUnaColumna(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CONT-1COL-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Contenedor 1 columna", "activo": true,
	})

	elementoID := "TEST-EL-CONT-1COL-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID,
		"tipo": "CONTENEDOR", "etiqueta": "Ancho completo", "configuracion": map[string]any{"columnas": 1}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("DIS-003: columnas=1 debería aceptarse, dio %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	recLista := httptest.NewRecorder()
	handler.ListarElementos(recLista, req)
	var res struct {
		OK        bool                   `json:"ok"`
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, recLista.Body.Bytes(), &res)
	var encontrado *elementoTabCotizador
	for i := range res.Elementos {
		if res.Elementos[i].ElementoID == elementoID {
			encontrado = &res.Elementos[i]
		}
	}
	if encontrado == nil {
		t.Fatalf("el elemento %s no apareció en el listado: %+v", elementoID, res)
	}
	if cols, _ := encontrado.Configuracion["columnas"].(float64); cols != 1 {
		t.Fatalf("esperaba columnas=1 guardado, dio %v", encontrado.Configuracion["columnas"])
	}
}
