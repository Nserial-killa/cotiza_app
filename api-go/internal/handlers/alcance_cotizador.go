package handlers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Ronda F2 — alcance de las referencias de datos.
//
// Las REFERENCIAS DE DATOS (operandos de un Campo Calculado, tokens de una
// Fórmula Avanzada, campo fuente de una Caja de Valor, campo principal de
// Opciones de Propuesta en modo COTIZACION) tienen alcance de COTIZADOR:
// todas las secciones activas de la calculadora MÁS las asociadas vía
// tabs_cotizador_asociaciones (Secciones Adicionales), porque también
// forman parte del compilado. La CONTENCIÓN ESTRUCTURAL (componente padre
// CONTENEDOR u OPCIONES_PROPUESTA LOCAL, columnas CAMPO_EXISTENTE de una
// Tabla) sigue siendo local a la sección: eso es dónde se dibuja un
// elemento, no de dónde lee un dato.
//
// sqlTabEnAlcanceCotizador es el predicado compartido, con t = tabs_cotizador
// y $1 = calculadora_id. Es el mismo criterio que validarConfiguracion
// (compilador.go) usa para armar el compilado.
const sqlTabEnAlcanceCotizador = `t.activo AND (t.calculadora_id=$1 OR EXISTS(
	SELECT 1 FROM tabs_cotizador_asociaciones a WHERE a.tab_id=t.tab_id AND a.calculadora_id=$1))`

// calculadoraDeTab devuelve la calculadora dueña de una sección. Un
// elemento se valida siempre contra el alcance de SU dueño; al publicar un
// cotizador que la usa como Sección Adicional, el compilador vuelve a
// enlazar las fórmulas contra el alcance del cotizador que publica.
func calculadoraDeTab(ctx context.Context, q consultadorFila, tabID string) (string, error) {
	var calculadoraID string
	err := q.QueryRow(ctx, `SELECT calculadora_id FROM tabs_cotizador WHERE tab_id=$1`, tabID).Scan(&calculadoraID)
	return calculadoraID, err
}

