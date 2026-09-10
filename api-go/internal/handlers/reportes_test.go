package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fixtureReportes struct {
	CalculadoraA string
	CalculadoraB string
	VendedorA    string
	VendedorB    string
	Codigos      []string
}

func crearFixtureReportes(t *testing.T, pool *pgxpool.Pool) fixtureReportes {
	t.Helper()
	sufijo := strings.ReplaceAll(sufijoUnico(), ".", "")
	vendedorA := crearUsuarioPrueba(t, pool, "reportes.a."+sufijo+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	vendedorB := crearUsuarioPrueba(t, pool, "reportes.b."+sufijo+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	calcA, calcB := "TEST-REP-A-"+sufijo, "TEST-REP-B-"+sufijo
	clienteA, clienteB := "TEST-REP-CLI-A-"+sufijo, "TEST-REP-CLI-B-"+sufijo
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO calculadoras(calculadora_id,nombre_calculadora,estado) VALUES
		($1,'Reporte A','Activo'),($2,'Reporte B','Activo')`, calcA, calcB); err != nil {
		t.Fatalf("no se pudo crear la base del reporte: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO clientes(cliente_id,nombre_comercial,razon_social,estado) VALUES
		($1,'Cliente, Reporte A','Empresa Reporte A S.A.','Activo'),
		($2,'Cliente Reporte B','Empresa Reporte B S.A.','Activo')`, clienteA, clienteB); err != nil {
		t.Fatalf("no se pudieron crear los clientes del reporte: %v", err)
	}

	type fila struct {
		id, codigo, calc, cliente, estado, vendedor, fecha string
		monto, margen                                      float64
	}
	filas := []fila{
		{"TEST-REP-Q1-" + sufijo, "OF-REP-1-" + sufijo, calcA, clienteA, "Borrador", vendedorA, "2026-08-10T12:00:00Z", 100, 10},
		{"TEST-REP-Q2-" + sufijo, "OF-REP-2-" + sufijo, calcA, clienteA, "Ganada", vendedorA, "2026-08-20T12:00:00Z", 200, 20},
		{"TEST-REP-Q3-" + sufijo, "OF-REP-3-" + sufijo, calcB, clienteB, "Perdida", vendedorB, "2026-09-05T12:00:00Z", 300, 30},
		{"TEST-REP-Q4-" + sufijo, "OF-REP-4-" + sufijo, calcB, clienteB, "Aceptada", vendedorA, "2026-07-01T12:00:00Z", 400, 40},
	}
	for _, f := range filas {
		fecha, err := time.Parse(time.RFC3339, f.fecha)
		if err != nil {
			t.Fatalf("fecha inválida en fixture: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cotizaciones(cotizacion_id,calculadora_id,cliente_id,codigo_oferta,estado,version_actual,fecha_creacion,fecha_actualizacion)
			VALUES($1,$2,$3,$4,$5,1,$6,$6)`, f.id, f.calc, f.cliente, f.codigo, f.estado, fecha); err != nil {
			t.Fatalf("no se pudo crear cotización del reporte: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_versiones(cotizacion_id,numero_version,estado,moneda,total_precio,margen_total)
			VALUES($1,1,$2,'US$',$3,$4)`, f.id, f.estado, f.monto, f.margen); err != nil {
			t.Fatalf("no se pudo crear la versión del reporte: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_usuarios(cotizacion_id,usuario_id,funcion) VALUES($1,$2,'Vendedor')`, f.id, f.vendedor); err != nil {
			t.Fatalf("no se pudo asignar vendedor del reporte: %v", err)
		}
	}

	t.Cleanup(func() {
		for _, f := range filas {
			pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, f.id)
		}
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=ANY($1)`, []string{clienteA, clienteB})
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=ANY($1)`, []string{calcA, calcB})
	})

	return fixtureReportes{
		CalculadoraA: calcA,
		CalculadoraB: calcB,
		VendedorA:    vendedorA,
		VendedorB:    vendedorB,
		Codigos:      []string{filas[0].codigo, filas[1].codigo, filas[2].codigo, filas[3].codigo},
	}
}

func ejecutarReporteJSON(t *testing.T, handler *ReportesHandler, actor string, params url.Values) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/reportes/cotizaciones?"+params.Encode(), nil)
	req = conActor(req, actor)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)
	var respuesta map[string]any
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	return rec, respuesta
}

