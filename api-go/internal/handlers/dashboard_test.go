package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type respuestaDashboardTest struct {
	OK                    bool                  `json:"ok"`
	KPIs                  dashboardKPIs         `json:"kpis"`
	CotizacionesRecientes []dashboardCotizacion `json:"cotizaciones_recientes"`
}

type fixtureDashboard struct {
	CalculadoraA string
	CalculadoraB string
	VendedorA    string
	VendedorB    string
}

func crearFixtureDashboard(t *testing.T, pool *pgxpool.Pool) fixtureDashboard {
	t.Helper()
	sufijo := sufijoUnico()
	vendedorA := crearUsuarioPrueba(t, pool, "dashboard.a."+sufijo+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	vendedorB := crearUsuarioPrueba(t, pool, "dashboard.b."+sufijo+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	calcA, calcB := "TEST-DASH-A-"+sufijo, "TEST-DASH-B-"+sufijo
	clienteA, clienteB, clienteC := "TEST-DASH-CLI-A-"+sufijo, "TEST-DASH-CLI-B-"+sufijo, "TEST-DASH-CLI-C-"+sufijo
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO calculadoras(calculadora_id,nombre_calculadora,estado) VALUES
		($1,'Dashboard A','Activo'),($2,'Dashboard B','Activo');
		INSERT INTO clientes(cliente_id,nombre_comercial,estado) VALUES
		($3,'Cliente Dashboard A','Activo'),($4,'Cliente Dashboard B','Activo'),($5,'Cliente Dashboard C','Activo')`,
		calcA, calcB, clienteA, clienteB, clienteC); err != nil {
		t.Fatalf("no se pudo crear la base del dashboard: %v", err)
	}

	type fila struct {
		id, calc, cliente, estado, vendedor string
		monto, margen                       float64
		dias                                int
	}
	filas := []fila{
		{"TEST-DASH-Q1-" + sufijo, calcA, clienteA, "Borrador", vendedorA, 100, 10, -4},
		{"TEST-DASH-Q2-" + sufijo, calcA, clienteA, "Aceptada", vendedorA, 200, 20, -3},
		{"TEST-DASH-Q3-" + sufijo, calcA, clienteB, "Ganada", vendedorB, 300, 30, -2},
		{"TEST-DASH-Q4-" + sufijo, calcB, clienteC, "Perdida", vendedorA, 400, 40, -1},
	}
	for _, f := range filas {
		fecha := time.Now().AddDate(0, 0, f.dias)
		if _, err := pool.Exec(ctx, `
			INSERT INTO cotizaciones(cotizacion_id,calculadora_id,cliente_id,codigo_oferta,estado,version_actual,fecha_creacion,fecha_actualizacion)
			VALUES($1,$2,$3,'OF-'||$1,$4,1,$5,$5);
			INSERT INTO cotizacion_versiones(cotizacion_id,numero_version,estado,moneda,total_precio,margen_total)
			VALUES($1,1,$4,'US$',$6,$7);
			INSERT INTO cotizacion_usuarios(cotizacion_id,usuario_id,funcion) VALUES($1,$8,'Vendedor')`,
			f.id, f.calc, f.cliente, f.estado, fecha, f.monto, f.margen, f.vendedor); err != nil {
			t.Fatalf("no se pudo crear cotización del dashboard: %v", err)
		}
	}

	t.Cleanup(func() {
		for _, f := range filas {
			pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, f.id)
		}
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=ANY($1)`, []string{clienteA, clienteB, clienteC})
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=ANY($1)`, []string{calcA, calcB})
	})
	return fixtureDashboard{CalculadoraA: calcA, CalculadoraB: calcB, VendedorA: vendedorA, VendedorB: vendedorB}
}

func obtenerDashboardTest(t *testing.T, handler *DashboardHandler, actorID string, params url.Values) respuestaDashboardTest {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard?"+params.Encode(), nil)
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	handler.Obtener(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard respondió %d: %s", rec.Code, rec.Body.String())
	}
	var res respuestaDashboardTest
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("respuesta inválida: %v", err)
	}
	return res
}

func TestDashboard_AgregaEstadosYMontosReales(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureDashboard(t, pool)
	res := obtenerDashboardTest(t, &DashboardHandler{DB: pool}, actor, url.Values{"calculadora_id": {fixture.CalculadoraA}})

	if !res.OK || len(res.CotizacionesRecientes) != 3 {
		t.Fatalf("respuesta inesperada: ok=%v filas=%d", res.OK, len(res.CotizacionesRecientes))
	}
	k := res.KPIs
	if k.CotizacionesMes != 3 || k.Vigentes != 1 || k.MontoCotizado != 100 ||
		k.Aceptadas != 1 || k.MontoAceptado != 200 || k.Ganadas != 1 ||
		k.MontoGanado != 300 || k.Perdidas != 0 || k.VencidasCanceladas != 0 ||
		k.TicketPromedioGeneral != 200 {
		t.Fatalf("KPIs incorrectos: %+v", k)
	}
	prospectos, existentes := 0, 0
	for _, fila := range res.CotizacionesRecientes {
		if fila.TipoCliente == "Prospecto" {
			prospectos++
		} else if fila.TipoCliente == "Cliente existente" {
			existentes++
		}
	}
	if prospectos != 2 || existentes != 1 {
		t.Fatalf("segmentación incorrecta: prospectos=%d existentes=%d", prospectos, existentes)
	}
}

func TestDashboard_FiltraPorVendedorID(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureDashboard(t, pool)
	res := obtenerDashboardTest(t, &DashboardHandler{DB: pool}, actor, url.Values{"vendedor_id": {fixture.VendedorA}})
	if len(res.CotizacionesRecientes) != 3 || res.KPIs.Vigentes != 1 || res.KPIs.Aceptadas != 1 || res.KPIs.Perdidas != 1 {
		t.Fatalf("filtro de vendedor incorrecto: filas=%d kpis=%+v", len(res.CotizacionesRecientes), res.KPIs)
	}
	for _, fila := range res.CotizacionesRecientes {
		if fila.VendedorID == nil || *fila.VendedorID != fixture.VendedorA {
			t.Fatalf("el filtro incluyó otro vendedor: %+v", fila)
		}
	}
}

func TestDashboard_FiltraPorTipoCliente(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	fixture := crearFixtureDashboard(t, pool)
	res := obtenerDashboardTest(t, &DashboardHandler{DB: pool}, actor, url.Values{
		"calculadora_id": {fixture.CalculadoraA},
		"tipo_cliente":   {"Cliente existente"},
	})
	if len(res.CotizacionesRecientes) != 1 || res.KPIs.Aceptadas != 1 || res.KPIs.MontoAceptado != 200 {
		t.Fatalf("filtro de tipo de cliente incorrecto: filas=%d kpis=%+v", len(res.CotizacionesRecientes), res.KPIs)
	}
	if res.CotizacionesRecientes[0].TipoCliente != "Cliente existente" {
		t.Fatalf("tipo inesperado: %+v", res.CotizacionesRecientes[0])
	}
}
