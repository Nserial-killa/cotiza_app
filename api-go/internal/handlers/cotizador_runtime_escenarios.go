package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Ronda F2, Parte 2 — Opciones de Propuesta con alcance COTIZACION.
//
// Un OPCIONES_PROPUESTA con configuracion.alcance_opciones=COTIZACION no
// tiene hijos: TODOS los campos con valor propio del cotizador (CAMPO,
// CAMPO_CATALOGO, LISTA_PRECIOS, TABLA) y todos los Campos Calculados pasan
// a tener un valor POR OPCIÓN, guardado en cotizacion_valores.opcion_id
// (migración 0021, sin esquema nuevo). El mecanismo es el mismo que ya
// usaban los hijos de un OPCIONES_PROPUESTA LOCAL: indexarElementosRuntime
// les asigna PadreOpcionesID = el componente global, y a partir de ahí
// GuardarValores exige opcion_id, resolverCamposCalculadosPorOpcion calcula
// por opción, DUPLICAR copia todas las filas de esa opción, las salidas
// ESCENARIO leen la opción efectiva y los totales se guardan por opción en
// cotizacion_opciones. Lo que este archivo agrega es lo que el modo LOCAL
// nunca tuvo: Reglas evaluadas por opción y la proyección de la opción
// efectiva para la plantilla.

