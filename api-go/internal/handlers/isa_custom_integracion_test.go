package handlers

// Casos de aceptación CP-01…CP-15 del documento de definición funcional ISA
// Custom (docs/Definicion_Calculadora_ISA_Custom_Cotiza-1.pdf, §10), contra
// el caso sembrado por internal/isacustom.
//
// Cómo está armada la prueba, a propósito:
//   - El caso se configura con el MISMO sembrador que usa el comando
//     cmd/seed-isa-custom, por HTTP real (httptest.Server) y con sesión real
//     (middleware.RequiereSesion). Nada del caso se fabrica con SQL.
//   - El árbol de rutas del binario vive dentro de main() y no es invocable
//     desde un test (ver cmd/server/rutas_protegidas_test.go), así que
//     routerCasoISA registra acá las rutas que el caso usa, con los mismos
//     handlers y el mismo middleware. Si main.go cambia una de estas rutas,
//     el sembrador falla y esta prueba también.
//   - Los importes esperados se CALCULAN en esperadoISA a partir de los
//     valores de internal/isacustom/caso.json (dataset §11 + cargos del
//     sembrador), nunca copiando lo que devuelve el sistema. Los valores
//     literales que da el documento (100.00, 142.86, ...) se afirman además
//     explícitamente.
//   - SQL solo se usa para LEER evidencia que el API no expone (el
//     snapshot_json) y para limpiar al final.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/isacustom"
	"cotiza/api/internal/middleware"
)

func routerCasoISA(pool *pgxpool.Pool) http.Handler {
	auth := &AuthHandler{DB: pool}
	catalogos := &CatalogosHandler{DB: pool}
	tabs := &CotizadorTabsHandler{DB: pool}
	salidas := &SalidasCotizadorHandler{DB: pool}
	compilador := &CompiladorHandler{DB: pool}
	reglasCotizador := &ReglasCotizadorHandler{DB: pool}
	cotizaciones := &CotizacionesHandler{DB: pool}
	clientes := &ClientesHandler{DB: pool}
	calculadoras := &CalculadorasHandler{DB: pool}
	runtime := &CotizadorRuntimeHandler{DB: pool}
	enlaces := &EnlacesPublicosHandler{DB: pool}
	vistaPrevia := &VistaPreviaOfertaHandler{DB: pool}
	plantillas := &PlantillasHandler{DB: pool}
	estructura := &PlantillaEstructuraHandler{DB: pool}
	vinculaciones := &PlantillaVinculacionesHandler{DB: pool}
	condiciones := &PlantillaCondicionesHandler{DB: pool}
	columnas := &PlantillaTablaColumnasHandler{DB: pool}
	estilo := &PlantillaEstiloHandler{DB: pool}

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", auth.Login)
		r.Get("/publico/cotizacion/{token}", enlaces.VerCotizacion)
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequiereSesion(pool))
			r.Delete("/auth/logout", auth.Logout)
			r.Post("/calculadoras", calculadoras.Crear)
			r.Post("/catalogos", catalogos.GuardarCatalogo)
			r.Post("/catalogos/valores", catalogos.GuardarValor)
			r.Post("/cotizador/tabs", tabs.GuardarTab)
			r.Post("/cotizador/elementos", tabs.GuardarElemento)
			r.Post("/cotizador/salidas", salidas.Guardar)
			r.Post("/cotizador/validar", compilador.Validar)
			r.Post("/cotizador/compilar", compilador.Compilar)
			r.Post("/cotizador/reglas", reglasCotizador.Guardar)
			r.Get("/cotizador/runtime/{cotizacion_id}", runtime.Obtener)
			r.Post("/cotizador/runtime/{cotizacion_id}/valores", runtime.GuardarValores)
			r.Post("/cotizador/runtime/{cotizacion_id}/opciones", runtime.AdministrarOpciones)
			r.Get("/clientes", cotizaciones.ListarClientes)
			r.Post("/clientes", clientes.Crear)
			r.Get("/cotizaciones", cotizaciones.Listar)
			r.Post("/cotizaciones", cotizaciones.Crear)
			r.Get("/cotizaciones/{id}", cotizaciones.Detalle)
			r.Post("/cotizaciones/{id}/enlace", enlaces.GenerarEnlace)
			r.Get("/cotizaciones/{id}/vista-previa-oferta", vistaPrevia.Ver)
			r.Get("/plantillas", plantillas.Listar)
			r.Post("/plantillas", plantillas.Crear)
			r.Get("/plantillas/{id}", plantillas.Detalle)
			r.Post("/plantillas/{id}/publicar", plantillas.Publicar)
			r.Post("/plantillas/{id}/secciones", estructura.CrearSeccion)
			r.Post("/plantillas/secciones/{seccion_id}/bloques", estructura.CrearBloque)
			r.Patch("/plantillas/bloques/{bloque_id}", estructura.EditarBloque)
			r.Post("/plantillas/bloques/{bloque_id}/vinculacion", vinculaciones.Guardar)
			r.Post("/plantillas/bloques/{bloque_id}/condicion", condiciones.Guardar)
			r.Post("/plantillas/bloques/{bloque_id}/columnas", columnas.Agregar)
			r.Patch("/plantillas/columnas/{columna_id}", columnas.Editar)
			r.Patch("/plantillas/{id}/estilo", estilo.Actualizar)
		})
	})
	return r
}

// casoISA es el estado compartido por los 15 casos: un solo sembrado (tarda
// unos segundos) y una cotización principal con las tres opciones de §11.
type casoISA struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	cli      *isacustom.Cliente
	datos    isacustom.Datos
	informe  isacustom.Informe
	opciones []string            // opcion_id en el orden de §11
	valores  []map[string]string // espejo local de lo guardado en cada opción
}

