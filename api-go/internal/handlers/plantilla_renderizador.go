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

// bloqueRenderizado es un bloque ya resuelto. Qué claves trae depende del
// tipo (Ronda P1, 14 tipos de la paleta):
//
//	contenido  TEXTO, ENCABEZADO, RESUMEN_EJECUTIVO, IMAGEN (URL), y el
//	           texto libre de PORTADA (subtítulo/viñetas) y FIRMA_ACEPTACION
//	campos     PORTADA, DATOS_CLIENTE, GRUPO_INFORMACION,
//	           CONDICIONES_COMERCIALES, FIRMA_ACEPTACION (pares Etiqueta/Valor)
//	columnas   TABLA_INVERSION, TABLA_DATOS, OPCIONES_PROPUESTA
//	+ filas
//	items      LISTA_PRECIOS
//	valor      CAMPO_VINCULADO (heredado)
//
// SALTO_PAGINA no trae nada más que su tipo: el salto en sí es el bloque.
type bloqueRenderizado struct {
	BloqueID   string                        `json:"bloque_id"`
	TipoBloque string                        `json:"tipo_bloque"`
	Titulo     string                        `json:"titulo,omitempty"`
	Contenido  string                        `json:"contenido,omitempty"`
	Valor      any                           `json:"valor,omitempty"`
	Columnas   []string                      `json:"columnas,omitempty"`
	Filas      [][]any                       `json:"filas,omitempty"`
	Campos     []campoRenderizado            `json:"campos,omitempty"`
	Items      []itemListaPreciosRenderizado `json:"items,omitempty"`
}

// campoRenderizado es un par Etiqueta/Valor de plantilla_bloque_campos ya
// resuelto contra la cotización. valor queda null si la fuente no tiene
// dato todavía — el documento lo muestra como "—", nunca inventa uno.
type campoRenderizado struct {
	Etiqueta string `json:"etiqueta"`
	Valor    any    `json:"valor"`
}

// itemListaPreciosRenderizado es la ÚNICA forma en que un ítem de Lista de
// Precios llega a la propuesta. Es un struct tipado a propósito, con los
// campos públicos contados uno a uno: costo_interno y margen_porcentaje no
// existen acá, así que no hay forma de que viajen al cliente aunque algún
// día aparecieran en la estructura compilada (que ya los excluye, ver
// incluirItemsListaPrecios en compilador.go).
type itemListaPreciosRenderizado struct {
	Codigo       string   `json:"codigo"`
	Nombre       string   `json:"nombre"`
	Descripcion  string   `json:"descripcion,omitempty"`
	Precio       float64  `json:"precio"`
	Moneda       string   `json:"moneda"`
	UnidadCobro  string   `json:"unidad_cobro,omitempty"`
	Seleccionado bool     `json:"seleccionado"`
	Cantidad     *float64 `json:"cantidad,omitempty"`
}

