package handlers

import (
	"context"
	"net/http"
	"testing"
)

type respuestaCompiladorTest struct {
	OK                   bool               `json:"ok"`
	Compilado            bool               `json:"compilado"`
	Valido               bool               `json:"valido"`
	VersionConfiguracion string             `json:"version_configuracion"`
	CompiladoID          string             `json:"compilado_id"`
	Errores              []string           `json:"errores"`
	Advertencias         []string           `json:"advertencias"`
	Resumen              resumenCompilacion `json:"resumen"`
}

func postCompilador(t *testing.T, handler http.HandlerFunc, calculadoraID string) respuestaCompiladorTest {
	t.Helper()
	rec := postCatalogos(t, handler, "/api/cotizador", map[string]any{"calculadora_id": calculadoraID})
	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res respuestaCompiladorTest
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK {
		t.Fatalf("esperaba ok:true: %s", rec.Body.String())
	}
	return res
}

func TestCompilador_ValidarSinTabsDaError(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	handler := &CompiladorHandler{DB: tabsHandler.DB}

	res := postCompilador(t, handler.Validar, calculadoraID)
	if res.Valido || len(res.Errores) == 0 {
		t.Fatalf("sin tabs esperaba valido:false y errores: %+v", res)
	}
	if res.Resumen.Tabs != 0 || res.Resumen.ElementosTab != 0 {
		t.Fatalf("resumen inesperado sin tabs: %+v", res.Resumen)
	}
}

func TestCompilador_CampoCatalogoInactivoDaError(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-TAB-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Datos", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %s", rec.Body.String())
	}
	catalogoID := crearCatalogoPrueba(t, tabsHandler.DB, "Catálogo inactivo compilador", "")
	if _, err := tabsHandler.DB.Exec(context.Background(), `UPDATE catalogos SET activo=false WHERE catalogo_id=$1`, catalogoID); err != nil {
		t.Fatal(err)
	}
	rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-COMP-EL-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO_CATALOGO", "etiqueta": "Cliente", "catalogo_id": catalogoID, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear elemento: %s", rec.Body.String())
	}

	res := postCompilador(t, (&CompiladorHandler{DB: tabsHandler.DB}).Validar, calculadoraID)
	if res.Valido || len(res.Errores) != 1 {
		t.Fatalf("catálogo inactivo esperaba un error: %+v", res)
	}
}

func TestCompilador_TabSinElementosEsAdvertencia(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	rec := postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": "TEST-COMP-VACIO-" + sufijoUnico(), "calculadora_id": calculadoraID,
		"nombre": "Sección vacía", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %s", rec.Body.String())
	}

	res := postCompilador(t, (&CompiladorHandler{DB: tabsHandler.DB}).Validar, calculadoraID)
	if !res.Valido || len(res.Errores) != 0 || len(res.Advertencias) != 1 {
		t.Fatalf("tab vacío debía ser válido con advertencia: %+v", res)
	}
}

func TestCompilador_DosPublicacionesVersionanYDejanUnaActiva(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-PUB-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Publicable", "activo": true,
	})
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-COMP-CAMPO-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Nombre", "orden": 1, "activo": true,
	})
	handler := &CompiladorHandler{DB: tabsHandler.DB}
	primera := postCompilador(t, handler.Compilar, calculadoraID)
	segunda := postCompilador(t, handler.Compilar, calculadoraID)
	if !primera.Compilado || !segunda.Compilado || primera.VersionConfiguracion != "1" || segunda.VersionConfiguracion != "2" {
		t.Fatalf("versiones inesperadas: primera=%+v segunda=%+v", primera, segunda)
	}
	if primera.CompiladoID == "" || segunda.CompiladoID == "" || primera.CompiladoID == segunda.CompiladoID {
		t.Fatalf("IDs compilados inválidos: %q %q", primera.CompiladoID, segunda.CompiladoID)
	}
	var activas, anteriores int
	if err := tabsHandler.DB.QueryRow(context.Background(), `
		SELECT COUNT(*) FILTER (WHERE estado='ACTIVA'), COUNT(*) FILTER (WHERE estado='ANTERIOR')
		FROM cotizadores_compilados WHERE calculadora_id=$1`, calculadoraID).Scan(&activas, &anteriores); err != nil {
		t.Fatal(err)
	}
	if activas != 1 || anteriores != 1 {
		t.Fatalf("esperaba una ACTIVA y una ANTERIOR, obtuvo activas=%d anteriores=%d", activas, anteriores)
	}
	var versionActual, estado string
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT version_actual, estado FROM calculadoras WHERE calculadora_id=$1`, calculadoraID).Scan(&versionActual, &estado); err != nil {
		t.Fatal(err)
	}
	if versionActual != "2" || estado != "Publicado" {
		t.Fatalf("calculadora no actualizada: versión=%q estado=%q", versionActual, estado)
	}
}