func TestISACustom_CasosAceptacionCP01aCP15(t *testing.T) {
	pool := setupTestDB(t)
	srv := httptest.NewServer(routerCasoISA(pool))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	sufijo := strings.ReplaceAll(sufijoUnico(), ".", "")
	correo := "isa.cp." + sufijo + "@test.local"
	crearUsuarioPrueba(t, pool, correo, "4321", "Administrador", "Activo")
	datos, err := isacustom.CargarDatos()
	if err != nil {
		t.Fatal(err)
	}
	cli := isacustom.NuevoCliente(srv.URL, "CPT"+sufijo)
	if err := cli.Login(ctx, correo, "4321"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cli.Logout(context.Background())
		limpiarCasoISA(pool, cli)
	})

	c := &casoISA{t: t, ctx: ctx, pool: pool, cli: cli, datos: datos}
	c.informe = cli.Sembrar(ctx, datos, false)
	for i := range datos.Escenarios {
		c.valores = append(c.valores, datos.ValoresEscenario(i))
	}
	for _, op := range c.informe.Opciones {
		c.opciones = append(c.opciones, fmt.Sprint(op["opcion_id"]))
	}

	// El orden de ejecución sigue el documento salvo CP-13 (generar la
	// oferta pública), que va al final para que la oferta refleje el estado
	// definitivo después de todos los cambios de los demás casos.
	t.Run("CP-01 crear calculadora y compilar", c.cp01)
	if t.Failed() {
		t.Fatal("sin el caso sembrado y compilado no tiene sentido seguir con CP-02…CP-15")
	}
	t.Run("CP-02 seleccionar catálogos", c.cp02)
	t.Run("CP-03 telefonía Sí a No", c.cp03)
	t.Run("CP-04 telefonía No a Sí", c.cp04)
	t.Run("CP-05 integración No", c.cp05)
	t.Run("CP-06 margen 20 a 30", c.cp06)
	t.Run("CP-07 guardar y reabrir", c.cp07)
	t.Run("CP-08 duplicar escenario", c.cp08)
	t.Run("CP-09 tres escenarios", c.cp09)
	t.Run("CP-10 sin recomendada", c.cp10)
	t.Run("CP-11 una sola opción", c.cp11)
	t.Run("CP-12 vista previa", c.cp12)
	t.Run("CP-14 modificar catálogo", c.cp14)
	t.Run("CP-15 miles y moneda", c.cp15)
	t.Run("CP-13 generar oferta", c.cp13)
}

// --- Cálculo esperado, independiente del motor --------------------------------

func r2(v float64) float64 { return math.Round(v*100) / 100 }

func (c *casoISA) catalogoCalculo(catalogo, codigo string) float64 {
	for _, cat := range c.datos.Catalogos {
		if cat.Codigo != catalogo {
			continue
		}
		for _, v := range cat.Valores {
			if fmt.Sprint(v[0]) == codigo {
				f, _ := v[2].(float64)
				return f
			}
		}
	}
	c.t.Fatalf("el dataset no tiene %s/%s", catalogo, codigo)
	return 0
}

func (c *casoISA) catalogoEtiqueta(catalogo, codigo string) string {
	for _, cat := range c.datos.Catalogos {
		if cat.Codigo == catalogo {
			for _, v := range cat.Valores {
				if fmt.Sprint(v[0]) == codigo {
					return fmt.Sprint(v[1])
				}
			}
		}
	}
	return ""
}

// esperadoISA replica las fórmulas del documento (§5, las de caso.json) con
// el mismo redondeo a 2 decimales por Campo Calculado que aplica el motor.
// Incluye el efecto de las reglas: con Telefonía=No los campos de voz y sus
// importes valen 0; con Integración=No, la cantidad oculta vale 0.
func (c *casoISA) esperadoISA(v map[string]string) map[string]float64 {
	num := func(k string) float64 {
		f, err := strconv.ParseFloat(v[k], 64)
		if err != nil {
			c.t.Fatalf("valor no numérico en %s: %q", k, v[k])
		}
		return f
	}
	si := func(k string) bool { return v[k] == "SI" }
	tel, integ := si("USA_TELEFONIA"), si("INTEGRACION_API")
	minutosExtra, cantidadInteg := num("MINUTOS_EXTRA"), num("CANTIDAD_INTEGRACIONES")
	if !tel {
		minutosExtra = 0
	}
	if !integ {
		cantidadInteg = 0
	}
	r := map[string]float64{}
	r["COSTO_CHAT"] = r2(c.catalogoCalculo("CAT_CONVERSACIONES", v["CONVERSACIONES_MES"]) * num("COSTO_CONVERSACION"))
	if tel {
		r["COSTO_VOZ"] = r2(c.catalogoCalculo("CAT_MINUTOS_VOZ", v["MINUTOS_MES"]) * num("COSTO_MINUTO_VOZ"))
		r["PRECIO_VOZ"] = r2(r["COSTO_VOZ"] / (1 - c.catalogoCalculo("CAT_MARGEN", v["MARGEN_VOZ"])))
		r["CONSUMO_EXTRA_VOZ"] = r2(minutosExtra * num("PRECIO_EXTRA_MINUTO"))
	}
	r["PRECIO_CHAT"] = r2(r["COSTO_CHAT"] / (1 - c.catalogoCalculo("CAT_MARGEN", v["MARGEN_CHAT"])))
	canales := 0.0
	for canal, cargo := range map[string]string{"USA_WHATSAPP": "CARGO_WHATSAPP", "USA_CHATWEB": "CARGO_CHATWEB", "USA_TEAMS": "CARGO_TEAMS", "USA_TELEFONIA": "CARGO_TELEFONIA"} {
		if si(canal) {
			canales += num(cargo)
		}
	}
	r["PRECIO_CANALES"] = r2(canales)
	if integ {
		r["PRECIO_INTEGRACIONES"] = r2(cantidadInteg * num("PRECIO_INTEGRACION_UNIT"))
	}
	impl := c.catalogoCalculo("CAT_COMPLEJIDAD", v["COMPLEJIDAD_IMPL"])
	if integ {
		impl += cantidadInteg * num("CARGO_IMPL_INTEGRACION")
	}
	if si("ENTRENAMIENTO_PERSONAL") {
		impl += num("CARGO_ENTRENAMIENTO")
	}
	if si("BASE_CONOCIMIENTO") {
		impl += num("CARGO_BASE_CONOCIMIENTO")
	}
	r["PRECIO_IMPLEMENTACION"] = r2(impl)
	r["CONSUMO_EXTRA_CHAT"] = r2(num("CONVERSACIONES_EXTRA") * num("PRECIO_EXTRA_CONVERSACION"))
	r["TOTAL_MENSUAL"] = r2(r["PRECIO_CHAT"] + r["PRECIO_VOZ"] + r["PRECIO_CANALES"] + r["PRECIO_INTEGRACIONES"] + r["CONSUMO_EXTRA_CHAT"] + r["CONSUMO_EXTRA_VOZ"])
	r["TOTAL_INICIAL"] = r["PRECIO_IMPLEMENTACION"]
	r["TOTAL_PRIMER_MES"] = r2(r["TOTAL_INICIAL"] + r["TOTAL_MENSUAL"])
	r["TOTAL_COSTO"] = r2(r["COSTO_CHAT"] + r["COSTO_VOZ"])
	r["TOTAL_GANANCIA"] = r2(r["TOTAL_MENSUAL"] - r["TOTAL_COSTO"])
	r["MARGEN_TOTAL"] = r2((1 - r["TOTAL_COSTO"]/r["TOTAL_MENSUAL"]) * 100)
	return r
}

// --- Acceso al API --------------------------------------------------------------

func (c *casoISA) llamar(metodo, ruta string, cuerpo any) isacustom.Respuesta {
	c.t.Helper()
	r, err := c.cli.Llamar(c.ctx, metodo, ruta, cuerpo)
	if err != nil {
		c.t.Fatalf("%s %s: %v", metodo, ruta, err)
	}
	return r
}

