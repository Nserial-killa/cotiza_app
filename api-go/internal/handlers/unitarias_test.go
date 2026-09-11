package handlers

// Auditoría de QA — dimensión 1 (Unit).
//
// A diferencia del resto de los _test.go de este paquete, NADA de acá
// toca Postgres: son pruebas de lógica pura. Por eso no llaman a
// setupTestDB y corren siempre, incluso sin DATABASE_URL seteada
// (verificable con: env -u DATABASE_URL go test ./internal/handlers/ -run TestUnit).
//
// El proyecto venía con toda su cobertura en integración, que es lo
// honesto para SQL + bcrypt, pero deja sin red las funciones que solo
// transforman datos: formateo de montos, parseo de filtros,
// normalización de ids, sanitización de correos. Esas son las que se
// fijan acá.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"
)

// ---------- reportes.go ----------

func TestUnitFormatoMontoCSV_UsaComaDecimalYDosDecimales(t *testing.T) {
	casos := []struct {
		nombre   string
		valor    float64
		esperado string
	}{
		{"cero", 0, "0,00"},
		{"entero", 100, "100,00"},
		{"con decimales", 18500.5, "18500,50"},
		{"redondea a dos decimales", 1234.567, "1234,57"},
		{"negativo", -1234.5, "-1234,50"},
		{"miles sin separador de miles", 1234567.89, "1234567,89"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			if got := formatoMontoCSV(caso.valor); got != caso.esperado {
				t.Errorf("formatoMontoCSV(%v) = %q, se esperaba %q: si el separador decimal deja de ser coma, "+
					"Excel en configuración regional latinoamericana lee el monto como texto y no como número",
					caso.valor, got, caso.esperado)
			}
		})
	}
}

func TestUnitFormatoMontoCSV_SoloReemplazaElPuntoDecimal(t *testing.T) {
	// El reemplazo es de una sola ocurrencia: si algún día el formato
	// incluyera separador de miles, esta prueba avisa que hay que
	// revisar la función en vez de dejar "1.234,56" mal armado.
	got := formatoMontoCSV(1000.25)
	if strings.Count(got, ",") != 1 {
		t.Errorf("formatoMontoCSV(1000.25) = %q: debe tener exactamente una coma (la decimal)", got)
	}
	if strings.Contains(got, ".") {
		t.Errorf("formatoMontoCSV(1000.25) = %q: no debe quedar ningún punto", got)
	}
}

func TestUnitValidarFechaReporte_AceptaVacioYFormatoISO(t *testing.T) {
	casos := []struct {
		nombre string
		valor  string
		valido bool
	}{
		{"vacío es válido porque el filtro es opcional", "", true},
		{"formato ISO", "2026-09-10", true},
		{"formato local con barras", "10/09/2026", false},
		{"texto basura", "basura", false},
		{"fecha imposible", "2026-13-45", false},
		{"solo el año", "2026", false},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			err := validarFechaReporte(caso.valor, "fecha_desde")
			if caso.valido && err != nil {
				t.Errorf("validarFechaReporte(%q) devolvió error %v y debía aceptarse", caso.valor, err)
			}
			if !caso.valido {
				if err == nil {
					t.Fatalf("validarFechaReporte(%q) no devolvió error: sin esta validación el valor llega al SQL "+
						"como ::date y Postgres lo rechaza, saliendo como 500 en vez de 400", caso.valor)
				}
				// El nombre del campo tiene que viajar en el mensaje: es
				// lo único que le dice al usuario cuál de los filtros
				// escribió mal.
				if !strings.Contains(err.Error(), "fecha_desde") {
					t.Errorf("el mensaje %q no menciona el campo fecha_desde", err.Error())
				}
			}
		})
	}
}

func TestUnitTextoReporte_NilQuedaEnCadenaVacia(t *testing.T) {
	if got := textoReporte(nil); got != "" {
		t.Errorf("textoReporte(nil) = %q, se esperaba cadena vacía", got)
	}
	vacio := ""
	if got := textoReporte(&vacio); got != "" {
		t.Errorf("textoReporte(puntero a \"\") = %q, se esperaba cadena vacía", got)
	}
	texto := "Cliente Ejemplo S.A."
	if got := textoReporte(&texto); got != texto {
		t.Errorf("textoReporte(%q) = %q", texto, got)
	}
}