// contextoRenderPlantilla agrupa todo lo que una cotización aporta al
// renderizar sus bloques — ya resuelto una sola vez en
// renderizarPlantillaCotizacion.
type contextoRenderPlantilla struct {
	db *pgxpool.Pool
	// calculadoraID es el cotizador de la cotización: de todas las
	// vinculaciones/columnas/campos por cotizador de un bloque, solo valen
	// las suyas.
	calculadoraID  string
	elementosPorID map[string]map[string]any
	metadatos      map[string]elementoRuntime
	// valores son los originales (por opción, para la tabla de escenarios);
	// valoresEfectivos, los de la opción recomendada (resto del documento).
	valores          map[string]any
	valoresEfectivos map[string]any
	base             map[string]any
	// salidas son las Salidas estándar PÚBLICAS de esta versión, leídas de
	// cotizacion_salidas (salidasPublicasCotizacion).
	salidas          map[string]any
	condicionValores map[string]any
	porNombreInterno map[string]string
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
	var valores map[string]any
	if runtime.Snapshot != nil {
		valores = runtime.Snapshot.Valores
	} else {
		if !runtime.Historica {
			if err := rt.asegurarOpcionesPropuesta(ctx, &runtime); err != nil {
				return nil, err
			}
		}
		valores, err = rt.leerValores(ctx, db, cotizacionID, runtime.Version)
		if err != nil {
			return nil, err
		}
	}
	elementosPorID := indexarElementosCompletoRuntime(runtime.Estructura)
	if runtime.Snapshot == nil {
		reglas, err := reglasCotizadorParaEvaluar(ctx, db, runtime.CalculadoraID)
		if err != nil {
			return nil, err
		}
		resolverCamposCalculados(elementosPorID, valores)
		resolverCamposCalculadosPorOpcionConReglas(elementosPorID, runtime.Elementos, valores, reglas)
	} else if !runtime.Historica {
		// El snapshot trae las opciones congeladas; en una versión viva se
		// relee la recomendada actual (pudo cambiar después del guardado).
		for _, padre := range elementosOpcionesPropuesta(runtime.Estructura) {
			opciones, err := listarOpcionesPropuesta(ctx, db, cotizacionID, runtime.Version, fmt.Sprint(padre["elemento_id"]))
			if err != nil {
				return nil, err
			}
			padre["opciones"] = opciones
		}
	}

	base, err := valoresBaseCotizacion(ctx, db, cotizacionID, runtime.Version)
	if err != nil {
		return nil, err
	}
	// Modo COTIZACION (Ronda F2): fuera de la tabla de escenarios, la oferta
	// habla de la opción recomendada (§9.5). La tabla sigue recibiendo los
	// valores por opción originales y resuelve cada fila con los suyos.
	valoresEfectivos, metadatosEfectivos := proyectarOpcionEfectiva(elementosPorID, runtime.Elementos, valores)
	condicionValores := valoresParaCondicionPlantilla(elementosPorID, metadatosEfectivos, valoresEfectivos, base)
	porNombreInterno := elementosPorNombreInterno(elementosPorID)

	salidas, err := salidasPublicasCotizacion(ctx, db, cotizacionID, runtime.Version)
	if err != nil {
		return nil, err
	}
	rc := &contextoRenderPlantilla{
		db: db, calculadoraID: runtime.CalculadoraID, elementosPorID: elementosPorID,
		metadatos: runtime.Elementos, valores: valores, valoresEfectivos: valoresEfectivos,
		base: base, salidas: salidas, condicionValores: condicionValores, porNombreInterno: porNombreInterno,
	}
	secciones, err := rc.renderizarSecciones(ctx, plantillaID)
	if err != nil {
		return nil, err
	}
	return &plantillaRenderizada{PlantillaID: plantillaID, Nombre: nombre, Secciones: secciones}, nil
}

// valoresBaseCotizacion arma el mapa de las fuentes COTIZACION_BASE fijas
// (ver fuentesCotizacionBase en plantilla_vinculaciones.go) para UNA
// cotización+versión puntual.
func valoresBaseCotizacion(ctx context.Context, db *pgxpool.Pool, cotizacionID string, version int) (map[string]any, error) {
	var codigoOferta, tipoPropuesta, cliente, empresa, contacto, correo, telefono, cargo *string
	var estado, moneda string
	var totalPrecio float64
	var fechaCreacion time.Time
	err := db.QueryRow(ctx, `
		SELECT c.codigo_oferta, c.tipo_propuesta, cl.nombre_comercial, COALESCE(cl.razon_social, cl.nombre_comercial),
		       cv.estado, cv.moneda, cv.total_precio, c.fecha_creacion,
		       ct.nombre, ct.correo, ct.telefono, ct.cargo
		  FROM cotizaciones c
		  LEFT JOIN clientes cl ON cl.cliente_id = c.cliente_id
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id = c.cotizacion_id AND cv.numero_version = $2
		  -- Portada ISA §9: el contacto principal del cliente; si no hay uno
		  -- marcado, el activo más antiguo. Correo/teléfono/cargo son del
		  -- MISMO contacto (Datos del cliente, Firma y aceptación).
		  LEFT JOIN LATERAL (
		       SELECT cc.nombre, cc.correo, cc.telefono, cc.cargo FROM cliente_contactos cc
		        WHERE cc.cliente_id = c.cliente_id AND cc.estado = 'Activo'
		        ORDER BY cc.contacto_principal DESC, cc.fecha_creacion, cc.contacto_id LIMIT 1) ct ON true
		 WHERE c.cotizacion_id = $1`,
		cotizacionID, version,
	).Scan(&codigoOferta, &tipoPropuesta, &cliente, &empresa, &estado, &moneda, &totalPrecio, &fechaCreacion,
		&contacto, &correo, &telefono, &cargo)
	if err != nil {
		return nil, err
	}
	vendedor, _, _, err := (&CotizacionesHandler{DB: db}).consultarPersonasAsignadas(ctx, cotizacionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"cliente":           valorTexto(cliente),
		"empresa":           valorTexto(empresa),
		"contacto":          valorTexto(contacto),
		"contacto_correo":   valorTexto(correo),
		"contacto_telefono": valorTexto(telefono),
		"contacto_cargo":    valorTexto(cargo),
		"codigo_oferta":     valorTexto(codigoOferta),
		"tipo_propuesta":    valorTexto(tipoPropuesta),
		"total_precio":      totalPrecio,
		"moneda":            moneda,
		"fecha_creacion":    fechaCreacion.Format("2006-01-02"),
		"vendedor":          valorTexto(vendedor),
		"estado":            estado,
	}, nil
}

