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

// TestCompilador_IncluyeTipoCalculoYValoresDeCatalogo cubre la tarea 4 de
// la migración 0023: el JSON compilado de un CAMPO_CATALOGO debe traer el
// tipo_calculo de su catálogo y el valor_calculo de cada valor activo, para
// que el Motor de Ejecución no necesite otra consulta aparte.
func TestCompilador_IncluyeTipoCalculoYValoresDeCatalogo(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-CATCALC-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Margen", "activo": true,
	})
	catalogoID := crearCatalogoPrueba(t, tabsHandler.DB, "Catálogo margen compilado", "")
	if _, err := tabsHandler.DB.Exec(context.Background(), `UPDATE catalogos SET tipo_calculo='PORCENTAJE' WHERE catalogo_id=$1`, catalogoID); err != nil {
		t.Fatal(err)
	}
	valorID := crearValorCatalogoPrueba(t, tabsHandler.DB, catalogoID, "30%", "")
	var valorSistema string
	if err := tabsHandler.DB.QueryRow(context.Background(), `
		UPDATE catalogo_valores SET valor_calculo=0.30 WHERE valor_id=$1
		RETURNING valor_sistema`, valorID).Scan(&valorSistema); err != nil {
		t.Fatal(err)
	}
	elementoID := "TEST-COMP-CATCALC-EL-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "CAMPO_CATALOGO",
		"etiqueta": "Margen", "catalogo_id": catalogoID, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear elemento: %s", rec.Body.String())
	}

	handler := &CompiladorHandler{DB: tabsHandler.DB}
	resultado, err := handler.validarConfiguracion(context.Background(), calculadoraID)
	if err != nil || !resultado.Valido {
		t.Fatalf("compilar: resultado=%+v err=%v", resultado, err)
	}
	cfg := resultado.Tabs[0].Elementos[0].Configuracion
	if cfg["catalogo_tipo_calculo"] != "PORCENTAJE" {
		t.Fatalf("catalogo_tipo_calculo = %v, esperaba PORCENTAJE", cfg["catalogo_tipo_calculo"])
	}
	valores, ok := cfg["catalogo_valores_calculo"].([]map[string]any)
	if !ok || len(valores) != 1 {
		t.Fatalf("catalogo_valores_calculo inesperado: %+v", cfg["catalogo_valores_calculo"])
	}
	if valores[0]["valor_sistema"] != valorSistema || valores[0]["valor_calculo"] != 0.30 {
		t.Fatalf("valor de catálogo compilado inesperado: %+v", valores[0])
	}
}

// TestCompilador_CatalogoSinValorNoTraeValorCalculo cubre que un catálogo
// SIN_VALOR (el default) quede marcado como tal en el compilado, sin que
// eso rompa la compilación de un elemento que lo usa solo como descriptivo.
func TestCompilador_CatalogoSinValorNoTraeValorCalculo(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-CATSV-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Descriptivo", "activo": true,
	})
	catalogoID := crearCatalogoPrueba(t, tabsHandler.DB, "Catálogo tipo de agente", "")
	elementoID := "TEST-COMP-CATSV-EL-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "CAMPO_CATALOGO",
		"etiqueta": "Tipo de agente", "catalogo_id": catalogoID, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear elemento: %s", rec.Body.String())
	}

	handler := &CompiladorHandler{DB: tabsHandler.DB}
	resultado, err := handler.validarConfiguracion(context.Background(), calculadoraID)
	if err != nil || !resultado.Valido {
		t.Fatalf("compilar: resultado=%+v err=%v", resultado, err)
	}
	cfg := resultado.Tabs[0].Elementos[0].Configuracion
	if cfg["catalogo_tipo_calculo"] != "SIN_VALOR" {
		t.Fatalf("catalogo_tipo_calculo = %v, esperaba SIN_VALOR", cfg["catalogo_tipo_calculo"])
	}
}