// idsOpcionesPadre lee padre["opciones"] tal como lo dejan
// asegurarOpcionesPropuesta ([]cotizacionOpcion) o un snapshot ya
// deserializado ([]any de mapas).
func idsOpcionesPadre(padre map[string]any) []string {
	ids := make([]string, 0)
	switch opciones := padre["opciones"].(type) {
	case []cotizacionOpcion:
		for _, opcion := range opciones {
			ids = append(ids, opcion.OpcionID)
		}
	case []any:
		for _, opcionRaw := range opciones {
			opcion, _ := opcionRaw.(map[string]any)
			if id := strings.TrimSpace(fmt.Sprint(opcion["opcion_id"])); id != "" && id != "<nil>" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// opcionRecomendadaPadre devuelve la opción efectiva de un componente: la
// única, o la marcada como recomendada. "" si hay varias y ninguna marcada.
func opcionRecomendadaPadre(padre map[string]any) string {
	switch opciones := padre["opciones"].(type) {
	case []cotizacionOpcion:
		if len(opciones) == 1 {
			return opciones[0].OpcionID
		}
		for _, opcion := range opciones {
			if opcion.EsRecomendada {
				return opcion.OpcionID
			}
		}
	case []any:
		if len(opciones) == 1 {
			opcion, _ := opciones[0].(map[string]any)
			return strings.TrimSpace(fmt.Sprint(opcion["opcion_id"]))
		}
		for _, opcionRaw := range opciones {
			opcion, _ := opcionRaw.(map[string]any)
			if recomendada, _ := opcion["es_recomendada"].(bool); recomendada {
				return strings.TrimSpace(fmt.Sprint(opcion["opcion_id"]))
			}
		}
	}
	return ""
}

// valoresPlanosOpcion aplana el mapa de valores para UNA opción de UN
// componente: los valores globales se copian tal cual, los del componente
// se reemplazan por su valor en esa opción, y los de otra colección de
// opciones se omiten (son ambiguos desde esta opción). Es la vista con la
// que se evalúan las reglas de esa opción.
func valoresPlanosOpcion(metadatos map[string]elementoRuntime, valores map[string]any, padreID, opcionID string) map[string]any {
	resultado := make(map[string]any, len(valores))
	for id, valor := range valores {
		switch metadatos[id].PadreOpcionesID {
		case "":
			resultado[id] = valor
		case padreID:
			if porOpcion, ok := valor.(map[string]any); ok {
				if v, existe := porOpcion[opcionID]; existe {
					resultado[id] = v
				}
			}
		}
	}
	return resultado
}

// estadosReglasPorOpcion evalúa las reglas de visibilidad/acción una vez por
// cada opción del componente COTIZACION, con los valores de esa opción. En
// la Opción 2 del caso ISA (Telefonía = No), R01 oculta los campos de voz y
// pone en 0 sus cálculos SOLO en esa opción. Devuelve opcion_id -> estados.
// Sin componente COTIZACION devuelve nil: el modo LOCAL no cambia (sus
// campos siguen fuera del motor de Reglas, igual que antes de la Ronda F2).
func estadosReglasPorOpcion(elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any, reglas []reglaCotizadorEval) (string, map[string]map[string]estadoCampoRegla) {
	padreID := padreOpcionesCotizacionIndexado(elementosPorID)
	if padreID == "" || len(reglas) == 0 {
		return padreID, nil
	}
	resultado := map[string]map[string]estadoCampoRegla{}
	for _, opcionID := range idsOpcionesPadre(elementosPorID[padreID]) {
		resultado[opcionID] = evaluarEstadoCamposRegla(valoresPlanosOpcion(metadatos, valores, padreID, opcionID), reglas)
	}
	return padreID, resultado
}

func padreOpcionesCotizacionIndexado(elementosPorID map[string]map[string]any) string {
	for id, el := range elementosPorID {
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(el["tipo"]))) != "OPCIONES_PROPUESTA" {
			continue
		}
		cfg, _ := el["configuracion"].(map[string]any)
		if alcanceOpcionesPropuesta(cfg) == "COTIZACION" {
			return id
		}
	}
	return ""
}

// forzadosPorOpcion convierte los estados por opción en el conjunto de
// elementos que, en esa opción, deben valer 0 en los cálculos: ocultos o
// PONER_EN_CERO (sección 6.1: un valor oculto no puede seguir sumándose en
// silencio), sean entradas o Campos Calculados.
func forzadosPorOpcion(estados map[string]map[string]estadoCampoRegla) map[string]map[string]bool {
	if estados == nil {
		return nil
	}
	resultado := make(map[string]map[string]bool, len(estados))
	for opcionID, porCampo := range estados {
		for id, estado := range porCampo {
			if !estado.Visible || estado.ForzarCero {
				if resultado[opcionID] == nil {
					resultado[opcionID] = map[string]bool{}
				}
				resultado[opcionID][id] = true
			}
		}
	}
	return resultado
}

// incluirEstadoReglasPorOpcion deja en cada elemento alcanzado por una regla
// su estado por opción bajo "estado_regla_por_opcion" (opcion_id -> estado).
// El Motor de Ejecución lo lee con la opción activa; un elemento sin la
// clave para esa opción usa el default (visible, habilitado).
func incluirEstadoReglasPorOpcion(elementosPorID map[string]map[string]any, estados map[string]map[string]estadoCampoRegla) {
	for opcionID, porCampo := range estados {
		for id, estado := range porCampo {
			el := elementosPorID[id]
			if el == nil {
				continue
			}
			porOpcion, _ := el["estado_regla_por_opcion"].(map[string]any)
			if porOpcion == nil {
				porOpcion = map[string]any{}
			}
			porOpcion[opcionID] = estado
			el["estado_regla_por_opcion"] = porOpcion
		}
	}
}

// resolverCamposCalculadosPorOpcionConReglas es resolverCamposCalculadosPorOpcion
// más las Reglas por opción del modo COTIZACION: un Campo Calculado oculto o
// PONER_EN_CERO en una opción vale 0 en esa opción (y los que lo suman lo
// ven en 0), sin tocar las demás opciones.
func resolverCamposCalculadosPorOpcionConReglas(elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any, reglas []reglaCotizadorEval) map[string]map[string]estadoCampoRegla {
	_, estados := estadosReglasPorOpcion(elementosPorID, metadatos, valores, reglas)
	resolverCamposCalculadosPorOpcionForzados(elementosPorID, metadatos, valores, forzadosPorOpcion(estados))
	incluirEstadoReglasPorOpcion(elementosPorID, estados)
	return estados
}

// proyectarOpcionEfectiva arma la vista "global" de una cotización en modo
// COTIZACION: valores y valor_resuelto de la opción efectiva/recomendada
// (§9.5 "Inversión de la opción recomendada"). Lo usa la plantilla para
// vinculaciones, condiciones y tokens [NOMBRE] fuera de la tabla de
// escenarios, que sigue resolviendo cada opción con sus propios valores.
// Devuelve el mapa de valores proyectado y metadatos donde esos campos ya
// no cuentan como "por opción"; los elementos reciben valor_resuelto de la
// opción efectiva. Sin componente COTIZACION devuelve lo mismo que recibe.
func proyectarOpcionEfectiva(elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any) (map[string]any, map[string]elementoRuntime) {
	padreID := padreOpcionesCotizacionIndexado(elementosPorID)
	if padreID == "" {
		return valores, metadatos
	}
	opcionID := opcionRecomendadaPadre(elementosPorID[padreID])
	proyectados := make(map[string]any, len(valores))
	for id, valor := range valores {
		proyectados[id] = valor
	}
	metaProyectada := make(map[string]elementoRuntime, len(metadatos))
	for id, meta := range metadatos {
		if meta.PadreOpcionesID != padreID {
			metaProyectada[id] = meta
			continue
		}
		meta.PadreOpcionesID = ""
		metaProyectada[id] = meta
		var valor any
		if porOpcion, ok := valores[id].(map[string]any); ok && opcionID != "" {
			valor = porOpcion[opcionID]
		}
		proyectados[id] = valor
		if el := elementosPorID[id]; el != nil {
			if meta.Tipo == "CAMPO_CALCULADO" || meta.Tipo == "LISTA_PRECIOS" || meta.Tipo == "TABLA" {
				porOpcion, _ := el["valores_resueltos_por_opcion"].(map[string]any)
				el["valor_resuelto"] = porOpcion[opcionID]
			}
		}
	}
	return proyectados, metaProyectada
}

// aplicarReglasPorOpcion es la mitad de GuardarValores que corresponde al
// modo COTIZACION: para cada opción que el guardado toca arma sus valores
// propuestos (lo persistido + lo que llega), corre las validaciones
// (BLOQUEAR_GUARDADO/CAMPO_REQUERIDO) y, si pasan, persiste "0" en los
// Campos numéricos que una regla deja ocultos o en cero EN ESA OPCIÓN
// (mismo criterio que el modo global).
//
// Una selección de catálogo, Lista de Precios o Tabla oculta NO se borra:
// resolverCamposCalculadosPorOpcionForzados ya la trata como 0 en esa
// opción (§6.1: un valor oculto no suma), y así, cuando la regla deja de
// aplicar, el campo reaparece con su valor válido (CP-04). Borrarla a null
// dejaba sin resolver los cálculos que dependen de ella y la salida
// requerida bloqueaba el guardado que la volvía a mostrar.
func (h *CotizadorRuntimeHandler) aplicarReglasPorOpcion(ctx context.Context, tx pgx.Tx, rt *contextoRuntime, version int, valoresActuales map[string]any, pendientes []valorPendienteRuntime, reglas []reglaCotizadorEval) ([]valorPendienteRuntime, []errorValidacionRegla, error) {
	global := padreOpcionesCotizacion(rt.Estructura)
	if global == "" || len(reglas) == 0 {
		return pendientes, nil, nil
	}
	tocadas := make([]string, 0)
	vistas := map[string]bool{}
	indice := map[string]int{}
	for i, p := range pendientes {
		if rt.Elementos[p.ElementoID].PadreOpcionesID != global || p.OpcionID == "" {
			continue
		}
		indice[p.ElementoID+"\x00"+p.OpcionID] = i
		if !vistas[p.OpcionID] {
			vistas[p.OpcionID] = true
			tocadas = append(tocadas, p.OpcionID)
		}
	}
	if len(tocadas) == 0 {
		return pendientes, nil, nil
	}
	opciones, err := listarOpcionesPropuesta(ctx, tx, rt.CotizacionID, version, global)
	if err != nil {
		return pendientes, nil, err
	}
	nombres := map[string]string{}
	for _, op := range opciones {
		nombres[op.OpcionID] = op.Nombre
	}
	elementosCompleto := indexarElementosCompletoRuntime(rt.Estructura)
	bloqueos := make([]errorValidacionRegla, 0)
	for _, opcionID := range tocadas {
		propuestos := valoresPlanosOpcion(rt.Elementos, valoresActuales, global, opcionID)
		for _, p := range pendientes {
			if p.OpcionID != opcionID && !(p.OpcionID == "" && rt.Elementos[p.ElementoID].PadreOpcionesID == "") {
				continue
			}
			var decodificado any
			if err := json.Unmarshal(p.Valor, &decodificado); err == nil {
				propuestos[p.ElementoID] = decodificado
			}
		}
		for _, e := range evaluarValidacionReglas(propuestos, reglas) {
			e.Mensaje = fmt.Sprintf("%s (opción %s)", e.Mensaje, nombres[opcionID])
			bloqueos = append(bloqueos, e)
		}
		for campoID, estado := range evaluarEstadoCamposRegla(propuestos, reglas) {
			if estado.Visible && !estado.ForzarCero {
				continue
			}
			meta, existe := rt.Elementos[campoID]
			if !existe || meta.PadreOpcionesID != global || meta.Tipo != "CAMPO" {
				continue
			}
			valorForzado := valorForzadoPorRegla(elementosCompleto[campoID])
			if string(valorForzado) == "null" {
				continue // Campo de texto oculto: se conserva, no participa en cálculos.
			}
			clave := campoID + "\x00" + opcionID
			if i, tocado := indice[clave]; tocado {
				pendientes[i].Valor = valorForzado
			} else {
				indice[clave] = len(pendientes)
				pendientes = append(pendientes, valorPendienteRuntime{ElementoID: campoID, OpcionID: opcionID, Valor: valorForzado})
			}
		}
	}
	return pendientes, bloqueos, nil
}

// reglasSinCondicionPorOpcion filtra las reglas cuya condición lee un campo
// que varía por opción. Esas se evalúan SOLO en la pasada por opción: en la
// pasada global el campo es un mapa opcion_id -> valor (o no existe aún) y
// ESTA_VACIO dispararía en falso. Sin campos por opción devuelve lo mismo.
func reglasSinCondicionPorOpcion(reglas []reglaCotizadorEval, metadatos map[string]elementoRuntime) []reglaCotizadorEval {
	resultado := make([]reglaCotizadorEval, 0, len(reglas))
	for _, regla := range reglas {
		if metadatos[regla.CampoCondicionID].PadreOpcionesID == "" {
			resultado = append(resultado, regla)
		}
	}
	return resultado
}
