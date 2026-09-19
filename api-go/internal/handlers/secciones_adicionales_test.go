package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSeccionesAdicionales_FlujoCompletoCompiladorYRuntime(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	sufijo := sufijoUnico()
	origenID := "TEST-CALC-ORIGEN-" + sufijo
	destinoID := "TEST-CALC-DESTINO-" + sufijo
	tabOrigenID := "TEST-TAB-REUTIL-" + sufijo
	tabDestinoID := "TEST-TAB-DESTINO-" + sufijo
	campoOrigenID := "TEST-EL-REUTIL-" + sufijo
	selectorID := "TEST-EL-SECCIONES-" + sufijo

	if _, err := pool.Exec(ctx, `
		INSERT INTO calculadoras (calculadora_id,nombre_calculadora) VALUES
		($1,'Cotizador origen'),($2,'Cotizador destino')`, origenID, destinoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id=ANY($1)`, []string{origenID, destinoID})
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE calculadora_id=ANY($1)`, []string{origenID, destinoID})
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=ANY($1)`, []string{origenID, destinoID})
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,alcance,orden,activo) VALUES
		($1,$2,'Información reutilizable','REUTILIZABLE',1,true),
		($3,$4,'Datos propios','PROPIO',1,true)`, tabOrigenID, origenID, tabDestinoID, destinoID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo) VALUES
		($1,$2,'CAMPO','Dato compartido',1,'{"tipo_campo":"TEXTO"}'::jsonb,true),
		($3,$4,'SECCIONES_ADICIONALES','Secciones compartidas',1,'{"presentacion":"CHECKS","columnas":2}'::jsonb,true)`,
		campoOrigenID, tabOrigenID, selectorID, tabDestinoID); err != nil {
		t.Fatal(err)
	}

	secciones := &SeccionesAdicionalesHandler{DB: pool}
	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/secciones-reutilizables?calculadora_id="+url.QueryEscape(destinoID), nil)
	rec := httptest.NewRecorder()
	secciones.ListarReutilizables(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tabOrigenID) {
		t.Fatalf("la sección reutilizable no apareció: %d %s", rec.Code, rec.Body.String())
	}

	router := chi.NewRouter()
	router.Post("/api/cotizador/elementos/{elemento_id}/secciones", secciones.Asociar)
	router.Delete("/api/cotizador/elementos/{elemento_id}/secciones/{tab_id}", secciones.Desasociar)
	rec = postCatalogos(t, router.ServeHTTP, "/api/cotizador/elementos/"+selectorID+"/secciones", map[string]any{"tab_ids": []string{tabOrigenID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("asociar sección: %d %s", rec.Code, rec.Body.String())
	}

	tabsHandler := &CotizadorTabsHandler{DB: pool}
	req = httptest.NewRequest(http.MethodGet, "/api/cotizador/tabs?calculadora_id="+url.QueryEscape(destinoID), nil)
	rec = httptest.NewRecorder()
	tabsHandler.ListarTabs(rec, req)
	var listado struct {
		Tabs []tabCotizador `json:"tabs"`
	}
	assertJSON(t, rec.Body.Bytes(), &listado)
	if len(listado.Tabs) != 2 {
		t.Fatalf("esperaba sección propia + asociada: %s", rec.Body.String())
	}
	var asociada *tabCotizador
	for i := range listado.Tabs {
		if listado.Tabs[i].TabID == tabOrigenID {
			asociada = &listado.Tabs[i]
		}
	}
	if asociada == nil || asociada.EsPropia || !asociada.SoloLectura || asociada.ElementoAsociacionID == nil {
		t.Fatalf("la sección asociada no quedó marcada como solo lectura: %+v", asociada)
	}

	rec = postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabOrigenID, "calculadora_id": origenID, "nombre": "Información reutilizable",
		"alcance": "PROPIO", "orden": 1, "activo": true,
	})
	if rec.Code != http.StatusConflict || !strings.Contains(strings.ToLower(rec.Body.String()), "desas") {
		t.Fatalf("cambiar a PROPIO asociada debía fallar claramente: %d %s", rec.Code, rec.Body.String())
	}

	compilador := &CompiladorHandler{DB: pool}
	resultado, err := compilador.validarConfiguracion(ctx, destinoID)
	if err != nil || !resultado.Valido {
		t.Fatalf("validar cotizador con sección asociada: resultado=%+v err=%v", resultado, err)
	}
	if len(resultado.Tabs) != 2 || resultado.Tabs[1].TabID != tabOrigenID || len(resultado.Tabs[1].Elementos) != 1 || resultado.Tabs[1].Elementos[0].ElementoID != campoOrigenID {
		t.Fatalf("el compilado no incorporó el contenido asociado: %+v", resultado.Tabs)
	}
	for _, tab := range resultado.Tabs {
		for _, elemento := range tab.Elementos {
			if elemento.Tipo == "SECCIONES_ADICIONALES" {
				t.Fatalf("el selector de diseño no debe convertirse en un campo de runtime: %+v", elemento)
			}
		}
	}
	resCompilado := postCompilador(t, compilador.Compilar, destinoID)
	if !resCompilado.Compilado {
		t.Fatalf("no se publicó el cotizador: %+v", resCompilado)
	}

	cotizacionID, calculadoraTemporalID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	if _, err := pool.Exec(ctx, `UPDATE cotizaciones SET calculadora_id=$2 WHERE cotizacion_id=$1`, cotizacionID, destinoID); err != nil {
		t.Fatal(err)
	}
	runtime := &CotizadorRuntimeHandler{DB: pool}
	routerRuntime := chi.NewRouter()
	routerRuntime.Get("/api/cotizador/runtime/{cotizacion_id}", runtime.Obtener)
	rec = httptest.NewRecorder()
	routerRuntime.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cotizador/runtime/"+cotizacionID, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), campoOrigenID) {
		t.Fatalf("runtime no mostró el campo de la sección asociada: %d %s", rec.Code, rec.Body.String())
	}
	_ = calculadoraTemporalID

	rec = postCatalogos(t, router.ServeHTTP, "/api/cotizador/elementos/"+selectorID+"/secciones/"+tabOrigenID, map[string]any{})
	if rec.Code != http.StatusMethodNotAllowed {
		// postCatalogos siempre usa POST; se prueba el DELETE real abajo.
		t.Fatalf("la ruta de desasociación no debe aceptar POST: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/cotizador/elementos/"+selectorID+"/secciones/"+tabOrigenID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("desasociar: %d %s", rec.Code, rec.Body.String())
	}
	var existeOrigen bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tabs_cotizador WHERE tab_id=$1 AND activo=true)`, tabOrigenID).Scan(&existeOrigen); err != nil || !existeOrigen {
		t.Fatalf("la sección original fue afectada al desasociar: existe=%v err=%v", existeOrigen, err)
	}
	resultado, err = compilador.validarConfiguracion(ctx, destinoID)
	if err != nil || len(resultado.Tabs) != 1 || resultado.Tabs[0].TabID != tabDestinoID {
		t.Fatalf("la sección desasociada aún aparece en el destino: %+v err=%v", resultado.Tabs, err)
	}
}

