package main

// Auditoría de QA — dimensión 4 (Auth & Authorization), nivel de wiring.
//
// Por qué esta prueba lee el código fuente en vez de hacer peticiones:
// el árbol de rutas se arma dentro de main(), que no es invocable desde
// un test, así que no hay forma de pedirle al router real que responda
// sin levantar el binario completo. Lo que sí se puede verificar de
// forma determinista es la propiedad que importa: que TODA ruta bajo
// /api esté registrada dentro del grupo que aplica
// middleware.RequiereSesion, salvo una whitelist explícita de rutas
// públicas y el grupo del API externo (RequiereApiKey).
//
// Esa verificación, combinada con las pruebas de
// internal/middleware/auth_test.go y apikey_test.go (que ya prueban que
// esos middlewares rechazan sin header, con token inventado y con token
// vencido), es lo que da la garantía completa: "el middleware rechaza"
// + "todas las rutas protegidas pasan por el middleware".
//
// El valor de esta prueba es atrapar la regresión más fácil de cometer:
// agregar un endpoint nuevo por fuera del r.Group protegido. Si eso
// pasa, este test falla nombrando la ruta.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// rutasPublicasEsperadas son las ÚNICAS rutas bajo /api que pueden
// responder sin credenciales. Agregar una acá es una decisión de
// seguridad deliberada: si este test falla porque apareció una ruta
// nueva en la lista de públicas, no la agregues sin entender por qué
// quedó fuera del grupo protegido.
var rutasPublicasEsperadas = map[string]string{
	"GET /api/health":                     "chequeo de vida, no expone datos de negocio",
	"POST /api/auth/login":                "es el endpoint que entrega la sesión; no puede exigirla",
	"GET /api/publico/cotizacion/{token}": "vista del cliente final; el token del enlace ES la credencial",
}

// rutasApiKeyEsperadas son las rutas que se autentican con
// X-Api-Key (integraciones externas) en vez de sesión de usuario.
var rutasApiKeyEsperadas = map[string]string{
	"POST /api/externo/solicitudes": "lo llama Bitrix24 u otro CRM con su propia clave",
}

type rutaRegistrada struct {
	metodo string
	ruta   string
	guarda string // "sesion" | "apikey" | "publica"
	linea  int
}

func (r rutaRegistrada) clave() string { return r.metodo + " " + r.ruta }

var metodosHTTP = map[string]bool{
	"Get": true, "Post": true, "Put": true, "Patch": true,
	"Delete": true, "Head": true, "Options": true,
}

func parsearRutasDeMain(t *testing.T) ([]rutaRegistrada, *token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	archivo, err := parser.ParseFile(fset, "main.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("no se pudo parsear main.go: %v", err)
	}

	rutas := make([]rutaRegistrada, 0, 80)
	var recorrer func(cuerpo *ast.BlockStmt, prefijo, guarda string)

	// guardaDelCuerpo detecta un r.Use(middleware.RequiereX(...)) en el
	// nivel superior de este bloque, que cambia la guarda para todo lo
	// que se registre dentro.
	guardaDelCuerpo := func(cuerpo *ast.BlockStmt, heredada string) string {
		guarda := heredada
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
			if !ok || sel.Sel.Name != "Use" || len(llamada.Args) == 0 {
				continue
			}
			for _, arg := range llamada.Args {
				argLlamada, ok := arg.(*ast.CallExpr)
				if !ok {
					continue
				}
				argSel, ok := argLlamada.Fun.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				switch argSel.Sel.Name {
				case "RequiereSesion":
					guarda = "sesion"
				case "RequiereApiKey":
					guarda = "apikey"
				}
			}
		}
		return guarda
	}

	unirRutas := func(prefijo, hijo string) string {
		if hijo == "" || hijo == "/" {
			if prefijo == "" {
				return "/"
			}
			return prefijo
		}
		return strings.TrimSuffix(prefijo, "/") + hijo
	}

	literalCadena := func(expr ast.Expr) (string, bool) {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		valor, err := strconv.Unquote(lit.Value)
		if err != nil {
			return "", false
		}
		return valor, true
	}

	recorrer = func(cuerpo *ast.BlockStmt, prefijo, guarda string) {
		guarda = guardaDelCuerpo(cuerpo, guarda)
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
			case metodosHTTP[sel.Sel.Name] && len(llamada.Args) >= 1:
				ruta, ok := literalCadena(llamada.Args[0])
				if !ok {
					t.Errorf("registro de ruta con path no literal en %s (línea %d): esta auditoría solo entiende paths literales",
						sel.Sel.Name, fset.Position(llamada.Pos()).Line)
					continue
				}
				rutas = append(rutas, rutaRegistrada{
					metodo: strings.ToUpper(sel.Sel.Name),
					ruta:   unirRutas(prefijo, ruta),
					guarda: guarda,
					linea:  fset.Position(llamada.Pos()).Line,
				})
			case sel.Sel.Name == "Route" && len(llamada.Args) == 2:
				ruta, ok := literalCadena(llamada.Args[0])
				if !ok {
					t.Errorf("r.Route con path no literal en la línea %d", fset.Position(llamada.Pos()).Line)
					continue
				}
				if fn, ok := llamada.Args[1].(*ast.FuncLit); ok {
					recorrer(fn.Body, unirRutas(prefijo, ruta), guarda)
				}
			case sel.Sel.Name == "Group" && len(llamada.Args) == 1:
				if fn, ok := llamada.Args[0].(*ast.FuncLit); ok {
					recorrer(fn.Body, prefijo, guarda)
				}
			case sel.Sel.Name == "Mount":
				// No se usa hoy. Si alguien lo usa, esta auditoría no
				// sabría qué rutas cuelgan del sub-router montado.
				t.Errorf("main.go usa r.Mount en la línea %d: esta auditoría no puede seguir las rutas de un sub-router montado, hay que extenderla",
					fset.Position(llamada.Pos()).Line)
			}
		}
	}

	// Punto de entrada: el cuerpo de main().
	for _, decl := range archivo.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" {
			continue
		}
		recorrer(fn.Body, "", "publica")
	}
	if len(rutas) == 0 {
		t.Fatal("la auditoría no encontró ninguna ruta en main(): el walker del AST está roto")
	}
	return rutas, fset, archivo
}