func (c *casoISA) ok(metodo, ruta string, cuerpo any) isacustom.Respuesta {
	c.t.Helper()
	r := c.llamar(metodo, ruta, cuerpo)
	if !r.OK() {
		c.t.Fatalf("%s %s: HTTP %d %s", metodo, ruta, r.Estado, r.Error())
	}
	return r
}

type runtimeISA struct {
	datos map[string]any
	els   map[string]map[string]any
}

func (c *casoISA) runtime(cotizacionID string) runtimeISA {
	c.t.Helper()
	r := c.ok("GET", "/api/cotizador/runtime/"+cotizacionID, nil)
	return runtimeISA{r.Datos, isacustom.Elementos(r.Datos)}
}

func (c *casoISA) el(rt runtimeISA, codigo string) map[string]any {
	c.t.Helper()
	el := rt.els[c.cli.ID(codigo)]
	if el == nil {
		c.t.Fatalf("el runtime no trae %s", codigo)
	}
	return el
}

func (c *casoISA) calculado(rt runtimeISA, codigo, opcionID string) float64 {
	c.t.Helper()
	porOpcion, _ := c.el(rt, codigo)["valores_resueltos_por_opcion"].(map[string]any)
	v, ok := porOpcion[opcionID].(float64)
	if !ok {
		c.t.Fatalf("%s no tiene valor resuelto para la opción %s: %v", codigo, opcionID, porOpcion[opcionID])
	}
	return v
}

func (c *casoISA) guardado(rt runtimeISA, codigo, opcionID string) any {
	porOpcion, _ := rt.datos["valores"].(map[string]any)[c.cli.ID(codigo)].(map[string]any)
	return porOpcion[opcionID]
}

func (c *casoISA) visible(rt runtimeISA, codigo, opcionID string) bool {
	porOpcion, _ := c.el(rt, codigo)["estado_regla_por_opcion"].(map[string]any)
	estado, _ := porOpcion[opcionID].(map[string]any)
	return estado == nil || estado["visible"] != false
}

func (c *casoISA) cotizacionPrincipal() string { return c.informe.CotizacionID }

// guardarOpcion guarda algunos campos de la opción i y actualiza el espejo.
func (c *casoISA) guardarOpcion(i int, cambios map[string]string) {
	c.t.Helper()
	c.ok("POST", "/api/cotizador/runtime/"+c.cotizacionPrincipal()+"/valores", c.cli.CuerpoValoresOpcion(c.opciones[i], cambios))
	for k, v := range cambios {
		c.valores[i][k] = v
	}
}

func igual(t *testing.T, que string, obtenido, esperado float64) {
	t.Helper()
	if math.Abs(obtenido-esperado) > 0.001 {
		t.Errorf("%s = %.4f, se esperaba %.2f", que, obtenido, esperado)
	}
}

// compararOpcion afirma, para la opción i, cada importe calculado contra el
// esperado derivado del espejo local.
func (c *casoISA) compararOpcion(t *testing.T, rt runtimeISA, i int, codigos ...string) map[string]float64 {
	t.Helper()
	esperado := c.esperadoISA(c.valores[i])
	if len(codigos) == 0 {
		for k := range esperado {
			codigos = append(codigos, k)
		}
		sort.Strings(codigos)
	}
	for _, k := range codigos {
		igual(t, fmt.Sprintf("%s (%s)", k, c.datos.Escenarios[i].Nombre), c.calculado(rt, k, c.opciones[i]), esperado[k])
	}
	return esperado
}

func (c *casoISA) detalleCotizacion(id string) map[string]any {
	c.t.Helper()
	cot, _ := c.ok("GET", "/api/cotizaciones/"+id, nil).Datos["cotizacion"].(map[string]any)
	return cot
}

type ofertaISA struct {
	raw       map[string]any
	secciones map[string][]map[string]any
	bloques   map[string]map[string]any
}

func leerOferta(datos map[string]any) ofertaISA {
	o := ofertaISA{raw: datos, secciones: map[string][]map[string]any{}, bloques: map[string]map[string]any{}}
	plantilla, _ := datos["plantilla"].(map[string]any)
	for _, s := range isacustom.Lista(plantilla["secciones"]) {
		bloques := isacustom.Lista(s["bloques"])
		o.secciones[fmt.Sprint(s["titulo"])] = bloques
		for _, b := range bloques {
			o.bloques[fmt.Sprint(s["titulo"])+"/"+fmt.Sprint(b["titulo"])] = b
		}
	}
	return o
}

func (c *casoISA) vistaPrevia(id string) ofertaISA {
	c.t.Helper()
	return leerOferta(c.ok("GET", "/api/cotizaciones/"+id+"/vista-previa-oferta", nil).Datos)
}

// --- Casos ------------------------------------------------------------------------

// CP-01: secciones y campos aparecen en orden y con la distribución
// configurada; el caso queda compilado y el sembrador no reporta incidencias.
func (c *casoISA) cp01(t *testing.T) {
	c.t = t
	for _, i := range c.informe.Incidencias {
		t.Errorf("incidencia del sembrador [%s] HTTP %d: %s", i.Paso, i.EstadoHTTP, i.Detalle)
	}
	if !c.informe.Completo || !c.informe.Compilado || c.informe.CotizacionID == "" || c.informe.PlantillaID == "" {
		t.Fatalf("el sembrador no dejó el caso completo: %+v", c.informe)
	}
	if len(c.opciones) != len(c.datos.Escenarios) {
		t.Fatalf("se esperaban %d opciones, hay %d", len(c.datos.Escenarios), len(c.opciones))
	}
	rt := c.runtime(c.cotizacionPrincipal())
	tabs := isacustom.Lista(rt.datos["estructura"].(map[string]any)["tabs"])
	nombres := []string{}
	for _, tab := range tabs {
		nombres = append(nombres, fmt.Sprint(tab["nombre"]))
	}
	if !reflect.DeepEqual(nombres, c.datos.Secciones) {
		t.Fatalf("orden de secciones = %v, se esperaba %v", nombres, c.datos.Secciones)
	}
	for _, tab := range tabs {
		seccion := fmt.Sprint(tab["nombre"])
		grid := c.el(rt, seccion+"-GRID")
		columnas := 2.0
		if seccion == "03_CHAT" || seccion == "05_IMPL" {
			columnas = 3
		}
		if grid["configuracion"].(map[string]any)["columnas"] != columnas {
			t.Errorf("%s: el contenedor debe tener %v columnas: %v", seccion, columnas, grid["configuracion"])
		}
		esperados := []string{}
		for _, campo := range c.datos.Entradas() {
			if campo.Seccion == seccion {
				esperados = append(esperados, c.cli.ID(campo.Codigo))
			}
		}
		obtenidos := []string{}
		for _, h := range isacustom.Lista(grid["hijos"]) {
			obtenidos = append(obtenidos, fmt.Sprint(h["elemento_id"]))
		}
		if !reflect.DeepEqual(obtenidos, esperados) {
			t.Errorf("%s: campos = %v, se esperaba %v", seccion, obtenidos, esperados)
		}
	}
	if rt.datos["opciones_cotizacion_id"] != c.cli.ID("ESCENARIOS") {
		t.Errorf("el runtime debe declarar el componente de alcance COTIZACION: %v", rt.datos["opciones_cotizacion_id"])
	}
}