// elementoEnAlcanceCotizador responde si elementoID vive en una sección del
// alcance de calculadoraID, y si está activo. existe=false cubre tanto un
// ID inexistente como uno de otro cotizador.
func elementoEnAlcanceCotizador(ctx context.Context, q consultadorFila, calculadoraID, elementoID string) (existe, activo bool, err error) {
	err = q.QueryRow(ctx, `
		SELECT e.activo FROM elementos_tab_cotizador e
		JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		WHERE e.elemento_id=$2 AND `+sqlTabEnAlcanceCotizador, calculadoraID, elementoID).Scan(&activo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, activo, err
}

// elementoConNombreInterno busca OTRO elemento activo del alcance del
// cotizador que ya use el mismo nombre_interno (§2.1 del documento ISA:
// códigos técnicos estables, únicos por cotizador). Devuelve "" si no hay.
// La comparación va en Go con nombreInternoElemento para respetar el mismo
// alias legado (nombre_elemento) que usan las fórmulas.
func elementoConNombreInterno(ctx context.Context, q consultadorRuntime, calculadoraID, elementoID, nombre string) (string, string, error) {
	rows, err := q.Query(ctx, `
		SELECT e.elemento_id, t.nombre, e.configuracion FROM elementos_tab_cotizador e
		JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		WHERE e.activo AND e.elemento_id<>$2 AND `+sqlTabEnAlcanceCotizador+`
		ORDER BY e.elemento_id`, calculadoraID, elementoID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id, seccion string
		var cfg map[string]any
		if err := rows.Scan(&id, &seccion, &cfg); err != nil {
			return "", "", err
		}
		if nombreInternoElemento(cfg) == nombre {
			return id, seccion, nil
		}
	}
	return "", "", rows.Err()
}

// duplicadosNombreInterno recorre el alcance completo de un cotizador y
// devuelve un mensaje legible por cada nombre_interno repetido entre
// elementos activos. Los datos anteriores a la Ronda F2 pueden traer
// duplicados entre secciones (antes el alcance era la sección): la migración
// no falla por eso, Validar/Publicar lo reporta como ERROR nombrando a los
// elementos involucrados para que el diseñador decida cuál renombrar.
//
// soloTabs, si no está vacío, limita el reporte a los nombres repetidos en
// los que participa al menos un elemento de esas secciones (lo usa Asociar
// para rechazar una Sección Adicional que choca con el cotizador, sin
// bloquearla por duplicados viejos que no tienen nada que ver con ella).
func duplicadosNombreInterno(ctx context.Context, q consultadorRuntime, calculadoraID string, soloTabs ...string) ([]string, error) {
	filtro := map[string]bool{}
	for _, tabID := range soloTabs {
		filtro[tabID] = true
	}
	rows, err := q.Query(ctx, `
		SELECT e.elemento_id, e.tab_id, t.nombre, e.configuracion FROM elementos_tab_cotizador e
		JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		WHERE e.activo AND e.tipo<>'SECCIONES_ADICIONALES' AND `+sqlTabEnAlcanceCotizador+`
		ORDER BY e.elemento_id`, calculadoraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type ubicacion struct {
		id, seccion string
		filtrada    bool
	}
	porNombre := map[string][]ubicacion{}
	for rows.Next() {
		var id, tabID, seccion string
		var cfg map[string]any
		if err := rows.Scan(&id, &tabID, &seccion, &cfg); err != nil {
			return nil, err
		}
		if nombre := nombreInternoElemento(cfg); nombre != "" {
			porNombre[nombre] = append(porNombre[nombre], ubicacion{id, fmt.Sprintf("%s (%s)", seccion, tabID), filtro[tabID]})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	nombres := make([]string, 0)
	for nombre, ubicaciones := range porNombre {
		if len(ubicaciones) < 2 {
			continue
		}
		incluir := len(filtro) == 0
		for _, u := range ubicaciones {
			incluir = incluir || u.filtrada
		}
		if incluir {
			nombres = append(nombres, nombre)
		}
	}
	sort.Strings(nombres)
	mensajes := make([]string, 0, len(nombres))
	for _, nombre := range nombres {
		partes := make([]string, 0, len(porNombre[nombre]))
		for _, u := range porNombre[nombre] {
			partes = append(partes, fmt.Sprintf("%s en la sección %s", u.id, u.seccion))
		}
		mensajes = append(mensajes, fmt.Sprintf("El nombre interno %s está repetido en el cotizador: %s. Renombre uno para que las fórmulas y la plantilla no queden ambiguas.", nombre, strings.Join(partes, " y ")))
	}
	return mensajes, nil
}

// alcanceOpcionesPropuesta normaliza configuracion.alcance_opciones de un
// OPCIONES_PROPUESTA: LOCAL (default, comportamiento previo: solo sus hijos
// varían por opción) o COTIZACION (escenarios de la cotización completa).
func alcanceOpcionesPropuesta(cfg map[string]any) string {
	if cfg == nil {
		return "LOCAL"
	}
	valor := strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["alcance_opciones"])))
	if valor == "COTIZACION" {
		return "COTIZACION"
	}
	return "LOCAL"
}

// tiposConValorPorOpcionCotizacion son los elementos que, bajo un
// OPCIONES_PROPUESTA en modo COTIZACION, pasan a tener un valor POR OPCIÓN:
// los cuatro con valor propio (CAMPO, CAMPO_CATALOGO, LISTA_PRECIOS, TABLA)
// y los Campos Calculados, que se evalúan por opción con esos valores.
var tiposConValorPorOpcionCotizacion = map[string]bool{
	"CAMPO": true, "CAMPO_CATALOGO": true, "LISTA_PRECIOS": true, "TABLA": true, "CAMPO_CALCULADO": true,
}

// padreOpcionesCotizacion devuelve el elemento_id del OPCIONES_PROPUESTA en
// modo COTIZACION de una estructura compilada, o "" si no hay. El
// compilador garantiza que haya a lo sumo uno.
func padreOpcionesCotizacion(estructura map[string]any) string {
	for _, padre := range elementosOpcionesPropuesta(estructura) {
		cfg, _ := padre["configuracion"].(map[string]any)
		if alcanceOpcionesPropuesta(cfg) == "COTIZACION" {
			return strings.TrimSpace(fmt.Sprint(padre["elemento_id"]))
		}
	}
	return ""
}

// otroOpcionesCotizacion busca otro OPCIONES_PROPUESTA activo en modo
// COTIZACION dentro del alcance del cotizador. Solo puede haber uno.
func otroOpcionesCotizacion(ctx context.Context, q consultadorRuntime, calculadoraID, elementoID string) (string, error) {
	rows, err := q.Query(ctx, `
		SELECT e.elemento_id, e.configuracion FROM elementos_tab_cotizador e
		JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		WHERE e.activo AND e.tipo='OPCIONES_PROPUESTA' AND e.elemento_id<>$2 AND `+sqlTabEnAlcanceCotizador+`
		ORDER BY e.elemento_id`, calculadoraID, elementoID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var cfg map[string]any
		if err := rows.Scan(&id, &cfg); err != nil {
			return "", err
		}
		if alcanceOpcionesPropuesta(cfg) == "COTIZACION" {
			return id, nil
		}
	}
	return "", rows.Err()
}

// validarOpcionesCotizacionCompiladas revisa, contra las tabs ya armadas
// por el compilador, las dos reglas del modo COTIZACION: a lo sumo uno por
// cotizador y sin hijos (todos los campos ya varían por opción; un hijo
// sería un segundo nivel de escenarios que el runtime no sabría resolver).
func validarOpcionesCotizacionCompiladas(tabs []tabCompilado) []string {
	errores := make([]string, 0)
	globales := make([]string, 0)
	padresCotizacion := map[string]bool{}
	for _, tab := range tabs {
		for _, el := range tab.Elementos {
			if el.Tipo == "OPCIONES_PROPUESTA" && alcanceOpcionesPropuesta(el.Configuracion) == "COTIZACION" {
				globales = append(globales, el.ElementoID)
				padresCotizacion[el.ElementoID] = true
			}
		}
	}
	if len(globales) > 1 {
		errores = append(errores, fmt.Sprintf("Solo puede haber una Opciones de Propuesta con alcance COTIZACION por cotizador; hay %d: %s.", len(globales), strings.Join(globales, ", ")))
	}
	for _, tab := range tabs {
		for _, el := range tab.Elementos {
			if padresCotizacion[el.componentePadreID] {
				errores = append(errores, fmt.Sprintf("El elemento %s está dentro de %s, que tiene alcance COTIZACION y no admite hijos: todos los campos del cotizador ya varían por opción. Muévalo fuera del componente.", el.ElementoID, el.componentePadreID))
			}
		}
	}
	return errores
}
