package handlers

// Pruebas de permisos por rol (permisos.go). Contra Postgres real y con
// los 5 roles tal como los siembran 0001 + 0033 + 0034: cada prueba crea
// usuarios descartables de cada rol, no inventa roles propios. Para
// cada bandera: quien la tiene en true puede, quien la tiene en false
// recibe 403 con mensaje claro y "permiso" = la columna que le falta.
//
// La cobertura del wiring de puede_parametrizar en main.go (que TODA
// escritura del Diseñador pase por el middleware) la da
// cmd/server/permisos_rutas_test.go; acá se prueba que el middleware
// rechaza/deja pasar a cada rol en cada una de esas rutas.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	rolAdmin     = "Administrador"
	rolGerente   = "Gerente Comercial"
	rolVendedor  = "Vendedor"
	rolConsultor = "Consultor"
	rolSolo      = "Solo Consulta"
)

var rolesSembrados = []string{rolAdmin, rolGerente, rolVendedor, rolConsultor, rolSolo}

// matrizEsperada es la tabla de 0001_init_schema.sql + 0033 + 0034. Si el seed
// cambia, TestPermisos_MatrizDeRolesSembrada falla primero y dice qué
// cambió, en vez de que fallen veinte pruebas sin explicación.
var matrizEsperada = map[string]permisosSesion{
	rolAdmin:     {PuedeCrear: true, PuedeEditarBorrador: true, PuedeCrearVersion: true, PuedeVerPrice: true, PuedeAprobar: true, PuedeParametrizar: true, PuedeVerDashboard: true, PuedeVerAdministracion: true},
	rolGerente:   {PuedeCrear: true, PuedeEditarBorrador: true, PuedeCrearVersion: true, PuedeVerPrice: true, PuedeAprobar: true, PuedeVerDashboard: true},
	rolVendedor:  {PuedeCrear: true, PuedeEditarBorrador: true, PuedeCrearVersion: true, AlcancePropio: true},
	rolConsultor: {PuedeCrear: true, PuedeEditarBorrador: true, AlcancePropio: true},
	rolSolo:      {PuedeVerPrice: true, PuedeVerDashboard: true},
}

func crearActorRol(t *testing.T, pool *pgxpool.Pool, rol string) string {
	t.Helper()
	correo := "perm." + strings.ReplaceAll(strings.ToLower(rol), " ", "") + "." + sufijoUnico() + "@exceltecgroup.com"
	id := crearUsuarioPrueba(t, pool, correo, "1234", rol, "Activo")
	desvincularAlLimpiar(t, pool, id)
	return id
}

// funcionPropiaDe es la función de cotizacion_usuarios que hace
// "propia" una cotización para ese rol (permisos.go).
func funcionPropiaDe(rol string) string {
	if rol == rolConsultor {
		return "Analista"
	}
	return "Vendedor"
}

// cotizacionAsignada crea una cotización con el usuario asignado en la
// función indicada ("" = sin responsables).
func cotizacionAsignada(t *testing.T, pool *pgxpool.Pool, usuarioID, funcion, estado string) string {
	t.Helper()
	vendedor, analista := "", ""
	switch funcion {
	case "Vendedor":
		vendedor = usuarioID
	case "Analista":
		analista = usuarioID
	}
	cotizacionID, _, _ := crearCotizacionPrueba(t, pool, estado, vendedor, analista)
	return cotizacionID
}

type respuestaPermiso struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Permiso string `json:"permiso"`
}

func afirmar403(t *testing.T, rec *httptest.ResponseRecorder, permiso, contexto string) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("%s: esperaba 403, dio %d: %s", contexto, rec.Code, rec.Body.String())
	}
	var res respuestaPermiso
	assertJSON(t, rec.Body.Bytes(), &res)
	if res.OK || res.Permiso != permiso || strings.TrimSpace(res.Error) == "" {
		t.Fatalf("%s: esperaba ok=false, permiso=%q y un mensaje; vino %s", contexto, permiso, rec.Body.String())
	}
}

func afirmarNo403(t *testing.T, rec *httptest.ResponseRecorder, esperado int, contexto string) {
	t.Helper()
	if rec.Code != esperado {
		t.Fatalf("%s: esperaba %d, dio %d: %s", contexto, esperado, rec.Code, rec.Body.String())
	}
}

