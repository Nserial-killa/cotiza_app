package handlers

// Ronda F2, Parte 1: las referencias de datos tienen alcance de COTIZADOR
// (secciones propias + asociadas); la contención estructural sigue siendo
// local a la sección; nombre_interno es único por cotizador.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// fixtureDosSecciones arma un cotizador con dos secciones propias y
// reutiliza los helpers de Fórmula Avanzada para crear elementos en
// cualquiera de las dos.
func fixtureDosSecciones(t *testing.T) (fixtureFormulaAvanzada, fixtureFormulaAvanzada) {
	t.Helper()
	a := crearFixtureFormulaAvanzada(t)
	b := fixtureFormulaAvanzada{a.handler, a.calculadoraID, "TEST-TAB-AV-B-" + sufijoUnico()}
	rec := postCatalogos(t, a.handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": b.tabID, "calculadora_id": a.calculadoraID, "nombre": "06_COMERCIAL", "orden": 2, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	return a, b
}

func TestAlcanceCotizador_ReferenciasEntreSeccionesYCiclos(t *testing.T) {
	chat, comercial := fixtureDosSecciones(t)
	chat.crear(t, "CAMPO", "CONVERSACIONES", map[string]any{"tipo_campo": "NUMERO"}, nil)
	costoUnit := comercial.crear(t, "CAMPO", "COSTO_UNITARIO", map[string]any{"tipo_campo": "MONEDA"}, nil)

	// Fórmula avanzada en 03_CHAT que lee un campo de 06_COMERCIAL (COSTO_CHAT del PDF).
	costo := chat.crear(t, "CAMPO_CALCULADO", "COSTO", configFormulaPrueba("CONVERSACIONES * COSTO_UNITARIO"), nil)
	// Simple en 06_COMERCIAL con un operando de 03_CHAT.
	total := comercial.crear(t, "CAMPO_CALCULADO", "TOTAL", map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "tipo_resultado": "MONEDA", "operandos": []string{costo, costoUnit}}, nil)
	// Caja de Valor en 03_CHAT cuya fuente vive en 06_COMERCIAL.
	if rec := chat.guardar(t, "TEST-EL-AV-CAJA-"+sufijoUnico(), "CAJA_VALOR", "CAJA_TOTAL", nil, map[string]any{"campo_fuente_id": total}); rec.Code != http.StatusOK {
		t.Fatalf("Caja de Valor con fuente en otra sección: %d %s", rec.Code, rec.Body.String())
	}

	// Un ciclo que cruza secciones se detecta igual que dentro de una.
	rec := chat.guardar(t, costo, "CAMPO_CALCULADO", "COSTO", configFormulaPrueba("TOTAL + 1"), nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "circular") {
		t.Fatalf("ciclo COSTO→TOTAL→COSTO entre secciones: esperaba 400 circular, %d %s", rec.Code, rec.Body.String())
	}
	rec = comercial.guardar(t, total, "CAMPO_CALCULADO", "TOTAL", map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "tipo_resultado": "MONEDA", "operandos": []string{total, costo}}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("autorreferencia: %d %s", rec.Code, rec.Body.String())
	}

	// Otro cotizador sigue fuera de alcance, también para la Caja de Valor.
	otro := crearFixtureFormulaAvanzada(t)
	ajeno := otro.crear(t, "CAMPO", "AJENO", map[string]any{"tipo_campo": "NUMERO"}, nil)
	if rec := chat.guardar(t, "TEST-EL-AV-CAJA-"+sufijoUnico(), "CAJA_VALOR", "CAJA_AJENA", nil, map[string]any{"campo_fuente_id": ajeno}); rec.Code != http.StatusBadRequest {
		t.Fatalf("Caja de Valor con fuente de otro cotizador: %d %s", rec.Code, rec.Body.String())
	}

	res := postCompilador(t, (&CompiladorHandler{DB: chat.handler.DB}).Validar, chat.calculadoraID)
	if !res.Valido {
		t.Fatalf("el cotizador con referencias entre secciones debe ser válido: %+v", res.Errores)
	}
}