// CP-02: el catálogo muestra la etiqueta, conserva el código y la fórmula
// recibe valor_calculo (1000, no "CONV_1000" ni "1.000").
func (c *casoISA) cp02(t *testing.T) {
	c.t = t
	rt := c.runtime(c.cotizacionPrincipal())
	conv := c.el(rt, "CONVERSACIONES_MES")
	opciones := isacustom.Lista(conv["opciones"])
	if len(opciones) != 6 {
		t.Errorf("el catálogo debe cargarse completo (6 valores), llegaron %d", len(opciones))
	}
	etiqueta := ""
	for _, op := range opciones {
		if op["valor_sistema"] == "CONV_1000" {
			etiqueta = fmt.Sprint(op["texto_visible"])
		}
	}
	if etiqueta != "1.000" {
		t.Errorf("CONV_1000 debe mostrarse como \"1.000\", se muestra %q", etiqueta)
	}
	if g := c.guardado(rt, "CONVERSACIONES_MES", c.opciones[0]); g != "CONV_1000" {
		t.Errorf("se debe conservar el código CONV_1000, se guardó %v", g)
	}
	// Opción 1 del documento: los cuatro importes base.
	igual(t, "COSTO_CHAT", c.calculado(rt, "COSTO_CHAT", c.opciones[0]), 100.00)
	igual(t, "COSTO_VOZ", c.calculado(rt, "COSTO_VOZ", c.opciones[0]), 80.00)
	igual(t, "PRECIO_CHAT", c.calculado(rt, "PRECIO_CHAT", c.opciones[0]), 142.86)
	igual(t, "PRECIO_VOZ", c.calculado(rt, "PRECIO_VOZ", c.opciones[0]), 114.29)
	c.compararOpcion(t, rt, 0)
}

// CP-03: Telefonía Sí→No en la opción 1. Los campos de voz se ocultan SOLO en
// esa opción y sus importes dejan de sumar. Primero se carga un consumo extra
// de voz real para comprobar que un valor oculto no sigue sumándose (§6.1).
func (c *casoISA) cp03(t *testing.T) {
	c.t = t
	c.guardarOpcion(0, map[string]string{"MINUTOS_EXTRA": "100"})
	antes := c.runtime(c.cotizacionPrincipal())
	esperadoAntes := c.compararOpcion(t, antes, 0)
	if esperadoAntes["CONSUMO_EXTRA_VOZ"] == 0 {
		t.Fatal("el caso necesita un consumo extra de voz > 0 antes del cambio")
	}
	precioVozAntes := c.calculado(antes, "PRECIO_VOZ", c.opciones[0])
	extraVozAntes := c.calculado(antes, "CONSUMO_EXTRA_VOZ", c.opciones[0])
	canalesAntes := c.calculado(antes, "PRECIO_CANALES", c.opciones[0])
	totalAntes := c.calculado(antes, "TOTAL_MENSUAL", c.opciones[0])

	c.guardarOpcion(0, map[string]string{"USA_TELEFONIA": "NO"})
	c.valores[0]["MINUTOS_EXTRA"] = "0" // R01 lo oculta: se persiste en 0.
	rt := c.runtime(c.cotizacionPrincipal())
	for _, k := range []string{"COSTO_VOZ", "PRECIO_VOZ", "CONSUMO_EXTRA_VOZ"} {
		igual(t, k+" con Telefonía=No", c.calculado(rt, k, c.opciones[0]), 0)
	}
	for _, k := range []string{"MINUTOS_MES", "MINUTOS_EXTRA"} {
		if c.visible(rt, k, c.opciones[0]) {
			t.Errorf("%s debe quedar oculto en la opción 1", k)
		}
		if !c.visible(rt, k, c.opciones[2]) {
			t.Errorf("%s NO debe ocultarse en Multicanal (la regla es por opción)", k)
		}
	}
	if g := c.guardado(rt, "MINUTOS_EXTRA", c.opciones[0]); g != "0" {
		t.Errorf("MINUTOS_EXTRA oculto debe persistirse en \"0\", quedó %v", g)
	}
	// TOTAL_MENSUAL baja exactamente en lo que aportaba la voz. Según las
	// fórmulas del documento eso es PRECIO_VOZ + CONSUMO_EXTRA_VOZ más el
	// cargo del canal de telefonía, que PRECIO_CANALES también condiciona a
	// USA_TELEFONIA (SI(USA_TELEFONIA; CARGO_TELEFONIA; 0)).
	cargoTelefonia, _ := strconv.ParseFloat(c.datos.Parametros["CARGO_TELEFONIA"], 64)
	igual(t, "caída de PRECIO_CANALES", canalesAntes-c.calculado(rt, "PRECIO_CANALES", c.opciones[0]), cargoTelefonia)
	igual(t, "TOTAL_MENSUAL", c.calculado(rt, "TOTAL_MENSUAL", c.opciones[0]), r2(totalAntes-precioVozAntes-extraVozAntes-cargoTelefonia))
	c.compararOpcion(t, rt, 0)
	// La otra opción con telefonía no se entera del cambio.
	c.compararOpcion(t, rt, 2, "COSTO_VOZ", "PRECIO_VOZ", "TOTAL_MENSUAL")
}

// CP-04: Telefonía No→Sí: los campos reaparecen y el cálculo vuelve a
// ejecutarse con valores válidos (el MINUTOS_EXTRA oculto quedó en 0).
func (c *casoISA) cp04(t *testing.T) {
	c.t = t
	c.guardarOpcion(0, map[string]string{"USA_TELEFONIA": "SI"})
	rt := c.runtime(c.cotizacionPrincipal())
	for _, k := range []string{"MINUTOS_MES", "MINUTOS_EXTRA"} {
		if !c.visible(rt, k, c.opciones[0]) {
			t.Errorf("%s debe reaparecer", k)
		}
	}
	igual(t, "COSTO_VOZ", c.calculado(rt, "COSTO_VOZ", c.opciones[0]), 80.00)
	igual(t, "PRECIO_VOZ", c.calculado(rt, "PRECIO_VOZ", c.opciones[0]), 114.29)
	igual(t, "CONSUMO_EXTRA_VOZ", c.calculado(rt, "CONSUMO_EXTRA_VOZ", c.opciones[0]), 0)
	c.compararOpcion(t, rt, 0)
}