// peticionRol ejecuta handler montado en patron (para que chi resuelva
// {id}) con actorID en el contexto.
func peticionRol(t *testing.T, metodo, patron, ruta string, cuerpo any, actorID string, handler http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if cuerpo != nil {
		var err error
		if raw, err = json.Marshal(cuerpo); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(metodo, ruta, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req = conActor(req, actorID)
	router := chi.NewRouter()
	router.Method(metodo, patron, handler)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPermisos_MatrizDeRolesSembrada(t *testing.T) {
	pool := setupTestDB(t)
	for _, rol := range rolesSembrados {
		actor := crearActorRol(t, pool, rol)
		req := conActor(httptest.NewRequest(http.MethodGet, "/", nil), actor)
		p, err := resolverPermisosSesion(context.Background(), pool, req)
		if err != nil {
			t.Fatalf("%s: %v", rol, err)
		}
		esperado := matrizEsperada[rol]
		esperado.UsuarioID, esperado.Rol = actor, rol
		if p != esperado {
			t.Errorf("banderas de %s no coinciden con el seed:\n  base:     %+v\n  esperado: %+v", rol, p, esperado)
		}
	}
}

func TestPermisos_SinUsuarioEnContextoDa401(t *testing.T) {
	pool := setupTestDB(t)
	rec := httptest.NewRecorder()
	RequierePuedeParametrizar(pool)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("el handler no debía ejecutarse sin usuario")
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/catalogos", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("esperaba 401, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------
// puede_crear
// ---------------------------------------------------------------------

func TestPermisos_CrearCotizacionSegunRol(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)

	for _, rol := range rolesSembrados {
		t.Run(rol, func(t *testing.T) {
			actor := crearActorRol(t, pool, rol)
			rec, res := postCrearCotizacion(t, handler, actor, map[string]any{"cliente_id": clienteID, "calculadora_id": calculadoraID})
			if !matrizEsperada[rol].PuedeCrear {
				afirmar403(t, rec, "puede_crear", rol+" crea cotización")
				return
			}
			afirmarNo403(t, rec, http.StatusCreated, rol+" crea cotización")
			cotizacionID, _ := res["cotizacion_id"].(string)
			limpiarCotizacionCreada(t, pool, cotizacionID)

			// Quien crea queda como responsable con la función de su rol, y
			// por lo tanto la ve aunque tenga alcance propio.
			var funcion string
			if err := pool.QueryRow(context.Background(), `SELECT funcion FROM cotizacion_usuarios WHERE cotizacion_id=$1 AND usuario_id=$2`, cotizacionID, actor).Scan(&funcion); err != nil {
				t.Fatalf("no quedó el creador en cotizacion_usuarios: %v", err)
			}
			if funcion != funcionPropiaDe(rol) {
				t.Fatalf("%s quedó registrado como %q, esperaba %q", rol, funcion, funcionPropiaDe(rol))
			}
			afirmarNo403(t, getDetalleCotizacion(t, handler, actor, cotizacionID, ""), http.StatusOK, rol+" abre la que creó")
		})
	}
}

func TestPermisos_ConvertirSolicitudExigePuedeCrear(t *testing.T) {
	pool := setupTestDB(t)
	solicitudes := &SolicitudesHandler{DB: pool, Cotizaciones: &CotizacionesHandler{DB: pool}}
	solo := crearActorRol(t, pool, rolSolo)
	rec := peticionRol(t, http.MethodPost, "/api/solicitudes/{id}/convertir", "/api/solicitudes/"+errIDInexistente+"/convertir",
		nil, solo, http.HandlerFunc(solicitudes.Convertir))
	afirmar403(t, rec, "puede_crear", "Solo Consulta convierte una solicitud")
}

// ---------------------------------------------------------------------
// puede_crear_version
// ---------------------------------------------------------------------

func TestPermisos_CrearVersionSegunRol(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	for _, rol := range rolesSembrados {
		t.Run(rol, func(t *testing.T) {
			actor := crearActorRol(t, pool, rol)
			cotizacionID := cotizacionAsignada(t, pool, actor, funcionPropiaDe(rol), "Enviada al Cliente")
			rec := peticionRol(t, http.MethodPost, "/api/cotizaciones/{id}/version", "/api/cotizaciones/"+cotizacionID+"/version",
				map[string]any{"nombre_version": "V2"}, actor, http.HandlerFunc(handler.CrearVersion))
			if !matrizEsperada[rol].PuedeCrearVersion {
				afirmar403(t, rec, "puede_crear_version", rol+" crea versión")
				return
			}
			afirmarNo403(t, rec, http.StatusOK, rol+" crea versión")
		})
	}
}

// ---------------------------------------------------------------------
// puede_editar_borrador
// ---------------------------------------------------------------------

func TestPermisos_GuardarValoresSegunRol(t *testing.T) {
	for _, rol := range rolesSembrados {
		t.Run(rol, func(t *testing.T) {
			fixture := crearFixtureRuntime(t)
			pool := fixture.Handler.DB
			actor := crearActorRol(t, pool, rol)
			if _, err := pool.Exec(context.Background(), `INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1,$2,$3)`,
				fixture.CotizacionID, actor, funcionPropiaDe(rol)); err != nil {
				t.Fatal(err)
			}
			ruta := "/api/cotizador/runtime/" + fixture.CotizacionID
			// Leer el cotizador sí puede cualquiera con acceso a la cotización.
			afirmarNo403(t, peticionRol(t, http.MethodGet, "/api/cotizador/runtime/{cotizacion_id}", ruta, nil, actor,
				http.HandlerFunc(fixture.Handler.Obtener)), http.StatusOK, rol+" abre el cotizador")

			rec := peticionRol(t, http.MethodPost, "/api/cotizador/runtime/{cotizacion_id}/valores", ruta+"/valores",
				map[string]any{"version": 1, "valores": map[string]any{fixture.CampoID: "Hola"}}, actor, http.HandlerFunc(fixture.Handler.GuardarValores))
			if !matrizEsperada[rol].PuedeEditarBorrador {
				afirmar403(t, rec, "puede_editar_borrador", rol+" guarda valores")
				recOpc := peticionRol(t, http.MethodPost, "/api/cotizador/runtime/{cotizacion_id}/opciones", ruta+"/opciones",
					map[string]any{"version": 1, "elemento_padre_id": "X", "accion": "AGREGAR"}, actor, http.HandlerFunc(fixture.Handler.AdministrarOpciones))
				afirmar403(t, recOpc, "puede_editar_borrador", rol+" administra opciones")
				return
			}
			afirmarNo403(t, rec, http.StatusOK, rol+" guarda valores")
		})
	}
}

// Consultor: edita el borrador pero NO crea versión nueva.
func TestPermisos_ConsultorEditaBorradorPeroNoVersiona(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	pool := fixture.Handler.DB
	consultor := crearActorRol(t, pool, rolConsultor)
	if _, err := pool.Exec(context.Background(), `INSERT INTO cotizacion_usuarios (cotizacion_id, usuario_id, funcion) VALUES ($1,$2,'Analista')`, fixture.CotizacionID, consultor); err != nil {
		t.Fatal(err)
	}
	ruta := "/api/cotizador/runtime/" + fixture.CotizacionID + "/valores"
	afirmarNo403(t, peticionRol(t, http.MethodPost, "/api/cotizador/runtime/{cotizacion_id}/valores", ruta,
		map[string]any{"version": 1, "valores": map[string]any{fixture.CampoID: "Alcance del consultor"}}, consultor,
		http.HandlerFunc(fixture.Handler.GuardarValores)), http.StatusOK, "Consultor guarda el borrador")

	cotizaciones := &CotizacionesHandler{DB: pool}
	rec := peticionRol(t, http.MethodPost, "/api/cotizaciones/{id}/version", "/api/cotizaciones/"+fixture.CotizacionID+"/version",
		map[string]any{"nombre_version": "V2"}, consultor, http.HandlerFunc(cotizaciones.CrearVersion))
	afirmar403(t, rec, "puede_crear_version", "Consultor crea versión")
	var version int
	if err := pool.QueryRow(context.Background(), `SELECT version_actual FROM cotizaciones WHERE cotizacion_id=$1`, fixture.CotizacionID).Scan(&version); err != nil || version != 1 {
		t.Fatalf("el 403 no debía crear ninguna versión: version_actual=%d err=%v", version, err)
	}
}

// ---------------------------------------------------------------------
// puede_aprobar
// ---------------------------------------------------------------------

func TestPermisos_CambiarEstadoSegunRol(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	cambiar := func(t *testing.T, actor, cotizacionID, estado string) *httptest.ResponseRecorder {
		cuerpo := map[string]any{"version": 1, "estado": estado}
		if estado == "Ganada" {
			cuerpo["version_aceptada"] = 1
		}
		return peticionRol(t, http.MethodPost, "/api/cotizaciones/{id}/estado", "/api/cotizaciones/"+cotizacionID+"/estado",
			cuerpo, actor, http.HandlerFunc(handler.CambiarEstado))
	}

	for _, rol := range rolesSembrados {
		t.Run(rol, func(t *testing.T) {
			actor := crearActorRol(t, pool, rol)
			// Las transiciones normales no piden puede_aprobar, pero toda
			// modificación exige puede_editar_borrador.
			for _, estado := range []string{"Enviada al Cliente", "Vista por el Cliente", "Perdida", "Cancelada", "Vencida"} {
				cotizacionID := cotizacionAsignada(t, pool, actor, funcionPropiaDe(rol), "Borrador")
				rec := cambiar(t, actor, cotizacionID, estado)
				if !matrizEsperada[rol].PuedeEditarBorrador {
					afirmar403(t, rec, "puede_editar_borrador", rol+" → "+estado)
					continue
				}
				afirmarNo403(t, rec, http.StatusOK, rol+" → "+estado)
			}
			for _, estado := range []string{"Aceptada", "Ganada"} {
				cotizacionID := cotizacionAsignada(t, pool, actor, funcionPropiaDe(rol), "Enviada al Cliente")
				rec := cambiar(t, actor, cotizacionID, estado)
				if !matrizEsperada[rol].PuedeEditarBorrador {
					afirmar403(t, rec, "puede_editar_borrador", rol+" → "+estado)
					continue
				}
				if !matrizEsperada[rol].PuedeAprobar {
					afirmar403(t, rec, "puede_aprobar", rol+" → "+estado)
					var actual string
					pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&actual)
					if actual != "Enviada al Cliente" {
						t.Fatalf("el 403 no debía cambiar el estado, quedó %q", actual)
					}
					continue
				}
				afirmarNo403(t, rec, http.StatusOK, rol+" → "+estado)
			}
		})
	}
}

func TestPermisos_VisibilidadDeModulosSegunRol(t *testing.T) {
	pool := setupTestDB(t)
	for _, rol := range rolesSembrados {
		t.Run(rol, func(t *testing.T) {
			actor := crearActorRol(t, pool, rol)
			probar := func(middleware func(http.Handler) http.Handler) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				req := conActor(httptest.NewRequest(http.MethodGet, "/", nil), actor)
				middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					escribirJSON(w, http.StatusOK, map[string]any{"ok": true})
				})).ServeHTTP(rec, req)
				return rec
			}

			dashboard := probar(RequierePuedeVerDashboard(pool))
			if matrizEsperada[rol].PuedeVerDashboard {
				afirmarNo403(t, dashboard, http.StatusOK, rol+" abre Dashboard")
			} else {
				afirmar403(t, dashboard, "puede_ver_dashboard", rol+" abre Dashboard")
			}

			admin := probar(RequierePuedeVerAdministracion(pool))
			if matrizEsperada[rol].PuedeVerAdministracion {
				afirmarNo403(t, admin, http.StatusOK, rol+" abre administración")
			} else {
				afirmar403(t, admin, "puede_ver_administracion", rol+" abre administración")
			}
		})
	}
}