// salidasPublicasCotizacion lee las Salidas estándar ya persistidas de UNA
// cotización+versión (cotizacion_salidas, que el Motor de Ejecución escribe
// al guardar) y devuelve clave -> valor para mostrar: el texto visible si la
// salida de texto lo trae (un Campo Catálogo, nunca su código); un monto,
// su número. Solo claves de salidasPublicasPlantilla: costo, ganancia y margen
// ni siquiera se leen. Una versión que todavía no guardó salidas da un mapa
// vacío y el bloque queda sin valor ("—"), sin caer a cotizacion_versiones.
func salidasPublicasCotizacion(ctx context.Context, db *pgxpool.Pool, cotizacionID string, version int) (map[string]any, error) {
	claves := make([]string, 0, len(salidasPublicasPlantilla))
	for _, salida := range salidasPublicasPlantilla {
		claves = append(claves, salida.FuenteID)
	}
	rows, err := db.Query(ctx, `
		SELECT clave_salida, valor_numero::float8, COALESCE(valor_visible, valor_texto)
		  FROM cotizacion_salidas
		 WHERE cotizacion_id=$1 AND numero_version=$2 AND clave_salida = ANY($3)`, cotizacionID, version, claves)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultado := make(map[string]any)
	for rows.Next() {
		var clave string
		var numero *float64
		var texto *string
		if err := rows.Scan(&clave, &numero, &texto); err != nil {
			return nil, err
		}
		// Un monto sale como número (igual que total_precio de los datos
		// base) para que el documento lo formatee; un texto, como texto.
		switch {
		case tiposSalidas[clave] == "MONEDA" && numero != nil:
			resultado[clave] = *numero
		case texto != nil:
			resultado[clave] = *texto
		case numero != nil:
			resultado[clave] = *numero
		}
	}
	return resultado, rows.Err()
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
// libre de un bloque (TEXTO, RESUMEN_EJECUTIVO, PORTADA, un VALOR_FIJO de
// plantilla_bloque_campos...) — ejemplo real del documento:
// "La solución contempla [CANTIDAD_INTEGRACIONES] integración(es)...".
var patronTokenPlantilla = regexp.MustCompile(`\[([A-Za-z_][A-Za-z0-9_]*)\]`)

// interpolarTextoPlantilla reemplaza cada token por el valor resuelto del
// elemento con ese nombre_interno. Un token que no coincide con ningún
// elemento, o cuyo valor todavía no está resuelto, se deja tal cual en vez
// de inventar un texto — más "controlado" es un placeholder visible (que el
// equipo de Cotiza puede notar y corregir en el diseño) que un dato falso.
// mostrar traduce el valor a lo que ve el cliente (un Campo Catálogo, a su
// texto_visible: nunca el código interno, CP-02/CP-12 del caso ISA); nil
// deja el valor tal cual.
func interpolarTextoPlantilla(texto string, porNombreInterno map[string]string, condicionValores map[string]any, mostrar func(id string, valor any) any) string {
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
		if mostrar != nil {
			valor = mostrar(id, valor)
		}
		return valorComoTextoRegla(valor)
	})
}

type seccionPlantillaCruda struct {
	SeccionID string
	Titulo    string
}

