package handlers

// Regresión: un cotizador compilado sin ninguna Salida mapeada guarda
// configuracion->'salidas' como JSON null (Salidas es un slice nil en
// configuracionCompilada). COALESCE solo cubre el SQL NULL, así que
// jsonb_array_elements reventaba con "cannot extract elements from a
// scalar" y GET /api/calculadoras y POST /api/cotizaciones respondían 500.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// publicarCotizadorSinSalidas compila de verdad un cotizador con un único
// campo y ninguna fila en mapa_salidas_cotizador. Con funcionCampo vacío
// el elemento queda NORMAL. Al final fuerza 'salidas' a JSON null: hoy el
// compilador ya lo deja así cuando no hay nada que mapear, y con
// TOTAL_PRECIO_OFERTA (que el compilador deriva a una Salida) es la forma
// de reproducir un compilado heredado que solo tiene la función de campo.
func publicarCotizadorSinSalidas(t *testing.T, funcionCampo string) (*pgxpool.Pool, string) {
	t.Helper()
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-NULLSAL-TAB-" + sufijoUnico()
	if rec := postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Datos", "activo": true,
	}); rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %s", rec.Body.String())
	}
	elemento := map[string]any{
		"elemento_id": "TEST-NULLSAL-CAMPO-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Monto", "orden": 1, "activo": true,
		"configuracion": map[string]any{"tipo_campo": "MONEDA"},
	}
	if funcionCampo != "" {
		elemento["funcion_campo"] = funcionCampo
	}
	if rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", elemento); rec.Code != http.StatusOK {
		t.Fatalf("crear elemento: %s", rec.Body.String())
	}
	pool := tabsHandler.DB
	if res := postCompilador(t, (&CompiladorHandler{DB: pool}).Compilar, calculadoraID); !res.Compilado {
		t.Fatalf("esperaba compilar: %+v", res)
	}
	var tipoSalidas string
	if err := pool.QueryRow(context.Background(), `
		UPDATE cotizadores_compilados
		   SET configuracion=jsonb_set(configuracion,'{salidas}','null'::jsonb)
		 WHERE calculadora_id=$1 AND estado='ACTIVA'
		RETURNING jsonb_typeof(configuracion->'salidas')`, calculadoraID).Scan(&tipoSalidas); err != nil {
		t.Fatalf("no se pudo dejar salidas en JSON null: %v", err)
	}
	if tipoSalidas != "null" {
		t.Fatalf("la fixture no reproduce el caso: salidas es %q", tipoSalidas)
	}
	return pool, calculadoraID
}

func buscarCalculadoraListada(t *testing.T, pool *pgxpool.Pool, url, calculadoraID string) (calculadoraSimple, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	(&CalculadorasHandler{DB: pool}).Listar(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: esperaba 200, dio %d: %s", url, rec.Code, rec.Body.String())
	}
	var res struct {
		Calculadoras []calculadoraSimple `json:"calculadoras"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, item := range res.Calculadoras {
		if item.CalculadoraID == calculadoraID {
			return item, true
		}
	}
	return calculadoraSimple{}, false
}

func crearClienteAltaPrueba(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	clienteID := "TEST-CLI-NULLSAL-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO clientes(cliente_id,nombre_comercial,estado) VALUES($1,'Cliente','Activo')`, clienteID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cliente_id=$1`, clienteID)
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=$1`, clienteID)
	})
	return clienteID
}

func TestSalidasJSONNull_SinPrecioListaSinRomperYRechazaAlta(t *testing.T) {
	pool, calculadoraID := publicarCotizadorSinSalidas(t, "")

	item, ok := buscarCalculadoraListada(t, pool, "/api/calculadoras", calculadoraID)
	if !ok {
		t.Fatal("el listado general omitió el cotizador publicado")
	}
	if item.DisponibleCotizacion {
		t.Fatalf("un cotizador sin TOTAL_PRECIO no debería estar disponible: %+v", item)
	}
	if _, ok := buscarCalculadoraListada(t, pool, "/api/calculadoras?uso=cotizacion", calculadoraID); ok {
		t.Fatal("?uso=cotizacion incluyó un cotizador sin TOTAL_PRECIO")
	}

	rec, _ := postCrearCotizacion(t, &CotizacionesHandler{DB: pool}, crearAdminActorPrueba(t, pool), map[string]any{
		"cliente_id": crearClienteAltaPrueba(t, pool), "calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Complete la salida y vuelva a publicarlo") {
		t.Fatalf("esperaba 409 con el mensaje accionable, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSalidasJSONNull_FuncionCampoTotalSigueHabilitandoAlta(t *testing.T) {
	pool, calculadoraID := publicarCotizadorSinSalidas(t, "TOTAL_PRECIO_OFERTA")

	for _, url := range []string{"/api/calculadoras", "/api/calculadoras?uso=cotizacion"} {
		item, ok := buscarCalculadoraListada(t, pool, url, calculadoraID)
		if !ok || !item.DisponibleCotizacion {
			t.Fatalf("%s: esperaba el cotizador disponible por funcion_campo, encontrado=%v item=%+v", url, ok, item)
		}
	}

	rec, res := postCrearCotizacion(t, &CotizacionesHandler{DB: pool}, crearAdminActorPrueba(t, pool), map[string]any{
		"cliente_id": crearClienteAltaPrueba(t, pool), "calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusCreated || res["ok"] != true {
		t.Fatalf("esperaba 201/ok, dio %d: %s", rec.Code, rec.Body.String())
	}
	if cotizacionID, _ := res["cotizacion_id"].(string); cotizacionID != "" {
		limpiarCotizacionCreada(t, pool, cotizacionID)
	}
}