// ---------------------------------------------------------------------
// alcance_propio
// ---------------------------------------------------------------------

func idsListado(t *testing.T, pool *pgxpool.Pool, actor, query string) map[string]bool {
	t.Helper()
	handler := &CotizacionesHandler{DB: pool}
	req := conActor(httptest.NewRequest(http.MethodGet, "/api/cotizaciones?"+query, nil), actor)
	rec := httptest.NewRecorder()
	handler.Listar(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Cotizaciones []cotizacionListado `json:"cotizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	ids := map[string]bool{}
	for _, c := range res.Cotizaciones {
		ids[c.CotizacionID] = true
	}
	return ids
}

// afirmarSinAccesoDirecto recorre todos los endpoints de una cotización
// puntual: con alcance propio, la ajena da 403 en cada uno, no solo en
// el detalle.
func afirmarSinAccesoDirecto(t *testing.T, pool *pgxpool.Pool, actor, cotizacionID, contexto string) {
	t.Helper()
	cot := &CotizacionesHandler{DB: pool}
	rt := &CotizadorRuntimeHandler{DB: pool}
	enl := &EnlacesPublicosHandler{DB: pool}
	vp := &VistaPreviaOfertaHandler{DB: pool}
	pdf := &OfertaPDFHandler{DB: pool} // sin Gotenberg: el alcance se corta antes de llegar a convertir
	base := "/api/cotizaciones/" + cotizacionID
	casos := []struct {
		nombre, metodo, patron, ruta string
		cuerpo                       any
		handler                      http.HandlerFunc
	}{
		{"detalle", http.MethodGet, "/api/cotizaciones/{id}", base, nil, cot.Detalle},
		{"estado", http.MethodPost, "/api/cotizaciones/{id}/estado", base + "/estado", map[string]any{"version": 1, "estado": "Enviada al Cliente"}, cot.CambiarEstado},
		{"enlace", http.MethodPost, "/api/cotizaciones/{id}/enlace", base + "/enlace", nil, enl.GenerarEnlace},
		{"vista previa", http.MethodGet, "/api/cotizaciones/{id}/vista-previa-oferta", base + "/vista-previa-oferta", nil, vp.Ver},
		{"pdf de la oferta", http.MethodGet, "/api/cotizaciones/{id}/enlace/pdf", base + "/enlace/pdf", nil, pdf.Descargar},
		{"runtime", http.MethodGet, "/api/cotizador/runtime/{cotizacion_id}", "/api/cotizador/runtime/" + cotizacionID, nil, rt.Obtener},
		{"guardar valores", http.MethodPost, "/api/cotizador/runtime/{cotizacion_id}/valores", "/api/cotizador/runtime/" + cotizacionID + "/valores", map[string]any{"version": 1, "valores": map[string]any{}}, rt.GuardarValores},
	}
	for _, caso := range casos {
		rec := peticionRol(t, caso.metodo, caso.patron, caso.ruta, caso.cuerpo, actor, caso.handler)
		afirmar403(t, rec, "alcance_propio", contexto+" ("+caso.nombre+")")
	}
}

func TestPermisos_VendedorNoVeNiAbreLaDeOtroVendedor(t *testing.T) {
	pool := setupTestDB(t)
	vendedorA := crearActorRol(t, pool, rolVendedor)
	vendedorB := crearActorRol(t, pool, rolVendedor)
	propia := cotizacionAsignada(t, pool, vendedorA, "Vendedor", "Borrador")
	ajena := cotizacionAsignada(t, pool, vendedorB, "Vendedor", "Borrador")

	ids := idsListado(t, pool, vendedorA, "")
	if !ids[propia] || ids[ajena] {
		t.Fatalf("listado del Vendedor A: propia=%v ajena=%v (esperaba true/false)", ids[propia], ids[ajena])
	}
	// El filtro "Responsable" no le sirve para pedir las de otro.
	if ids := idsListado(t, pool, vendedorA, "filtro_usuario_id="+vendedorB); ids[ajena] {
		t.Fatal("con filtro_usuario_id del otro Vendedor, la ajena no debía aparecer")
	}
	afirmarNo403(t, getDetalleCotizacion(t, &CotizacionesHandler{DB: pool}, vendedorA, propia, ""), http.StatusOK, "Vendedor abre la propia")
	afirmarSinAccesoDirecto(t, pool, vendedorA, ajena, "Vendedor abre la de otro")
	// Versionar la ajena también se corta por alcance (tiene la bandera).
	rec := peticionRol(t, http.MethodPost, "/api/cotizaciones/{id}/version", "/api/cotizaciones/"+ajena+"/version",
		map[string]any{"nombre_version": "V2"}, vendedorA, http.HandlerFunc((&CotizacionesHandler{DB: pool}).CrearVersion))
	afirmar403(t, rec, "alcance_propio", "Vendedor versiona la de otro")
	// Inexistente: mismo 403 que la ajena (no delata si existe).
	afirmar403(t, getDetalleCotizacion(t, &CotizacionesHandler{DB: pool}, vendedorA, errIDInexistente, ""), "alcance_propio", "Vendedor abre una inexistente")
}

func TestPermisos_ConsultorSoloVeDondeEsAnalista(t *testing.T) {
	pool := setupTestDB(t)
	consultor := crearActorRol(t, pool, rolConsultor)
	otroConsultor := crearActorRol(t, pool, rolConsultor)
	comoAnalista := cotizacionAsignada(t, pool, consultor, "Analista", "Borrador")
	deOtro := cotizacionAsignada(t, pool, otroConsultor, "Analista", "Borrador")
	// "Propia" para el Consultor es la función Analista, no Vendedor: el
	// criterio es explícito por rol, no uno solo forzado para los dos.
	comoVendedor := cotizacionAsignada(t, pool, consultor, "Vendedor", "Borrador")

	ids := idsListado(t, pool, consultor, "")
	if !ids[comoAnalista] || ids[deOtro] || ids[comoVendedor] {
		t.Fatalf("listado del Consultor: comoAnalista=%v deOtro=%v comoVendedor=%v (esperaba true/false/false)",
			ids[comoAnalista], ids[deOtro], ids[comoVendedor])
	}
	afirmarNo403(t, getDetalleCotizacion(t, &CotizacionesHandler{DB: pool}, consultor, comoAnalista, ""), http.StatusOK, "Consultor abre donde es Analista")
	afirmarSinAccesoDirecto(t, pool, consultor, deOtro, "Consultor abre la de otro")
	afirmarSinAccesoDirecto(t, pool, consultor, comoVendedor, "Consultor abre donde figura como Vendedor")
}

// El Analista asignado en la Solicitud queda como responsable de la
// cotización que nace de ella — es lo que hace que un Consultor la vea.
func TestPermisos_ConvertirSolicitudLlevaResponsablesALaCotizacion(t *testing.T) {
	pool := setupTestDB(t)
	admin := crearActorRol(t, pool, rolAdmin)
	consultor := crearActorRol(t, pool, rolConsultor)
	vendedor := crearActorRol(t, pool, rolVendedor)
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)
	var solicitudID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO solicitudes (origen, cliente_nombre, calculadora_id, vendedor_id, analista_id)
		VALUES ('COTIZA', 'Cliente permisos', $1, $2, $3) RETURNING solicitud_id::text`,
		calculadoraID, vendedor, consultor).Scan(&solicitudID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID)
	})

	solicitudes := &SolicitudesHandler{DB: pool, Cotizaciones: &CotizacionesHandler{DB: pool}}
	rec := peticionRol(t, http.MethodPost, "/api/solicitudes/{id}/convertir", "/api/solicitudes/"+solicitudID+"/convertir", nil, admin, http.HandlerFunc(solicitudes.Convertir))
	afirmarNo403(t, rec, http.StatusOK, "Admin convierte la solicitud")
	var res struct {
		CotizacionID string `json:"cotizacion_id"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE solicitudes SET cotizacion_id_generada=NULL WHERE solicitud_id::text=$1`, solicitudID)
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, res.CotizacionID)
	})

	handler := &CotizacionesHandler{DB: pool}
	afirmarNo403(t, getDetalleCotizacion(t, handler, consultor, res.CotizacionID, ""), http.StatusOK, "Consultor (Analista de la solicitud) abre la cotización")
	afirmarNo403(t, getDetalleCotizacion(t, handler, vendedor, res.CotizacionID, ""), http.StatusOK, "Vendedor de la solicitud abre la cotización")
}

