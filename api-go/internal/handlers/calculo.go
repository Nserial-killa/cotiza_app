package handlers

import (
	"errors"
	"fmt"
	"math"
	"strings"
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

// valorListaPrecios calcula el número que una Lista de Precios aporta a un
// cálculo o muestra en el Motor de Ejecución (Ronda 3): para UNICA, precio
// del ítem seleccionado × cantidad; para MULTIPLE, la suma de precio ×
// cantidad de cada fila. "valorGuardado" es el valor tal como quedó en
// cotizacion_valores ({"item_id":"...","cantidad":N} o
// {"filas":[{"item_id":"...","cantidad":N}, ...]}); precioPorItem debe
// traer solo los ítems activos de ESTE elemento (item_id -> precio) — ya
// resueltos por el llamador, esta función no toca la base de datos.
// El segundo valor de retorno es false cuando no hay nada que calcular
// todavía (sin selección, o ítem que ya no existe/está inactivo) — un 0
// sería engañoso, no es lo mismo "vale cero" que "no hay dato".
func valorListaPrecios(tipoLista string, valorGuardado map[string]any, precioPorItem map[string]float64) (float64, bool) {
	if strings.ToUpper(strings.TrimSpace(tipoLista)) == "MULTIPLE" {
		filasRaw, _ := valorGuardado["filas"].([]any)
		if len(filasRaw) == 0 {
			return 0, false
		}
		var total float64
		huboAlMenosUna := false
		for _, filaRaw := range filasRaw {
			fila, _ := filaRaw.(map[string]any)
			itemID := strings.TrimSpace(fmt.Sprint(fila["item_id"]))
			precio, existe := precioPorItem[itemID]
			if !existe {
				continue
			}
			cantidad, ok := numeroDesdeValor(fila["cantidad"])
			if !ok {
				cantidad = 1
			}
			total += precio * cantidad
			huboAlMenosUna = true
		}
		return redondear(total, 2), huboAlMenosUna
	}

	// UNICA
	itemID := strings.TrimSpace(fmt.Sprint(valorGuardado["item_id"]))
	if itemID == "" {
		return 0, false
	}
	precio, existe := precioPorItem[itemID]
	if !existe {
		return 0, false
	}
	cantidad, ok := numeroDesdeValor(valorGuardado["cantidad"])
	if !ok {
		cantidad = 1
	}
	return redondear(precio*cantidad, 2), true
}
