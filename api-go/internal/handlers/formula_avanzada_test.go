package handlers

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestFormulaAvanzada_AritmeticaYCondicionales(t *testing.T) {
	casos := []struct {
		nombre, texto string
		valores       map[string]any
		esperado      float64
	}{
		{"precedencia", "2 + 3 * 4 - 10 / 2", nil, 9},
		{"parentesis", "(2 + 3) * 4", nil, 20},
		{"asociatividad", "20 / 2 / 2 - 3 - 1", nil, 1},
		{"negativo por resta", "0 - 2 * 3", nil, -6},
		{"decimal", "0.125 * 8", nil, 1},
		{"telefonia si", "SI(USA_TELEFONIA; MINUTOS_MES * COSTO_MINUTO_VOZ; 0)", map[string]any{"USA_TELEFONIA": "Sí", "MINUTOS_MES": 100.0, "COSTO_MINUTO_VOZ": 0.25}, 25},
		{"telefonia no", "SI(USA_TELEFONIA; MINUTOS_MES * COSTO_MINUTO_VOZ; 0)", map[string]any{"USA_TELEFONIA": "No"}, 0},
		{"margen", "COSTO_CHAT / (1 - MARGEN_CHAT)", map[string]any{"COSTO_CHAT": 70.0, "MARGEN_CHAT": 0.30}, 100},
		{"si anidado entonces", "SI(A; SI(B; 10; 20); 30)", map[string]any{"A": true, "B": "No"}, 20},
		{"si anidado sino", "SI(A; 10; SI(B; 20; SI(C; 30; 40)))", map[string]any{"A": "No", "B": false, "C": "SI"}, 30},
		{"rama no evaluada", "SI(A; 8; 1 / 0)", map[string]any{"A": "1"}, 8},
		{"condicion ausente", "SI(A; FALTA; 9)", nil, 9},
		{"espacios", " SI ( A ; 3 ; 4 ) \n + 1", map[string]any{"A": "true"}, 4},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			formula, err := compilarFormulaAvanzada(caso.texto)
			if err != nil {
				t.Fatal(err)
			}
			resultado, err := formula.evaluar(caso.valores)
			if err != nil || math.Abs(resultado-caso.esperado) > 1e-9 {
				t.Fatalf("resultado=%v esperado=%v error=%v", resultado, caso.esperado, err)
			}
		})
	}
}

func TestFormulaAvanzada_CondicionSoloVerdaderosDocumentados(t *testing.T) {
	for _, valor := range []any{"Sí", "SI", " true ", "1", true, 1.0} {
		if !condicionFormula(valor) {
			t.Errorf("%v debería ser verdadero", valor)
		}
	}
	for _, valor := range []any{"No", "false", "0", "2", "yes", "1.0", "", false, 2.0, nil} {
		if condicionFormula(valor) {
			t.Errorf("%v debería ser falso", valor)
		}
	}
}

func TestFormulaAvanzada_TokensYUsosSinDuplicados(t *testing.T) {
	f, err := compilarFormulaAvanzada("SI(A; A + B; SI(C; B; 0))")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Tokens, []string{"A", "B", "C"}) || !f.Usos["A"].Numerico || !f.Usos["A"].Condicion || f.Usos["C"].Numerico {
		t.Fatalf("tokens/uso inesperados: %+v %+v", f.Tokens, f.Usos)
	}
}

func TestFormulaAvanzada_RechazaSintaxisFueraDeGramatica(t *testing.T) {
	for _, texto := range []string{"", "1 +", ";", "1;2", "SI(A;1;2;3)", "SI(A;;0)", "SI(1;2;3)", "SI(A + B;1;2)", "SI(A==1;2;3)", "SI(A>1;2;3)", "SI(A<1;2;3)", "A.VALOR_CALCULO", "alert(1)", "Math.random()", "1e2", "1,5", "1.", "-1", "+1", "2 ** 3", "A B", "(1+2", "1+2)", "'texto'", "A[0]", "SI(A;1;)", "1 🚀 2", "SI", strings.Repeat("(", 65) + "1" + strings.Repeat(")", 65), strings.Repeat("1+", 65) + "1", strings.Repeat("1", 4097), strings.Repeat("9", 400)} {
		t.Run(texto[:min(len(texto), 50)], func(t *testing.T) {
			if _, err := compilarFormulaAvanzada(texto); err == nil {
				t.Fatalf("se aceptó una fórmula inválida: %q", texto)
			}
		})
	}
}

func TestFormulaAvanzada_ErroresControladosDeEvaluacion(t *testing.T) {
	f, _ := compilarFormulaAvanzada("10 / (2 - 2)")
	if _, err := f.evaluar(nil); !errors.Is(err, errDivisionEntreCero) {
		t.Fatalf("esperaba división entre cero, dio %v", err)
	}
	f, _ = compilarFormulaAvanzada("A + 1")
	for _, valor := range []any{nil, "texto", math.Inf(1), math.NaN()} {
		if _, err := f.evaluar(map[string]any{"A": valor}); err == nil {
			t.Fatalf("aceptó valor inválido %v", valor)
		}
	}
	f, _ = compilarFormulaAvanzada("A * A")
	if _, err := f.evaluar(map[string]any{"A": math.MaxFloat64}); err == nil {
		t.Fatal("debe controlar el desbordamiento")
	}
}

func FuzzFormulaAvanzada_NoPanic(f *testing.F) {
	for _, texto := range []string{"1", "SI(A; B; 0)", "SI(A;SI(B;1;0);2)", "(", ";", "A/0"} {
		f.Add(texto)
	}
	f.Fuzz(func(t *testing.T, texto string) {
		formula, err := compilarFormulaAvanzada(texto)
		if err == nil {
			formula.evaluar(map[string]any{"A": true, "B": 1.0})
		}
	})
}