func (rc *contextoRenderPlantilla) renderizarSecciones(ctx context.Context, plantillaID string) ([]seccionRenderizada, error) {
	rows, err := rc.db.Query(ctx, `
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
		bloques, err := rc.renderizarBloques(ctx, s.SeccionID)
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

func (rc *contextoRenderPlantilla) renderizarBloques(ctx context.Context, seccionID string) ([]bloqueRenderizado, error) {
	rows, err := rc.db.Query(ctx, `
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
		visible, err := condicionBloqueSeCumple(ctx, rc.db, b.BloqueID, rc.condicionValores)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		renderizado := bloqueRenderizado{BloqueID: b.BloqueID, TipoBloque: b.TipoBloque, Titulo: b.Titulo}
		switch {
		case b.TipoBloque == "SALTO_PAGINA":
			// Puramente estructural: ni título ni contenido, solo el salto.
			renderizado.Titulo = ""
		case b.TipoBloque == "CAMPO_VINCULADO":
			valor, err := rc.valorVinculacion(ctx, b.BloqueID)
			if err != nil {
				return nil, err
			}
			renderizado.Valor = valor
		case tiposBloqueConColumnas[b.TipoBloque]:
			origen := b.OrigenFilas
			if b.TipoBloque == "OPCIONES_PROPUESTA" {
				origen = "OPCIONES_PROPUESTA"
			}
			columnas, filas, err := filasTablaInversionPlantilla(ctx, rc.db, b.BloqueID, rc.calculadoraID, origen, rc.elementosPorID, rc.metadatos, rc.valores, rc.base)
			if err != nil {
				return nil, err
			}
			renderizado.Columnas = columnas
			renderizado.Filas = filas
		case b.TipoBloque == "LISTA_PRECIOS":
			items, err := rc.itemsListaPrecios(ctx, b.BloqueID)
			if err != nil {
				return nil, err
			}
			renderizado.Items = items
		case tiposBloqueConCampos[b.TipoBloque]:
			campos, err := rc.camposBloque(ctx, b.BloqueID)
			if err != nil {
				return nil, err
			}
			renderizado.Campos = campos
			renderizado.Contenido = rc.interpolar(ctx, b.Contenido)
		default: // TEXTO, ENCABEZADO, RESUMEN_EJECUTIVO, IMAGEN y los heredados LISTA/CONDICIONES.
			renderizado.Contenido = rc.interpolar(ctx, b.Contenido)
		}
		bloques = append(bloques, renderizado)
	}
	return bloques, nil
}

// interpolar resuelve los tokens [NOMBRE_INTERNO] de un texto libre con los
// valores de la cotización; un Campo Catálogo se muestra con su
// texto_visible, nunca el código interno.
func (rc *contextoRenderPlantilla) interpolar(ctx context.Context, texto string) string {
	return interpolarTextoPlantilla(texto, rc.porNombreInterno, rc.condicionValores, func(id string, valor any) any {
		el := rc.elementosPorID[id]
		if el == nil || strings.ToUpper(strings.TrimSpace(fmt.Sprint(el["tipo"]))) != "CAMPO_CATALOGO" {
			return valor
		}
		visible, err := textoVisibleCatalogo(ctx, rc.db, strings.TrimSpace(fmt.Sprint(el["catalogo_id"])), valor)
		if err != nil {
			return valor
		}
		return visible
	})
}

