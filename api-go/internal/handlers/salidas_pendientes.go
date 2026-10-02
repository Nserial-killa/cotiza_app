package handlers

// Pendientes de un borrador (ver docs/DECISION_GUARDADO_BORRADOR.md).
//
// Una salida requerida (TOTAL_PRECIO, por ejemplo) puede quedar sin valor
// por dos motivos muy distintos, y este archivo existe para separarlos:
//
//   - Faltan datos: algún campo de entrada del que depende todavía está
//     vacío. En un Borrador eso es normal mientras la persona va llenando
//     el cotizador, así que NO bloquea el guardado: se informa como
//     "pendiente" y se exige recién al avanzar la cotización.
//   - El cálculo falla con datos completos: división entre cero, resultado
//     fuera de rango, valor que no es un número, referencia circular...
//     Eso es un error de verdad y sigue bloqueando el guardado como antes.
//
// resolverCamposCalculados no distingue un caso del otro (en ambos deja
// valor_resuelto en nil), por eso diagnosticarFuente recorre de nuevo la
// cadena de cálculo de UNA fuente, con los mismos helpers, para averiguar
// cuál de los dos pasó. Solo se llama cuando una salida requerida quedó sin
// resolver, así que no cuesta nada en el camino feliz.

import (
	"fmt"
	"strings"
)

// nombresSalidasLegibles traduce cada clave de salida al nombre que entiende
// quien usa el cotizador. Nunca mostrar la clave técnica (TOTAL_PRECIO) ni
// su tipo (MONEDA/TEXTO) en un mensaje que llegue a la pantalla.
var nombresSalidasLegibles = map[string]struct{ nombre, articulo string }{
	"TOTAL_PRECIO":   {"Precio total", "el"},
	"TOTAL_COSTO":    {"Costo total", "el"},
	"TOTAL_GANANCIA": {"Ganancia total", "la"},
	"MARGEN_TOTAL":   {"Margen total", "el"},
	"SUBTOTAL":       {"Subtotal", "el"},
	"DESCUENTO":      {"Descuento", "el"},
	"IMPUESTOS":      {"Impuestos", "los"},
	"MONEDA":         {"Moneda", "la"},
	"TIPO_CLIENTE":   {"Tipo de cliente", "el"},
	"TIPO_PROPUESTA": {"Tipo de propuesta", "el"},
}

// nombreSalida devuelve "Precio total"; conArticulo, "el Precio total".
func nombreSalida(clave string, conArticulo bool) string {
	n, ok := nombresSalidasLegibles[clave]
	if !ok {
		n = struct{ nombre, articulo string }{"valor de salida", "el"}
	}
	if conArticulo {
		return n.articulo + " " + n.nombre
	}
	return n.nombre
}

// etiquetaElemento devuelve lo que la persona ve en pantalla para un
// elemento: su etiqueta, o su nombre interno si no tiene etiqueta. Nunca el
// elemento_id (CTZ-ELE-..., EL-...), que no significa nada para nadie.
func etiquetaElemento(els map[string]map[string]any, id string) string {
	el := els[id]
	if el == nil {
		return "un campo que ya no existe en el cotizador"
	}
	if etiqueta, ok := el["etiqueta"].(string); ok && strings.TrimSpace(etiqueta) != "" {
		return strings.TrimSpace(etiqueta)
	}
	cfg, _ := el["configuracion"].(map[string]any)
	if nombre := nombreInternoElemento(cfg); nombre != "" {
		return nombre
	}
	return "un campo sin nombre"
}

// listaEtiquetas arma «A», «B» y «C» a partir de elemento_ids, sin repetir.
func listaEtiquetas(els map[string]map[string]any, ids []string) string {
	vistos := map[string]bool{}
	partes := []string{}
	for _, id := range ids {
		etiqueta := "«" + etiquetaElemento(els, id) + "»"
		if !vistos[etiqueta] {
			vistos[etiqueta] = true
			partes = append(partes, etiqueta)
		}
	}
	if len(partes) <= 1 {
		return strings.Join(partes, "")
	}
	return strings.Join(partes[:len(partes)-1], ", ") + " y " + partes[len(partes)-1]
}

// mensajeFaltanDatosSalida: "Falta completar «Monto» para calcular el Precio total."
func mensajeFaltanDatosSalida(els map[string]map[string]any, clave string, faltantes []string) string {
	return fmt.Sprintf("Falta completar %s para calcular %s.", listaEtiquetas(els, faltantes), nombreSalida(clave, true))
}

// mensajeCampoObligatorio: "Complete el campo obligatorio «Tipo de Servicio»."
func mensajeCampoObligatorio(els map[string]map[string]any, id string) string {
	return fmt.Sprintf("Complete el campo obligatorio «%s».", etiquetaElemento(els, id))
}

// diagnosticoFuente explica por qué una fuente quedó sin valor.
// faltantes son los elemento_id de los datos de entrada vacíos de los que
// depende; errorCalculo indica que algo falló aunque los datos estuvieran
// (o que hay un dato que no es un número). Si errorCalculo es true, manda
// sobre faltantes: un error real no se disimula como "pendiente".
type diagnosticoFuente struct {
	faltantes    []string
	errorCalculo bool
}

func (d diagnosticoFuente) soloFaltanDatos() bool { return !d.errorCalculo && len(d.faltantes) > 0 }

// valorSinDatos es "la persona todavía no escribió nada": nil, texto en
// blanco, o una selección de lista/tabla sin ninguna fila ni ítem.
func valorSinDatos(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case map[string]any:
		filas, _ := x["filas"].([]any)
		item, _ := x["item_id"].(string)
		return len(filas) == 0 && strings.TrimSpace(item) == ""
	}
	return false
}