// ---------- cotizador_runtime.go ----------

func TestUnitVersionOpcional_SoloEnterosPositivosOVacio(t *testing.T) {
	casos := []struct {
		nombre   string
		entrada  string
		esperado int
		conError bool
	}{
		{"vacío significa 'la versión actual'", "", 0, false},
		{"entero positivo", "3", 3, false},
		{"espacios alrededor del número", " 3 ", 3, false},
		{"solo espacios se trata como vacío", " ", 0, false},
		{"texto", "abc", 0, true},
		{"cero no es una versión", "0", 0, true},
		{"negativo", "-1", 0, true},
		{"decimal", "3.5", 0, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			version, err := versionOpcional(caso.entrada)
			if caso.conError {
				if err == nil {
					t.Fatalf("versionOpcional(%q) no devolvió error: este helper es lo que evita que "+
						"?version=%s llegue al SQL como ::int y devuelva 500", caso.entrada, caso.entrada)
				}
				return
			}
			if err != nil {
				t.Fatalf("versionOpcional(%q) devolvió error %v", caso.entrada, err)
			}
			if version != caso.esperado {
				t.Errorf("versionOpcional(%q) = %d, se esperaba %d", caso.entrada, version, caso.esperado)
			}
		})
	}
}

// ---------- catalogos.go ----------

func TestUnitNormalizarIDs_LimpiaDeduplicaYPreservaOrden(t *testing.T) {
	casos := []struct {
		nombre   string
		entrada  []string
		esperado []string
	}{
		{"nil devuelve slice vacío, no nil", nil, []string{}},
		{"quita vacíos y espacios", []string{"a", "", "  ", " b "}, []string{"a", "b"}},
		{"deduplica preservando el orden de aparición", []string{"b", "a", "b", "c", "a"}, []string{"b", "a", "c"}},
		{"deduplica después del trim", []string{"a", " a "}, []string{"a"}},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			got := normalizarIDs(caso.entrada)
			if got == nil {
				t.Fatal("normalizarIDs nunca debe devolver nil: los callers lo insertan en bucle sin chequear")
			}
			if len(got) != len(caso.esperado) {
				t.Fatalf("normalizarIDs(%v) = %v, se esperaba %v", caso.entrada, got, caso.esperado)
			}
			for i := range got {
				if got[i] != caso.esperado[i] {
					t.Fatalf("normalizarIDs(%v) = %v, se esperaba %v (el orden importa: define el orden de inserción)",
						caso.entrada, got, caso.esperado)
				}
			}
		})
	}
}

func TestUnitEnteroFlexible_AceptaNumeroStringVacioYNull(t *testing.T) {
	// enteroFlexible existe porque el frontend heredado manda el mismo
	// campo a veces como número y a veces como string (o vacío). Si
	// alguna de estas formas dejara de aceptarse, la pantalla de
	// catálogos rompería al guardar.
	casos := []struct {
		nombre   string
		cuerpo   string
		esperado enteroFlexible
		conError bool
	}{
		{"número", `{"orden": 5}`, 5, false},
		{"string con número", `{"orden": "5"}`, 5, false},
		{"string vacío cae a 0", `{"orden": ""}`, 0, false},
		{"null cae a 0", `{"orden": null}`, 0, false},
		{"campo ausente queda en 0", `{}`, 0, false},
		{"string con espacios", `{"orden": " 7 "}`, 7, false},
		{"string no numérico", `{"orden": "abc"}`, 0, true},
		{"booleano", `{"orden": true}`, 0, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			var destino struct {
				Orden enteroFlexible `json:"orden"`
			}
			err := json.Unmarshal([]byte(caso.cuerpo), &destino)
			if caso.conError {
				if err == nil {
					t.Fatalf("%s: se esperaba error de deserialización", caso.cuerpo)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: error inesperado %v", caso.cuerpo, err)
			}
			if destino.Orden != caso.esperado {
				t.Errorf("%s → orden = %d, se esperaba %d", caso.cuerpo, destino.Orden, caso.esperado)
			}
		})
	}
}

// ---------- usuarios.go ----------

