package main

// Auditoría de wiring de puede_parametrizar. Mismo método que
// rutas_protegidas_test.go (leer main.go con go/ast, porque el router se
// arma dentro de main() y no se puede invocar desde un test), pero una
// pregunta distinta: no "¿exige sesión?" sino "¿toda ruta de ESCRITURA
// del Diseñador pasa además por handlers.RequierePuedeParametrizar?".
//
// Complementa las pruebas funcionales de
// internal/handlers/permisos_test.go, que prueban que el middleware
// rechaza a cada rol sin la bandera: esta prueba garantiza que el
// middleware está puesto donde tiene que estar, y atrapa la regresión
// más fácil de cometer — agregar un POST nuevo del Diseñador fuera del
// grupo protegido.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// tiposHandlerDisenador son los handlers de los módulos del Diseñador
// (catalogos.go, cotizador_tabs.go, secciones_adicionales.go,
// lista_precios_items.go, tabla_columnas.go, salidas_cotizador.go,
// reglas.go, reglas_cotizador.go, compilador.go, calculadoras.go y
// todos los plantilla*.go). Sus rutas de escritura exigen
// puede_parametrizar; sus GET usan la guarda independiente
// puede_ver_administracion.
var tiposHandlerDisenador = map[string]bool{
	"CatalogosHandler":              true,
	"CotizadorTabsHandler":          true,
	"SeccionesAdicionalesHandler":   true,
	"ListaPreciosItemsHandler":      true,
	"TablaColumnasHandler":          true,
	"SalidasCotizadorHandler":       true,
	"ReglasHandler":                 true,
	"ReglasCotizadorHandler":        true,
	"CompiladorHandler":             true,
	"CalculadorasHandler":           true,
	"PlantillasHandler":             true,
	"PlantillaEstructuraHandler":    true,
	"PlantillaVinculacionesHandler": true,
	"PlantillaCondicionesHandler":   true,
	"PlantillaTablaColumnasHandler": true,
	"PlantillaEstiloHandler":        true,
	"PlantillaBloqueCamposHandler":  true,
}

// tiposHandlerOperacion son los demás: validan sus propios permisos por
// bandera dentro del handler (o no aplican ninguna) y NUNCA deben quedar
// detrás de puede_parametrizar — un Vendedor necesita cotizar.
var tiposHandlerOperacion = map[string]bool{
	"HealthHandler":              true,
	"AuthHandler":                true,
	"UsuariosHandler":            true,
	"CotizacionesHandler":        true,
	"ClientesHandler":            true,
	"DashboardHandler":           true,
	"ReportesHandler":            true,
	"CotizadorRuntimeHandler":    true,
	"EnlacesPublicosHandler":     true,
	"VistaPreviaOfertaHandler":   true,
	"OfertaPDFHandler":           true, // PDF de la oferta: mismos permisos que ver la cotización
	"IntegracionesHandler":       true,
	"SolicitudesHandler":         true,
	"SolicitudesExternasHandler": true,
}

type rutaConPermiso struct {
	metodo       string
	ruta         string
	handlerVar   string
	parametrizar bool
	linea        int
}

