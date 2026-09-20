package handlers

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// plantilla_renderizador.go arma la propuesta real (cuarto y último hueco
// del documento de definición funcional del jefe, caso ISA Custom): toma
// una plantilla ya diseñada (secciones/bloques/vinculaciones/condiciones/
// columnas — Ronda de Plantillas) y la resuelve contra una cotización real.
// No existía ningún código que hiciera esto todavía: el editor de
// plantillas solo mostraba una vista esquemática (títulos y tipos de
// bloque, sin datos) y el enlace público mostraba las tabs crudas del
// cotizador, sin pasar por ninguna plantilla — ver el comentario de
// paquete de enlaces_publicos.go, que hoy llama a este renderer primero y
// cae a las tabs crudas solo si el cotizador no tiene una plantilla
// publicada que aplique.

// plantillaRenderizada es la propuesta ya resuelta: condiciones evaluadas
// (un bloque que no cumple su condición ni siquiera aparece) y, para una
// TABLA_INVERSION con origen_filas=OPCIONES_PROPUESTA, una fila por cada
// Opción de Propuesta con SUS PROPIOS valores — nunca el mismo valor
// repetido en las N filas.
type plantillaRenderizada struct {
	PlantillaID string               `json:"plantilla_id"`
	Nombre      string               `json:"nombre"`
	Secciones   []seccionRenderizada `json:"secciones"`
}

type seccionRenderizada struct {
	SeccionID string              `json:"seccion_id"`
	Titulo    string              `json:"titulo"`
	Bloques   []bloqueRenderizado `json:"bloques"`
}

type bloqueRenderizado struct {
	BloqueID   string   `json:"bloque_id"`
	TipoBloque string   `json:"tipo_bloque"`
	Titulo     string   `json:"titulo,omitempty"`
	Contenido  string   `json:"contenido,omitempty"`
	Valor      any      `json:"valor,omitempty"`
	Columnas   []string `json:"columnas,omitempty"`
	Filas      [][]any  `json:"filas,omitempty"`
}

// renderizarPlantillaCotizacion busca la plantilla Publicada asociada al
// cotizador de la cotización (y a su tipo_propuesta, si la plantilla
// restringe tipos) y arma la propuesta resuelta contra los datos reales de
// esa cotización+versión. Devuelve (nil, nil) cuando ningún cotizador tiene
// una plantilla publicada que aplique — eso no es un error, es el estado
// normal de un cotizador al que todavía no le armaron una plantilla.
//
// Selección de plantilla (decisión de producto documentada acá porque no
// hay ningún plantilla_id en cotizaciones/cotizacion_versiones que la haga
// explícita — nunca se agregó esa relación): entre las plantillas
// Publicadas asociadas al cotizador de la cotización, se prefieren las que
// restringen tipo_propuesta y coinciden con el de la cotización sobre las
// que aplican a cualquier tipo; en caso de empate, la más recientemente
// actualizada.
func renderizarPlantillaCotizacion(ctx context.Context, db *pgxpool.Pool, cotizacionID string, version int) (*plantillaRenderizada, error) {
	var calculadoraID, tipoPropuesta string
	if err := db.QueryRow(ctx, `SELECT calculadora_id, COALESCE(tipo_propuesta,'') FROM cotizaciones WHERE cotizacion_id=$1`,
		cotizacionID).Scan(&calculadoraID, &tipoPropuesta); err != nil {
		return nil, err
	}

	var plantillaID, nombre string
	err := db.QueryRow(ctx, `
		SELECT p.plantilla_id::text, p.nombre
		  FROM plantillas p
		  JOIN plantilla_calculadoras pc ON pc.plantilla_id = p.plantilla_id
		 WHERE pc.calculadora_id = $1 AND p.estado = 'Publicada'
		   AND (NOT EXISTS(SELECT 1 FROM plantilla_tipos_propuesta pt WHERE pt.plantilla_id = p.plantilla_id)
		        OR EXISTS(SELECT 1 FROM plantilla_tipos_propuesta pt WHERE pt.plantilla_id = p.plantilla_id AND pt.tipo_propuesta = $2))
		 ORDER BY EXISTS(SELECT 1 FROM plantilla_tipos_propuesta pt WHERE pt.plantilla_id = p.plantilla_id) DESC,
		          p.fecha_actualizacion DESC
		 LIMIT 1`, calculadoraID, tipoPropuesta).Scan(&plantillaID, &nombre)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	rt := &CotizadorRuntimeHandler{DB: db}
	runtime, err := rt.cargarContexto(ctx, cotizacionID, version, false)
	if err != nil {
		return nil, err
	}
	// Crea las opciones en la primera apertura igual que el Motor de
	// Ejecución (Obtener) — un cliente puede abrir el link público antes de
	// que nadie haya abierto el cotizador todavía.
	if err := rt.asegurarOpcionesPropuesta(ctx, &runtime); err != nil {
		return nil, err
	}
	valores, err := rt.leerValores(ctx, db, cotizacionID, runtime.Version)
	if err != nil {
		return nil, err
	}
	elementosPorID := indexarElementosCompletoRuntime(runtime.Estructura)
	resolverCamposCalculados(elementosPorID, valores)
	resolverCamposCalculadosPorOpcion(elementosPorID, runtime.Elementos, valores)

	base, err := valoresBaseCotizacion(ctx, db, cotizacionID, runtime.Version)
	if err != nil {
		return nil, err
	}
	condicionValores := valoresParaCondicionPlantilla(elementosPorID, runtime.Elementos, valores, base)
	porNombreInterno := elementosPorNombreInterno(elementosPorID)

	secciones, err := renderizarSeccionesPlantilla(ctx, db, plantillaID, elementosPorID, runtime.Elementos, valores, base, condicionValores, porNombreInterno)
	if err != nil {
		return nil, err
	}
	return &plantillaRenderizada{PlantillaID: plantillaID, Nombre: nombre, Secciones: secciones}, nil
}