func TestUnitGenerarUsuarioID_FormaYSanitizacion(t *testing.T) {
	casos := []struct {
		nombre       string
		correo       string
		baseEsperada string
	}{
		{"correo simple", "juan@exceltecgroup.com", "juan"},
		{"punto en el local colapsa a guion", "juan.perez@exceltecgroup.com", "juan-perez"},
		{"guion bajo colapsa a guion", "juan_perez@exceltecgroup.com", "juan-perez"},
		{"mayúsculas se bajan", "Juan.PEREZ@exceltecgroup.com", "juan-perez"},
		{"caracteres raros se descartan", "juan+etiqueta!@exceltecgroup.com", "juanetiqueta"},
		{"guiones de los extremos se recortan", ".juan.@exceltecgroup.com", "juan"},
		{"local no ASCII cae al fallback", "ñ@exceltecgroup.com", "usuario"},
		{"sin arroba usa todo el texto", "solotexto", "solotexto"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			id := generarUsuarioID(caso.correo)
			esperadoPrefijo := "usr-" + caso.baseEsperada + "-"
			if !strings.HasPrefix(id, esperadoPrefijo) {
				t.Fatalf("generarUsuarioID(%q) = %q, se esperaba que empezara con %q", caso.correo, id, esperadoPrefijo)
			}
			sufijo := strings.TrimPrefix(id, esperadoPrefijo)
			if len(sufijo) != 6 {
				t.Errorf("el sufijo aleatorio de %q es %q (%d chars), se esperaban 6 hex", id, sufijo, len(sufijo))
			}
			if !regexp.MustCompile(`^[0-9a-f]{6}$`).MatchString(sufijo) {
				t.Errorf("el sufijo %q no es hexadecimal", sufijo)
			}
		})
	}
}

func TestUnitGenerarUsuarioID_TruncaLaBaseA24Caracteres(t *testing.T) {
	correo := strings.Repeat("a", 40) + "@exceltecgroup.com"
	id := generarUsuarioID(correo)
	base := strings.TrimSuffix(strings.TrimPrefix(id, "usr-"), id[len(id)-7:])
	if len(base) != 24 {
		t.Errorf("la base de %q mide %d caracteres, se esperaban 24 (el truncado evita ids ilegibles)", id, len(base))
	}
}

func TestUnitGenerarUsuarioID_DosLlamadasConElMismoCorreoDanIdsDistintos(t *testing.T) {
	// El sufijo aleatorio es lo que permite dos altas simultáneas con
	// correos parecidos sin chocar en la clave primaria.
	primero := generarUsuarioID("juan.perez@exceltecgroup.com")
	segundo := generarUsuarioID("juan.perez@exceltecgroup.com")
	if primero == segundo {
		t.Errorf("generarUsuarioID devolvió el mismo id dos veces (%q): el sufijo aleatorio no está funcionando", primero)
	}
}

func TestUnitPatronCorreoValido_AceptaYRechaza(t *testing.T) {
	validos := []string{
		"juan@exceltecgroup.com",
		"juan.perez@exceltecgroup.com",
		"juan+etiqueta@sub.exceltecgroup.co.cr",
	}
	invalidos := []string{
		"sin-arroba.com",
		"@sinlocal.com",
		"sindominio@",
		"sinpunto@exceltecgroup",
		"con espacio@exceltecgroup.com",
		"",
		"doble@@exceltecgroup.com",
	}
	for _, correo := range validos {
		if !patronCorreoValido.MatchString(correo) {
			t.Errorf("%q debería considerarse un correo válido", correo)
		}
	}
	for _, correo := range invalidos {
		if patronCorreoValido.MatchString(correo) {
			t.Errorf("%q no debería pasar la validación de correo", correo)
		}
	}
}

