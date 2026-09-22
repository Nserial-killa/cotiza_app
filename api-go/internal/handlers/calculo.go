package handlers

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// operacionesCalculoValidas es el conjunto de operaciones que acepta un
// Campo Calculado en modo Simple. Avanzada usa formula_avanzada.go.
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

// valorCalculoCatalogo resuelve el valor_calculo del valor SELECCIONADO de
// un CAMPO_CATALOGO, usando los datos embebidos en su configuracion por
// incluirValorCalculoCatalogo (compilador.go): "catalogo_tipo_calculo" y
// "catalogo_valores_calculo" ([{valor_sistema, valor_calculo}]), ya
// congelados en el momento de compilar (mismo criterio que el precio de un
// ítem de Lista de Precios). Principio del documento de definición
// funcional (caso ISA Custom): "la referencia estable es el código; para
// cálculos se consume valor_calculo" — nunca la etiqueta. Un catálogo
// SIN_VALOR, o un campo sin selección todavía (placeholder "Seleccione...",
// valorGuardado vacío), no resuelven ningún número: ok=false en vez de
// tomar el primer valor del catálogo por defecto.
func valorCalculoCatalogo(cfg map[string]any, valorGuardado any) (float64, bool) {
	if cfg == nil {
		return 0, false
	}
	tipoCalculo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["catalogo_tipo_calculo"])))
	if tipoCalculo == "" || tipoCalculo == "SIN_VALOR" {
		return 0, false
	}
	valorSistema, ok := valorGuardado.(string)
	if !ok {
		return 0, false
	}
	valorSistema = strings.TrimSpace(valorSistema)
	if valorSistema == "" {
		return 0, false
	}
	valores, _ := cfg["catalogo_valores_calculo"].([]any)
	for _, vRaw := range valores {
		v, _ := vRaw.(map[string]any)
		if strings.TrimSpace(fmt.Sprint(v["valor_sistema"])) == valorSistema {
			return numeroDesdeValor(v["valor_calculo"])
		}
	}
	return 0, false
}

// primeraColumnaNumericaTabla busca, en el orden en que vienen, la primera
// columna con tipo_dato NUMERO o MONEDA — el total inferido de la tabla
// cuando ninguna columna está marcada Totalizable explícitamente (TAB-003/
// CTZ-TBL-002; ver columnasTotalizables para el caso explícito, que tiene
// prioridad). "columnas" es el array ya compilado (ver incluirColumnasTabla
// en compilador.go), donde CAMPO_EXISTENTE y PROPIA ya vienen normalizadas
// a la misma forma {columna_id, tipo_dato, totalizable, ...}.
func primeraColumnaNumericaTabla(columnas []any) string {
	for _, colRaw := range columnas {
		col, _ := colRaw.(map[string]any)
		tipoDato := strings.ToUpper(strings.TrimSpace(fmt.Sprint(col["tipo_dato"])))
		if tipoDato == "NUMERO" || tipoDato == "MONEDA" {
			return strings.TrimSpace(fmt.Sprint(col["columna_id"]))
		}
	}
	return ""
}

// columnasTotalizables devuelve los columna_id marcados totalizable=true,
// en el orden en que vienen. TAB-003/CTZ-TBL-002: el diseñador puede marcar
// una o varias; el total suma todas las que estén marcadas.
func columnasTotalizables(columnas []any) []string {
	resultado := make([]string, 0)
	for _, colRaw := range columnas {
		col, _ := colRaw.(map[string]any)
		if totalizable, _ := col["totalizable"].(bool); totalizable {
			resultado = append(resultado, strings.TrimSpace(fmt.Sprint(col["columna_id"])))
		}
	}
	return resultado
}

// valorTotalTabla suma, a través de todas las filas guardadas, las columnas
// de la Tabla marcadas Totalizable; sin ninguna marcada, cae a la primera
// columna numérica (compatibilidad con cotizadores ya publicados — ver el
// aviso TABLA_TOTALES_COLUMNAS_INFERIDAS en compilador.go). Mismo patrón
// que valorListaPrecios: puro, sin acceso a base de datos, y con un segundo
// valor de retorno que distingue "no hay nada que sumar todavía" (sin
// columna numérica/totalizable, o sin filas) de "el total da cero".
func valorTotalTabla(cfg map[string]any, valorGuardado map[string]any) (float64, bool) {
	if cfg == nil {
		return 0, false
	}
	columnas, _ := cfg["columnas"].([]any)
	columnaIDs := columnasTotalizables(columnas)
	if len(columnaIDs) == 0 {
		if id := primeraColumnaNumericaTabla(columnas); id != "" {
			columnaIDs = []string{id}
		}
	}
	if len(columnaIDs) == 0 {
		return 0, false
	}
	filasRaw, _ := valorGuardado["filas"].([]any)
	if len(filasRaw) == 0 {
		return 0, false
	}
	var total float64
	huboAlMenosUna := false
	for _, filaRaw := range filasRaw {
		fila, _ := filaRaw.(map[string]any)
		for _, columnaID := range columnaIDs {
			valor, ok := numeroDesdeValor(fila[columnaID])
			if !ok {
				continue
			}
			total += valor
			huboAlMenosUna = true
		}
	}
	return redondear(total, 2), huboAlMenosUna
}