func TestPermisos_RolesSinAlcancePropioVenTodas(t *testing.T) {
	pool := setupTestDB(t)
	vendedorA := crearActorRol(t, pool, rolVendedor)
	vendedorB := crearActorRol(t, pool, rolVendedor)
	deA := cotizacionAsignada(t, pool, vendedorA, "Vendedor", "Borrador")
	deB := cotizacionAsignada(t, pool, vendedorB, "Vendedor", "Borrador")
	sinResponsable := cotizacionAsignada(t, pool, "", "", "Borrador")

	for _, rol := range []string{rolAdmin, rolGerente, rolSolo} {
		t.Run(rol, func(t *testing.T) {
			actor := crearActorRol(t, pool, rol)
			ids := idsListado(t, pool, actor, "")
			for _, id := range []string{deA, deB, sinResponsable} {
				if !ids[id] {
					t.Errorf("%s no ve %s en el listado", rol, id)
				}
				afirmarNo403(t, getDetalleCotizacion(t, &CotizacionesHandler{DB: pool}, actor, id, ""), http.StatusOK, rol+" abre "+id)
			}
			// El filtro "Responsable" sigue funcionando para quien no tiene
			// la restricción.
			if ids := idsListado(t, pool, actor, "filtro_usuario_id="+vendedorA); !ids[deA] || ids[deB] {
				t.Errorf("%s con filtro_usuario_id=A: deA=%v deB=%v (esperaba true/false)", rol, ids[deA], ids[deB])
			}
		})
	}
}