func TestUnitComoViolacionUnica_DetectaElCodigo23505(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "usuarios_correo_key"}

	if _, ok := comoViolacionUnica(nil); ok {
		t.Error("comoViolacionUnica(nil) devolvió true: un error nulo no es una violación de unicidad")
	}
	if _, ok := comoViolacionUnica(errors.New("cualquier otra cosa")); ok {
		t.Error("un error genérico no debe reportarse como violación de unicidad")
	}
	if _, ok := comoViolacionUnica(&pgconn.PgError{Code: "23503"}); ok {
		t.Error("23503 es violación de clave ajena, no de unicidad: no debe confundirse (una daría 409, la otra no)")
	}
	detectado, ok := comoViolacionUnica(pgErr)
	if !ok {
		t.Fatal("no detectó el código 23505: sin esto un correo duplicado devolvería 500 en vez de 409")
	}
	if detectado.ConstraintName != "usuarios_correo_key" {
		t.Errorf("devolvió el error con constraint %q, se esperaba usuarios_correo_key", detectado.ConstraintName)
	}
	// errors.As tiene que atravesar el envoltorio: los handlers reciben
	// el error ya envuelto por pgx/pgxpool.
	if _, ok := comoViolacionUnica(fmt.Errorf("insertando usuario: %w", pgErr)); !ok {
		t.Error("no detectó el 23505 dentro de un error envuelto con %w: errors.As debe atravesar la cadena")
	}
}

// ---------- auth.go ----------

func TestUnitGenerarToken_Hex64YSiempreDistinto(t *testing.T) {
	token, err := generarToken()
	if err != nil {
		t.Fatalf("generarToken devolvió error: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("el token mide %d caracteres, se esperaban 64 (32 bytes en hex)", len(token))
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(token) {
		t.Errorf("el token %q no es hexadecimal en minúsculas", token)
	}
	otro, err := generarToken()
	if err != nil {
		t.Fatalf("generarToken devolvió error en la segunda llamada: %v", err)
	}
	if token == otro {
		t.Error("dos llamadas devolvieron el mismo token: la fuente de aleatoriedad no está funcionando")
	}
}

func TestUnitHashSenuelo_MantieneCosto12(t *testing.T) {
	// El señuelo existe para que un correo inexistente cueste lo mismo
	// que uno real (ver auth.go y CLAUDE.md). Si su costo baja del que
	// usa migration-python (12), el tiempo de respuesta vuelve a
	// delatar qué correos existen y la defensa muere en silencio.
	costo, err := bcrypt.Cost([]byte(hashSenuelo))
	if err != nil {
		t.Fatalf("hashSenuelo no es un hash bcrypt válido: %v", err)
	}
	if costo != costoPin {
		t.Errorf("hashSenuelo tiene costo %d y los PIN reales se hashean con costo %d: "+
			"con costos distintos, el tiempo de respuesta permite distinguir un correo que existe de uno que no",
			costo, costoPin)
	}
}

func TestUnitEscribirJSON_SeteaContentTypeYStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	escribirJSON(rec, http.StatusTeapot, map[string]any{"ok": true, "dato": "x"})

	if rec.Code != http.StatusTeapot {
		t.Errorf("escribirJSON respondió %d, se esperaba %d", rec.Code, http.StatusTeapot)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, se esperaba application/json (el frontend heredado parsea sin chequear)", got)
	}
	var cuerpo map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &cuerpo); err != nil {
		t.Fatalf("el cuerpo no es JSON válido: %v (crudo: %s)", err, rec.Body.String())
	}
	if cuerpo["ok"] != true || cuerpo["dato"] != "x" {
		t.Errorf("el cuerpo serializado no coincide: %#v", cuerpo)
	}
}

// ---------- cotizaciones.go ----------

func TestUnitFechaLocalCotiza_UsaZonaFijaDeCostaRica(t *testing.T) {
	// Zona fija -06:00 a propósito: Costa Rica no aplica horario de
	// verano y la imagen Alpine no trae tzdata, así que usar UTC haría
	// que los códigos de oferta salgan con la fecha del día siguiente
	// durante las últimas seis horas de la jornada local.
	_, offset := fechaLocalCotiza().Zone()
	if offset != -6*60*60 {
		t.Errorf("el offset es %d segundos, se esperaban %d (-06:00)", offset, -6*60*60)
	}
}

