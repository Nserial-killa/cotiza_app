package handlers

// Pruebas de la decisión de docs/DECISION_GUARDADO_BORRADOR.md: un Borrador
// se guarda aunque le falten datos para el precio o campos obligatorios
// (los devuelve en "pendientes"); el precio se exige al avanzar de estado o
// al generar el enlace público; los errores de cálculo siguen bloqueando.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureBorrador: SUBTOTAL + IMPUESTO = TOTAL (Campo Calculado con
// funcion_campo=TOTAL_PRECIO_OFERTA, de ahí sale el Precio total) y un
// campo obligatorio de texto "Tipo de Servicio".
type fixtureBorrador struct {
	diseno                       fixtureFormulaAvanzada
	subtotal, impuesto, tipoServ string
}

func crearFixtureBorrador(t *testing.T) fixtureBorrador {
	t.Helper()
	f := crearFixtureFormulaAvanzada(t)
	subtotal := f.crear(t, "CAMPO", "SUBTOTAL", map[string]any{"tipo_campo": "MONEDA"}, nil)
	impuesto := f.crear(t, "CAMPO", "IMPUESTO", map[string]any{"tipo_campo": "MONEDA"}, nil)
	tipoServ := f.crear(t, "CAMPO", "Tipo de Servicio", map[string]any{"tipo_campo": "TEXTO"}, map[string]any{"requerido": true})
	// Fórmula simple (SUMA de los dos operandos): si faltan ambos, el
	// pendiente tiene que nombrar a los dos.
	f.crear(t, "CAMPO_CALCULADO", "TOTAL", map[string]any{
		"tipo_formula": "SIMPLE", "operacion": "SUMA", "tipo_resultado": "MONEDA", "decimales": 2,
		"operandos": []string{subtotal, impuesto},
	}, map[string]any{"funcion_campo": "TOTAL_PRECIO_OFERTA"})
	return fixtureBorrador{f, subtotal, impuesto, tipoServ}
}

// nuevaCotizacion compila el cotizador y crea una cotización en Borrador.
func (f fixtureBorrador) nuevaCotizacion(t *testing.T) fixtureRuntime {
	t.Helper()
	return f.diseno.runtime(t)
}

type respuestaGuardadoBorrador struct {
	OK              bool     `json:"ok"`
	Error           string   `json:"error"`
	Pendientes      []string `json:"pendientes"`
	PrecioPendiente []string `json:"precio_pendiente"`
}

func guardarBorrador(t *testing.T, rt fixtureRuntime, valores map[string]any) (*httptest.ResponseRecorder, respuestaGuardadoBorrador) {
	t.Helper()
	rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": valores})
	var res respuestaGuardadoBorrador
	assertJSON(t, rec.Body.Bytes(), &res)
	return rec, res
}

// exigirMensajeLegible: nada que llegue a la persona puede traer un id de
// elemento ni una clave o tipo técnico.
func exigirMensajeLegible(t *testing.T, mensaje string) {
	t.Helper()
	for _, prohibido := range []string{"CTZ-ELE-", "EL-", "MONEDA", "TOTAL_PRECIO"} {
		if strings.Contains(mensaje, prohibido) {
			t.Fatalf("el mensaje muestra un código interno (%q): %s", prohibido, mensaje)
		}
	}
}

// precioGuardado devuelve total_precio de la versión y si existe la fila
// TOTAL_PRECIO en cotizacion_salidas.
func precioGuardado(t *testing.T, rt fixtureRuntime) (float64, bool) {
	t.Helper()
	var total float64
	var fila bool
	if err := rt.Handler.DB.QueryRow(context.Background(), `
		SELECT cv.total_precio, EXISTS(SELECT 1 FROM cotizacion_salidas s WHERE s.cotizacion_id=cv.cotizacion_id AND s.numero_version=1 AND s.clave_salida='TOTAL_PRECIO')
		  FROM cotizacion_versiones cv WHERE cv.cotizacion_id=$1 AND cv.numero_version=1`, rt.CotizacionID).Scan(&total, &fila); err != nil {
		t.Fatal(err)
	}
	return total, fila
}