// valoresBaseCotizacion arma el mapa de las 9 fuentes COTIZACION_BASE fijas
// (ver fuentesCotizacionBase en plantilla_vinculaciones.go) para UNA
// cotización+versión puntual.
func valoresBaseCotizacion(ctx context.Context, db *pgxpool.Pool, cotizacionID string, version int) (map[string]any, error) {
	var codigoOferta, tipoPropuesta, cliente, empresa *string
	var estado, moneda string
	var totalPrecio float64
	var fechaCreacion time.Time
	err := db.QueryRow(ctx, `
		SELECT c.codigo_oferta, c.tipo_propuesta, cl.nombre_comercial, COALESCE(cl.razon_social, cl.nombre_comercial),
		       cv.estado, cv.moneda, cv.total_precio, c.fecha_creacion
		  FROM cotizaciones c
		  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id = c.cotizacion_id AND cv.numero_version = $2
		 WHERE c.cotizacion_id = $1`,
		cotizacionID, version,
	).Scan(&codigoOferta, &tipoPropuesta, &cliente, &empresa, &estado, &moneda, &totalPrecio, &fechaCreacion)
	if err != nil {
		return nil, err
	}
	vendedor, _, _, err := (&CotizacionesHandler{DB: db}).consultarPersonasAsignadas(ctx, cotizacionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"cliente":        valorTexto(cliente),
		"empresa":        valorTexto(empresa),
		"codigo_oferta":  valorTexto(codigoOferta),
		"tipo_propuesta": valorTexto(tipoPropuesta),
		"total_precio":   totalPrecio,
		"moneda":         moneda,
		"fecha_creacion": fechaCreacion.Format("2006-01-02"),
		"vendedor":       valorTexto(vendedor),
		"estado":         estado,
	}, nil
}