func TestUnitPunterosDeCotizaciones_NilYNoNil(t *testing.T) {
	if got := valorTexto(nil); got != "" {
		t.Errorf("valorTexto(nil) = %q, se esperaba cadena vacía", got)
	}
	texto := "algo"
	if got := valorTexto(&texto); got != texto {
		t.Errorf("valorTexto(%q) = %q", texto, got)
	}

	if got := valorIntPtr(nil); got != nil {
		t.Errorf("valorIntPtr(nil) = %v, se esperaba nil (así el JSON sale como null y no como 0)", got)
	}
	numero := 7
	if got := valorIntPtr(&numero); got != 7 {
		t.Errorf("valorIntPtr(&7) = %v, se esperaba 7", got)
	}

	if got := valorFechaPtr(nil); got != nil {
		t.Errorf("valorFechaPtr(nil) = %v, se esperaba nil", got)
	}
	fecha := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if got := valorFechaPtr(&fecha); got != fecha {
		t.Errorf("valorFechaPtr devolvió %v, se esperaba %v", got, fecha)
	}

	p := strPtr("hola")
	if p == nil || *p != "hola" {
		t.Errorf("strPtr(\"hola\") no devolvió un puntero al valor esperado")
	}
}

// TestUnitEstadosCotizacion_CoincidenConElCheckDeLaMigracion ata dos de
// los tres lugares donde vive la lista de estados. CLAUDE.md advierte
// que el mapa de Go, el CHECK de 0007_cotizaciones_shell.sql y el
// arreglo ESTADOS de cotiza_scripts.html tienen que cambiar juntos; si
// alguien agrega un estado solo en Go, el INSERT explota en producción
// contra el CHECK. Esta prueba lee el .sql como archivo (no la base),
// así que corre sin Postgres.
func TestUnitEstadosCotizacion_CoincidenConElCheckDeLaMigracion(t *testing.T) {
	const ruta = "../../migrations/0007_cotizaciones_shell.sql"
	contenido, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", ruta, err)
	}

	estadosSQL := estadosDelCheckUnit(t, string(contenido), "CREATE TABLE cotizaciones (")
	estadosGo := make([]string, 0, len(estadosCotizacionValidos))
	for estado := range estadosCotizacionValidos {
		estadosGo = append(estadosGo, estado)
	}
	sort.Strings(estadosSQL)
	sort.Strings(estadosGo)

	if strings.Join(estadosSQL, "|") != strings.Join(estadosGo, "|") {
		t.Errorf("la lista de estados no coincide entre Go y el CHECK de la migración.\n"+
			"  Go  (estadosCotizacionValidos): %v\n"+
			"  SQL (0007_cotizaciones_shell):  %v\n"+
			"Los dos tienen que cambiar juntos, junto con el arreglo ESTADOS de cotiza_scripts.html",
			estadosGo, estadosSQL)
	}
}

// TestUnitEstadosCotizacionBloqueados_SonUnSubconjuntoDeLosValidos evita
// que un estado terminal quede escrito distinto en los dos mapas (un
// typo dejaría una cotización editable para siempre).
func TestUnitEstadosCotizacionBloqueados_SonUnSubconjuntoDeLosValidos(t *testing.T) {
	for estado := range estadosCotizacionBloqueados {
		if !estadosCotizacionValidos[estado] {
			t.Errorf("%q está en estadosCotizacionBloqueados pero no en estadosCotizacionValidos: "+
				"si es un typo, ese estado nunca bloquearía la edición", estado)
		}
	}
}

// estadosDelCheckUnit extrae los valores entre comillas simples del
// primer CHECK (estado IN (...)) que aparece después del marcador dado.
func estadosDelCheckUnit(t *testing.T, sql, marcador string) []string {
	t.Helper()
	inicioTabla := strings.Index(sql, marcador)
	if inicioTabla < 0 {
		t.Fatalf("no se encontró %q en la migración", marcador)
	}
	resto := sql[inicioTabla:]
	inicioCheck := strings.Index(resto, "CHECK (estado IN (")
	if inicioCheck < 0 {
		t.Fatalf("no se encontró el CHECK de estado después de %q", marcador)
	}
	resto = resto[inicioCheck:]
	fin := strings.Index(resto, "))")
	if fin < 0 {
		t.Fatal("el CHECK de estado no cierra con '))'")
	}
	valores := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(resto[:fin], -1)
	if len(valores) == 0 {
		t.Fatal("el CHECK de estado no tiene valores entre comillas simples")
	}
	estados := make([]string, 0, len(valores))
	for _, coincidencia := range valores {
		estados = append(estados, coincidencia[1])
	}
	return estados
}

