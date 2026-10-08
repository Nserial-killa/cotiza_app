package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func postCambioCotizador(t *testing.T, h *CotizacionesHandler, actor, id, calculadora string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/api/cotizaciones/{id}/cambiar-cotizador", h.CambiarCotizador)
	body, _ := json.Marshal(map[string]any{"calculadora_id": calculadora})
	req := conActor(httptest.NewRequest(http.MethodPost, "/api/cotizaciones/"+id+"/cambiar-cotizador", bytes.NewReader(body)), actor)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func cotizacionParaCambio(t *testing.T, pool *pgxpool.Pool, creador string) (*CotizacionesHandler, string, string, string) {
	t.Helper()
	h := &CotizacionesHandler{DB: pool}
	origen, cliente := crearBaseAltaCotizacion(t, pool, true)
	destino, _ := crearBaseAltaCotizacion(t, pool, false)
	rec, res := postCrearCotizacion(t, h, creador, map[string]any{"cliente_id": cliente, "calculadora_id": origen})
	if rec.Code != 201 {
		t.Fatalf("alta inicial: %d %s", rec.Code, rec.Body.String())
	}
	id := res["cotizacion_id"].(string)
	limpiarCotizacionCreada(t, pool, id)
	return h, id, origen, destino
}

func TestCambiarCotizador_CreadorConservaVersionYEnlaceAnterior(t *testing.T) {
	pool := setupTestDB(t)
	creador := crearUsuarioPrueba(t, pool, "creador.cambio."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	h, id, origen, destino := cotizacionParaCambio(t, pool, creador)
	cargarPrecioAltaPrueba(t, pool, id, 1250)
	enlace := postEnlace(t, &EnlacesPublicosHandler{DB: pool}, id, map[string]any{"version": 1, "correo_destinatario": "cliente@example.com"})
	if enlace.Code != 200 {
		t.Fatalf("enlace anterior: %d %s", enlace.Code, enlace.Body.String())
	}
	var link struct {
		Token string `json:"token"`
	}
	assertJSON(t, enlace.Body.Bytes(), &link)
	var compiladoAnterior string
	if err := pool.QueryRow(context.Background(), `SELECT compilado_id_usado::text FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, id).Scan(&compiladoAnterior); err != nil {
		t.Fatal(err)
	}
	rec := postCambioCotizador(t, h, creador, id, destino)
	if rec.Code != 200 {
		t.Fatalf("el creador debe poder cambiar: %d %s", rec.Code, rec.Body.String())
	}
	var version int
	var calculadoraActual string
	if err := pool.QueryRow(context.Background(), `SELECT version_actual, calculadora_id FROM cotizaciones WHERE cotizacion_id=$1`, id).Scan(&version, &calculadoraActual); err != nil {
		t.Fatal(err)
	}
	if version != 2 || calculadoraActual != destino {
		t.Fatalf("cotización actual incorrecta: V%d %s", version, calculadoraActual)
	}
	var calcV1, calcV2, compiladoV1, compiladoV2, estadoV2 string
	var precioV1, precioV2 float64
	if err := pool.QueryRow(context.Background(), `SELECT calculadora_id,compilado_id_usado::text,total_precio FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, id).Scan(&calcV1, &compiladoV1, &precioV1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT calculadora_id,compilado_id_usado::text,estado,total_precio FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=2`, id).Scan(&calcV2, &compiladoV2, &estadoV2, &precioV2); err != nil {
		t.Fatal(err)
	}
	if calcV1 != origen || compiladoV1 != compiladoAnterior || precioV1 != 1250 || calcV2 != destino || compiladoV2 == compiladoV1 || estadoV2 != "Borrador" || precioV2 != 0 {
		t.Fatalf("versiones mezcladas: v1=%s/%s/%v v2=%s/%s/%s/%v", calcV1, compiladoV1, precioV1, calcV2, compiladoV2, estadoV2, precioV2)
	}
	rt := &CotizadorRuntimeHandler{DB: pool}
	viejo, err := rt.cargarContexto(context.Background(), id, 1, false)
	if err != nil || viejo.CalculadoraID != origen || viejo.CompiladoID != compiladoAnterior {
		t.Fatalf("runtime histórico perdió su cotizador: %+v %v", viejo, err)
	}
	nuevo, err := rt.cargarContexto(context.Background(), id, 2, false)
	if err != nil || nuevo.CalculadoraID != destino || nuevo.CompiladoID != compiladoV2 {
		t.Fatalf("runtime nuevo no usa el cotizador elegido: %+v %v", nuevo, err)
	}
	var valoresNuevos int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_valores WHERE cotizacion_id=$1 AND version=2`, id).Scan(&valoresNuevos); err != nil || valoresNuevos != 0 {
		t.Fatalf("la versión nueva debe empezar vacía: %d %v", valoresNuevos, err)
	}
	var versionEnlace int
	if err := pool.QueryRow(context.Background(), `SELECT version FROM cotizacion_enlaces_publicos WHERE token=$1`, link.Token).Scan(&versionEnlace); err != nil || versionEnlace != 1 {
		t.Fatalf("el enlace anterior debe conservar V1: %d %v", versionEnlace, err)
	}
	doc, err := construirDocumentoOferta(context.Background(), pool, id, 1)
	if err != nil || doc.CotizadorNombre != "Cotizador alta" || doc.TotalPrecio != 1250 {
		t.Fatalf("la oferta histórica cambió: %+v %v", doc, err)
	}
}

func TestCambiarCotizador_RechazaUsuarioAsignadoQueNoCreo(t *testing.T) {
	pool := setupTestDB(t)
	creador := crearUsuarioPrueba(t, pool, "creador.restriccion."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	h, id, _, destino := cotizacionParaCambio(t, pool, creador)
	otro := crearUsuarioPrueba(t, pool, "otro.restriccion."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	if _, err := pool.Exec(context.Background(), `INSERT INTO cotizacion_usuarios(cotizacion_id,usuario_id,funcion) VALUES($1,$2,'Vendedor')`, id, otro); err != nil {
		t.Fatal(err)
	}
	rec := postCambioCotizador(t, h, otro, id, destino)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "creó") {
		t.Fatalf("usuario asignado no creador debe ser rechazado: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCambiarCotizador_AdminYGerentePuedenCorregir(t *testing.T) {
	pool := setupTestDB(t)
	creador := crearUsuarioPrueba(t, pool, "creador.gerencia."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	h, id, origen, destino := cotizacionParaCambio(t, pool, creador)
	admin := crearAdminActorPrueba(t, pool)
	gerente := crearUsuarioPrueba(t, pool, "gerente.cambio."+sufijoUnico()+"@exceltecgroup.com", "1234", "Gerente Comercial", "Activo")
	if rec := postCambioCotizador(t, h, admin, id, destino); rec.Code != 200 {
		t.Fatalf("administrador: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postCambioCotizador(t, h, gerente, id, origen); rec.Code != 200 {
		t.Fatalf("gerente comercial: %d %s", rec.Code, rec.Body.String())
	}
	var version int
	if err := pool.QueryRow(context.Background(), `SELECT version_actual FROM cotizaciones WHERE cotizacion_id=$1`, id).Scan(&version); err != nil || version != 3 {
		t.Fatalf("se esperaban tres versiones: %d %v", version, err)
	}
}

func TestCambiarCotizador_RechazaDestinoSinPublicar(t *testing.T) {
	pool := setupTestDB(t)
	admin := crearAdminActorPrueba(t, pool)
	h, id, _, _ := cotizacionParaCambio(t, pool, admin)
	rec := postCambioCotizador(t, h, admin, id, "COTIZADOR-INEXISTENTE")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "publicado") {
		t.Fatalf("destino inválido: %d %s", rec.Code, rec.Body.String())
	}
}