// diagnosticarFuente recorre la cadena de cálculo de la fuente id con los
// mismos helpers que resolverCamposCalculados (valorListaPrecios,
// valorTotalTabla, valorCalculoCatalogo, resolverFormulaConfigurada), pero
// en vez de un simple "resuelto/no resuelto" acumula el motivo de cada
// falla. valores ya tiene que venir aplanado a la opción efectiva si la
// fuente vive dentro de Opciones de Propuesta (valoresPlanosOpcion).
func diagnosticarFuente(id string, els map[string]map[string]any, valores map[string]any) diagnosticoFuente {
	type resultado struct {
		valor float64
		diag  *diagnosticoFuente // nil = se pudo calcular
	}
	memo := map[string]resultado{}
	enProceso := map[string]bool{}

	// combinar junta los motivos de varios operandos fallidos en uno solo.
	combinar := func(fallos []*diagnosticoFuente) *diagnosticoFuente {
		total := &diagnosticoFuente{}
		for _, f := range fallos {
			total.errorCalculo = total.errorCalculo || f.errorCalculo
			total.faltantes = append(total.faltantes, f.faltantes...)
		}
		return total
	}

	var resolver func(string) (float64, *diagnosticoFuente)
	resolver = func(id string) (float64, *diagnosticoFuente) {
		if r, ok := memo[id]; ok {
			return r.valor, r.diag
		}
		guardar := func(v float64, d *diagnosticoFuente) (float64, *diagnosticoFuente) {
			memo[id] = resultado{v, d}
			return v, d
		}
		// sinValor decide, para un dato de entrada que no dio número, si es
		// que está vacío (pendiente) o que tiene algo que no sirve (error).
		sinValor := func() (float64, *diagnosticoFuente) {
			if valorSinDatos(valores[id]) {
				return guardar(0, &diagnosticoFuente{faltantes: []string{id}})
			}
			return guardar(0, &diagnosticoFuente{errorCalculo: true})
		}
		el := els[id]
		if el == nil {
			return guardar(0, &diagnosticoFuente{errorCalculo: true})
		}
		cfg, _ := el["configuracion"].(map[string]any)
		tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(el["tipo"])))
		switch tipo {
		case "LISTA_PRECIOS", "TABLA":
			// Una lista o tabla sin resolver es una selección o filas sin
			// completar: los ítems que ya no existen se rechazan antes, en
			// persistirSalidasSnapshot, con su propio mensaje.
			guardado, _ := valores[id].(map[string]any)
			var v float64
			var ok bool
			if tipo == "LISTA_PRECIOS" {
				v, ok = valorListaPrecios(fmt.Sprint(cfg["tipo_lista_precios"]), guardado, precioPorItemDesdeConfiguracion(cfg))
			} else {
				v, ok = valorTotalTabla(cfg, guardado)
			}
			if ok {
				return guardar(v, nil)
			}
			return guardar(0, &diagnosticoFuente{faltantes: []string{id}})
		case "CAMPO_CATALOGO":
			if v, ok := valorCalculoCatalogo(cfg, valores[id]); ok {
				return guardar(v, nil)
			}
			return sinValor()
		case "CAMPO_CALCULADO":
		default:
			if v, ok := numeroDesdeValor(valores[id]); ok {
				return guardar(v, nil)
			}
			return sinValor()
		}

		// Campo Calculado. Un ciclo es siempre un error de configuración.
		if enProceso[id] || cfg == nil {
			return guardar(0, &diagnosticoFuente{errorCalculo: true})
		}
		enProceso[id] = true
		defer delete(enProceso, id)

		// Fórmula simple: se revisan TODOS los operandos antes de calcular,
		// para que el pendiente nombre todo lo que falta de una sola vez.
		if cfg["tipo_formula"] != "AVANZADA" {
			fallos := []*diagnosticoFuente{}
			for _, op := range operandosDesdeConfiguracion(cfg) {
				if _, d := resolver(op); d != nil {
					fallos = append(fallos, d)
				}
			}
			if len(fallos) > 0 {
				return guardar(0, combinar(fallos))
			}
		}
		// Fórmula avanzada (o simple con todos sus operandos): se evalúa
		// con un resolver que anota qué operando falló. La avanzada es
		// perezosa (SI solo evalúa una rama), así que solo se anotan los
		// operandos que de verdad se necesitaron.
		fallos := []*diagnosticoFuente{}
		anotar := func(op string) (float64, bool) {
			v, d := resolver(op)
			if d != nil {
				fallos = append(fallos, d)
			}
			return v, d == nil
		}
		v, err := resolverFormulaConfigurada(cfg, anotar, func(op string) (any, bool) {
			return resolverCondicionFormulaRuntime(els[op], valores[op], op, anotar)
		})
		if err == nil {
			return guardar(v, nil)
		}
		if len(fallos) > 0 {
			return guardar(0, combinar(fallos))
		}
		// Todos los operandos tenían valor y aun así falló: división entre
		// cero, rango numérico, operación desconocida. Error real.
		return guardar(0, &diagnosticoFuente{errorCalculo: true})
	}

	if _, d := resolver(id); d != nil {
		return *d
	}
	// La fuente sí se puede calcular con estos valores, pero el flujo real
	// no la resolvió (por ejemplo, una regla que la forzó): ante la duda,
	// se trata como error y se mantiene el bloqueo de siempre.
	return diagnosticoFuente{errorCalculo: true}
}