func cambiarEstadoPrueba(t *testing.T, rt fixtureRuntime, actorID, estado string) *httptest.ResponseRecorder {
	t.Helper()
	return audPostSubruta(t, (&CotizacionesHandler{DB: rt.Handler.DB}).CambiarEstado, "/api/cotizaciones/{id}/estado", rt.CotizacionID, actorID,
		map[string]any{"version": 1, "estado": estado, "version_aceptada": 1})
}

func TestBorrador_GuardarSoloSubtotalGuardaConPendientes(t *testing.T) {
	f := crearFixtureBorrador(t)
	rt := f.nuevaCotizacion(t)

	rec, res := guardarBorrador(t, rt, map[string]any{f.subtotal: "100", f.tipoServ: "Implementación"})
	if rec.Code != http.StatusOK || !res.OK {
		t.Fatalf("un borrador incompleto tiene que guardarse: %d %s", rec.Code, rec.Body.String())
	}
	var guardado string
	if err := rt.Handler.DB.QueryRow(context.Background(), `SELECT valor #>> '{}' FROM cotizacion_valores WHERE cotizacion_id=$1 AND elemento_id=$2`, rt.CotizacionID, f.subtotal).Scan(&guardado); err != nil || guardado != "100" {
		t.Fatalf("el valor escrito se perdió: %q %v", guardado, err)
	}
	if len(res.Pendientes) != 1 || res.Pendientes[0] != "Falta completar «IMPUESTO» para calcular el Precio total." {
		t.Fatalf("pendientes inesperados: %+v", res.Pendientes)
	}
	exigirMensajeLegible(t, rec.Body.String())
	if len(res.PrecioPendiente) != 1 || res.PrecioPendiente[0] != "IMPUESTO" {
		t.Fatalf("precio_pendiente inesperado: %+v", res.PrecioPendiente)
	}
	if total, fila := precioGuardado(t, rt); total != 0 || fila {
		t.Fatalf("sin precio calculado no puede haber total ni fila: total=%v fila=%v", total, fila)
	}

	// Sin ningún operando el pendiente nombra a los dos.
	rec, res = guardarBorrador(t, rt, map[string]any{f.subtotal: ""})
	if rec.Code != http.StatusOK || len(res.Pendientes) != 1 || res.Pendientes[0] != "Falta completar «SUBTOTAL» y «IMPUESTO» para calcular el Precio total." {
		t.Fatalf("esperaba los dos operandos pendientes: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBorrador_CompletarSubtotalEImpuestoCalculaPrecio(t *testing.T) {
	f := crearFixtureBorrador(t)
	rt := f.nuevaCotizacion(t)
	rec, res := guardarBorrador(t, rt, map[string]any{f.subtotal: "100", f.impuesto: "13", f.tipoServ: "Soporte"})
	if rec.Code != http.StatusOK || len(res.Pendientes) != 0 || len(res.PrecioPendiente) != 0 {
		t.Fatalf("completo no debería tener pendientes: %d %s", rec.Code, rec.Body.String())
	}
	if total, fila := precioGuardado(t, rt); total != 113 || !fila {
		t.Fatalf("precio incorrecto: total=%v fila=%v", total, fila)
	}
	if v := leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO"); v != 113 {
		t.Fatalf("salida TOTAL_PRECIO=%v", v)
	}
}

func TestBorrador_BorrarImpuestoQuitaElPrecioViejo(t *testing.T) {
	f := crearFixtureBorrador(t)
	rt := f.nuevaCotizacion(t)
	if rec, _ := guardarBorrador(t, rt, map[string]any{f.subtotal: "100", f.impuesto: "13", f.tipoServ: "Soporte"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec, res := guardarBorrador(t, rt, map[string]any{f.impuesto: ""})
	if rec.Code != http.StatusOK || len(res.Pendientes) != 1 {
		t.Fatalf("borrar un operando en Borrador debe guardar con pendiente: %d %s", rec.Code, rec.Body.String())
	}
	if total, fila := precioGuardado(t, rt); total != 0 || fila {
		t.Fatalf("quedó circulando el precio anterior: total=%v fila=%v", total, fila)
	}
}

func TestAvance_EstadosQueExigenPrecioRechazanSinPrecio(t *testing.T) {
	f := crearFixtureBorrador(t)
	actorID := crearAdminActorPrueba(t, f.diseno.handler.DB)
	for _, estado := range []string{"Revisión Comercial", "Enviada al Cliente", "Aceptada", "Ganada"} {
		t.Run(estado, func(t *testing.T) {
			rt := f.nuevaCotizacion(t)
			if rec, _ := guardarBorrador(t, rt, map[string]any{f.subtotal: "100", f.tipoServ: "Soporte"}); rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			rec := cambiarEstadoPrueba(t, rt, actorID, estado)
			cuerpo := rec.Body.String()
			if rec.Code != http.StatusConflict || !strings.Contains(cuerpo, "«"+estado+"»") || !strings.Contains(cuerpo, "Falta completar «IMPUESTO» para calcular el Precio total.") {
				t.Fatalf("esperaba 409 legible: %d %s", rec.Code, cuerpo)
			}
			exigirMensajeLegible(t, cuerpo)
			var estadoGuardado string
			rt.Handler.DB.QueryRow(context.Background(), `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, rt.CotizacionID).Scan(&estadoGuardado)
			if estadoGuardado != "Borrador" {
				t.Fatalf("el rechazo cambió el estado igual: %s", estadoGuardado)
			}

			if rec, _ := guardarBorrador(t, rt, map[string]any{f.impuesto: "13"}); rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			if rec := cambiarEstadoPrueba(t, rt, actorID, estado); rec.Code != http.StatusOK {
				t.Fatalf("con precio resuelto debía avanzar: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAvance_DescartarNoExigePrecio(t *testing.T) {
	f := crearFixtureBorrador(t)
	actorID := crearAdminActorPrueba(t, f.diseno.handler.DB)
	for _, estado := range []string{"Cancelada", "Perdida", "Vencida"} {
		t.Run(estado, func(t *testing.T) {
			rt := f.nuevaCotizacion(t)
			if rec, _ := guardarBorrador(t, rt, map[string]any{f.subtotal: "100"}); rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			if rec := cambiarEstadoPrueba(t, rt, actorID, estado); rec.Code != http.StatusOK {
				t.Fatalf("descartar un borrador incompleto no debe exigir el precio: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAvance_EnlacePublicoExigePrecio(t *testing.T) {
	f := crearFixtureBorrador(t)
	rt := f.nuevaCotizacion(t)
	enlaces := &EnlacesPublicosHandler{DB: rt.Handler.DB}
	if rec, _ := guardarBorrador(t, rt, map[string]any{f.subtotal: "100", f.tipoServ: "Soporte"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec := postEnlace(t, enlaces, rt.CotizacionID, map[string]any{"version": 1})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "generar el enlace") || !strings.Contains(rec.Body.String(), "«IMPUESTO»") {
		t.Fatalf("esperaba 409 legible al generar el enlace sin precio: %d %s", rec.Code, rec.Body.String())
	}
	exigirMensajeLegible(t, rec.Body.String())
	var enlacesCreados int
	rt.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM enlaces_publicos WHERE cotizacion_id=$1`, rt.CotizacionID).Scan(&enlacesCreados)
	if enlacesCreados != 0 {
		t.Fatal("el rechazo igual dejó un enlace creado")
	}
	if rec, _ := guardarBorrador(t, rt, map[string]any{f.impuesto: "13"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if rec := postEnlace(t, enlaces, rt.CotizacionID, map[string]any{"version": 1}); rec.Code != http.StatusOK {
		t.Fatalf("con precio debía generar el enlace: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBorrador_CampoObligatorioVacioGuardaYBloqueaAlAvanzar(t *testing.T) {
	f := crearFixtureBorrador(t)
	rt := f.nuevaCotizacion(t)
	actorID := crearAdminActorPrueba(t, rt.Handler.DB)
	rec, res := guardarBorrador(t, rt, map[string]any{f.subtotal: "100", f.impuesto: "13"})
	if rec.Code != http.StatusOK {
		t.Fatalf("el obligatorio vacío no debe impedir guardar el borrador: %d %s", rec.Code, rec.Body.String())
	}
	if len(res.Pendientes) != 1 || res.Pendientes[0] != "Complete el campo obligatorio «Tipo de Servicio»." || len(res.PrecioPendiente) != 0 {
		t.Fatalf("pendientes inesperados: %s", rec.Body.String())
	}
	exigirMensajeLegible(t, rec.Body.String())
	rec = cambiarEstadoPrueba(t, rt, actorID, "Enviada al Cliente")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Complete el campo obligatorio «Tipo de Servicio».") {
		t.Fatalf("esperaba rechazo por el obligatorio: %d %s", rec.Code, rec.Body.String())
	}
	exigirMensajeLegible(t, rec.Body.String())
}

func TestGuardado_DivisionEntreCeroSigueBloqueando(t *testing.T) {
	f := crearFixtureFormulaAvanzada(t)
	monto := f.crear(t, "CAMPO", "MONTO", map[string]any{"tipo_campo": "MONEDA"}, nil)
	divisor := f.crear(t, "CAMPO", "CUOTAS", map[string]any{"tipo_campo": "NUMERO"}, nil)
	f.crear(t, "CAMPO_CALCULADO", "POR_CUOTA", configFormulaPrueba("MONTO / CUOTAS"), map[string]any{"funcion_campo": "TOTAL_PRECIO_OFERTA"})
	rt := f.runtime(t)
	rec := postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{monto: "1000", divisor: "0"}})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "división entre cero") {
		t.Fatalf("una división entre cero tiene que seguir bloqueando: %d %s", rec.Code, rec.Body.String())
	}
	exigirMensajeLegible(t, rec.Body.String())
	var guardados int
	rt.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_valores WHERE cotizacion_id=$1`, rt.CotizacionID).Scan(&guardados)
	if guardados != 0 {
		t.Fatalf("un error de cálculo dejó %d valores guardados", guardados)
	}
}

func TestGuardado_ValorIncompatibleSigueBloqueando(t *testing.T) {
	f := crearFixtureBorrador(t)
	rt := f.nuevaCotizacion(t)
	rec, _ := guardarBorrador(t, rt, map[string]any{f.subtotal: "cien", f.impuesto: "13"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("un texto en un campo de moneda tiene que rechazarse: %d %s", rec.Code, rec.Body.String())
	}
	var guardados int
	rt.Handler.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM cotizacion_valores WHERE cotizacion_id=$1`, rt.CotizacionID).Scan(&guardados)
	if guardados != 0 {
		t.Fatalf("un valor incompatible dejó %d valores guardados", guardados)
	}
}

func TestCompilador_AdvierteCotizadorSinPrecio(t *testing.T) {
	sinPrecio := crearFixtureFormulaAvanzada(t)
	sinPrecio.crear(t, "CAMPO", "NOTAS", map[string]any{"tipo_campo": "TEXTO"}, nil)
	compilador := &CompiladorHandler{DB: sinPrecio.handler.DB}
	for _, paso := range []string{"validar", "publicar"} {
		handler := compilador.Validar
		if paso == "publicar" {
			handler = compilador.Compilar
		}
		res := postCompilador(t, handler, sinPrecio.calculadoraID)
		if !res.Valido || len(res.Errores) != 0 || !strings.Contains(fmt.Sprint(res.Advertencias), mensajeCotizadorSinPrecio) {
			t.Fatalf("%s sin precio: esperaba válido con la advertencia: %+v", paso, res)
		}
		if paso == "publicar" && !res.Compilado {
			t.Fatalf("la advertencia no debe impedir publicar: %+v", res)
		}
	}

	conPrecio := crearFixtureBorrador(t)
	res := postCompilador(t, (&CompiladorHandler{DB: conPrecio.diseno.handler.DB}).Compilar, conPrecio.diseno.calculadoraID)
	if !res.Compilado || strings.Contains(fmt.Sprint(res.Advertencias), "no tiene un precio configurado") {
		t.Fatalf("con precio no debe advertir: %+v", res)
	}
}
