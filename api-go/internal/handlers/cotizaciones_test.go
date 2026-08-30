package handlers

// Pruebas de integración del cascarón del Gestor de Cotizaciones.
// Mismo criterio que el resto del paquete: contra Postgres real, con
// fixtures propias que se limpian solas. No hay endpoint de creación
// en este sprint — las cotizaciones de prueba se insertan directo,
// igual que nacerían desde una Solicitud en un sprint futuro.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// crearCotizacionPrueba inserta una calculadora, un cliente y una
// cotización descartables con su versión 1, y la borra (cascada sobre
// versiones/usuarios/historial) al terminar el test. vendedorID y
// analistaID son opcionales (cadena vacía = no asignar) — si se
// pasan, deben venir de usuarios creados ANTES de llamar acá, para
// que el orden de limpieza sea correcto (la fila de
// cotizacion_usuarios tiene que desaparecer antes de que
// crearUsuarioPrueba intente borrar el usuario).
func crearCotizacionPrueba(t *testing.T, pool *pgxpool.Pool, estado, vendedorID, analistaID string) (cotizacionID, calculadoraID, clienteID string) {
	t.Helper()
	ctx := context.Background()
	sufijo := sufijoUnico()
	calculadoraID = "TEST-CALC-COT-" + sufijo
	clienteID = "TEST-CLI-" + sufijo
	cotizacionID = "TEST-COT-" + sufijo

	if _, err := pool.Exec(ctx, `INSERT INTO calculadoras (calculadora_id, nombre_calculadora) VALUES ($1, 'Cotizador de prueba')`, calculadoraID); err != nil {
		t.Fatalf("no se pudo crear la calculadora de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO clientes (cliente_id, nombre_comercial, razon_social) VALUES ($1, 'Cliente de prueba', 'Cliente de Prueba S.A.')`, clienteID); err != nil {
		t.Fatalf("no se pudo crear el cliente de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cotizaciones (cotizacion_id, calculadora_id, cliente_id, codigo_oferta, tipo_propuesta, estado, version_actual)
		VALUES ($1, $2, $3, 'CTZ-PRUEBA-'||$1, 'Nueva', $4, 1)`,
		cotizacionID, calculadoraID, clienteID, estado,
	); err != nil {
		t.Fatalf("no se pudo crear la cotización de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cotizacion_versiones (cotizacion_id, numero_version, nombre_version, estado, moneda, total_precio, total_costo, total_ganancia, margen_total)
		VALUES ($1, 1, 'Versión inicial', $2, 'US$', 1000, 600, 400, 40)`,
		cotizacionID, estado,
	); err != nil {
		t.Fatalf("no se pudo crear la versión 1 de prueba: %v", err)
	}
	if vendedorID != "" {
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1, $2, 'Vendedor')`, cotizacionID, vendedorID); err != nil {
			t.Fatalf("no se pudo asignar el vendedor de prueba: %v", err)
		}
	}
	if analistaID != "" {
		if _, err := pool.Exec(ctx, `INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1, $2, 'Analista')`, cotizacionID, analistaID); err != nil {
			t.Fatalf("no se pudo asignar el analista de prueba: %v", err)
		}
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM clientes WHERE cliente_id = $1`, clienteID)
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id = $1`, calculadoraID)
	})

	return cotizacionID, calculadoraID, clienteID
}

func getCotizaciones(t *testing.T, handler *CotizacionesHandler, query string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/cotizaciones"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)
	return rec
}

func getDetalleCotizacion(t *testing.T, handler *CotizacionesHandler, cotizacionID, query string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/api/cotizaciones/{id}", handler.Detalle)

	url := "/api/cotizaciones/" + cotizacionID
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func postCotizacionSubruta(t *testing.T, handler http.HandlerFunc, patron, cotizacionID string, body any) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post(patron, handler)

	ruta := "/api/cotizaciones/" + cotizacionID + "/" + patron[len("/api/cotizaciones/{id}/"):]
	return postCatalogos(t, func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) }, ruta, body)
}

func TestCotizacionesListar_AparecceLaFixtureCreada(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := getCotizaciones(t, handler, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK           bool                `json:"ok"`
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	encontrada := false
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			encontrada = true
			if c.Version != 1 {
				t.Errorf("version = %d, esperaba 1", c.Version)
			}
			if c.TotalPrecio != 1000 {
				t.Errorf("total_precio = %v, esperaba 1000", c.TotalPrecio)
			}
		}
	}
	if !encontrada {
		t.Errorf("la cotización de prueba %q no apareció en el listado", cotizacionID)
	}
}

func TestCotizacionesListar_FiltroBusqueda(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := getCotizaciones(t, handler, "busqueda="+cotizacionID)
	var res struct {
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	encontrada := false
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			encontrada = true
		}
	}
	if !encontrada {
		t.Error("buscar por el código de oferta debería encontrar la cotización de prueba")
	}

	rec = getCotizaciones(t, handler, "busqueda=texto-que-no-deberia-existir-nunca")
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			t.Error("una búsqueda que no matchea no debería devolver la cotización de prueba")
		}
	}
}

func TestCotizacionesListar_FiltroEstado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Aceptada", "", "")

	rec := getCotizaciones(t, handler, "estado=Aceptada")
	var res struct {
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	encontrada := false
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			encontrada = true
		}
	}
	if !encontrada {
		t.Error("filtrar por estado=Aceptada debería encontrar la cotización de prueba")
	}

	rec = getCotizaciones(t, handler, "estado=Perdida")
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			t.Error("filtrar por un estado distinto no debería devolver la cotización de prueba")
		}
	}
}

func TestCotizacionesListar_FiltroCalculadora(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := getCotizaciones(t, handler, "calculadora_id="+calculadoraID)
	var res struct {
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if len(res.Cotizaciones) != 1 || res.Cotizaciones[0].CotizacionID != cotizacionID {
		t.Errorf("filtrar por calculadora_id debería devolver solo la cotización de prueba, dio %+v", res.Cotizaciones)
	}
}

func TestCotizacionesListar_FiltroUsuario(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	vendedorID := crearUsuarioPrueba(t, pool, "vendedor.cotiz.prueba@exceltecgroup.com", "1234", "Vendedor", "Activo")
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", vendedorID, "")

	rec := getCotizaciones(t, handler, "filtro_usuario_id="+vendedorID)
	var res struct {
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	encontrada := false
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			encontrada = true
			if c.Vendedor == nil || *c.Vendedor == "" {
				t.Error("esperaba el nombre del vendedor agregado en el listado")
			}
		}
	}
	if !encontrada {
		t.Error("filtrar por filtro_usuario_id debería encontrar la cotización asignada a ese usuario")
	}
}

func TestCotizacionesListar_FiltroFechaDesde(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	// UTC, no hora local: cotizacion_versiones.fecha_creacion sale de
	// now() de Postgres, que corre en UTC (ver docker-compose.yml). Si
	// esto se compara contra la hora local de la máquina donde corre
	// "go test", un huso horario distinto puede correr las fechas un
	// día y dar un falso resultado en el filtro cerca de la
	// medianoche.
	manana := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	rec := getCotizaciones(t, handler, "fecha_desde="+manana)
	var res struct {
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			t.Error("una fecha_desde en el futuro no debería incluir una cotización recién creada")
		}
	}

	ayer := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	rec = getCotizaciones(t, handler, "fecha_desde="+ayer)
	assertJSON(t, rec.Body.Bytes(), &res)
	encontrada := false
	for _, c := range res.Cotizaciones {
		if c.CotizacionID == cotizacionID {
			encontrada = true
		}
	}
	if !encontrada {
		t.Error("una fecha_desde en el pasado debería incluir la cotización recién creada")
	}
}

func TestCotizacionesDetalle_DevuelveCotizacionYVersiones(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	vendedorID := crearUsuarioPrueba(t, pool, "vendedor.detalle.prueba@exceltecgroup.com", "1234", "Vendedor", "Activo")
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", vendedorID, "")

	rec := getDetalleCotizacion(t, handler, cotizacionID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK         bool              `json:"ok"`
		Cotizacion map[string]any    `json:"cotizacion"`
		Versiones  []versionResumen  `json:"versiones"`
		Historial  []eventoHistorial `json:"historial"`
		Links      []any             `json:"links"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	if res.Cotizacion["calculadora_id"] != calculadoraID {
		t.Errorf("calculadora_id = %v, esperaba %q", res.Cotizacion["calculadora_id"], calculadoraID)
	}
	if res.Cotizacion["vendedor"] == nil || res.Cotizacion["vendedor"] == "" {
		t.Error("esperaba el nombre del vendedor en el detalle")
	}
	if res.Cotizacion["puede_editar"] != true {
		t.Error("una cotización en Borrador debería poder editarse")
	}
	if len(res.Versiones) != 1 {
		t.Fatalf("esperaba 1 versión, dio %d", len(res.Versiones))
	}
	if len(res.Links) != 0 {
		t.Error("la publicación al cliente no está implementada — links debería venir vacío")
	}
}