// ---------- cotizador_tabs.go ----------

func TestUnitNormalizarConfiguracionElemento_TolerancíaDelFrontendHeredado(t *testing.T) {
	casos := []struct {
		nombre    string
		principal string
		alias     string
		esperado  map[string]any
		conError  bool
	}{
		{"ambos ausentes dan objeto vacío", "", "", map[string]any{}, false},
		{"null cae al alias ausente", `null`, "", map[string]any{}, false},
		{"string vacío da objeto vacío", `""`, "", map[string]any{}, false},
		{"objeto directo", `{"ancho": 2}`, "", map[string]any{"ancho": float64(2)}, false},
		{"objeto codificado como string (lo que manda el GAS viejo)", `"{\"ancho\": 2}"`, "", map[string]any{"ancho": float64(2)}, false},
		{"usa el alias cuando el principal está ausente", "", `{"ancho": 3}`, map[string]any{"ancho": float64(3)}, false},
		{"usa el alias cuando el principal es null", `null`, `{"ancho": 3}`, map[string]any{"ancho": float64(3)}, false},
		{"string que no es JSON", `"basura"`, "", nil, true},
		{"un arreglo no es configuración", `[1,2]`, "", nil, true},
		{"JSON inválido", `{no-json}`, "", nil, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			var principal, alias json.RawMessage
			if caso.principal != "" {
				principal = json.RawMessage(caso.principal)
			}
			if caso.alias != "" {
				alias = json.RawMessage(caso.alias)
			}
			got, err := normalizarConfiguracionElemento(principal, alias)
			if caso.conError {
				if err == nil {
					t.Fatalf("se esperaba error para principal=%q alias=%q, devolvió %#v", caso.principal, caso.alias, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado para principal=%q alias=%q: %v", caso.principal, caso.alias, err)
			}
			if got == nil {
				t.Fatal("nunca debe devolver nil sin error: el caller lo serializa directo a JSONB")
			}
			if len(got) != len(caso.esperado) {
				t.Fatalf("configuración = %#v, se esperaba %#v", got, caso.esperado)
			}
			for clave, valor := range caso.esperado {
				if got[clave] != valor {
					t.Errorf("configuración[%q] = %#v, se esperaba %#v", clave, got[clave], valor)
				}
			}
		})
	}
}

// ---------- plantilla_estilo.go ----------

func TestUnitNormalizarOpcionEstilo_NormalizaAMayusculasYValida(t *testing.T) {
	casos := []struct {
		nombre     string
		valor      string
		permitidos map[string]bool
		campo      string
		esperado   string
		conError   bool
	}{
		{"formato en minúsculas se normaliza", "carta", formatosPaginaValidos, "formato_pagina", "CARTA", false},
		{"formato con espacios", " a4 ", formatosPaginaValidos, "formato_pagina", "A4", false},
		{"formato inválido", "oficio", formatosPaginaValidos, "formato_pagina", "OFICIO", true},
		{"margen válido", "amplio", margenesValidos, "margenes", "AMPLIO", false},
		{"margen inválido", "gigante", margenesValidos, "margenes", "GIGANTE", true},
		{"portada válida", "banda_superior", portadasValidas, "diseno_portada", "BANDA_SUPERIOR", false},
		{"portada inválida", "circular", portadasValidas, "diseno_portada", "CIRCULAR", true},
		{"tabla válida", "tarjetas", estilosTablaValidos, "estilo_tablas", "TARJETAS", false},
		{"tabla inválida", "zebra", estilosTablaValidos, "estilo_tablas", "ZEBRA", true},
		{"vacío no es un valor válido", "", formatosPaginaValidos, "formato_pagina", "", true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			valor := caso.valor
			err := normalizarOpcionEstilo(&valor, caso.permitidos, caso.campo)
			if valor != caso.esperado {
				t.Errorf("el valor quedó en %q, se esperaba %q (la función normaliza in-place a mayúsculas)", valor, caso.esperado)
			}
			if caso.conError {
				if err == nil {
					t.Fatalf("%q debía rechazarse para %s", caso.valor, caso.campo)
				}
				if !strings.Contains(err.Error(), caso.campo) {
					t.Errorf("el mensaje %q no menciona el campo %q", err.Error(), caso.campo)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q debía aceptarse para %s: %v", caso.valor, caso.campo, err)
			}
		})
	}
}

func TestUnitNormalizarOpcionEstilo_NilEsAusenciaNoError(t *testing.T) {
	// Los campos del PATCH son punteros: nil significa "no lo mandaron"
	// y tiene que pasar sin error para que el patch parcial funcione.
	if err := normalizarOpcionEstilo(nil, formatosPaginaValidos, "formato_pagina"); err != nil {
		t.Errorf("un valor nil debe aceptarse (campo ausente en un PATCH parcial), devolvió: %v", err)
	}
}

// ---------- plantilla_estructura.go ----------

func TestUnitValidarOrdenCompleto_ExigeLaMismaListaSinRepetidos(t *testing.T) {
	existentes := []string{"a", "b", "c"}
	casos := []struct {
		nombre   string
		nuevo    []string
		conError bool
	}{
		{"permutación completa", []string{"c", "a", "b"}, false},
		{"mismo orden", []string{"a", "b", "c"}, false},
		{"falta un id", []string{"a", "b"}, true},
		{"id repetido", []string{"a", "a", "b"}, true},
		{"id de otro contenedor", []string{"a", "b", "z"}, true},
		{"id vacío", []string{"a", "b", ""}, true},
		{"más ids que los existentes", []string{"a", "b", "c", "d"}, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			err := validarOrdenCompleto(caso.nuevo, existentes)
			if caso.conError && err == nil {
				t.Errorf("validarOrdenCompleto(%v, %v) no dio error: un orden parcial dejaría filas con "+
					"el orden viejo y la pantalla mostraría los bloques mezclados", caso.nuevo, existentes)
			}
			if !caso.conError && err != nil {
				t.Errorf("validarOrdenCompleto(%v, %v) dio error %v", caso.nuevo, existentes, err)
			}
		})
	}
}