func TestFormulaAvanzada_RuntimeReutilizaTodosLosOperandos(t *testing.T) {
	elementos := map[string]map[string]any{
		"CAMPO": {"tipo": "CAMPO"},
		"CATALOGO": {"tipo": "CAMPO_CATALOGO", "configuracion": map[string]any{
			"catalogo_tipo_calculo": "NUMERO", "catalogo_valores_calculo": []any{map[string]any{"valor_sistema": "CLAVE", "valor_calculo": 5.0}},
		}},
		"lista": {"tipo": "LISTA_PRECIOS", "configuracion": map[string]any{
			"tipo_lista_precios": "UNICA", "items": []any{map[string]any{"item_id": "ITEM", "precio": 10.0}},
		}},
		"tabla": {"tipo": "TABLA", "configuracion": map[string]any{
			"columnas": []any{map[string]any{"columna_id": "COL", "tipo_dato": "NUMERO"}},
		}},
		"simple": {"tipo": "CAMPO_CALCULADO", "configuracion": map[string]any{
			"operacion": "SUMA", "operandos": []any{"CAMPO", "CATALOGO"},
		}},
		"avanzada": {"tipo": "CAMPO_CALCULADO", "configuracion": map[string]any{
			"tipo_formula": "AVANZADA", "formula_texto": "CAMPO + CATALOGO + LISTA + TABLA + SIMPLE",
			"tokens_operandos": map[string]string{"CAMPO": "CAMPO", "CATALOGO": "CATALOGO", "LISTA": "lista", "TABLA": "tabla", "SIMPLE": "simple"},
		}},
		"error": {"tipo": "CAMPO_CALCULADO", "configuracion": map[string]any{"tipo_formula": "AVANZADA", "formula_texto": "1 / 0"}},
	}
	valores := map[string]any{
		"CAMPO": "2", "CATALOGO": "CLAVE", "lista": map[string]any{"item_id": "ITEM", "cantidad": 3.0},
		"tabla": map[string]any{"filas": []any{map[string]any{"COL": "4"}, map[string]any{"COL": 6.0}}},
	}
	resolverCamposCalculados(elementos, valores)
	if elementos["avanzada"]["valor_resuelto"] != 54.0 || elementos["error"]["valor_resuelto"] != nil {
		t.Fatalf("resolución incorrecta: avanzada=%v, división entre cero=%v", elementos["avanzada"], elementos["error"])
	}
	// Un número finito que desborda al redondear tampoco llega al serializador JSON.
	cfg := map[string]any{"tipo_formula": "AVANZADA", "formula_texto": "N", "tokens_operandos": map[string]string{"N": "campo"}}
	if _, err := resolverFormulaConfigurada(cfg, func(string) (float64, bool) { return math.MaxFloat64, true }, nil); err == nil {
		t.Fatal("el desbordamiento de redondeo debe ser controlado")
	}
}

func TestFormulaAvanzada_RuntimeCondicionPorOpcion(t *testing.T) {
	elementos := map[string]map[string]any{
		"padre":     {"tipo": "OPCIONES_PROPUESTA", "opciones": []cotizacionOpcion{{OpcionID: "starter"}, {OpcionID: "premium"}}},
		"condicion": {"tipo": "CAMPO"}, "cantidad": {"tipo": "CAMPO"}, "precio": {"tipo": "CAMPO"},
		"total": {"tipo": "CAMPO_CALCULADO", "configuracion": map[string]any{
			"tipo_formula": "AVANZADA", "formula_texto": "SI(USA; CANTIDAD * PRECIO; 0)",
			"tokens_operandos": map[string]string{"USA": "condicion", "CANTIDAD": "cantidad", "PRECIO": "precio"},
		}},
	}
	meta := map[string]elementoRuntime{
		"condicion": {Tipo: "CAMPO", PadreOpcionesID: "padre"}, "cantidad": {Tipo: "CAMPO", PadreOpcionesID: "padre"},
		"precio": {Tipo: "CAMPO"}, "total": {Tipo: "CAMPO_CALCULADO", PadreOpcionesID: "padre"},
	}
	valores := map[string]any{
		"condicion": map[string]any{"starter": "No", "premium": "Sí"},
		"cantidad":  map[string]any{"starter": "10", "premium": "30"}, "precio": "2",
	}
	resolverCamposCalculadosPorOpcion(elementos, meta, valores)
	resueltos := elementos["total"]["valores_resueltos_por_opcion"].(map[string]any)
	if resueltos["starter"] != 0.0 || resueltos["premium"] != 60.0 {
		t.Fatalf("se mezclaron opciones: %+v", resueltos)
	}
}

func TestFormulaAvanzada_CicloReenlazadoAlCompilar(t *testing.T) {
	tabs := []tabCompilado{{Elementos: []elementoCompilado{
		{ElementoID: "A", Tipo: "CAMPO_CALCULADO", Configuracion: map[string]any{"operandos": []string{"B"}}},
		{ElementoID: "B", Tipo: "CAMPO_CALCULADO", Configuracion: map[string]any{"operandos": []any{"A"}}},
	}}}
	if cicloCamposCalculadosCompilados(tabs) == "" {
		t.Fatal("no detectó el ciclo del grafo compilado")
	}
	tabs[0].Elementos[1].Configuracion["operandos"] = []any{"NUMERICO"}
	if ciclo := cicloCamposCalculadosCompilados(tabs); ciclo != "" {
		t.Fatalf("ciclo inexistente: %s", ciclo)
	}
}
