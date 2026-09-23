package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type fixtureDashboard struct {
	CalculadoraA string
	CalculadoraB string
	VendedorA    string
	VendedorB    string
	CotizacionA  string
	CotizacionB  string
	CotizacionC  string
	CotizacionD  string
	FechaBase    time.Time
}

func insertarSalidasDashboard(t *testing.T, pool *pgxpool.Pool, cotizacionID string, version int, monto, margen float64, moneda string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO cotizacion_salidas
		(cotizacion_id,numero_version,clave_salida,fuente_id,tipo_dato,valor_numero,moneda)
		VALUES($1,$2,'TOTAL_PRECIO','TEST-FUENTE','MONEDA',$3,$4),
		      ($1,$2,'MARGEN_TOTAL','TEST-MARGEN','PORCENTAJE',$5,NULL)`,
		cotizacionID, version, monto, moneda, margen)
	if err != nil {
		t.Fatalf("no se pudieron insertar salidas normalizadas: %v", err)
	}
}

func crearFixtureDashboard(t *testing.T, pool *pgxpool.Pool) fixtureDashboard {
	t.Helper()
	sufijo := strings.ReplaceAll(sufijoUnico(), ".", "")
	fixture := fixtureDashboard{
		CalculadoraA: "TEST-DASH-A-" + sufijo,
		CalculadoraB: "TEST-DASH-B-" + sufijo,
		CotizacionA:  "TEST-DASH-Q1-" + sufijo,
		CotizacionB:  "TEST-DASH-Q2-" + sufijo,
		CotizacionC:  "TEST-DASH-Q3-" + sufijo,
		CotizacionD:  "TEST-DASH-Q4-" + sufijo,
		FechaBase:    time.Now().AddDate(0, 0, -10).Truncate(time.Second),
	}
	fixture.VendedorA = crearUsuarioPrueba(t, pool, "dashboard.a."+sufijo+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	fixture.VendedorB = crearUsuarioPrueba(t, pool, "dashboard.b."+sufijo+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	ctx := context.Background()
	clienteA, clienteB, clienteC := "TEST-DASH-CLI-A-"+sufijo, "TEST-DASH-CLI-B-"+sufijo, "TEST-DASH-CLI-C-"+sufijo

	if _, err := pool.Exec(ctx, `INSERT INTO calculadoras(calculadora_id,nombre_calculadora,estado) VALUES
		($1,'Dashboard A','Activo'),($2,'Dashboard B','Activo')`, fixture.CalculadoraA, fixture.CalculadoraB); err != nil {
		t.Fatalf("no se pudo crear la base del dashboard: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO clientes(cliente_id,nombre_comercial,estado) VALUES
		($1,'Cliente Dashboard A','Activo'),($2,'Cliente Dashboard B','Activo'),($3,'Cliente Dashboard C','Activo')`, clienteA, clienteB, clienteC); err != nil {
		t.Fatalf("no se pudieron crear los clientes: %v", err)
	}

	type fila struct {
		id, calc, cliente, estado, vendedor, moneda string
		monto, margen                               float64
		dias                                        int
	}
	filas := []fila{
		{fixture.CotizacionA, fixture.CalculadoraA, clienteA, "Borrador", fixture.VendedorA, "USD", 100, .10, 0},
		// Q2 conserva V1 aceptada por USD 200, pero su versión actual V2 vale
		// USD 900. Es el caso de regresión explícito AT-04.
		{fixture.CotizacionB, fixture.CalculadoraA, clienteA, "Borrador", fixture.VendedorA, "USD", 900, .20, 1},
		{fixture.CotizacionC, fixture.CalculadoraA, clienteB, "Ganada", fixture.VendedorB, "CRC", 300, .30, 2},
		{fixture.CotizacionD, fixture.CalculadoraB, clienteC, "Perdida", fixture.VendedorA, "USD", 400, .40, 3},
	}
	for _, f := range filas {
		fecha := fixture.FechaBase.AddDate(0, 0, f.dias)
		versionActual := 1
		var versionAceptada any
		if f.id == fixture.CotizacionB {
			versionActual, versionAceptada = 2, 1
		} else if f.id == fixture.CotizacionC {
			versionAceptada = 1
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cotizaciones
			(cotizacion_id,calculadora_id,cliente_id,codigo_oferta,estado,version_actual,version_aceptada,fecha_creacion,fecha_actualizacion)
			VALUES($1,$2,$3,'OF-'||$1,$4,$5,$6,$7,$7)`, f.id, f.calc, f.cliente, f.estado, versionActual, versionAceptada, fecha); err != nil {
			t.Fatalf("no se pudo crear cotización: %v", err)
		}
		if f.id == fixture.CotizacionB {
			if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_versiones(cotizacion_id,numero_version,estado,moneda,total_precio,margen_total)
				VALUES($1,1,'Aceptada','CACHE',999999,99),($1,2,'Borrador','CACHE',999999,99)`, f.id); err != nil {
				t.Fatalf("no se pudieron crear versiones AT-04: %v", err)
			}
			insertarSalidasDashboard(t, pool, f.id, 1, 200, .15, "USD")
			insertarSalidasDashboard(t, pool, f.id, 2, f.monto, f.margen, f.moneda)
		} else {
			if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_versiones(cotizacion_id,numero_version,estado,moneda,total_precio,margen_total)
				VALUES($1,1,$2,'CACHE',999999,99)`, f.id, f.estado); err != nil {
				t.Fatalf("no se pudo crear versión: %v", err)
			}
			insertarSalidasDashboard(t, pool, f.id, 1, f.monto, f.margen, f.moneda)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_usuarios(cotizacion_id,usuario_id,funcion) VALUES($1,$2,'Vendedor')`, f.id, f.vendedor); err != nil {
			t.Fatalf("no se pudo asignar vendedor: %v", err)
		}
	}
	// El ciclo se mide desde fecha_creacion hasta el primer cierre del
	// historial: Q2=1 día, Q3=2 días y Q4=3 días.
	for _, evento := range []struct {
		id, estado string
		dias       int
	}{
		{fixture.CotizacionB, "Aceptada", 2},
		{fixture.CotizacionC, "Ganada", 4},
		{fixture.CotizacionD, "Perdida", 6},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_historial(cotizacion_id,numero_version,accion,estado_nuevo,fecha)
			VALUES($1,1,'CAMBIO_ESTADO',$2,$3)`, evento.id, evento.estado, fixture.FechaBase.AddDate(0, 0, evento.dias)); err != nil {
			t.Fatalf("no se pudo insertar historial: %v", err)
		}
	}

	t.Cleanup(func() {
		for _, id := range []string{fixture.CotizacionA, fixture.CotizacionB, fixture.CotizacionC, fixture.CotizacionD} {
			pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, id)
		}
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id=ANY($1)`, []string{clienteA, clienteB, clienteC})
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=ANY($1)`, []string{fixture.CalculadoraA, fixture.CalculadoraB})
	})
	return fixture
}

type metodoDashboard func(http.ResponseWriter, *http.Request)

func llamarDashboard(t *testing.T, metodo metodoDashboard, actorID, ruta string, params url.Values) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, ruta+"?"+params.Encode(), nil)
	req = conActor(req, actorID)
	rec := httptest.NewRecorder()
	metodo(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s respondió %d: %s", ruta, rec.Code, rec.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("respuesta inválida de %s: %v", ruta, err)
	}
	return res
}

func montoDashboard(t *testing.T, raw any, moneda string) float64 {
	t.Helper()
	for _, item := range raw.([]any) {
		m := item.(map[string]any)
		if m["moneda"] == moneda {
			return m["monto"].(float64)
		}
	}
	t.Fatalf("no se encontró moneda %s en %#v", moneda, raw)
	return 0
}

func TestDashboard_AT04_MontoAceptadoUsaVersionAceptada(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	f := crearFixtureDashboard(t, pool)
	res := llamarDashboard(t, (&DashboardHandler{DB: pool}).Resumen, actor, "/api/dashboard/resumen", url.Values{"calculadora_id": {f.CalculadoraA}})
	resumen := res["resumen"].(map[string]any)
	if got := montoDashboard(t, resumen["montos_aceptados"], "USD"); got != 200 {
		t.Fatalf("AT-04: monto aceptado debía usar V1=200, obtuvo %.2f (V2 vale 900)", got)
	}
	if resumen["aceptadas"].(float64) != 2 {
		t.Fatalf("Aceptadas debe incluir las dos cotizaciones con version_aceptada: %#v", resumen)
	}
}

func TestDashboard_CincoEndpointsConDatosNormalizados(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	f := crearFixtureDashboard(t, pool)
	h := &DashboardHandler{DB: pool}
	casos := []struct {
		ruta, clave string
		metodo      metodoDashboard
	}{
		{"/api/dashboard/resumen", "resumen", h.Resumen},
		{"/api/dashboard/tendencia", "tendencia", h.Tendencia},
		{"/api/dashboard/estados", "estados", h.Estados},
		{"/api/dashboard/segmentacion-clientes", "segmentos", h.SegmentacionClientes},
		{"/api/dashboard/cotizadores", "cotizadores", h.Cotizadores},
	}
	for _, tc := range casos {
		t.Run(tc.clave, func(t *testing.T) {
			res := llamarDashboard(t, tc.metodo, actor, tc.ruta, url.Values{"calculadora_id": {f.CalculadoraA}})
			if res[tc.clave] == nil {
				t.Fatalf("%s no devolvió %s: %#v", tc.ruta, tc.clave, res)
			}
		})
	}
}

func TestDashboard_SeparaMontosPorMoneda(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	f := crearFixtureDashboard(t, pool)
	res := llamarDashboard(t, (&DashboardHandler{DB: pool}).Resumen, actor, "/api/dashboard/resumen", url.Values{"calculadora_id": {f.CalculadoraA}})
	resumen := res["resumen"].(map[string]any)
	if got := montoDashboard(t, resumen["tickets_promedio"], "USD"); got != 500 {
		t.Fatalf("ticket USD debía promediar 100 y 900 sin CRC: %.2f", got)
	}
	if got := montoDashboard(t, resumen["tickets_promedio"], "CRC"); got != 300 {
		t.Fatalf("ticket CRC incorrecto: %.2f", got)
	}
	if _, mezclado := resumen["monto_cotizado"]; mezclado {
		t.Fatal("no debe existir un total monetario escalar que mezcle monedas")
	}
	monedas := fmt.Sprint(res["monedas"])
	if !strings.Contains(monedas, "USD") || !strings.Contains(monedas, "CRC") {
		t.Fatalf("desglose de monedas incompleto: %v", res["monedas"])
	}
}

func TestDashboard_FiltrosUniformesEnCincoEndpoints(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	f := crearFixtureDashboard(t, pool)
	h := &DashboardHandler{DB: pool}
	params := url.Values{
		"cotizador_id": {f.CalculadoraA},
		"vendedor_id":  {f.VendedorA},
		"tipo_cliente": {"Cliente existente"},
		"fecha_desde":  {f.FechaBase.AddDate(0, 0, 1).Format("2006-01-02")},
		"fecha_hasta":  {f.FechaBase.AddDate(0, 0, 1).Format("2006-01-02")},
	}
	resumen := llamarDashboard(t, h.Resumen, actor, "/api/dashboard/resumen", params)["resumen"].(map[string]any)
	if resumen["cotizaciones"].(float64) != 1 {
		t.Fatalf("el resumen no aplicó todos los filtros: %#v", resumen)
	}
	for _, tc := range []struct {
		metodo metodoDashboard
		ruta   string
		clave  string
	}{
		{h.Tendencia, "/api/dashboard/tendencia", "tendencia"},
		{h.Estados, "/api/dashboard/estados", "estados"},
		{h.SegmentacionClientes, "/api/dashboard/segmentacion-clientes", "segmentos"},
		{h.Cotizadores, "/api/dashboard/cotizadores", "cotizadores"},
	} {
		res := llamarDashboard(t, tc.metodo, actor, tc.ruta, params)
		filas := res[tc.clave].([]any)
		cantidad := 0.0
		for _, raw := range filas {
			cantidad += raw.(map[string]any)["cantidad"].(float64)
		}
		if cantidad != 1 {
			t.Fatalf("%s no aplicó filtros uniformes: %#v", tc.ruta, res)
		}
	}
}

func contarClaveJSON(valor any, clave string) int {
	total := 0
	switch v := valor.(type) {
	case map[string]any:
		for k, item := range v {
			if k == clave {
				total++
			}
			total += contarClaveJSON(item, clave)
		}
	case []any:
		for _, item := range v {
			total += contarClaveJSON(item, clave)
		}
	}
	return total
}

func TestDashboard_PermisoMargenEnCincoEndpoints(t *testing.T) {
	pool := setupTestDB(t)
	admin := crearAdminActorPrueba(t, pool)
	vendedor := crearUsuarioPrueba(t, pool, "dashboard.sin.margen."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	f := crearFixtureDashboard(t, pool)
	h := &DashboardHandler{DB: pool}
	for _, tc := range []struct {
		metodo metodoDashboard
		ruta   string
	}{
		{h.Resumen, "/api/dashboard/resumen"},
		{h.Tendencia, "/api/dashboard/tendencia"},
		{h.Estados, "/api/dashboard/estados"},
		{h.SegmentacionClientes, "/api/dashboard/segmentacion-clientes"},
		{h.Cotizadores, "/api/dashboard/cotizadores"},
	} {
		params := url.Values{"calculadora_id": {f.CalculadoraA}}
		conPermiso := llamarDashboard(t, tc.metodo, admin, tc.ruta, params)
		sinPermiso := llamarDashboard(t, tc.metodo, vendedor, tc.ruta, params)
		if contarClaveJSON(conPermiso, "margen_promedio") == 0 {
			t.Fatalf("%s debe incluir margen para Administrador", tc.ruta)
		}
		if contarClaveJSON(sinPermiso, "margen_promedio") != 0 {
			t.Fatalf("%s filtró datos sensibles después de responder: %#v", tc.ruta, sinPermiso)
		}
	}
}

func TestDashboard_CicloYTasasDesdeHistorial(t *testing.T) {
	pool := setupTestDB(t)
	actor := crearAdminActorPrueba(t, pool)
	f := crearFixtureDashboard(t, pool)
	res := llamarDashboard(t, (&DashboardHandler{DB: pool}).Resumen, actor, "/api/dashboard/resumen", url.Values{"calculadora_id": {f.CalculadoraA}})
	k := res["resumen"].(map[string]any)
	if k["tiempo_promedio_ciclo_dias"].(float64) != 1.5 || k["tasa_aceptacion"].(float64) != 100 || k["tasa_ganancia"].(float64) != 50 {
		t.Fatalf("ciclo o tasas incorrectas: %#v", k)
	}
}
