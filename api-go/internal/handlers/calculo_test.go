package handlers

import (
	"errors"
	"testing"
)

func TestUnitCalcularOperacion_LasCincoOperaciones(t *testing.T) {
	casos := []struct {
		nombre    string
		operandos []float64
		operacion string
		decimales int
		esperado  float64
	}{
		{"suma", []float64{10, 5, 2.5}, "SUMA", 2, 17.5},
		{"resta", []float64{10, 5, 2}, "RESTA", 2, 3},
		{"multiplicacion", []float64{2, 3, 4}, "MULTIPLICACION", 2, 24},
		{"division", []float64{100, 2, 5}, "DIVISION", 2, 10},
		{"promedio", []float64{10, 20, 30}, "PROMEDIO", 2, 20},
		{"promedio de uno solo", []float64{7}, "PROMEDIO", 2, 7},
		{"redondeo a 2 decimales", []float64{10, 3}, "DIVISION", 2, 3.33},
		{"redondeo a 0 decimales", []float64{10, 3}, "DIVISION", 0, 3},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			resultado, err := calcularOperacion(c.operandos, c.operacion, c.decimales)
			if err != nil {
				t.Fatalf("no esperaba error: %v", err)
			}
			if resultado != c.esperado {
				t.Fatalf("esperaba %v, obtuvo %v", c.esperado, resultado)
			}
		})
	}
}

func TestUnitCalcularOperacion_DivisionEntreCeroDaErrorControlado(t *testing.T) {
	_, err := calcularOperacion([]float64{100, 0}, "DIVISION", 2)
	if !errors.Is(err, errDivisionEntreCero) {
		t.Fatalf("esperaba errDivisionEntreCero, obtuvo %v", err)
	}
	// un cero que no sea el divisor (primer operando) es válido.
	resultado, err := calcularOperacion([]float64{0, 5}, "DIVISION", 2)
	if err != nil || resultado != 0 {
		t.Fatalf("0/5 debería dar 0 sin error: resultado=%v err=%v", resultado, err)
	}
}

func TestUnitCalcularOperacion_OperacionDesconocida(t *testing.T) {
	if _, err := calcularOperacion([]float64{1, 2}, "POTENCIA", 2); err == nil {
		t.Fatal("esperaba error para una operación desconocida")
	}
}

func TestUnitCalcularOperacion_FaltanOperandos(t *testing.T) {
	if _, err := calcularOperacion([]float64{1}, "SUMA", 2); err == nil {
		t.Fatal("SUMA con un solo operando debería fallar (mínimo 2)")
	}
	if _, err := calcularOperacion([]float64{}, "PROMEDIO", 2); err == nil {
		t.Fatal("PROMEDIO sin operandos debería fallar (mínimo 1)")
	}
}

func TestUnitRedondear(t *testing.T) {
	if v := redondear(3.14159, 2); v != 3.14 {
		t.Fatalf("esperaba 3.14, obtuvo %v", v)
	}
	if v := redondear(3.14159, 0); v != 3 {
		t.Fatalf("esperaba 3, obtuvo %v", v)
	}
	if v := redondear(2.5, 0); v != 3 {
		t.Fatalf("esperaba redondeo estándar 2.5->3, obtuvo %v", v)
	}
}
