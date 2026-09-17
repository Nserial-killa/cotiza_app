package handlers

import (
	"errors"
	"math"
)

// operacionesCalculoValidas es el conjunto de operaciones que acepta un
// Campo Calculado (Ronda 2 del Diseñador). AVANZADA/otras fórmulas quedan
// para una ronda futura.
var operacionesCalculoValidas = map[string]bool{
	"SUMA": true, "RESTA": true, "MULTIPLICACION": true, "DIVISION": true, "PROMEDIO": true,
}

// errDivisionEntreCero se devuelve cuando DIVISION encuentra un operando en
// cero después del primero — un resultado infinito no sirve para mostrarlo
// ni para sumarlo en cotizacion_versiones.
var errDivisionEntreCero = errors.New("no se puede dividir entre cero")

// calcularOperacion aplica la operación indicada sobre la lista de
// operandos, en el orden en que vienen, y redondea el resultado a
// "decimales" posiciones. Es una función pura (sin acceso a base de datos)
// a propósito, para poder probarla con pruebas unitarias simples.
func calcularOperacion(operandos []float64, operacion string, decimales int) (float64, error) {
	if !operacionesCalculoValidas[operacion] {
		return 0, errors.New("operación de cálculo desconocida: " + operacion)
	}
	minimoOperandos := 2
	if operacion == "PROMEDIO" {
		minimoOperandos = 1
	}
	if len(operandos) < minimoOperandos {
		return 0, errors.New("faltan operandos para la operación")
	}

	var resultado float64
	switch operacion {
	case "SUMA":
		for _, v := range operandos {
			resultado += v
		}
	case "RESTA":
		resultado = operandos[0]
		for _, v := range operandos[1:] {
			resultado -= v
		}
	case "MULTIPLICACION":
		resultado = 1
		for _, v := range operandos {
			resultado *= v
		}
	case "DIVISION":
		resultado = operandos[0]
		for _, v := range operandos[1:] {
			if v == 0 {
				return 0, errDivisionEntreCero
			}
			resultado /= v
		}
	case "PROMEDIO":
		var suma float64
		for _, v := range operandos {
			suma += v
		}
		resultado = suma / float64(len(operandos))
	}

	return redondear(resultado, decimales), nil
}

// redondear redondea a "decimales" posiciones (0 a 4 en la práctica, pero la
// función no impone ese rango — GuardarElemento ya lo valida antes).
func redondear(valor float64, decimales int) float64 {
	if decimales < 0 {
		decimales = 0
	}
	factor := math.Pow(10, float64(decimales))
	return math.Round(valor*factor) / factor
}
