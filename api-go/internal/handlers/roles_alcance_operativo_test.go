package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRolesAlcance_SolicitudesVendedorYConsultorSoloVenAsignadas(t *testing.T) {
	pool := setupTestDB(t)
	handler := &SolicitudesHandler{DB: pool}
	vendedor := crearActorRol(t, pool, rolVendedor)
	consultor := crearActorRol(t, pool, rolConsultor)
	otro := crearActorRol(t, pool, rolVendedor)

	propiaVendedor := crearSolicitudPrueba(t, pool, "Cliente vendedor propio", "", "", "Nueva")
	propiaConsultor := crearSolicitudPrueba(t, pool, "Cliente consultor propio", "", "", "Nueva")
	ajena := crearSolicitudPrueba(t, pool, "Cliente ajeno", "", "", "Nueva")
	for _, cambio := range []struct {
		consulta string
		usuario  string
		id       string
	}{
		{`UPDATE solicitudes SET vendedor_id=$1 WHERE solicitud_id::text=$2`, vendedor, propiaVendedor},
		{`UPDATE solicitudes SET analista_id=$1 WHERE solicitud_id::text=$2`, consultor, propiaConsultor},
		{`UPDATE solicitudes SET vendedor_id=$1 WHERE solicitud_id::text=$2`, otro, ajena},
	} {
		if _, err := pool.Exec(context.Background(), cambio.consulta, cambio.usuario, cambio.id); err != nil {
			t.Fatal(err)
		}
	}

	listar := func(actor string) map[string]bool {
		req := conActor(httptest.NewRequest(http.MethodGet, "/api/solicitudes", nil), actor)
		rec := httptest.NewRecorder()
		handler.Listar(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("listar solicitudes: %d %s", rec.Code, rec.Body.String())
		}
		var respuesta struct {
			Solicitudes []solicitudListado `json:"solicitudes"`
		}
		assertJSON(t, rec.Body.Bytes(), &respuesta)
		ids := map[string]bool{}
		for _, item := range respuesta.Solicitudes {
			ids[item.SolicitudID] = true
		}
		return ids
	}

	idsVendedor := listar(vendedor)
	if !idsVendedor[propiaVendedor] || idsVendedor[propiaConsultor] || idsVendedor[ajena] {
		t.Fatalf("alcance del Vendedor incorrecto: %#v", idsVendedor)
	}
	idsConsultor := listar(consultor)
	if !idsConsultor[propiaConsultor] || idsConsultor[propiaVendedor] || idsConsultor[ajena] {
		t.Fatalf("alcance del Consultor incorrecto: %#v", idsConsultor)
	}
}

func TestRolesAlcance_ClientesSoloIncluyeCreadosOVinculados(t *testing.T) {
	pool := setupTestDB(t)
	vendedor := crearActorRol(t, pool, rolVendedor)
	otro := crearActorRol(t, pool, rolVendedor)
	propio := cliCrearClientePrueba(t, pool, "Cliente propio "+sufijoUnico(), nil, "COTIZA", "Activo")
	ajeno := cliCrearClientePrueba(t, pool, "Cliente ajeno "+sufijoUnico(), nil, "COTIZA", "Activo")
	for _, cambio := range []struct{ usuario, cliente string }{{vendedor, propio}, {otro, ajeno}} {
		if _, err := pool.Exec(context.Background(), `UPDATE clientes SET usuario_creador_id=$1 WHERE cliente_id=$2`, cambio.usuario, cambio.cliente); err != nil {
			t.Fatal(err)
		}
	}

	req := conActor(httptest.NewRequest(http.MethodGet, "/api/clientes/gestion", nil), vendedor)
	rec := httptest.NewRecorder()
	(&ClientesHandler{DB: pool}).Listar(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar clientes: %d %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		Clientes []clienteGestion `json:"clientes"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	ids := map[string]bool{}
	for _, item := range respuesta.Clientes {
		ids[item.ClienteID] = true
	}
	if !ids[propio] || ids[ajeno] {
		t.Fatalf("alcance de clientes incorrecto: propio=%v ajeno=%v", ids[propio], ids[ajeno])
	}
}

func TestRolesAlcance_ReportesRespetaResponsable(t *testing.T) {
	pool := setupTestDB(t)
	fixture := crearFixtureReportes(t, pool)
	rec, respuesta := ejecutarReporteJSON(t, &ReportesHandler{DB: pool}, fixture.VendedorA, url.Values{})
	if rec.Code != http.StatusOK {
		t.Fatalf("reporte: %d %s", rec.Code, rec.Body.String())
	}
	obtenidos := codigosReporte(filasReporteTest(t, respuesta))
	for _, esperado := range []string{fixture.Codigos[0], fixture.Codigos[1], fixture.Codigos[3]} {
		if !contieneTexto(obtenidos, esperado) {
			t.Errorf("faltó cotización propia %s en %v", esperado, obtenidos)
		}
	}
	if contieneTexto(obtenidos, fixture.Codigos[2]) {
		t.Fatalf("el reporte incluyó una cotización del otro vendedor: %v", obtenidos)
	}
}

func TestRolesSoloConsulta_NoModificaClientesNiSolicitudes(t *testing.T) {
	pool := setupTestDB(t)
	solo := crearActorRol(t, pool, rolSolo)
	cliente := cliCrearClientePrueba(t, pool, "Cliente auditor "+sufijoUnico(), nil, "COTIZA", "Activo")
	solicitud := crearSolicitudPrueba(t, pool, "Solicitud auditor", "", "", "Nueva")

	recCliente, _ := cliPatchCliente(t, &ClientesHandler{DB: pool}, solo, cliente, map[string]any{"nombre_comercial": "No debe cambiar"})
	afirmar403(t, recCliente, "puede_editar_borrador", "Solo Consulta edita cliente")

	recSolicitud := peticionRol(t, http.MethodPatch, "/api/solicitudes/{id}", "/api/solicitudes/"+solicitud,
		map[string]any{"estado": "En revisión"}, solo, http.HandlerFunc((&SolicitudesHandler{DB: pool}).CambiarEstado))
	afirmar403(t, recSolicitud, "puede_editar_borrador", "Solo Consulta edita solicitud")
}

func contieneTexto(valores []string, buscado string) bool {
	for _, valor := range valores {
		if valor == buscado {
			return true
		}
	}
	return false
}