// CP-05: Integración No: la cantidad se oculta y el cargo queda en cero.
func (c *casoISA) cp05(t *testing.T) {
	c.t = t
	totalAntes := c.calculado(c.runtime(c.cotizacionPrincipal()), "TOTAL_MENSUAL", c.opciones[0])
	c.guardarOpcion(0, map[string]string{"INTEGRACION_API": "NO"})
	c.valores[0]["CANTIDAD_INTEGRACIONES"] = "0"
	rt := c.runtime(c.cotizacionPrincipal())
	if c.visible(rt, "CANTIDAD_INTEGRACIONES", c.opciones[0]) {
		t.Error("CANTIDAD_INTEGRACIONES debe ocultarse")
	}
	if g := c.guardado(rt, "CANTIDAD_INTEGRACIONES", c.opciones[0]); g != "0" {
		t.Errorf("CANTIDAD_INTEGRACIONES oculta debe persistirse en \"0\", quedó %v", g)
	}
	igual(t, "PRECIO_INTEGRACIONES", c.calculado(rt, "PRECIO_INTEGRACIONES", c.opciones[0]), 0)
	unit, _ := strconv.ParseFloat(c.datos.Parametros["PRECIO_INTEGRACION_UNIT"], 64)
	igual(t, "TOTAL_MENSUAL", c.calculado(rt, "TOTAL_MENSUAL", c.opciones[0]), r2(totalAntes-unit))
	c.compararOpcion(t, rt, 0)

	c.guardarOpcion(0, map[string]string{"INTEGRACION_API": "SI", "CANTIDAD_INTEGRACIONES": "1"})
	c.compararOpcion(t, c.runtime(c.cotizacionPrincipal()), 0, "PRECIO_INTEGRACIONES", "PRECIO_IMPLEMENTACION", "TOTAL_MENSUAL")
}

// CP-06: el precio se recalcula con 0.20/0.30, no con 20/30.
func (c *casoISA) cp06(t *testing.T) {
	c.t = t
	c.guardarOpcion(0, map[string]string{"MARGEN_CHAT": "M20"})
	rt := c.runtime(c.cotizacionPrincipal())
	igual(t, "PRECIO_CHAT con M20", c.calculado(rt, "PRECIO_CHAT", c.opciones[0]), 125.00)
	c.compararOpcion(t, rt, 0)
	c.guardarOpcion(0, map[string]string{"MARGEN_CHAT": "M30"})
	rt = c.runtime(c.cotizacionPrincipal())
	igual(t, "PRECIO_CHAT con M30", c.calculado(rt, "PRECIO_CHAT", c.opciones[0]), 142.86)
	c.compararOpcion(t, rt, 0)
}

// CP-07: valores, reglas, escenario recomendado y cálculos se conservan al
// reabrir; las salidas salen de la opción recomendada (AT-06) y el snapshot
// guarda los valores de TODAS las opciones.
func (c *casoISA) cp07(t *testing.T) {
	c.t = t
	rt := c.runtime(c.cotizacionPrincipal()) // reabrir: GET nuevo, sin estado del cliente
	for i := range c.opciones {
		for _, campo := range c.datos.Entradas() {
			if g := fmt.Sprint(c.guardado(rt, campo.Codigo, c.opciones[i])); g != c.valores[i][campo.Codigo] {
				t.Errorf("%s/%s reabierto = %q, se guardó %q", c.datos.Escenarios[i].Nombre, campo.Codigo, g, c.valores[i][campo.Codigo])
			}
		}
		c.compararOpcion(t, rt, i)
	}
	if c.visible(rt, "MINUTOS_MES", c.opciones[1]) {
		t.Error("la regla R01 de Solo Chat (Telefonía=No) debe seguir aplicada al reabrir")
	}
	padre := c.el(rt, "ESCENARIOS")
	for i, op := range isacustom.Lista(padre["opciones"]) {
		if (op["es_recomendada"] == true) != c.datos.Escenarios[i].Recomendada {
			t.Errorf("recomendada de %v no se conservó", op["nombre"])
		}
	}
	esperado := c.esperadoISA(c.valores[0])
	cot := c.detalleCotizacion(c.cotizacionPrincipal())
	igual(t, "total_precio (salida desde la recomendada)", cot["total_precio"].(float64), esperado["TOTAL_MENSUAL"])
	igual(t, "total_costo", cot["total_costo"].(float64), esperado["TOTAL_COSTO"])
	igual(t, "margen_total", cot["margen_total"].(float64), esperado["MARGEN_TOTAL"])
	if cot["moneda"] != "USD" {
		t.Errorf("moneda = %v", cot["moneda"])
	}

	// AT-06 en modo COTIZACION: cambiar la recomendada cambia las salidas.
	c.ok("POST", "/api/cotizador/runtime/"+c.cotizacionPrincipal()+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "RECOMENDAR", "opcion_id": c.opciones[2], "es_recomendada": true})
	igual(t, "total_precio con Multicanal recomendada", c.detalleCotizacion(c.cotizacionPrincipal())["total_precio"].(float64), c.esperadoISA(c.valores[2])["TOTAL_MENSUAL"])
	c.ok("POST", "/api/cotizador/runtime/"+c.cotizacionPrincipal()+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "RECOMENDAR", "opcion_id": c.opciones[0], "es_recomendada": true})
	igual(t, "total_precio restaurado", c.detalleCotizacion(c.cotizacionPrincipal())["total_precio"].(float64), esperado["TOTAL_MENSUAL"])

	var raw []byte
	if err := c.pool.QueryRow(c.ctx, `SELECT snapshot_json FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, c.cotizacionPrincipal()).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Valores map[string]any `json:"valores"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	porOpcion, _ := snapshot.Valores[c.cli.ID("CONVERSACIONES_MES")].(map[string]any)
	for i, op := range c.opciones {
		if porOpcion[op] != c.valores[i]["CONVERSACIONES_MES"] {
			t.Errorf("snapshot_json sin el valor de %s: %v", c.datos.Escenarios[i].Nombre, porOpcion)
		}
	}
}