// TestRutasProtegidas_ElWalkerNoSeSalteaNingunRegistro es la red de
// seguridad de la auditoría misma: si el walker estructurado (que solo
// mira sentencias de nivel superior) dejara de ver registros —por
// ejemplo porque alguien registra rutas dentro de un if o un for—, las
// otras pruebas de este archivo pasarían en falso. Acá se cuenta por
// fuerza bruta con ast.Inspect sobre TODO el archivo y se comparan los
// totales.
func TestRutasProtegidas_ElWalkerNoSeSalteaNingunRegistro(t *testing.T) {
	rutas, fset, archivo := parsearRutasDeMain(t)

	porFuerzaBruta := 0
	ast.Inspect(archivo, func(n ast.Node) bool {
		llamada, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := llamada.Fun.(*ast.SelectorExpr)
		if !ok || !metodosHTTP[sel.Sel.Name] || len(llamada.Args) == 0 {
			return true
		}
		// router.Handle("/*", fileServer) no entra acá porque Handle no
		// está en metodosHTTP; los Get/Post/... del file server tampoco
		// existen. Cualquier otro Get/Post sí debe haber sido visto.
		if _, esLiteral := llamada.Args[0].(*ast.BasicLit); !esLiteral {
			return true
		}
		porFuerzaBruta++
		return true
	})

	if len(rutas) != porFuerzaBruta {
		t.Fatalf("el walker estructurado encontró %d rutas pero hay %d registros en el archivo: "+
			"hay rutas que la auditoría no está clasificando (¿registradas dentro de un if/for/helper?). "+
			"Arreglar parsearRutasDeMain antes de confiar en el resto de este archivo",
			len(rutas), porFuerzaBruta)
	}
	t.Logf("auditoría de rutas: %d endpoints clasificados en %s", len(rutas), fset.Position(archivo.Pos()).Filename)
}

// TestRutasProtegidas_TodaRutaApiExigeSesionSalvoWhitelist es el
// corazón de la dimensión 4: ninguna ruta nueva bajo /api puede quedar
// accesible sin credenciales por descuido.
func TestRutasProtegidas_TodaRutaApiExigeSesionSalvoWhitelist(t *testing.T) {
	rutas, _, _ := parsearRutasDeMain(t)

	for _, ruta := range rutas {
		if !strings.HasPrefix(ruta.ruta, "/api") {
			continue // el file server estático no es parte del API
		}
		clave := ruta.clave()
		switch ruta.guarda {
		case "sesion":
			// Correcto: está dentro del grupo con RequiereSesion. Y no
			// debería estar además en las whitelists.
			if _, publica := rutasPublicasEsperadas[clave]; publica {
				t.Errorf("%s está en rutasPublicasEsperadas pero en main.go quedó dentro del grupo protegido (línea %d): actualizar la whitelist", clave, ruta.linea)
			}
		case "apikey":
			if _, esperada := rutasApiKeyEsperadas[clave]; !esperada {
				t.Errorf("%s (main.go línea %d) se autentica con X-Api-Key pero no está en rutasApiKeyEsperadas: "+
					"si es deliberado, agregarla ahí con su justificación", clave, ruta.linea)
			}
		case "publica":
			if _, esperada := rutasPublicasEsperadas[clave]; !esperada {
				t.Errorf("HUECO DE SEGURIDAD: %s (main.go línea %d) está registrada FUERA del grupo con "+
					"middleware.RequiereSesion y no está en la whitelist de rutas públicas. "+
					"Cualquiera puede llamarla sin sesión. Moverla dentro del r.Group protegido, "+
					"o agregarla a rutasPublicasEsperadas si de verdad debe ser pública", clave, ruta.linea)
			}
		default:
			t.Errorf("%s quedó con guarda desconocida %q", clave, ruta.guarda)
		}
	}
}