// Solo Consulta ve todas pero no puede crear, editar, versionar ni
// aprobar ninguna.
func TestPermisos_SoloConsultaVeTodoPeroNoModificaNada(t *testing.T) {
	fixture := crearFixtureRuntime(t)
	pool := fixture.Handler.DB
	solo := crearActorRol(t, pool, rolSolo)
	cot := &CotizacionesHandler{DB: pool}
	base := "/api/cotizaciones/" + fixture.CotizacionID

	afirmarNo403(t, getDetalleCotizacion(t, cot, solo, fixture.CotizacionID, ""), http.StatusOK, "Solo Consulta abre el detalle")
	afirmarNo403(t, peticionRol(t, http.MethodGet, "/api/cotizador/runtime/{cotizacion_id}", "/api/cotizador/runtime/"+fixture.CotizacionID,
		nil, solo, http.HandlerFunc(fixture.Handler.Obtener)), http.StatusOK, "Solo Consulta abre el cotizador")

	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)
	recCrear, _ := postCrearCotizacion(t, cot, solo, map[string]any{"cliente_id": clienteID, "calculadora_id": calculadoraID})
	afirmar403(t, recCrear, "puede_crear", "Solo Consulta crea")
	afirmar403(t, peticionRol(t, http.MethodPost, "/api/cotizador/runtime/{cotizacion_id}/valores", "/api/cotizador/runtime/"+fixture.CotizacionID+"/valores",
		map[string]any{"version": 1, "valores": map[string]any{fixture.CampoID: "x"}}, solo, http.HandlerFunc(fixture.Handler.GuardarValores)),
		"puede_editar_borrador", "Solo Consulta edita")
	afirmar403(t, peticionRol(t, http.MethodPost, "/api/cotizaciones/{id}/version", base+"/version",
		map[string]any{"nombre_version": "V2"}, solo, http.HandlerFunc(cot.CrearVersion)), "puede_crear_version", "Solo Consulta versiona")
	for _, estado := range []string{"Aceptada", "Ganada"} {
		afirmar403(t, peticionRol(t, http.MethodPost, "/api/cotizaciones/{id}/estado", base+"/estado",
			map[string]any{"version": 1, "estado": estado, "version_aceptada": 1}, solo, http.HandlerFunc(cot.CambiarEstado)),
			"puede_editar_borrador", "Solo Consulta → "+estado)
	}
}