// valoresParaCondicionPlantilla arma el mapa elemento_id/clave_base -> valor
// que evaluarCondicionRegla (reglas_evaluacion.go) espera, reusando el mismo
// motor de evaluación que la Ronda de Reglas en vez de escribir uno nuevo.
// Un campo anidado bajo Opciones de Propuesta se deja fuera a propósito
// (mismo criterio que la Ronda de Reglas con PadreOpcionesID, ver el
// comentario de GuardarValores en cotizador_runtime.go): no tiene un único
// valor global con el que condicionar un bloque de toda la sección.
func valoresParaCondicionPlantilla(elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any, base map[string]any) map[string]any {
	resultado := make(map[string]any, len(elementosPorID)+len(base))
	for id, elemento := range elementosPorID {
		if metadatos[id].PadreOpcionesID != "" {
			continue
		}
		tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
		if tipo == "CAMPO_CALCULADO" || tipo == "LISTA_PRECIOS" || tipo == "TABLA" {
			resultado[id] = elemento["valor_resuelto"]
			continue
		}
		resultado[id] = valores[id]
	}
	for clave, valor := range base {
		resultado[clave] = valor
	}
	return resultado
}

func elementosPorNombreInterno(elementosPorID map[string]map[string]any) map[string]string {
	resultado := make(map[string]string, len(elementosPorID))
	for id, elemento := range elementosPorID {
		cfg, _ := elemento["configuracion"].(map[string]any)
		if nombre := nombreInternoElemento(cfg); nombre != "" {
			resultado[nombre] = id
		}
	}
	return resultado
}

// patronTokenPlantilla reconoce "[NOMBRE_INTERNO]" dentro del contenido
// libre de un bloque TEXTO/LISTA/CONDICIONES — ejemplo real del documento:
// "La solución contempla [CANTIDAD_INTEGRACIONES] integración(es)...".
var patronTokenPlantilla = regexp.MustCompile(`\[([A-Za-z_][A-Za-z0-9_]*)\]`)

// interpolarTextoPlantilla reemplaza cada token por el valor resuelto del
// elemento con ese nombre_interno. Un token que no coincide con ningún
// elemento, o cuyo valor todavía no está resuelto, se deja tal cual en vez
// de inventar un texto — más "controlado" es un placeholder visible (que el
// equipo de Cotiza puede notar y corregir en el diseño) que un dato falso.
func interpolarTextoPlantilla(texto string, porNombreInterno map[string]string, condicionValores map[string]any) string {
	return patronTokenPlantilla.ReplaceAllStringFunc(texto, func(coincidencia string) string {
		nombre := coincidencia[1 : len(coincidencia)-1]
		id, existe := porNombreInterno[nombre]
		if !existe {
			return coincidencia
		}
		valor, existeValor := condicionValores[id]
		if !existeValor || valor == nil {
			return coincidencia
		}
		return valorComoTextoRegla(valor)
	})
}

type seccionPlantillaCruda struct {
	SeccionID string
	Titulo    string
}