func parsearRutasConPermiso(t *testing.T) (rutas []rutaConPermiso, tipoPorVar map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	archivo, err := parser.ParseFile(fset, "main.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("no se pudo parsear main.go: %v", err)
	}

	tipoPorVar = map[string]string{}
	var cuerpoMain *ast.BlockStmt
	for _, decl := range archivo.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
			cuerpoMain = fn.Body
		}
	}
	if cuerpoMain == nil {
		t.Fatal("no se encontró func main en main.go")
	}

	// x := &handlers.TipoHandler{...}
	for _, stmt := range cuerpoMain.List {
		asig, ok := stmt.(*ast.AssignStmt)
		if !ok || len(asig.Lhs) != 1 || len(asig.Rhs) != 1 {
			continue
		}
		ident, ok := asig.Lhs[0].(*ast.Ident)
		if !ok {
			continue
		}
		unario, ok := asig.Rhs[0].(*ast.UnaryExpr)
		if !ok || unario.Op != token.AND {
			continue
		}
		lit, ok := unario.X.(*ast.CompositeLit)
		if !ok {
			continue
		}
		if sel, ok := lit.Type.(*ast.SelectorExpr); ok {
			tipoPorVar[ident.Name] = sel.Sel.Name
		}
	}

	usaParametrizar := func(cuerpo *ast.BlockStmt) bool {
		for _, stmt := range cuerpo.List {
			expr, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			llamada, ok := expr.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := llamada.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Use" {
				continue
			}
			for _, arg := range llamada.Args {
				if argLlamada, ok := arg.(*ast.CallExpr); ok {
					if argSel, ok := argLlamada.Fun.(*ast.SelectorExpr); ok && argSel.Sel.Name == "RequierePuedeParametrizar" {
						return true
					}
				}
			}
		}
		return false
	}

	unir := func(prefijo, hijo string) string {
		if hijo == "" || hijo == "/" {
			return prefijo
		}
		return strings.TrimSuffix(prefijo, "/") + hijo
	}

	var recorrer func(cuerpo *ast.BlockStmt, prefijo string, parametrizar bool)
	recorrer = func(cuerpo *ast.BlockStmt, prefijo string, parametrizar bool) {
		parametrizar = parametrizar || usaParametrizar(cuerpo)
		for _, stmt := range cuerpo.List {
			expr, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			llamada, ok := expr.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := llamada.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			switch {
			case metodosHTTP[sel.Sel.Name] && len(llamada.Args) == 2:
				lit, ok := llamada.Args[0].(*ast.BasicLit)
				if !ok {
					continue
				}
				ruta, _ := strconv.Unquote(lit.Value)
				handlerVar := ""
				if hs, ok := llamada.Args[1].(*ast.SelectorExpr); ok {
					if id, ok := hs.X.(*ast.Ident); ok {
						handlerVar = id.Name
					}
				}
				rutas = append(rutas, rutaConPermiso{
					metodo: strings.ToUpper(sel.Sel.Name), ruta: unir(prefijo, ruta),
					handlerVar: handlerVar, parametrizar: parametrizar,
					linea: fset.Position(llamada.Pos()).Line,
				})
			case sel.Sel.Name == "Route" && len(llamada.Args) == 2:
				lit, ok := llamada.Args[0].(*ast.BasicLit)
				if !ok {
					continue
				}
				ruta, _ := strconv.Unquote(lit.Value)
				if fn, ok := llamada.Args[1].(*ast.FuncLit); ok {
					recorrer(fn.Body, unir(prefijo, ruta), parametrizar)
				}
			case sel.Sel.Name == "Group" && len(llamada.Args) == 1:
				if fn, ok := llamada.Args[0].(*ast.FuncLit); ok {
					recorrer(fn.Body, prefijo, parametrizar)
				}
			}
		}
	}
	recorrer(cuerpoMain, "", false)
	if len(rutas) == 0 {
		t.Fatal("la auditoría no encontró rutas en main(): el walker está roto")
	}
	return rutas, tipoPorVar
}

// Todo handler construido en main.go tiene que estar clasificado: un
// módulo nuevo obliga a decidir explícitamente si es del Diseñador.
func TestPermisosRutas_TodoHandlerEstaClasificado(t *testing.T) {
	_, tipoPorVar := parsearRutasConPermiso(t)
	for variable, tipo := range tipoPorVar {
		if !strings.HasSuffix(tipo, "Handler") {
			continue
		}
		if !tiposHandlerDisenador[tipo] && !tiposHandlerOperacion[tipo] {
			t.Errorf("main.go construye %s (%s) pero no está en tiposHandlerDisenador ni en tiposHandlerOperacion: "+
				"decidir si sus escrituras exigen puede_parametrizar", variable, tipo)
		}
	}
	for tipo := range tiposHandlerDisenador {
		encontrado := false
		for _, t2 := range tipoPorVar {
			if t2 == tipo {
				encontrado = true
			}
		}
		if !encontrado {
			t.Errorf("tiposHandlerDisenador incluye %s, que main.go ya no construye: borrar la entrada", tipo)
		}
	}
}

func TestPermisosRutas_EscriturasDelDisenadorExigenParametrizar(t *testing.T) {
	rutas, tipoPorVar := parsearRutasConPermiso(t)
	escrituras := 0
	for _, ruta := range rutas {
		tipo := tipoPorVar[ruta.handlerVar]
		clave := ruta.metodo + " " + ruta.ruta
		esDisenador := tiposHandlerDisenador[tipo]
		esEscritura := ruta.metodo != "GET"

		switch {
		case esDisenador && esEscritura && !ruta.parametrizar:
			t.Errorf("HUECO DE PERMISOS: %s (%s, main.go línea %d) escribe en el Diseñador pero está fuera del "+
				"grupo con handlers.RequierePuedeParametrizar", clave, tipo, ruta.linea)
		case esDisenador && !esEscritura && ruta.parametrizar:
			t.Errorf("%s (main.go línea %d) es de lectura y quedó detrás de puede_parametrizar: "+
				"las lecturas usan puede_ver_administracion, no el permiso de escritura", clave, ruta.linea)
		case !esDisenador && ruta.parametrizar:
			t.Errorf("%s (%s, main.go línea %d) no es del Diseñador pero quedó detrás de puede_parametrizar",
				clave, tipo, ruta.linea)
		}
		if esDisenador && esEscritura {
			escrituras++
		}
	}
	if escrituras == 0 {
		t.Fatal("no se encontró ninguna escritura del Diseñador: la clasificación está rota")
	}
	t.Logf("puede_parametrizar: %d rutas de escritura del Diseñador verificadas", escrituras)
}