// CP-08: duplicar copia TODOS los valores; después son independientes.
func (c *casoISA) cp08(t *testing.T) {
	c.t = t
	res := c.ok("POST", "/api/cotizador/runtime/"+c.cotizacionPrincipal()+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "DUPLICAR", "opcion_id": c.opciones[1], "nombre": "Solo Chat copia"})
	copia := ""
	for _, op := range isacustom.Lista(res.Datos["opciones"]) {
		id := fmt.Sprint(op["opcion_id"])
		if id != c.opciones[0] && id != c.opciones[1] && id != c.opciones[2] {
			copia = id
		}
	}
	if copia == "" {
		t.Fatalf("DUPLICAR no creó una opción nueva: %v", res.Datos)
	}
	defer c.ok("POST", "/api/cotizador/runtime/"+c.cotizacionPrincipal()+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "ELIMINAR", "opcion_id": copia})

	rt := c.runtime(c.cotizacionPrincipal())
	for _, campo := range c.datos.Entradas() {
		if o, d := c.guardado(rt, campo.Codigo, c.opciones[1]), c.guardado(rt, campo.Codigo, copia); o != d || d == nil {
			t.Errorf("la copia no trae %s: original %v, copia %v", campo.Codigo, o, d)
		}
	}
	igual(t, "COSTO_CHAT de la copia", c.calculado(rt, "COSTO_CHAT", copia), 250.00)
	igual(t, "PRECIO_CHAT de la copia", c.calculado(rt, "PRECIO_CHAT", copia), 357.14)
	igual(t, "PRECIO_VOZ de la copia", c.calculado(rt, "PRECIO_VOZ", copia), 0)

	c.ok("POST", "/api/cotizador/runtime/"+c.cotizacionPrincipal()+"/valores", c.cli.CuerpoValoresOpcion(copia, map[string]string{"CONVERSACIONES_MES": "CONV_300"}))
	rt = c.runtime(c.cotizacionPrincipal())
	costoConv, _ := strconv.ParseFloat(c.valores[1]["COSTO_CONVERSACION"], 64)
	igual(t, "COSTO_CHAT de la copia editada", c.calculado(rt, "COSTO_CHAT", copia), r2(c.catalogoCalculo("CAT_CONVERSACIONES", "CONV_300")*costoConv))
	igual(t, "COSTO_CHAT de Solo Chat (original intacto)", c.calculado(rt, "COSTO_CHAT", c.opciones[1]), 250.00)
	if g := c.guardado(rt, "CONVERSACIONES_MES", c.opciones[1]); g != "CONV_2500" {
		t.Errorf("editar la copia alteró el original: %v", g)
	}
}

// CP-09: la tabla de la plantilla genera tres filas, Concepto con el nombre
// real y cada fila con los totales de SU opción.
func (c *casoISA) cp09(t *testing.T) {
	c.t = t
	oferta := c.vistaPrevia(c.cotizacionPrincipal())
	tabla := oferta.bloques["6. Opciones/Opciones comerciales"]
	if tabla == nil {
		t.Fatalf("no está la tabla de escenarios: %v", oferta.secciones["6. Opciones"])
	}
	if cols := fmt.Sprint(tabla["columnas"]); cols != "[Concepto Implementación Mensualidad Recomendada]" {
		t.Errorf("columnas = %s", cols)
	}
	filas, _ := tabla["filas"].([]any)
	if len(filas) != 3 {
		t.Fatalf("la tabla debe tener 3 filas, tiene %d: %v", len(filas), filas)
	}
	for i, filaRaw := range filas {
		fila := filaRaw.([]any)
		esperado := c.esperadoISA(c.valores[i])
		if fila[0] != c.datos.Escenarios[i].Nombre {
			t.Errorf("fila %d: Concepto = %v, se esperaba %q", i+1, fila[0], c.datos.Escenarios[i].Nombre)
		}
		igual(t, fmt.Sprintf("fila %d Implementación", i+1), fila[1].(float64), esperado["TOTAL_INICIAL"])
		igual(t, fmt.Sprintf("fila %d Mensualidad", i+1), fila[2].(float64), esperado["TOTAL_MENSUAL"])
		if fila[3] != c.datos.Escenarios[i].Recomendada {
			t.Errorf("fila %d: Recomendada = %v", i+1, fila[3])
		}
	}
	// Los tres totales son distintos entre sí: nunca el mismo valor repetido.
	if filas[0].([]any)[2] == filas[1].([]any)[2] || filas[1].([]any)[2] == filas[2].([]any)[2] {
		t.Errorf("las filas repiten la mensualidad: %v", filas)
	}
}

// CP-10: sin recomendada, el sistema alerta antes de guardar la oferta.
func (c *casoISA) cp10(t *testing.T) {
	c.t = t
	cot := c.cli.NuevaCotizacion(c.ctx, c.informe.ClienteID)
	if cot == "" {
		t.Fatalf("no se pudo crear la cotización de CP-10: %v", c.cli.Incidencias)
	}
	rt := c.runtime(cot)
	opciones := isacustom.Lista(c.el(rt, "ESCENARIOS")["opciones"])
	if len(opciones) != 3 {
		t.Fatalf("se esperaban 3 opciones iniciales, hay %d", len(opciones))
	}
	for _, op := range opciones {
		if op["es_recomendada"] == true {
			t.Fatal("una cotización nueva con 3 opciones no debe tener recomendada")
		}
	}
	primera := fmt.Sprint(opciones[0]["opcion_id"])
	r := c.llamar("POST", "/api/cotizador/runtime/"+cot+"/valores", c.cli.CuerpoEscenario(c.datos, 0, primera))
	if r.Estado != http.StatusBadRequest || !strings.Contains(r.Error(), "recomendada") {
		t.Fatalf("sin recomendada el guardado debe rechazarse pidiendo marcar una: HTTP %d %s", r.Estado, r.Error())
	}
	res := c.ok("POST", "/api/cotizador/runtime/"+cot+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "RENOMBRAR", "opcion_id": primera, "nombre": "Principal"})
	if !strings.Contains(fmt.Sprint(res.Datos["advertencia"]), "ninguna está marcada como recomendada") {
		t.Errorf("administrar opciones debe advertir la falta de recomendada: %v", res.Datos)
	}
	c.ok("POST", "/api/cotizador/runtime/"+cot+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "RECOMENDAR", "opcion_id": primera, "es_recomendada": true})
	c.ok("POST", "/api/cotizador/runtime/"+cot+"/valores", c.cli.CuerpoEscenario(c.datos, 0, primera))
	igual(t, "total_precio tras marcar recomendada", c.detalleCotizacion(cot)["total_precio"].(float64), c.esperadoISA(c.datos.ValoresEscenario(0))["TOTAL_MENSUAL"])
}

// CP-11: con una sola opción, se trata como recomendada por defecto (R09).
func (c *casoISA) cp11(t *testing.T) {
	c.t = t
	cot := c.cli.NuevaCotizacion(c.ctx, c.informe.ClienteID)
	opciones := isacustom.Lista(c.el(c.runtime(cot), "ESCENARIOS")["opciones"])
	if len(opciones) != 3 {
		t.Fatalf("se esperaban 3 opciones iniciales, hay %d", len(opciones))
	}
	var res isacustom.Respuesta
	for _, op := range opciones[1:] {
		res = c.ok("POST", "/api/cotizador/runtime/"+cot+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "ELIMINAR", "opcion_id": op["opcion_id"]})
	}
	quedan := isacustom.Lista(res.Datos["opciones"])
	if len(quedan) != 1 || quedan[0]["es_recomendada"] != true {
		t.Fatalf("la única opción debe quedar recomendada sola: %v", quedan)
	}
	unica := fmt.Sprint(quedan[0]["opcion_id"])
	c.ok("POST", "/api/cotizador/runtime/"+cot+"/valores", c.cli.CuerpoEscenario(c.datos, 1, unica))
	igual(t, "total_precio de la única opción", c.detalleCotizacion(cot)["total_precio"].(float64), c.esperadoISA(c.datos.ValoresEscenario(1))["TOTAL_MENSUAL"])
	r := c.llamar("POST", "/api/cotizador/runtime/"+cot+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.cli.ID("ESCENARIOS"), "accion": "ELIMINAR", "opcion_id": unica})
	if r.Estado != http.StatusConflict {
		t.Errorf("no se debe poder eliminar la única opción: HTTP %d", r.Estado)
	}
}