func TestCotizacionesDetalle_VersionPuntual(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := postCotizacionSubruta(t, handler.CrearVersion, "/api/cotizaciones/{id}/version", cotizacionID, map[string]any{
		"nombre_version": "V2 de prueba",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear versión: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	recV1 := getDetalleCotizacion(t, handler, cotizacionID, "version=1")
	var resV1 struct {
		Cotizacion map[string]any `json:"cotizacion"`
	}
	assertJSON(t, recV1.Body.Bytes(), &resV1)
	if resV1.Cotizacion["version"] != float64(1) {
		t.Errorf("con ?version=1 esperaba version=1, dio %v", resV1.Cotizacion["version"])
	}

	recV2 := getDetalleCotizacion(t, handler, cotizacionID, "version=2")
	var resV2 struct {
		Cotizacion map[string]any `json:"cotizacion"`
	}
	assertJSON(t, recV2.Body.Bytes(), &resV2)
	if resV2.Cotizacion["version"] != float64(2) {
		t.Errorf("con ?version=2 esperaba version=2, dio %v", resV2.Cotizacion["version"])
	}
}

func TestCotizacionesDetalle_NoExiste(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}

	rec := getDetalleCotizacion(t, handler, "COT-QUE-NO-EXISTE-"+sufijoUnico(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizacionesCrearVersion_IncrementaVersionActualYRegistraHistorial(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")

	rec := postCotizacionSubruta(t, handler.CrearVersion, "/api/cotizaciones/{id}/version", cotizacionID, map[string]any{
		"nombre_version":  "10 licencias adicionales",
		"resumen_cambios": "Se agregaron 10 licencias",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK      bool `json:"ok"`
		Version int  `json:"version"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || res.Version != 2 {
		t.Fatalf("esperaba ok:true version:2, dio %+v", res)
	}

	var versionActual int
	var estado string
	if err := pool.QueryRow(context.Background(), `SELECT version_actual, estado FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&versionActual, &estado); err != nil {
		t.Fatalf("no se pudo leer la cotización: %v", err)
	}
	if versionActual != 2 {
		t.Errorf("version_actual = %d, esperaba 2", versionActual)
	}
	if estado != "Borrador" {
		t.Errorf("una versión nueva debería arrancar en Borrador, dio %q", estado)
	}

	var existeHistorial bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM cotizacion_historial WHERE cotizacion_id = $1 AND numero_version = 2 AND accion = 'CREAR_VERSION')`,
		cotizacionID,
	).Scan(&existeHistorial); err != nil {
		t.Fatalf("no se pudo verificar el historial: %v", err)
	}
	if !existeHistorial {
		t.Error("crear una versión debería quedar registrado en cotizacion_historial")
	}
}

func TestCotizacionesCambiarEstado_ActualizaYRegistraHistorial(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")

	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 1, "estado": "Vista por el Cliente",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var estado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&estado); err != nil {
		t.Fatalf("no se pudo leer la cotización: %v", err)
	}
	if estado != "Vista por el Cliente" {
		t.Errorf("estado = %q, esperaba %q", estado, "Vista por el Cliente")
	}

	var existeHistorial bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM cotizacion_historial WHERE cotizacion_id = $1 AND estado_nuevo = 'Vista por el Cliente')`,
		cotizacionID,
	).Scan(&existeHistorial); err != nil {
		t.Fatalf("no se pudo verificar el historial: %v", err)
	}
	if !existeHistorial {
		t.Error("cambiar el estado debería quedar registrado en cotizacion_historial")
	}
}

func TestCotizacionesCambiarEstado_AceptarFijaVersionAceptada(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Enviada al Cliente", "", "")

	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 1, "estado": "Aceptada", "aceptada_por": "Cliente de Prueba",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var versionAceptada *int
	if err := pool.QueryRow(context.Background(), `SELECT version_aceptada FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&versionAceptada); err != nil {
		t.Fatalf("no se pudo leer la cotización: %v", err)
	}
	if versionAceptada == nil || *versionAceptada != 1 {
		t.Errorf("version_aceptada = %v, esperaba 1", versionAceptada)
	}

	var fechaAceptacion *time.Time
	var aceptadaPor *string
	if err := pool.QueryRow(context.Background(), `SELECT fecha_aceptacion, aceptada_por FROM cotizacion_versiones WHERE cotizacion_id = $1 AND numero_version = 1`, cotizacionID).Scan(&fechaAceptacion, &aceptadaPor); err != nil {
		t.Fatalf("no se pudo leer la versión: %v", err)
	}
	if fechaAceptacion == nil {
		t.Error("aceptar debería sellar fecha_aceptacion")
	}
	if aceptadaPor == nil || *aceptadaPor != "Cliente de Prueba" {
		t.Errorf("aceptada_por = %v, esperaba %q", aceptadaPor, "Cliente de Prueba")
	}
}

func TestCotizacionesCambiarEstado_EstadoInvalidoRechazado(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 1, "estado": "Liberada",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con un estado fuera del CHECK, dio %d: %s", rec.Code, rec.Body.String())
	}

	var estado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id = $1`, cotizacionID).Scan(&estado); err != nil {
		t.Fatalf("no se pudo leer la cotización: %v", err)
	}
	if estado != "Borrador" {
		t.Errorf("un estado inválido no debería haber cambiado nada, quedó %q", estado)
	}
}

func TestCotizacionesCambiarEstado_VersionNoExiste(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")

	rec := postCotizacionSubruta(t, handler.CambiarEstado, "/api/cotizaciones/{id}/estado", cotizacionID, map[string]any{
		"version": 99, "estado": "Aceptada",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("esperaba 404 con una versión inexistente, dio %d: %s", rec.Code, rec.Body.String())
	}
}