func TestSeccionesAdicionales_RechazaSeccionPropiaONoReutilizable(t *testing.T) {
	pool := setupTestDB(t)
	ctx := context.Background()
	sufijo := sufijoUnico()
	calculadoraID := "TEST-CALC-SEC-VAL-" + sufijo
	tabID := "TEST-TAB-SEC-VAL-" + sufijo
	elementoID := "TEST-EL-SEC-VAL-" + sufijo
	if _, err := pool.Exec(ctx, `INSERT INTO calculadoras (calculadora_id,nombre_calculadora) VALUES ($1,'Validación secciones')`, calculadoraID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE calculadora_id=$1`, calculadoraID)
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, calculadoraID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,alcance,activo) VALUES ($1,$2,'Propia','PROPIO',true)`, tabID, calculadoraID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,configuracion,activo)
		VALUES ($1,$2,'SECCIONES_ADICIONALES','Selector','{"columnas":2}'::jsonb,true)`, elementoID, tabID); err != nil {
		t.Fatal(err)
	}
	handler := &SeccionesAdicionalesHandler{DB: pool}
	router := chi.NewRouter()
	router.Post("/api/cotizador/elementos/{elemento_id}/secciones", handler.Asociar)
	rec := postCatalogos(t, router.ServeHTTP, "/api/cotizador/elementos/"+elementoID+"/secciones", map[string]any{"tab_ids": []string{tabID}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("asociar una sección propia debía fallar: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSeccionesAdicionales_RespuestaEsJSONValido(t *testing.T) {
	// Guardia pequeña contra respuestas HTML accidentales en los endpoints
	// nuevos; los casos de persistencia se cubren en el flujo completo.
	pool := setupTestDB(t)
	handler := &SeccionesAdicionalesHandler{DB: pool}
	rec := httptest.NewRecorder()
	handler.ListarReutilizables(rec, httptest.NewRequest(http.MethodGet, "/api/cotizador/secciones-reutilizables", nil))
	var respuesta map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &respuesta); err != nil || rec.Code != http.StatusBadRequest {
		t.Fatalf("respuesta inválida: status=%d body=%s err=%v", rec.Code, rec.Body.String(), err)
	}
}