// camposBloque resuelve los pares Etiqueta/Valor de un bloque: los comunes
// (calculadora_id NULL) y los del cotizador de esta cotización, en el orden
// único del bloque. Un VALOR_FIJO admite tokens [NOMBRE_INTERNO] igual que
// el contenido libre; vacío queda en null.
func (rc *contextoRenderPlantilla) camposBloque(ctx context.Context, bloqueID string) ([]campoRenderizado, error) {
	rows, err := rc.db.Query(ctx, `
		SELECT etiqueta, fuente_tipo, COALESCE(fuente_id,''), COALESCE(valor_fijo,'')
		  FROM plantilla_bloque_campos
		 WHERE bloque_id::text=$1 AND (calculadora_id IS NULL OR calculadora_id=$2)
		 ORDER BY orden, campo_id`, bloqueID, rc.calculadoraID)
	if err != nil {
		return nil, err
	}
	type campoCrudo struct{ Etiqueta, FuenteTipo, FuenteID, ValorFijo string }
	crudos := make([]campoCrudo, 0)
	for rows.Next() {
		var c campoCrudo
		if err := rows.Scan(&c.Etiqueta, &c.FuenteTipo, &c.FuenteID, &c.ValorFijo); err != nil {
			rows.Close()
			return nil, err
		}
		crudos = append(crudos, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	campos := make([]campoRenderizado, 0, len(crudos))
	for _, c := range crudos {
		campo := campoRenderizado{Etiqueta: c.Etiqueta}
		if c.FuenteTipo == "VALOR_FIJO" {
			if texto := rc.interpolar(ctx, c.ValorFijo); texto != "" {
				campo.Valor = texto
			}
		} else {
			valor, err := resolverValorFuentePlantilla(ctx, rc.db, c.FuenteTipo, c.FuenteID, rc.elementosPorID, rc.valoresEfectivos, rc.base)
			if err != nil {
				return nil, err
			}
			campo.Valor = valor
		}
		campos = append(campos, campo)
	}
	return campos, nil
}

// fuenteVinculada devuelve la vinculación del bloque para el cotizador de
// esta cotización (plantilla_vinculaciones es una por bloque y cotizador).
func (rc *contextoRenderPlantilla) fuenteVinculada(ctx context.Context, bloqueID string) (string, string, bool, error) {
	var fuenteTipo, fuenteID string
	err := rc.db.QueryRow(ctx, `
		SELECT fuente_tipo, fuente_id FROM plantilla_vinculaciones
		 WHERE bloque_id::text=$1 AND calculadora_id=$2`, bloqueID, rc.calculadoraID).Scan(&fuenteTipo, &fuenteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return fuenteTipo, fuenteID, true, nil
}

// itemsListaPrecios arma los ítems del elemento LISTA_PRECIOS vinculado al
// bloque, leídos de la estructura compilada (configuracion["items"], que
// incluirItemsListaPrecios ya arma sin costo ni margen) y copiados campo a
// campo a itemListaPreciosRenderizado — dos barreras, no una. Se listan
// todos los ítems activos (es una lista de precios); los que la cotización
// eligió vienen con seleccionado=true y su cantidad.
func (rc *contextoRenderPlantilla) itemsListaPrecios(ctx context.Context, bloqueID string) ([]itemListaPreciosRenderizado, error) {
	fuenteTipo, fuenteID, existe, err := rc.fuenteVinculada(ctx, bloqueID)
	if err != nil || !existe || fuenteTipo != "CAMPO" {
		return nil, err
	}
	elemento := rc.elementosPorID[fuenteID]
	if elemento == nil || strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) != "LISTA_PRECIOS" {
		return nil, nil
	}
	cfg, _ := elemento["configuracion"].(map[string]any)
	seleccion := seleccionListaPrecios(rc.valoresEfectivos[fuenteID])
	items := make([]itemListaPreciosRenderizado, 0)
	for _, crudo := range listaDeMapas(cfg["items"]) {
		precio, _ := numeroDesdeValor(crudo["precio"])
		item := itemListaPreciosRenderizado{
			Codigo: textoMapa(crudo, "codigo"), Nombre: textoMapa(crudo, "nombre"),
			Descripcion: textoMapa(crudo, "descripcion"), Precio: precio,
			Moneda: textoMapa(crudo, "moneda"), UnidadCobro: textoMapa(crudo, "unidad_cobro"),
		}
		if cantidad, elegido := seleccion[textoMapa(crudo, "item_id")]; elegido {
			item.Seleccionado = true
			item.Cantidad = cantidad
		}
		items = append(items, item)
	}
	return items, nil
}

// seleccionListaPrecios lee el valor guardado de una Lista de Precios (UNICA:
// {"item_id","cantidad"?}; MULTIPLE: {"filas":[{"item_id","cantidad"}]}) y
// devuelve item_id -> cantidad (nil si no se indicó).
func seleccionListaPrecios(raw any) map[string]*float64 {
	resultado := make(map[string]*float64)
	valor, _ := raw.(map[string]any)
	if valor == nil {
		return resultado
	}
	agregar := func(fila map[string]any) {
		id := textoMapa(fila, "item_id")
		if id == "" {
			return
		}
		var cantidad *float64
		if n, ok := numeroDesdeValor(fila["cantidad"]); ok {
			cantidad = &n
		}
		resultado[id] = cantidad
	}
	if filas := listaDeMapas(valor["filas"]); len(filas) > 0 {
		for _, fila := range filas {
			agregar(fila)
		}
		return resultado
	}
	agregar(valor)
	return resultado
}

// listaDeMapas acepta una lista tal como sale de json.Unmarshal ([]any) o
// armada en memoria ([]map[string]any).
func listaDeMapas(raw any) []map[string]any {
	switch lista := raw.(type) {
	case []map[string]any:
		return lista
	case []any:
		resultado := make([]map[string]any, 0, len(lista))
		for _, item := range lista {
			if m, ok := item.(map[string]any); ok {
				resultado = append(resultado, m)
			}
		}
		return resultado
	}
	return nil
}

func textoMapa(m map[string]any, clave string) string {
	valor, existe := m[clave]
	if !existe || valor == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(valor))
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

func (rc *contextoRenderPlantilla) valorVinculacion(ctx context.Context, bloqueID string) (any, error) {
	fuenteTipo, fuenteID, existe, err := rc.fuenteVinculada(ctx, bloqueID)
	if err != nil || !existe {
		return nil, err
	}
	if fuenteTipo == "SALIDA_ESTANDAR" {
		return rc.salidas[fuenteID], nil
	}
	return resolverValorFuentePlantilla(ctx, rc.db, fuenteTipo, fuenteID, rc.elementosPorID, rc.valoresEfectivos, rc.base)
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

// filasTablaInversionPlantilla arma columnas + filas de un bloque de tabla
// (TABLA_INVERSION, TABLA_DATOS u OPCIONES_PROPUESTA), con las columnas del
// cotizador de la cotización. FIJO da siempre una sola fila (los valores globales de
// la cotización); OPCIONES_PROPUESTA da una fila por cada Opción de
// Propuesta del ÚNICO componente OPCIONES_PROPUESTA del cotizador (ver el
// comentario de simplificación abajo), con NOMBRE_ESCENARIO/ES_RECOMENDADA
// resueltos desde cotizacion_opciones y CAMPO/COTIZACION_BASE resueltos
// PARA ESA OPCIÓN puntual cuando el campo vive anidado bajo ese componente.
func filasTablaInversionPlantilla(ctx context.Context, db *pgxpool.Pool, bloqueID, calculadoraID, origenFilas string,
	elementosPorID map[string]map[string]any, metadatos map[string]elementoRuntime, valores map[string]any, base map[string]any,
) ([]string, [][]any, error) {
	rows, err := db.Query(ctx, `
		SELECT titulo, fuente_tipo, COALESCE(fuente_id,'')
		  FROM plantilla_tabla_columnas WHERE bloque_id::text=$1 AND calculadora_id=$2
		 ORDER BY orden, columna_id`, bloqueID, calculadoraID)
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
//
// Ronda F2: si existe el componente de alcance COTIZACION, es ese — es el
// único cuyas opciones representan escenarios de la cotización completa.
func unicoComponenteOpcionesPropuesta(elementosPorID map[string]map[string]any) (string, []cotizacionOpcion) {
	if global := padreOpcionesCotizacionIndexado(elementosPorID); global != "" {
		return global, opcionesComoLista(elementosPorID[global]["opciones"])
	}
	mejorID := ""
	mejorOrden := 0
	var mejorOpciones []cotizacionOpcion
	primero := true
	for id, elemento := range elementosPorID {
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) != "OPCIONES_PROPUESTA" {
			continue
		}
		opciones := opcionesComoLista(elemento["opciones"])
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

// opcionesComoLista acepta las opciones tal como las deja
// asegurarOpcionesPropuesta ([]cotizacionOpcion) o como vuelven de un
// snapshot deserializado ([]any de mapas) — antes un snapshot histórico
// dejaba la tabla de escenarios vacía.
func opcionesComoLista(raw any) []cotizacionOpcion {
	switch opciones := raw.(type) {
	case []cotizacionOpcion:
		return opciones
	case []any:
		resultado := make([]cotizacionOpcion, 0, len(opciones))
		for _, opcionRaw := range opciones {
			m, _ := opcionRaw.(map[string]any)
			if m == nil {
				continue
			}
			orden, _ := numeroDesdeValor(m["orden"])
			recomendada, _ := m["es_recomendada"].(bool)
			resultado = append(resultado, cotizacionOpcion{
				OpcionID: fmt.Sprint(m["opcion_id"]), ElementoPadreID: fmt.Sprint(m["elemento_padre_id"]),
				Nombre: fmt.Sprint(m["nombre"]), EsRecomendada: recomendada, Orden: int(orden),
			})
		}
		return resultado
	}
	return nil
}