func TestAlcanceCotizador_ContencionSigueLocal(t *testing.T) {
	a, b := fixtureDosSecciones(t)
	contenedor := a.crear(t, "CONTENEDOR", "GRID_A", map[string]any{"columnas": 2}, nil)
	rec := b.guardar(t, "TEST-EL-AV-"+sufijoUnico(), "CAMPO", "HIJO", map[string]any{"tipo_campo": "NUMERO"}, map[string]any{"componente_padre_id": contenedor})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "misma sección") {
		t.Fatalf("un componente padre de otra sección debe rechazarse: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAlcanceCotizador_NombreInternoUnicoYDuplicadosHeredados(t *testing.T) {
	a, b := fixtureDosSecciones(t)
	primero := a.crear(t, "CAMPO", "MARGEN", map[string]any{"tipo_campo": "PORCENTAJE"}, nil)
	rec := b.guardar(t, "TEST-EL-AV-"+sufijoUnico(), "CAMPO", "MARGEN", map[string]any{"tipo_campo": "PORCENTAJE"}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), primero) {
		t.Fatalf("nombre_interno repetido en otra sección debe rechazarse nombrando al otro: %d %s", rec.Code, rec.Body.String())
	}
	// Re-guardar el mismo elemento con su propio nombre no choca consigo mismo.
	if rec := a.guardar(t, primero, "CAMPO", "MARGEN", map[string]any{"tipo_campo": "PORCENTAJE"}, nil); rec.Code != http.StatusOK {
		t.Fatalf("re-guardar el mismo elemento: %d %s", rec.Code, rec.Body.String())
	}

	// Datos anteriores a la Ronda F2: el duplicado ya existe entre secciones.
	// La migración no falla; Validar/Publicar lo reporta nombrando a ambos.
	legado := "TEST-EL-AV-LEGADO-" + sufijoUnico()
	if _, err := a.handler.DB.Exec(context.Background(), `
		INSERT INTO elementos_tab_cotizador (elemento_id, tab_id, tipo, etiqueta, configuracion, activo)
		VALUES ($1, $2, 'CAMPO', 'Margen', '{"tipo_campo":"PORCENTAJE","nombre_interno":"MARGEN"}', true)`, legado, b.tabID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.handler.DB.Exec(context.Background(), `DELETE FROM elementos_tab_cotizador WHERE elemento_id=$1`, legado)
	})
	res := postCompilador(t, (&CompiladorHandler{DB: a.handler.DB}).Validar, a.calculadoraID)
	encontrado := false
	for _, e := range res.Errores {
		if strings.Contains(e, "MARGEN") && strings.Contains(e, primero) && strings.Contains(e, legado) {
			encontrado = true
		}
	}
	if res.Valido || !encontrado {
		t.Fatalf("el duplicado heredado debe ser un ERROR legible con ambos elementos: %+v", res.Errores)
	}
	if comp := postCompilador(t, (&CompiladorHandler{DB: a.handler.DB}).Compilar, a.calculadoraID); comp.Compilado {
		t.Fatal("no se debe publicar con un nombre interno duplicado")
	}
}