// TestCompilador_IncluyeReglasCotizador cubre la tarea 5 de la migración
// 0024: el JSON compilado debe traer las reglas_cotizador activas de la
// calculadora, con sus campos_objetivo ya resueltos, para que el frontend
// no tenga que pedirlas aparte.
func TestCompilador_IncluyeReglasCotizador(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-REGLAS-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Reglas", "activo": true,
	})
	condicionID := "TEST-COMP-REGLA-COND-" + sufijoUnico()
	objetivoID := "TEST-COMP-REGLA-OBJ-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": condicionID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Usa teléfono",
		"configuracion": map[string]any{"tipo_campo": "TEXTO"}, "activo": true,
	})
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": objetivoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Minutos",
		"configuracion": map[string]any{"tipo_campo": "NUMERO"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear elementos: %s", rec.Body.String())
	}
	reglasHandler := &ReglasCotizadorHandler{DB: tabsHandler.DB}
	rec = postCatalogos(t, reglasHandler.Guardar, "/api/cotizador/reglas", map[string]any{
		"regla_id": "TEST-COMP-R01-" + sufijoUnico(), "calculadora_id": calculadoraID, "nombre": "R01",
		"campo_condicion_id": condicionID, "operador": "IGUAL_A", "valor_comparacion": "No",
		"accion": "OCULTAR", "campos_objetivo": []string{objetivoID}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear regla: %s", rec.Body.String())
	}

	handler := &CompiladorHandler{DB: tabsHandler.DB}
	resultado, err := handler.validarConfiguracion(context.Background(), calculadoraID)
	if err != nil || !resultado.Valido {
		t.Fatalf("compilar: resultado=%+v err=%v", resultado, err)
	}
	if len(resultado.Reglas) != 1 {
		t.Fatalf("esperaba 1 regla compilada, obtuvo %+v", resultado.Reglas)
	}
	regla := resultado.Reglas[0]
	if regla.CampoCondicionID != condicionID || regla.Operador != "IGUAL_A" || regla.ValorComparacion != "No" || regla.Accion != "OCULTAR" {
		t.Fatalf("regla compilada con datos inesperados: %+v", regla)
	}
	if len(regla.CamposObjetivo) != 1 || regla.CamposObjetivo[0] != objetivoID {
		t.Fatalf("campos_objetivo compilados inesperados: %+v", regla.CamposObjetivo)
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

func TestCompilador_AnidaHijosYConfiguracionDeOpcionesPropuesta(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-OPC-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Planes", "activo": true,
	})
	padreID := "TEST-COMP-OPC-PADRE-" + sufijoUnico()
	hijoID := "TEST-COMP-OPC-HIJO-" + sufijoUnico()
	rec := postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": padreID, "tab_id": tabID, "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Opciones",
		"configuracion": map[string]any{
			"cantidad_inicial": 2, "nombres_sugeridos": "Starter, Premium", "vista_editar": "PESTANAS",
			"vista_resumen": "CAJAS", "vista_oferta": "TABLA_COMPARATIVA", "permitir_duplicar": true,
		}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear padre: %d: %s", rec.Code, rec.Body.String())
	}
	rec = postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": hijoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Precio",
		"componente_padre_id": padreID, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear hijo: %d: %s", rec.Code, rec.Body.String())
	}
	handler := &CompiladorHandler{DB: tabsHandler.DB}
	resultado, err := handler.validarConfiguracion(context.Background(), calculadoraID)
	if err != nil || !resultado.Valido {
		t.Fatalf("compilar opciones: resultado=%+v err=%v", resultado, err)
	}
	elementos := resultado.Tabs[0].Elementos
	if len(elementos) != 1 || elementos[0].ElementoID != padreID || len(elementos[0].Hijos) != 1 || elementos[0].Hijos[0].ElementoID != hijoID {
		t.Fatalf("estructura de opciones no quedó anidada: %+v", elementos)
	}
	cfg := elementos[0].Configuracion
	if cfg["cantidad_inicial"] != float64(2) || cfg["nombres_sugeridos"] != "Starter, Premium" || cfg["vista_editar"] != "PESTANAS" {
		t.Fatalf("configuración de opciones incompleta en compilado: %+v", cfg)
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