func TestUnitValidarOrdenCompleto_DosListasVaciasSeAceptan(t *testing.T) {
	// Comportamiento real y deliberado: el caso "no hay nada que
	// ordenar" no lo bloquea esta función sino decodificarOrden, que
	// rechaza una lista vacía antes de llegar acá.
	if err := validarOrdenCompleto([]string{}, []string{}); err != nil {
		t.Errorf("dos listas vacías deberían aceptarse acá (el rechazo vive en decodificarOrden): %v", err)
	}
}

func TestUnitDecodificarOrden_AceptaListaUObjetoYRechazaElResto(t *testing.T) {
	casos := []struct {
		nombre   string
		cuerpo   string
		campo    string
		esperado []string
		conError bool
	}{
		{"lista de ids", `["a","b"]`, "secciones", []string{"a", "b"}, false},
		{"objeto con la clave esperada", `{"secciones":["a","b"]}`, "secciones", []string{"a", "b"}, false},
		{"hace trim de cada id", `[" a ","b "]`, "secciones", []string{"a", "b"}, false},
		{"objeto con otra clave", `{"otra":["a"]}`, "secciones", nil, true},
		{"la clave no es una lista", `{"secciones":"a"}`, "secciones", nil, true},
		{"lista vacía", `[]`, "secciones", nil, true},
		{"un string suelto", `"basura"`, "secciones", nil, true},
		{"JSON inválido", `{`, "secciones", nil, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(caso.cuerpo))
			ids, err := decodificarOrden(req, caso.campo)
			if caso.conError {
				if err == nil {
					t.Fatalf("cuerpo %s: se esperaba error, devolvió %v", caso.cuerpo, ids)
				}
				return
			}
			if err != nil {
				t.Fatalf("cuerpo %s: error inesperado %v", caso.cuerpo, err)
			}
			if len(ids) != len(caso.esperado) {
				t.Fatalf("cuerpo %s → %v, se esperaba %v", caso.cuerpo, ids, caso.esperado)
			}
			for i := range ids {
				if ids[i] != caso.esperado[i] {
					t.Errorf("cuerpo %s → %v, se esperaba %v", caso.cuerpo, ids, caso.esperado)
				}
			}
		})
	}
}