// leerCSVReporteTest quita el BOM UTF-8 que Exportar antepone (ver Fix 1
// de reportes.go) y lee con ';' como separador, igual que csv.Writer allá.
func leerCSVReporteTest(cuerpo string) ([][]string, error) {
	cuerpo = strings.TrimPrefix(cuerpo, "\uFEFF")
	lector := csv.NewReader(strings.NewReader(cuerpo))
	lector.Comma = ';'
	return lector.ReadAll()
}

func filasReporteTest(t *testing.T, respuesta map[string]any) []map[string]any {
	t.Helper()
	raw, ok := respuesta["filas"].([]any)
	if !ok {
		t.Fatalf("filas no tiene el formato esperado: %#v", respuesta["filas"])
	}
	filas := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		fila, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("fila inválida: %#v", item)
		}
		filas = append(filas, fila)
	}
	return filas
}

func codigosReporte(filas []map[string]any) []string {
	codigos := make([]string, 0, len(filas))
	for _, fila := range filas {
		if codigo, ok := fila["codigo_oferta"].(string); ok {
			codigos = append(codigos, codigo)
		}
	}
	return codigos
}

func TestReportes_ListarSinFiltrosIncluyeFixtures(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureReportes(t, pool)
	rec, respuesta := ejecutarReporteJSON(t, &ReportesHandler{DB: pool}, actor, url.Values{})
	if rec.Code != http.StatusOK || respuesta["ok"] != true {
		t.Fatalf("respuesta inesperada: %d %s", rec.Code, rec.Body.String())
	}
	codigos := codigosReporte(filasReporteTest(t, respuesta))
	for _, esperado := range fixture.Codigos {
		if !slices.Contains(codigos, esperado) {
			t.Errorf("el listado sin filtros no incluyó %s", esperado)
		}
	}
}

func TestReportes_FiltraCadaParametro(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureReportes(t, pool)
	handler := &ReportesHandler{DB: pool}

	casos := []struct {
		nombre   string
		params   url.Values
		esperado []string
	}{
		{"calculadora_id", url.Values{"calculadora_id": {fixture.CalculadoraA}}, fixture.Codigos[:2]},
		{"vendedor_id", url.Values{"vendedor_id": {fixture.VendedorB}}, []string{fixture.Codigos[2]}},
		{"estado", url.Values{"calculadora_id": {fixture.CalculadoraA}, "estado": {"Ganada"}}, []string{fixture.Codigos[1]}},
		{"fecha_desde", url.Values{"calculadora_id": {fixture.CalculadoraA}, "fecha_desde": {"2026-08-15"}}, []string{fixture.Codigos[1]}},
		{"fecha_hasta", url.Values{"calculadora_id": {fixture.CalculadoraA}, "fecha_hasta": {"2026-08-15"}}, []string{fixture.Codigos[0]}},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			rec, respuesta := ejecutarReporteJSON(t, handler, actor, caso.params)
			if rec.Code != http.StatusOK {
				t.Fatalf("respondió %d: %s", rec.Code, rec.Body.String())
			}
			obtenidos := codigosReporte(filasReporteTest(t, respuesta))
			if len(obtenidos) != len(caso.esperado) {
				t.Fatalf("esperaba %v, obtuvo %v", caso.esperado, obtenidos)
			}
			for _, esperado := range caso.esperado {
				if !slices.Contains(obtenidos, esperado) {
					t.Errorf("faltó %s en %v", esperado, obtenidos)
				}
			}
		})
	}
}