// TestRutasProtegidas_LaWhitelistNoTieneRutasFantasma evita que la
// whitelist se llene de entradas viejas: si una ruta pública se
// renombra o se borra, su entrada acá tiene que irse también, para que
// la whitelist siga siendo una lista corta y auditable de verdad.
func TestRutasProtegidas_LaWhitelistNoTieneRutasFantasma(t *testing.T) {
	rutas, _, _ := parsearRutasDeMain(t)
	registradas := make(map[string]bool, len(rutas))
	for _, ruta := range rutas {
		registradas[ruta.clave()] = true
	}

	for clave := range rutasPublicasEsperadas {
		if !registradas[clave] {
			t.Errorf("rutasPublicasEsperadas incluye %q, que ya no existe en main.go: borrar la entrada", clave)
		}
	}
	for clave := range rutasApiKeyEsperadas {
		if !registradas[clave] {
			t.Errorf("rutasApiKeyEsperadas incluye %q, que ya no existe en main.go: borrar la entrada", clave)
		}
	}
}

// TestRutasProtegidas_InventarioParaLaAuditoria no afirma nada: imprime
// el inventario clasificado para poder pegarlo en docs/AUDITORIA_QA.md
// y revisarlo a ojo. Correr con: go test ./cmd/server/ -run Inventario -v
func TestRutasProtegidas_InventarioParaLaAuditoria(t *testing.T) {
	rutas, _, _ := parsearRutasDeMain(t)
	porGuarda := map[string][]string{}
	for _, ruta := range rutas {
		if !strings.HasPrefix(ruta.ruta, "/api") {
			continue
		}
		porGuarda[ruta.guarda] = append(porGuarda[ruta.guarda], ruta.clave())
	}
	for _, guarda := range []string{"publica", "apikey", "sesion"} {
		lista := porGuarda[guarda]
		sort.Strings(lista)
		t.Logf("=== %s (%d) ===", strings.ToUpper(guarda), len(lista))
		for _, clave := range lista {
			t.Logf("    %s", clave)
		}
	}
}

// TestRutasProtegidas_RechazoRealSobreHTTP es la contraparte en vivo del
// análisis estático: le pega al servidor de verdad y confirma que cada
// ruta protegida responde 401 sin credenciales. Se salta por defecto
// porque necesita el binario corriendo (docker compose up); no se puede
// exigir en "go test ./..." sin volver la suite dependiente de un
// servicio extra. Para correrla:
//
//	API_BASE_URL=http://localhost:8080 go test ./cmd/server/ -run RechazoReal -v
func TestRutasProtegidas_RechazoRealSobreHTTP(t *testing.T) {
	base := strings.TrimSuffix(os.Getenv("API_BASE_URL"), "/")
	if base == "" {
		t.Skip("API_BASE_URL no está seteada — ver el comentario de esta función")
	}
	rutas, _, _ := parsearRutasDeMain(t)
	cliente := &http.Client{}

	for _, ruta := range rutas {
		if ruta.guarda != "sesion" || !strings.HasPrefix(ruta.ruta, "/api") {
			continue
		}
		// Los path params se rellenan con un id inexistente: lo que se
		// verifica es que ni siquiera llegue a mirarlo (401 antes de 404).
		url := base + reemplazarParams(ruta.ruta)
		t.Run(ruta.metodo+" "+ruta.ruta, func(t *testing.T) {
			req, err := http.NewRequest(ruta.metodo, url, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("no se pudo armar la petición: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := cliente.Do(req)
			if err != nil {
				t.Fatalf("no se pudo llamar a %s: %v", url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("sin Authorization se esperaba 401 y respondió %d", resp.StatusCode)
			}
		})
	}
}

// reemplazarParams cambia {algo} por un valor inexistente pero con
// forma válida, para no depender de datos de la base.
func reemplazarParams(ruta string) string {
	partes := strings.Split(ruta, "/")
	for i, parte := range partes {
		if strings.HasPrefix(parte, "{") && strings.HasSuffix(parte, "}") {
			partes[i] = "auditoria-qa-id-inexistente"
		}
	}
	return strings.Join(partes, "/")
}