// CP-12: la vista previa muestra datos reales de catálogos y tablas, sin
// placeholders ni códigos internos.
func (c *casoISA) cp12(t *testing.T) {
	c.t = t
	oferta := c.vistaPrevia(c.cotizacionPrincipal())
	for seccion, bloques := range oferta.secciones {
		for _, b := range bloques {
			if contenido := fmt.Sprint(b["contenido"]); strings.Contains(contenido, "[") {
				t.Errorf("%s/%v deja un placeholder sin resolver: %s", seccion, b["titulo"], contenido)
			}
		}
	}
	v := c.valores[0]
	for bloque, esperado := range map[string]string{
		"2. Configuración/TIPO_AGENTE":      c.catalogoEtiqueta("CAT_TIPO_AGENTE", v["TIPO_AGENTE"]),
		"2. Configuración/IDIOMA":           c.catalogoEtiqueta("CAT_IDIOMA", v["IDIOMA"]),
		"2. Configuración/CANTIDAD_AGENTES": v["CANTIDAD_AGENTES"],
		"4. Capacidad/CONVERSACIONES_MES":   c.catalogoEtiqueta("CAT_CONVERSACIONES", v["CONVERSACIONES_MES"]),
		"4. Capacidad/MINUTOS_MES":          c.catalogoEtiqueta("CAT_MINUTOS_VOZ", v["MINUTOS_MES"]),
	} {
		if b := oferta.bloques[bloque]; b == nil || fmt.Sprint(b["valor"]) != esperado {
			t.Errorf("%s = %v, se esperaba %q", bloque, b, esperado)
		}
	}
	solucion := fmt.Sprint(oferta.bloques["1. Solución propuesta/Solución propuesta"]["contenido"])
	for _, fragmento := range []string{"implementar " + v["CANTIDAD_AGENTES"] + " agente", "orientado(s) a " + c.catalogoEtiqueta("CAT_TIPO_AGENTE", v["TIPO_AGENTE"]), v["NOMBRE_SOLUCION"]} {
		if !strings.Contains(solucion, fragmento) {
			t.Errorf("el texto de la solución no contiene %q: %s", fragmento, solucion)
		}
	}
	if len(oferta.bloques["6. Opciones/Opciones comerciales"]["filas"].([]any)) != 3 {
		t.Error("la tabla de escenarios de la vista previa debe tener datos reales")
	}
}

// CP-13: el documento (enlace público) presenta cabecera, configuración,
// opciones, totales y condicionales, con la MISMA resolución que la vista
// previa (CTZ-TEC-004).
func (c *casoISA) cp13(t *testing.T) {
	c.t = t
	enlace := c.ok("POST", "/api/cotizaciones/"+c.cotizacionPrincipal()+"/enlace", map[string]any{})
	token := fmt.Sprint(enlace.Datos["token"])
	publico := c.llamar("GET", "/api/publico/cotizacion/"+url.PathEscape(token), nil)
	if !publico.OK() {
		t.Fatalf("enlace público: HTTP %d %s", publico.Estado, publico.Error())
	}
	oferta := leerOferta(publico.Datos)
	previa := c.vistaPrevia(c.cotizacionPrincipal())
	if !reflect.DeepEqual(oferta.raw["plantilla"], previa.raw["plantilla"]) {
		t.Error("la oferta pública y la vista previa no resuelven la misma plantilla (CTZ-TEC-004)")
	}
	cot := c.detalleCotizacion(c.cotizacionPrincipal())
	for bloque, esperado := range map[string]any{
		"Portada/Propuesta preparada para": cot["empresa"],
		"Portada/Oferta":                   cot["codigo_oferta"],
	} {
		if b := oferta.bloques[bloque]; b == nil || b["valor"] != esperado || esperado == nil || esperado == "" {
			t.Errorf("cabecera %s = %v, se esperaba %v", bloque, b, esperado)
		}
	}
	if b := oferta.bloques["Portada/Fecha"]; b == nil || !strings.HasPrefix(fmt.Sprint(b["valor"]), "20") {
		t.Errorf("cabecera sin fecha: %v", b)
	}
	for _, seccion := range []string{"Portada", "1. Solución propuesta", "2. Configuración", "3. Canales", "4. Capacidad", "5. Integraciones", "6. Opciones", "7. Inversión", "8. Alcance", "9. Próximos pasos"} {
		if _, existe := oferta.secciones[seccion]; !existe {
			t.Errorf("falta la sección %s", seccion)
		}
	}
	// §9.5: inversión de la opción recomendada.
	esperado := c.esperadoISA(c.valores[0])
	for bloque, clave := range map[string]string{"7. Inversión/PRECIO_IMPLEMENTACION": "PRECIO_IMPLEMENTACION", "7. Inversión/TOTAL_MENSUAL": "TOTAL_MENSUAL", "7. Inversión/TOTAL_PRIMER_MES": "TOTAL_PRIMER_MES"} {
		b := oferta.bloques[bloque]
		if b == nil {
			t.Errorf("falta %s", bloque)
			continue
		}
		igual(t, bloque, b["valor"].(float64), esperado[clave])
	}
	// Condicionales: canales según la opción recomendada (Teams=No).
	for canal, codigo := range map[string]string{"WhatsApp": "USA_WHATSAPP", "Chat Web": "USA_CHATWEB", "Microsoft Teams": "USA_TEAMS", "Telefonía IA": "USA_TELEFONIA"} {
		_, visible := oferta.bloques["3. Canales/"+canal]
		if visible != (c.valores[0][codigo] == "SI") {
			t.Errorf("bloque %s visible=%t con %s=%s", canal, visible, codigo, c.valores[0][codigo])
		}
	}
	if _, visible := oferta.bloques["5. Integraciones/Integraciones"]; !visible {
		t.Error("con INTEGRACION_API=SI el bloque de integraciones debe mostrarse")
	}
	if len(oferta.bloques["6. Opciones/Opciones comerciales"]["filas"].([]any)) != 3 {
		t.Error("la oferta debe llevar la tabla de opciones")
	}
}