// ---------------------------------------------------------------------
// puede_parametrizar
// ---------------------------------------------------------------------

type rutaDisenador struct {
	metodo, patron, ruta string
	handler              http.HandlerFunc
}

// rutasEscrituraDisenador son TODAS las rutas que main.go pone detrás de
// RequierePuedeParametrizar (mismas 54 que verifica
// cmd/server/permisos_rutas_test.go).
func rutasEscrituraDisenador(pool *pgxpool.Pool) []rutaDisenador {
	cat := &CatalogosHandler{DB: pool}
	tabs := &CotizadorTabsHandler{DB: pool}
	sal := &SalidasCotizadorHandler{DB: pool}
	sec := &SeccionesAdicionalesHandler{DB: pool}
	lp := &ListaPreciosItemsHandler{DB: pool}
	tc := &TablaColumnasHandler{DB: pool}
	comp := &CompiladorHandler{DB: pool}
	reg := &ReglasHandler{DB: pool}
	regc := &ReglasCotizadorHandler{DB: pool}
	calc := &CalculadorasHandler{DB: pool}
	pl := &PlantillasHandler{DB: pool}
	ple := &PlantillaEstructuraHandler{DB: pool}
	plv := &PlantillaVinculacionesHandler{DB: pool}
	plc := &PlantillaCondicionesHandler{DB: pool}
	plt := &PlantillaTablaColumnasHandler{DB: pool}
	pls := &PlantillaEstiloHandler{DB: pool}
	plb := &PlantillaBloqueCamposHandler{DB: pool}
	const x = "PERM-X"
	r := func(metodo, patron string, h http.HandlerFunc) rutaDisenador {
		ruta := patron
		for _, p := range []string{"{id}", "{elemento_id}", "{tab_id}", "{item_id}", "{columna_id}", "{clave_salida}", "{seccion_id}", "{bloque_id}", "{campo_id}"} {
			ruta = strings.ReplaceAll(ruta, p, x)
		}
		return rutaDisenador{metodo, patron, ruta, h}
	}
	P, D, A := http.MethodPost, http.MethodDelete, http.MethodPatch
	return []rutaDisenador{
		r(P, "/api/calculadoras", calc.Crear),
		r(P, "/api/catalogos", cat.GuardarCatalogo),
		r(D, "/api/catalogos/{id}", cat.EliminarCatalogo),
		r(P, "/api/catalogos/valores", cat.GuardarValor),
		r(D, "/api/catalogos/valores/{id}", cat.EliminarValor),
		r(P, "/api/catalogos/relaciones", cat.GuardarRelaciones),
		r(D, "/api/catalogos/relaciones/{id}", cat.EliminarRelacion),
		r(P, "/api/cotizador/salidas", sal.Guardar),
		r(D, "/api/cotizador/salidas/{clave_salida}", sal.Eliminar),
		r(P, "/api/cotizador/tabs", tabs.GuardarTab),
		r(D, "/api/cotizador/tabs/{id}", tabs.EliminarTab),
		r(P, "/api/cotizador/elementos", tabs.GuardarElemento),
		r(D, "/api/cotizador/elementos/{id}", tabs.EliminarElemento),
		r(P, "/api/cotizador/elementos/{elemento_id}/secciones", sec.Asociar),
		r(D, "/api/cotizador/elementos/{elemento_id}/secciones/{tab_id}", sec.Desasociar),
		r(P, "/api/cotizador/elementos/{elemento_id}/items", lp.Crear),
		r(A, "/api/cotizador/items/{item_id}", lp.Editar),
		r(D, "/api/cotizador/items/{item_id}", lp.Eliminar),
		r(P, "/api/cotizador/elementos/{elemento_id}/columnas", tc.Crear),
		r(A, "/api/cotizador/columnas/{columna_id}", tc.Editar),
		r(D, "/api/cotizador/columnas/{columna_id}", tc.Eliminar),
		r(P, "/api/cotizador/validar", comp.Validar),
		r(P, "/api/cotizador/compilar", comp.Compilar),
		r(P, "/api/reglas", reg.Guardar),
		r(D, "/api/reglas/{id}", reg.Eliminar),
		r(P, "/api/cotizador/reglas", regc.Guardar),
		r(D, "/api/cotizador/reglas/{id}", regc.Eliminar),
		r(P, "/api/plantillas", pl.Crear),
		r(A, "/api/plantillas/{id}", pl.Editar),
		r(P, "/api/plantillas/{id}/publicar", pl.Publicar),
		r(P, "/api/plantillas/{id}/nueva-version", pl.NuevaVersion),
		r(D, "/api/plantillas/{id}", pl.Eliminar),
		r(P, "/api/plantillas/{id}/secciones", ple.CrearSeccion),
		r(P, "/api/plantillas/{id}/estructura-sugerida", ple.AplicarEstructuraSugerida),
		r(A, "/api/plantillas/secciones/{seccion_id}", ple.EditarSeccion),
		r(D, "/api/plantillas/secciones/{seccion_id}", ple.EliminarSeccion),
		r(P, "/api/plantillas/secciones/{seccion_id}/orden", ple.OrdenarSecciones),
		r(P, "/api/plantillas/secciones/{seccion_id}/bloques", ple.CrearBloque),
		r(A, "/api/plantillas/bloques/{bloque_id}", ple.EditarBloque),
		r(D, "/api/plantillas/bloques/{bloque_id}", ple.EliminarBloque),
		r(P, "/api/plantillas/bloques/{bloque_id}/orden", ple.OrdenarBloques),
		r(P, "/api/plantillas/bloques/{bloque_id}/vinculacion", plv.Guardar),
		r(D, "/api/plantillas/bloques/{bloque_id}/vinculacion", plv.Eliminar),
		r(P, "/api/plantillas/bloques/{bloque_id}/condicion", plc.Guardar),
		r(D, "/api/plantillas/bloques/{bloque_id}/condicion", plc.Eliminar),
		r(P, "/api/plantillas/bloques/{bloque_id}/columnas", plt.Agregar),
		r(A, "/api/plantillas/columnas/{columna_id}", plt.Editar),
		r(D, "/api/plantillas/columnas/{columna_id}", plt.Eliminar),
		r(P, "/api/plantillas/bloques/{bloque_id}/columnas/orden", plt.Ordenar),
		r(P, "/api/plantillas/bloques/{bloque_id}/campos", plb.Agregar),
		r(A, "/api/plantillas/campos/{campo_id}", plb.Editar),
		r(D, "/api/plantillas/campos/{campo_id}", plb.Eliminar),
		r(P, "/api/plantillas/bloques/{bloque_id}/campos/orden", plb.Ordenar),
		r(A, "/api/plantillas/{id}/estilo", pls.Actualizar),
	}
}