func renderizarSeccionesPlantilla(ctx context.Context, db *pgxpool.Pool, plantillaID string,
	elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any,
	base map[string]any, condicionValores map[string]any, porNombreInterno map[string]string,
) ([]seccionRenderizada, error) {
	rows, err := db.Query(ctx, `
		SELECT seccion_id::text, COALESCE(NULLIF(titulo,''), nombre)
		  FROM plantilla_secciones WHERE plantilla_id::text=$1 ORDER BY orden, seccion_id`, plantillaID)
	if err != nil {
		return nil, err
	}
	crudas := make([]seccionPlantillaCruda, 0)
	for rows.Next() {
		var s seccionPlantillaCruda
		if err := rows.Scan(&s.SeccionID, &s.Titulo); err != nil {
			rows.Close()
			return nil, err
		}
		crudas = append(crudas, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	secciones := make([]seccionRenderizada, 0, len(crudas))
	for _, s := range crudas {
		bloques, err := renderizarBloquesPlantilla(ctx, db, s.SeccionID, elementosPorID, metadatos, valores, base, condicionValores, porNombreInterno)
		if err != nil {
			return nil, err
		}
		secciones = append(secciones, seccionRenderizada{SeccionID: s.SeccionID, Titulo: s.Titulo, Bloques: bloques})
	}
	return secciones, nil
}

type bloquePlantillaCrudo struct {
	BloqueID    string
	TipoBloque  string
	Titulo      string
	Contenido   string
	OrigenFilas string
}

func renderizarBloquesPlantilla(ctx context.Context, db *pgxpool.Pool, seccionID string,
	elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any,
	base map[string]any, condicionValores map[string]any, porNombreInterno map[string]string,
) ([]bloqueRenderizado, error) {
	rows, err := db.Query(ctx, `
		SELECT bloque_id::text, tipo_bloque, COALESCE(titulo,''), COALESCE(contenido,''), origen_filas
		  FROM plantilla_bloques WHERE seccion_id::text=$1 AND mostrar_web=true ORDER BY orden, bloque_id`, seccionID)
	if err != nil {
		return nil, err
	}
	crudos := make([]bloquePlantillaCrudo, 0)
	for rows.Next() {
		var b bloquePlantillaCrudo
		if err := rows.Scan(&b.BloqueID, &b.TipoBloque, &b.Titulo, &b.Contenido, &b.OrigenFilas); err != nil {
			rows.Close()
			return nil, err
		}
		crudos = append(crudos, b)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	bloques := make([]bloqueRenderizado, 0, len(crudos))
	for _, b := range crudos {
		visible, err := condicionBloqueSeCumple(ctx, db, b.BloqueID, condicionValores)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		renderizado := bloqueRenderizado{BloqueID: b.BloqueID, TipoBloque: b.TipoBloque, Titulo: b.Titulo}
		switch b.TipoBloque {
		case "CAMPO_VINCULADO":
			valor, err := valorVinculacionPlantilla(ctx, db, b.BloqueID, elementosPorID, valores, base)
			if err != nil {
				return nil, err
			}
			renderizado.Valor = valor
		case "TABLA_INVERSION":
			columnas, filas, err := filasTablaInversionPlantilla(ctx, db, b.BloqueID, b.OrigenFilas, elementosPorID, metadatos, valores, base)
			if err != nil {
				return nil, err
			}
			renderizado.Columnas = columnas
			renderizado.Filas = filas
		default: // TEXTO, LISTA, CONDICIONES, IMAGEN: contenido libre, sin fuente.
			renderizado.Contenido = interpolarTextoPlantilla(b.Contenido, porNombreInterno, condicionValores)
		}
		bloques = append(bloques, renderizado)
	}
	return bloques, nil
}

// condicionBloqueSeCumple consulta la condición del bloque para TODAS las
// calculadoras asociadas a él (un bloque puede repetirse en varios
// cotizadores de la misma plantilla, cada uno con su propia condición) y
// evalúa la que corresponde a la fuente que existe en condicionValores. En
// la práctica una plantilla se renderiza contra la cotización de un único
// cotizador, así que basta con probar cada condición configurada hasta
// encontrar una cuya fuente resuelva — si ninguna aplica, el bloque se
// muestra (sin condición configurada, el default sigue siendo visible).
func condicionBloqueSeCumple(ctx context.Context, db *pgxpool.Pool, bloqueID string, condicionValores map[string]any) (bool, error) {
	rows, err := db.Query(ctx, `
		SELECT fuente_id, operador, COALESCE(valor_comparacion,'')
		  FROM plantilla_bloque_condiciones WHERE bloque_id::text=$1`, bloqueID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	tieneCondicion := false
	for rows.Next() {
		var fuenteID, operador, valorComparacion string
		if err := rows.Scan(&fuenteID, &operador, &valorComparacion); err != nil {
			return false, err
		}
		if _, existe := condicionValores[fuenteID]; !existe {
			continue
		}
		tieneCondicion = true
		regla := reglaCotizadorEval{CampoCondicionID: fuenteID, Operador: operador, ValorComparacion: valorComparacion}
		if evaluarCondicionRegla(condicionValores, regla) {
			return true, rows.Err()
		}
		return false, rows.Err()
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return !tieneCondicion, nil
}

func valorVinculacionPlantilla(ctx context.Context, db *pgxpool.Pool, bloqueID string,
	elementosPorID map[string]map[string]any, valores map[string]any, base map[string]any,
) (any, error) {
	var fuenteTipo, fuenteID string
	err := db.QueryRow(ctx, `
		SELECT fuente_tipo, fuente_id FROM plantilla_vinculaciones WHERE bloque_id::text=$1 LIMIT 1`,
		bloqueID).Scan(&fuenteTipo, &fuenteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return resolverValorFuentePlantilla(ctx, db, fuenteTipo, fuenteID, elementosPorID, valores, base)
}

// resolverValorFuentePlantilla resuelve una fuente CAMPO/COTIZACION_BASE
// contra los datos globales de la cotización (no por opción — para eso ver
// filasTablaInversionPlantilla). Reusa exactamente el mismo valor que ya
// calculó resolverCamposCalculados para un Campo Calculado/Lista de
// Precios/Tabla, y traduce un Campo Catálogo a su texto_visible, igual
// criterio que enlaces_publicos.go: nunca el código interno.
func resolverValorFuentePlantilla(ctx context.Context, db *pgxpool.Pool, fuenteTipo, fuenteID string,
	elementosPorID map[string]map[string]any, valores map[string]any, base map[string]any,
) (any, error) {
	if fuenteTipo == "COTIZACION_BASE" {
		return base[fuenteID], nil
	}
	elemento, existe := elementosPorID[fuenteID]
	if !existe {
		return nil, nil
	}
	tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
	switch tipo {
	case "CAMPO_CALCULADO", "LISTA_PRECIOS", "TABLA":
		return elemento["valor_resuelto"], nil
	case "CAMPO_CATALOGO":
		catalogoID := strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"]))
		return textoVisibleCatalogo(ctx, db, catalogoID, valores[fuenteID])
	default:
		return valores[fuenteID], nil
	}
}

func textoVisibleCatalogo(ctx context.Context, db *pgxpool.Pool, catalogoID string, valorSeleccionado any) (any, error) {
	codigo := strings.TrimSpace(fmt.Sprint(valorSeleccionado))
	if codigo == "" || codigo == "<nil>" || catalogoID == "" {
		return valorSeleccionado, nil
	}
	var texto string
	err := db.QueryRow(ctx, `SELECT texto_visible FROM catalogo_valores WHERE catalogo_id=$1 AND valor_sistema=$2`,
		catalogoID, codigo).Scan(&texto)
	if errors.Is(err, pgx.ErrNoRows) {
		return valorSeleccionado, nil
	}
	if err != nil {
		return nil, err
	}
	return texto, nil
}

// filasTablaInversionPlantilla arma columnas + filas de un bloque
// TABLA_INVERSION. FIJO da siempre una sola fila (los valores globales de
// la cotización); OPCIONES_PROPUESTA da una fila por cada Opción de
// Propuesta del ÚNICO componente OPCIONES_PROPUESTA del cotizador (ver el
// comentario de simplificación abajo), con NOMBRE_ESCENARIO/ES_RECOMENDADA
// resueltos desde cotizacion_opciones y CAMPO/COTIZACION_BASE resueltos
// PARA ESA OPCIÓN puntual cuando el campo vive anidado bajo ese componente.
func filasTablaInversionPlantilla(ctx context.Context, db *pgxpool.Pool, bloqueID, origenFilas string,
	elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any, base map[string]any,
) ([]string, [][]any, error) {
	rows, err := db.Query(ctx, `
		SELECT titulo, fuente_tipo, COALESCE(fuente_id,'')
		  FROM plantilla_tabla_columnas WHERE bloque_id::text=$1 ORDER BY orden, columna_id`, bloqueID)
	if err != nil {
		return nil, nil, err
	}
	type columnaCruda struct{ Titulo, FuenteTipo, FuenteID string }
	crudas := make([]columnaCruda, 0)
	for rows.Next() {
		var c columnaCruda
		if err := rows.Scan(&c.Titulo, &c.FuenteTipo, &c.FuenteID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		crudas = append(crudas, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	titulos := make([]string, len(crudas))
	for i, c := range crudas {
		titulos[i] = c.Titulo
	}

	if origenFilas != "OPCIONES_PROPUESTA" {
		fila := make([]any, len(crudas))
		for i, c := range crudas {
			if c.FuenteTipo == "NOMBRE_ESCENARIO" || c.FuenteTipo == "ES_RECOMENDADA" {
				continue // Sin opción de propuesta no hay escenario que nombrar.
			}
			valor, err := resolverValorFuentePlantilla(ctx, db, c.FuenteTipo, c.FuenteID, elementosPorID, valores, base)
			if err != nil {
				return nil, nil, err
			}
			fila[i] = valor
		}
		return titulos, [][]any{fila}, nil
	}

	// Simplificación deliberada: se toma el único componente OPCIONES_
	// PROPUESTA del cotizador (orden más bajo si hubiera más de uno). El
	// esquema no impide varios, pero nada en el documento del jefe ni en el
	// resto del producto distingue "cuál" cuando una tabla no está ligada a
	// un componente puntual — ver docs/FORMULAS_AVANZADAS.md para el mismo
	// criterio de acotar la gramática a los casos reales, no a lo posible.
	padreID, opciones := unicoComponenteOpcionesPropuesta(elementosPorID)
	if padreID == "" {
		return titulos, [][]any{}, nil
	}
	filas := make([][]any, 0, len(opciones))
	for _, opcion := range opciones {
		fila := make([]any, len(crudas))
		for i, c := range crudas {
			switch c.FuenteTipo {
			case "NOMBRE_ESCENARIO":
				fila[i] = opcion.Nombre
			case "ES_RECOMENDADA":
				fila[i] = opcion.EsRecomendada
			case "COTIZACION_BASE":
				fila[i] = base[c.FuenteID]
			case "CAMPO":
				valor, err := valorPorOpcionPlantilla(c.FuenteID, opcion.OpcionID, padreID, elementosPorID, metadatos, valores, ctx, db)
				if err != nil {
					return nil, nil, err
				}
				fila[i] = valor
			}
		}
		filas = append(filas, fila)
	}
	return titulos, filas, nil
}

// unicoComponenteOpcionesPropuesta devuelve el elemento_id y las opciones ya
// resueltas (asegurarOpcionesPropuesta las deja en padre["opciones"]) del
// componente OPCIONES_PROPUESTA de menor "orden" de la estructura.
func unicoComponenteOpcionesPropuesta(elementosPorID map[string]map[string]any) (string, []cotizacionOpcion) {
	mejorID := ""
	mejorOrden := 0
	var mejorOpciones []cotizacionOpcion
	primero := true
	for id, elemento := range elementosPorID {
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) != "OPCIONES_PROPUESTA" {
			continue
		}
		opciones, _ := elemento["opciones"].([]cotizacionOpcion)
		orden, _ := enteroDesdeConfiguracion(elemento, "orden")
		if primero || orden < mejorOrden {
			mejorID, mejorOrden, mejorOpciones, primero = id, orden, opciones, false
		}
	}
	return mejorID, mejorOpciones
}

// valorPorOpcionPlantilla resuelve una columna CAMPO para una opción
// puntual: si el campo vive anidado bajo el MISMO componente Opciones de
// Propuesta que generó esta fila, usa su valor por opción (mismo mapa que
// resolverCamposCalculadosPorOpcion ya construyó); si no, es un campo
// externo/global y su valor se repite en todas las filas — mismo criterio
// que un dato COTIZACION_BASE, que tampoco varía por escenario.
func valorPorOpcionPlantilla(fuenteID, opcionID, padreID string, elementosPorID map[string]map[string]any,
	metadatos map[string]elementoRuntime, valores map[string]any, ctx context.Context, db *pgxpool.Pool,
) (any, error) {
	elemento, existe := elementosPorID[fuenteID]
	if !existe {
		return nil, nil
	}
	if metadatos[fuenteID].PadreOpcionesID != padreID {
		return resolverValorFuentePlantilla(ctx, db, "CAMPO", fuenteID, elementosPorID, valores, nil)
	}
	tipo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"])))
	if tipo == "CAMPO_CALCULADO" || tipo == "LISTA_PRECIOS" || tipo == "TABLA" {
		porOpcion, _ := elemento["valores_resueltos_por_opcion"].(map[string]any)
		return porOpcion[opcionID], nil
	}
	porOpcion, _ := valores[fuenteID].(map[string]any)
	valorSeleccionado := porOpcion[opcionID]
	if tipo == "CAMPO_CATALOGO" {
		catalogoID := strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"]))
		return textoVisibleCatalogo(ctx, db, catalogoID, valorSeleccionado)
	}
	return valorSeleccionado, nil
}