// CP-14: cambiar la etiqueta de un valor de catálogo no rompe la fórmula;
// cambiar su valor_calculo sí recalcula.
func (c *casoISA) cp14(t *testing.T) {
	c.t = t
	const catalogo, codigo = "CAT_CONVERSACIONES", "CONV_1000"
	orden := 2
	calculo := c.catalogoCalculo(catalogo, codigo)
	etiqueta := c.catalogoEtiqueta(catalogo, codigo)
	restaurar := func() {
		c.ok("POST", "/api/catalogos/valores", c.cli.ValorCatalogo(catalogo, codigo, etiqueta, calculo, orden))
		c.guardarOpcion(0, map[string]string{"CONVERSACIONES_MES": codigo})
	}
	defer restaurar()

	c.ok("POST", "/api/catalogos/valores", c.cli.ValorCatalogo(catalogo, codigo, "1.000 conversaciones", calculo, orden))
	c.guardarOpcion(0, map[string]string{"CONVERSACIONES_MES": codigo})
	rt := c.runtime(c.cotizacionPrincipal())
	igual(t, "COSTO_CHAT tras cambiar solo la etiqueta", c.calculado(rt, "COSTO_CHAT", c.opciones[0]), 100.00)
	if b := c.vistaPrevia(c.cotizacionPrincipal()).bloques["4. Capacidad/CONVERSACIONES_MES"]; b == nil || b["valor"] != "1.000 conversaciones" {
		t.Errorf("la oferta debe mostrar la etiqueta nueva: %v", b)
	}

	nuevoCalculo := 1500.0
	c.ok("POST", "/api/catalogos/valores", c.cli.ValorCatalogo(catalogo, codigo, "1.000 conversaciones", nuevoCalculo, orden))
	c.guardarOpcion(0, map[string]string{"CONVERSACIONES_MES": codigo})
	rt = c.runtime(c.cotizacionPrincipal())
	costoConv, _ := strconv.ParseFloat(c.valores[0]["COSTO_CONVERSACION"], 64)
	margen := c.catalogoCalculo("CAT_MARGEN", c.valores[0]["MARGEN_CHAT"])
	costo := r2(nuevoCalculo * costoConv)
	igual(t, "COSTO_CHAT con valor_calculo 1500", c.calculado(rt, "COSTO_CHAT", c.opciones[0]), costo)
	igual(t, "PRECIO_CHAT con valor_calculo 1500", c.calculado(rt, "PRECIO_CHAT", c.opciones[0]), r2(costo/(1-margen)))

	restaurar()
	igual(t, "COSTO_CHAT restaurado", c.calculado(c.runtime(c.cotizacionPrincipal()), "COSTO_CHAT", c.opciones[0]), 100.00)
}

// CP-15: 1.000/2.500 y los montos se presentan de forma consistente. Lo
// que el API controla: las cantidades de catálogo viajan como su etiqueta
// con separador de miles (nunca como 1000 ni como el código), los montos
// salen redondeados a 2 decimales y la moneda es la misma en cotización,
// salidas y oferta. El formato visual final (Intl.NumberFormat es-CR en
// formatoMoneda del frontend) no lo cubre esta prueba.
func (c *casoISA) cp15(t *testing.T) {
	c.t = t
	rt := c.runtime(c.cotizacionPrincipal())
	etiquetas := map[string]string{}
	for _, op := range isacustom.Lista(c.el(rt, "CONVERSACIONES_MES")["opciones"]) {
		etiquetas[fmt.Sprint(op["valor_sistema"])] = fmt.Sprint(op["texto_visible"])
	}
	if etiquetas["CONV_1000"] != "1.000" || etiquetas["CONV_2500"] != "2.500" {
		t.Errorf("etiquetas de miles inconsistentes: %v", etiquetas)
	}
	oferta := c.vistaPrevia(c.cotizacionPrincipal())
	if b := oferta.bloques["4. Capacidad/CONVERSACIONES_MES"]; b == nil || b["valor"] != "1.000" {
		t.Errorf("la oferta debe mostrar 1.000: %v", b)
	}
	montos := []float64{}
	for _, filaRaw := range oferta.bloques["6. Opciones/Opciones comerciales"]["filas"].([]any) {
		fila := filaRaw.([]any)
		montos = append(montos, fila[1].(float64), fila[2].(float64))
	}
	for _, b := range oferta.secciones["7. Inversión"] {
		montos = append(montos, b["valor"].(float64))
	}
	for _, m := range montos {
		if math.Abs(m*100-math.Round(m*100)) > 1e-6 {
			t.Errorf("monto con más de 2 decimales: %v", m)
		}
	}
	if moneda := oferta.raw["moneda"]; moneda != "USD" || c.detalleCotizacion(c.cotizacionPrincipal())["moneda"] != "USD" {
		t.Errorf("moneda inconsistente: oferta %v", moneda)
	}
	var monedaSalida string
	if err := c.pool.QueryRow(c.ctx, `SELECT valor_texto FROM cotizacion_salidas WHERE cotizacion_id=$1 AND numero_version=1 AND clave_salida='MONEDA'`, c.cotizacionPrincipal()).Scan(&monedaSalida); err != nil || monedaSalida != "USD" {
		t.Errorf("salida MONEDA = %q (%v)", monedaSalida, err)
	}
}

// limpiarCasoISA borra lo que sembró la prueba (todo lleva el prefijo único
// del cliente). Es de mejor esfuerzo, como el resto de los t.Cleanup del
// paquete: un error acá no invalida los resultados.
func limpiarCasoISA(pool *pgxpool.Pool, cli *isacustom.Cliente) {
	ctx := context.Background()
	calc := cli.ID("CALC")
	prefijo := cli.Prefijo + "-%"
	for _, sql := range []string{
		`DELETE FROM cotizaciones WHERE calculadora_id=$1`,
		`DELETE FROM plantillas WHERE plantilla_id IN (SELECT plantilla_id FROM plantilla_calculadoras WHERE calculadora_id=$1)`,
		`DELETE FROM reglas_cotizador WHERE calculadora_id=$1`,
		`DELETE FROM mapa_salidas_cotizador WHERE calculadora_id=$1`,
		`DELETE FROM cotizadores_compilados WHERE calculadora_id=$1`,
		`DELETE FROM elementos_tab_cotizador WHERE tab_id IN (SELECT tab_id FROM tabs_cotizador WHERE calculadora_id=$1)`,
		`DELETE FROM tabs_cotizador WHERE calculadora_id=$1`,
		`DELETE FROM calculadoras WHERE calculadora_id=$1`,
	} {
		pool.Exec(ctx, sql, calc)
	}
	pool.Exec(ctx, `DELETE FROM catalogo_valores WHERE catalogo_id LIKE $1`, prefijo)
	pool.Exec(ctx, `DELETE FROM catalogos WHERE catalogo_id LIKE $1`, prefijo)
	pool.Exec(ctx, `DELETE FROM clientes WHERE nombre_comercial=$1`, "Cliente de prueba ISA Custom "+cli.Prefijo)
}