func TestPermisos_EscriturasDelDisenadorExigenParametrizar(t *testing.T) {
	pool := setupTestDB(t)
	guarda := RequierePuedeParametrizar(pool)
	rutas := rutasEscrituraDisenador(pool)
	if len(rutas) != 54 {
		t.Fatalf("la tabla tiene %d rutas; main.go protege 54 (ver cmd/server/permisos_rutas_test.go)", len(rutas))
	}
	for _, rol := range rolesSembrados {
		actor := crearActorRol(t, pool, rol)
		for _, ruta := range rutas {
			nombre := rol + " " + ruta.metodo + " " + ruta.patron
			alcanzado := false
			sonda := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				alcanzado = true
				escribirJSON(w, http.StatusTeapot, map[string]any{"ok": true})
			})
			// Con la sonda: se prueba la decisión de la guarda sin que el
			// handler real escriba nada en la base con el Administrador.
			rec := peticionRol(t, ruta.metodo, ruta.patron, ruta.ruta, map[string]any{}, actor, guarda(sonda))
			if matrizEsperada[rol].PuedeParametrizar {
				if !alcanzado || rec.Code != http.StatusTeapot {
					t.Errorf("%s: el Administrador debía pasar la guarda, dio %d: %s", nombre, rec.Code, rec.Body.String())
				}
				continue
			}
			if alcanzado {
				t.Errorf("%s: la guarda dejó pasar a un rol sin puede_parametrizar", nombre)
				continue
			}
			afirmar403(t, rec, "puede_parametrizar", nombre)
		}
	}

	// Y con el handler real detrás, un Vendedor no deja rastro: el
	// catálogo no se crea.
	vendedor := crearActorRol(t, pool, rolVendedor)
	catalogoID := "PERM-CAT-" + sufijoUnico()
	cat := &CatalogosHandler{DB: pool}
	rec := peticionRol(t, http.MethodPost, "/api/catalogos", "/api/catalogos",
		map[string]any{"catalogo_id": catalogoID, "nombre_catalogo": "No debería existir"}, vendedor, guarda(http.HandlerFunc(cat.GuardarCatalogo)))
	afirmar403(t, rec, "puede_parametrizar", "Vendedor crea catálogo con el handler real")
	var existe bool
	pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM catalogos WHERE catalogo_id=$1)`, catalogoID).Scan(&existe)
	if existe {
		pool.Exec(context.Background(), `DELETE FROM catalogos WHERE catalogo_id=$1`, catalogoID)
		t.Fatal("el 403 no debía crear el catálogo")
	}
}

// Las lecturas del Diseñador y Plantillas también son administrativas:
// main.go las envuelve con esta guarda aunque sus handlers sigan siendo
// reutilizables de forma aislada en las pruebas de dominio.
func TestPermisos_LecturasDelDisenadorExigenAdministracion(t *testing.T) {
	pool := setupTestDB(t)
	cat := &CatalogosHandler{DB: pool}
	reg := &ReglasHandler{DB: pool}
	pl := &PlantillasHandler{DB: pool}
	lecturas := []struct {
		nombre, ruta string
		handler      http.HandlerFunc
	}{
		{"catálogos", "/api/catalogos/designer", cat.ListarDesigner},
		{"reglas", "/api/reglas", reg.Listar},
		{"plantillas", "/api/plantillas", pl.Listar},
		{"opciones de plantillas", "/api/plantillas/opciones", pl.Opciones},
	}
	for _, rol := range rolesSembrados {
		actor := crearActorRol(t, pool, rol)
		for _, l := range lecturas {
			protegido := RequierePuedeVerAdministracion(pool)(l.handler)
			rec := peticionRol(t, http.MethodGet, l.ruta, l.ruta, nil, actor, protegido)
			if matrizEsperada[rol].PuedeVerAdministracion {
				afirmarNo403(t, rec, http.StatusOK, rol+" lee "+l.nombre)
			} else {
				afirmar403(t, rec, "puede_ver_administracion", rol+" lee "+l.nombre)
			}
		}
	}
}
