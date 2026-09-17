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

// TestCompilador_AnidaHijosDeContenedor cubre la Ronda 1 de tipos nuevos
// (migración 0017): un Contenedor con dos Campos hijos no debe aparecer con
// esos hijos en el array plano "elementos" del tab compilado — deben quedar
// anidados en su "hijos".
func TestCompilador_AnidaHijosDeContenedor(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-CONT-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Contenedor compilado", "activo": true,
	})
	contenedorID := "TEST-COMP-CONT-EL-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": contenedorID, "tab_id": tabID, "tipo": "CONTENEDOR", "etiqueta": "Datos",
		"orden": 1, "configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear contenedor: %s", rec.Body.String())
	}
	hijoAID := "TEST-COMP-HIJO-A-" + sufijoUnico()
	hijoBID := "TEST-COMP-HIJO-B-" + sufijoUnico()
	for i, id := range []string{hijoAID, hijoBID} {
		rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": id, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Campo " + id,
			"componente_padre_id": contenedorID, "orden": i + 2, "activo": true,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("crear hijo %s: %s", id, rec.Body.String())
		}
	}

	handler := &CompiladorHandler{DB: tabsHandler.DB}
	res := postCompilador(t, handler.Validar, calculadoraID)
	if !res.Valido {
		t.Fatalf("esperaba válido, errores: %+v", res.Errores)
	}

	// postCompilador no expone los tabs compilados (solo el resumen), así
	// que se vuelve a llamar validarConfiguracion directo para inspeccionar
	// la estructura anidada.
	resultado, err := handler.validarConfiguracion(context.Background(), calculadoraID)
	if err != nil {
		t.Fatalf("validarConfiguracion: %v", err)
	}
	if len(resultado.Tabs) != 1 {
		t.Fatalf("esperaba 1 tab, obtuvo %d", len(resultado.Tabs))
	}
	elementos := resultado.Tabs[0].Elementos
	if len(elementos) != 1 || elementos[0].ElementoID != contenedorID {
		t.Fatalf("esperaba solo el contenedor en el nivel plano, obtuvo: %+v", elementos)
	}
	if len(elementos[0].Hijos) != 2 {
		t.Fatalf("esperaba 2 hijos anidados en el contenedor, obtuvo %d: %+v", len(elementos[0].Hijos), elementos[0].Hijos)
	}
	idsHijos := map[string]bool{elementos[0].Hijos[0].ElementoID: true, elementos[0].Hijos[1].ElementoID: true}
	if !idsHijos[hijoAID] || !idsHijos[hijoBID] {
		t.Fatalf("hijos anidados no son los esperados: %+v", elementos[0].Hijos)
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