func TestReportes_SinPermisoOmiteMargenEnJSONYCSV(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearUsuarioPrueba(t, pool, "reportes.sin.margen."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	fixture := crearFixtureReportes(t, pool)
	handler := &ReportesHandler{DB: pool}
	params := url.Values{"calculadora_id": {fixture.CalculadoraA}}

	recJSON, respuesta := ejecutarReporteJSON(t, handler, actor, params)
	if recJSON.Code != http.StatusOK {
		t.Fatalf("JSON respondió %d: %s", recJSON.Code, recJSON.Body.String())
	}
	for _, fila := range filasReporteTest(t, respuesta) {
		if _, existe := fila["margen_total"]; existe {
			t.Errorf("margen_total no debe existir para un Vendedor: %#v", fila)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/reportes/cotizaciones/exportar?"+params.Encode(), nil)
	req = conActor(req, actor)
	recCSV := httptest.NewRecorder()
	handler.Exportar(recCSV, req)
	if recCSV.Code != http.StatusOK {
		t.Fatalf("CSV respondió %d: %s", recCSV.Code, recCSV.Body.String())
	}
	registros, err := leerCSVReporteTest(recCSV.Body.String())
	if err != nil {
		t.Fatalf("CSV inválido: %v\n%s", err, recCSV.Body.String())
	}
	if slices.Contains(registros[0], "Margen total") {
		t.Fatalf("la columna Margen total no debe existir: %v", registros[0])
	}
	for _, registro := range registros {
		if len(registro) != 9 {
			t.Fatalf("se esperaban 9 columnas sin margen, obtuvo %d: %v", len(registro), registro)
		}
	}
}

func TestReportes_ExportaCSVConEncabezadosYSeparadorCorrectos(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureReportes(t, pool)
	handler := &ReportesHandler{DB: pool}
	params := url.Values{"calculadora_id": {fixture.CalculadoraA}}
	req := httptest.NewRequest(http.MethodGet, "/api/reportes/cotizaciones/exportar?"+params.Encode(), nil)
	req = conActor(req, actor)
	rec := httptest.NewRecorder()
	handler.Exportar(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("CSV respondió %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Errorf("Content-Type inesperado: %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="reporte_cotizaciones.csv"` {
		t.Errorf("Content-Disposition inesperado: %q", got)
	}
	registros, err := leerCSVReporteTest(rec.Body.String())
	if err != nil {
		t.Fatalf("encoding/csv no pudo releer la exportación: %v", err)
	}
	esperados := []string{"Código de oferta", "Cliente", "Empresa", "Cotizador", "Estado", "Total precio", "Moneda", "Vendedor", "Fecha de creación", "Margen total"}
	if len(registros) != 3 || !slices.Equal(registros[0], esperados) {
		t.Fatalf("CSV inesperado: encabezados=%v filas=%d", registros[0], len(registros))
	}
	// El nombre contiene una coma; poder releerlo como una sola columna
	// confirma que encoding/csv aplicó las comillas y el separador bien.
	for _, registro := range registros[1:] {
		if len(registro) != len(esperados) {
			t.Fatalf("fila CSV mal separada: %v", registro)
		}
	}
}

func TestReportes_RechazaFiltrosInvalidos(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	handler := &ReportesHandler{DB: pool}
	for _, params := range []url.Values{
		{"fecha_desde": {"05-09-2026"}},
		{"fecha_desde": {"2026-09-05"}, "fecha_hasta": {"2026-09-04"}},
		{"estado": {"Inventado"}},
	} {
		rec, _ := ejecutarReporteJSON(t, handler, actor, params)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("esperaba 400 para %v, obtuvo %d: %s", params, rec.Code, rec.Body.String())
		}
	}
}

// Asegura que la respuesta sigue siendo JSON estándar y no contiene
// números codificados como texto por accidente.
func TestReportes_TotalPrecioEsNumeroJSON(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureReportes(t, pool)
	rec, _ := ejecutarReporteJSON(t, &ReportesHandler{DB: pool}, actor, url.Values{"calculadora_id": {fixture.CalculadoraA}})
	var respuesta struct {
		Filas []struct {
			TotalPrecio json.Number `json:"total_precio"`
		} `json:"filas"`
	}
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	dec.UseNumber()
	if err := dec.Decode(&respuesta); err != nil || len(respuesta.Filas) != 2 {
		t.Fatalf("respuesta numérica inválida: %v", err)
	}
}