func TestAlcanceCotizador_SeccionAdicionalConNombreRepetido(t *testing.T) {
	destino := crearFixtureFormulaAvanzada(t)
	destino.crear(t, "CAMPO", "DATO_COMPARTIDO", map[string]any{"tipo_campo": "TEXTO"}, nil)
	selector := destino.crear(t, "SECCIONES_ADICIONALES", "SELECTOR", map[string]any{"presentacion": "CHECKS", "columnas": 2}, nil)

	origen := crearFixtureFormulaAvanzada(t)
	if _, err := origen.handler.DB.Exec(context.Background(), `UPDATE tabs_cotizador SET alcance='REUTILIZABLE' WHERE tab_id=$1`, origen.tabID); err != nil {
		t.Fatal(err)
	}
	origen.crear(t, "CAMPO", "DATO_COMPARTIDO", map[string]any{"tipo_campo": "TEXTO"}, nil)
	t.Cleanup(func() {
		destino.handler.DB.Exec(context.Background(), `DELETE FROM tabs_cotizador_asociaciones WHERE elemento_id=$1`, selector)
	})

	secciones := &SeccionesAdicionalesHandler{DB: destino.handler.DB}
	router := chi.NewRouter()
	router.Post("/api/cotizador/elementos/{elemento_id}/secciones", secciones.Asociar)
	rec := postCatalogos(t, router.ServeHTTP, "/api/cotizador/elementos/"+selector+"/secciones", map[string]any{"tab_ids": []string{origen.tabID}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "DATO_COMPARTIDO") {
		t.Fatalf("asociar una sección que repite un nombre interno debe rechazarse: %d %s", rec.Code, rec.Body.String())
	}
	var asociadas int
	destino.handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM tabs_cotizador_asociaciones WHERE elemento_id=$1`, selector).Scan(&asociadas)
	if asociadas != 0 {
		t.Fatal("el rechazo debe revertir la asociación completa")
	}
}

func TestAlcanceCotizador_OpcionesPropuestaModoCotizacion(t *testing.T) {
	a, b := fixtureDosSecciones(t)
	cfg := func(alcance string) map[string]any {
		return map[string]any{"cantidad_inicial": 2, "alcance_opciones": alcance}
	}
	if rec := a.guardar(t, "TEST-EL-AV-"+sufijoUnico(), "OPCIONES_PROPUESTA", "OP_INVALIDA", cfg("GLOBAL"), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("alcance_opciones inválido: %d %s", rec.Code, rec.Body.String())
	}
	// LOCAL es el default y conserva el comportamiento anterior (con hijos).
	local := a.crear(t, "OPCIONES_PROPUESTA", "OP_LOCAL", map[string]any{"cantidad_inicial": 2}, nil)
	a.crear(t, "CAMPO", "HIJO_LOCAL", map[string]any{"tipo_campo": "NUMERO"}, map[string]any{"componente_padre_id": local})
	var guardado map[string]any
	a.handler.DB.QueryRow(context.Background(), `SELECT configuracion FROM elementos_tab_cotizador WHERE elemento_id=$1`, local).Scan(&guardado)
	if guardado["alcance_opciones"] != "LOCAL" {
		t.Fatalf("el default debe ser LOCAL: %v", guardado)
	}
	// Pasar a COTIZACION con hijos se rechaza.
	if rec := a.guardar(t, local, "OPCIONES_PROPUESTA", "OP_LOCAL", cfg("COTIZACION"), nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("COTIZACION con hijos: %d %s", rec.Code, rec.Body.String())
	}

	global := b.crear(t, "OPCIONES_PROPUESTA", "OP_GLOBAL", cfg("COTIZACION"), nil)
	if rec := b.guardar(t, "TEST-EL-AV-"+sufijoUnico(), "CAMPO", "HIJO_GLOBAL", map[string]any{"tipo_campo": "NUMERO"}, map[string]any{"componente_padre_id": global}); rec.Code != http.StatusBadRequest {
		t.Fatalf("un hijo de COTIZACION debe rechazarse: %d %s", rec.Code, rec.Body.String())
	}
	if rec := a.guardar(t, "TEST-EL-AV-"+sufijoUnico(), "OPCIONES_PROPUESTA", "OP_GLOBAL_2", cfg("COTIZACION"), nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), global) {
		t.Fatalf("solo una COTIZACION por cotizador: %d %s", rec.Code, rec.Body.String())
	}

	// Al publicar también se exige (datos que entraron por fuera del API).
	segundo := "TEST-EL-AV-OP2-" + sufijoUnico()
	if _, err := a.handler.DB.Exec(context.Background(), `
		INSERT INTO elementos_tab_cotizador (elemento_id, tab_id, tipo, etiqueta, configuracion, activo)
		VALUES ($1, $2, 'OPCIONES_PROPUESTA', 'Otra', '{"cantidad_inicial":1,"alcance_opciones":"COTIZACION"}', true)`, segundo, a.tabID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a.handler.DB.Exec(context.Background(), `DELETE FROM elementos_tab_cotizador WHERE elemento_id=$1`, segundo)
	})
	res := postCompilador(t, (&CompiladorHandler{DB: a.handler.DB}).Validar, a.calculadoraID)
	if res.Valido || !strings.Contains(strings.Join(res.Errores, " "), "Solo puede haber una Opciones de Propuesta con alcance COTIZACION") {
		t.Fatalf("Validar debe rechazar dos componentes COTIZACION: %+v", res.Errores)
	}
}
