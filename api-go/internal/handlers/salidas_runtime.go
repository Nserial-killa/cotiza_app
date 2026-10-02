package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

type salidaNormalizada struct {
	ClaveSalida  string   `json:"clave_salida"`
	FuenteID     string   `json:"fuente_id"`
	TipoDato     string   `json:"tipo_dato"`
	ValorNumero  *float64 `json:"valor_numero,omitempty"`
	ValorTexto   *string  `json:"valor_texto,omitempty"`
	ValorVisible *string  `json:"valor_visible,omitempty"`
	Moneda       string   `json:"moneda,omitempty"`
}

type itemCotizacionSnapshot struct {
	FuenteID       string         `json:"fuente_id"`
	OpcionID       string         `json:"opcion_id,omitempty"`
	Categoria      string         `json:"categoria"`
	Descripcion    string         `json:"descripcion"`
	Cantidad       float64        `json:"cantidad"`
	PrecioUnitario *float64       `json:"precio_unitario,omitempty"`
	TotalPrecio    *float64       `json:"total_precio,omitempty"`
	TotalCosto     *float64       `json:"total_costo,omitempty"`
	Moneda         string         `json:"moneda"`
	Detalle        map[string]any `json:"detalle"`
}

type snapshotCotizacion struct {
	Version           int               `json:"numero_version"`
	CompiladoID       string            `json:"compilado_id"`
	Estructura        map[string]any    `json:"estructura"`
	Valores           map[string]any    `json:"valores"`
	OpcionesEfectivas map[string]string `json:"opciones_efectivas"`
	// Incluye costos internos: este bloque nunca se entrega por el runtime ni por enlaces públicos.
	FuentesExternas map[string][]map[string]any `json:"fuentes_externas"`
	Salidas         []salidaNormalizada         `json:"salidas"`
	Items           []itemCotizacionSnapshot    `json:"items"`
	// Lo que le falta a un Borrador (mensajes legibles) y, de eso, las
	// etiquetas de los campos que faltan para el Precio total. Viven en el
	// snapshot para que el Gestor pueda mostrarlos sin recalcular nada.
	Pendientes      []string `json:"pendientes,omitempty"`
	PrecioPendiente []string `json:"precio_pendiente,omitempty"`
}

func estadoVersionEditable(estado string) bool {
	return estado == "Borrador" || estado == "Cambios solicitados" || estado == "Revisión Comercial"
}

func bloquearVersionEditable(ctx context.Context, tx pgx.Tx, id string, version int) error {
	var actual int
	if err := tx.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id=$1 FOR UPDATE`, id).Scan(&actual); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &errorRuntime{404, "La cotización no existe."}
		}
		return err
	}
	var estado string
	if err := tx.QueryRow(ctx, `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2 FOR UPDATE`, id, version).Scan(&estado); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &errorRuntime{404, "La versión no existe."}
		}
		return err
	}
	if actual != version || !estadoVersionEditable(estado) {
		return &errorRuntime{409, "Esta versión es histórica o está cerrada. Cree una nueva versión para modificar sus valores."}
	}
	return nil
}

func errorSalida(mensaje string) error { return &errorRuntime{http.StatusBadRequest, mensaje} }

// pendienteCotizacion es algo que le falta a la versión para poder avanzar
// (ver salidas_pendientes.go). Mensaje es el texto legible que ve la
// persona; Campos son las etiquetas de los campos de entrada que faltan y
// EsPrecio marca los que impiden calcular el Precio total (el Gestor los
// muestra al lado del $0,00).
type pendienteCotizacion struct {
	Mensaje  string
	Campos   []string
	EsPrecio bool
}

// resultadoSalidasVersion es lo que calcularSalidasVersion deja listo para
// persistir (o, al avanzar de estado, solo para revisar sus pendientes).
type resultadoSalidasVersion struct {
	snapshot   snapshotCotizacion
	mapa       []salidaCotizador
	salidas    []salidaNormalizada
	items      []itemCotizacionSnapshot
	pendientes []pendienteCotizacion
}

// mensajesPendientes devuelve solo los textos, sin repetir, en orden.
func mensajesPendientes(pendientes []pendienteCotizacion) []string {
	vistos := map[string]bool{}
	mensajes := []string{}
	for _, p := range pendientes {
		if !vistos[p.Mensaje] {
			vistos[p.Mensaje] = true
			mensajes = append(mensajes, p.Mensaje)
		}
	}
	return mensajes
}

// camposPrecioPendiente devuelve las etiquetas de lo que falta para el
// Precio total, sin repetir.
func camposPrecioPendiente(pendientes []pendienteCotizacion) []string {
	vistos := map[string]bool{}
	campos := []string{}
	for _, p := range pendientes {
		if !p.EsPrecio {
			continue
		}
		for _, c := range p.Campos {
			if !vistos[c] {
				vistos[c] = true
				campos = append(campos, c)
			}
		}
	}
	return campos
}

// persistirSalidasSnapshot se invoca dentro de la transacción del guardado.
// Las escrituras previas de campos solo son visibles aquí y se revierten si
// falla cualquier validación, salida, desglose o actualización de la versión.
//
// DECISIÓN DE DISEÑO (docs/DECISION_GUARDADO_BORRADOR.md, pendiente de
// confirmar con el jefe): esto relaja el "guardado atómico" del Anexo
// Técnico §7 SOLO para un caso. Si la versión está en Borrador, una salida
// requerida cuya fuente está vacía o incompleta, o un campo obligatorio
// vacío, ya NO revierte el guardado: los valores se guardan, la salida
// queda sin fila en cotizacion_salidas (y su caché en 0) y se devuelve como
// pendiente. Todo lo demás sigue siendo atómico y sigue bloqueando: valores
// incompatibles, errores de cálculo (división entre cero), referencias
// circulares y las reglas BLOQUEAR_GUARDADO/CAMPO_REQUERIDO. Fuera de
// Borrador tampoco se relaja nada: el pendiente vuelve a ser un error.
func (h *CotizadorRuntimeHandler) persistirSalidasSnapshot(ctx context.Context, tx pgx.Tx, rt *contextoRuntime, reglas []reglaCotizadorEval) ([]pendienteCotizacion, error) {
	var estado string
	if err := tx.QueryRow(ctx, `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2`, rt.CotizacionID, rt.Version).Scan(&estado); err != nil {
		return nil, err
	}
	calculo, err := h.calcularSalidasVersion(ctx, tx, rt, reglas, estado == "Borrador")
	if err != nil {
		return nil, err
	}
	salidas, items, mapa := calculo.salidas, calculo.items, calculo.mapa
	raw, err := json.Marshal(calculo.snapshot)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET snapshot_json=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, rt.CotizacionID, rt.Version, raw); err != nil {
		return nil, err
	}
	// Regenerar el conjunto completo elimina salidas que dejaron de
	// resolverse (también una requerida que quedó pendiente en un
	// Borrador: no puede quedar circulando el precio anterior); el UNIQUE
	// de la BD protege también frente a duplicados.
	if _, err = tx.Exec(ctx, `DELETE FROM cotizacion_salidas WHERE cotizacion_id=$1 AND numero_version=$2`, rt.CotizacionID, rt.Version); err != nil {
		return nil, err
	}
	for _, s := range salidas {
		if _, err = tx.Exec(ctx, `INSERT INTO cotizacion_salidas(cotizacion_id,numero_version,clave_salida,fuente_id,tipo_dato,valor_numero,valor_texto,valor_visible,moneda) VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''))`, rt.CotizacionID, rt.Version, s.ClaveSalida, s.FuenteID, s.TipoDato, s.ValorNumero, s.ValorTexto, s.ValorVisible, s.Moneda); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM cotizacion_items WHERE cotizacion_id=$1 AND numero_version=$2`, rt.CotizacionID, rt.Version); err != nil {
		return nil, err
	}
	for i, item := range items {
		detalle, err := json.Marshal(item.Detalle)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO cotizacion_items(cotizacion_id,numero_version,fuente_id,opcion_id,categoria,descripcion,cantidad,precio_unitario,total_precio,total_costo,moneda,detalle_json,orden)
			VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11,$12,$13)`, rt.CotizacionID, rt.Version, item.FuenteID, item.OpcionID, item.Categoria, item.Descripcion, item.Cantidad, item.PrecioUnitario, item.TotalPrecio, item.TotalCosto, item.Moneda, detalle, i+1); err != nil {
			return nil, err
		}
	}
	// Cachés legadas conviven con la tabla nueva. El mapa explícito prevalece
	// sobre funcion_campo; no se cambia ningún consumidor del Dashboard.
	if err = h.actualizarTotalesCotizacionVersion(ctx, tx, rt.Estructura, rt.Elementos, rt.CotizacionID, rt.Version, reglas); err != nil {
		return nil, err
	}
	// Primero en 0 todas las columnas numéricas mapeadas y después el valor
	// de las salidas que sí se resolvieron: una salida pendiente queda en 0,
	// nunca con el valor de un guardado anterior.
	columnas := map[string]string{"TOTAL_PRECIO": "total_precio", "TOTAL_COSTO": "total_costo", "TOTAL_GANANCIA": "total_ganancia", "MARGEN_TOTAL": "margen_total", "SUBTOTAL": "subtotal", "DESCUENTO": "descuento", "IMPUESTOS": "impuestos", "MONEDA": "moneda"}
	for _, s := range mapa {
		if !s.Activo || tiposSalidas[s.ClaveSalida] == "TEXTO" {
			continue
		}
		if col := columnas[s.ClaveSalida]; col != "" {
			if err := actualizarColumnaCotizacionVersion(ctx, tx, rt.CotizacionID, rt.Version, col, 0); err != nil {
				return nil, err
			}
		}
	}
	for _, s := range salidas {
		col := columnas[s.ClaveSalida]
		if col == "" {
			continue
		}
		var v any
		if s.ValorNumero != nil {
			v = *s.ValorNumero
		} else if s.ValorTexto != nil {
			v = *s.ValorTexto
		}
		if err = actualizarColumnaCotizacionVersion(ctx, tx, rt.CotizacionID, rt.Version, col, v); err != nil {
			return nil, err
		}
	}
	return calculo.pendientes, nil
}

// calcularSalidasVersion hace todo el cálculo de salidas, desglose y
// snapshot de una versión SIN escribir nada en la base: lo usan el
// guardado (que después persiste el resultado) y la verificación previa a
// avanzar de estado o generar el enlace público (que solo mira los
// pendientes). permitirPendientes es true en un Borrador: lo incompleto se
// acumula en pendientes en vez de devolverse como error.
func (h *CotizadorRuntimeHandler) calcularSalidasVersion(ctx context.Context, tx pgx.Tx, rt *contextoRuntime, reglas []reglaCotizadorEval, permitirPendientes bool) (*resultadoSalidasVersion, error) {
	valores, err := h.leerValores(ctx, tx, rt.CotizacionID, rt.Version)
	if err != nil {
		return nil, err
	}
	mapa, err := mapaSalidasEstructura(rt.Estructura)
	if err != nil {
		return nil, err
	}
	els := indexarElementosCompletoRuntime(rt.Estructura)
	pendientes := []pendienteCotizacion{}
	// En modo COTIZACION las reglas que leen un campo por opción se validan
	// más abajo contra la opción efectiva (Ronda F2). Las reglas explícitas
	// (BLOQUEAR_GUARDADO, CAMPO_REQUERIDO) bloquean siempre, también en
	// Borrador: son una decisión de quien diseñó el cotizador.
	reglasGlobales := reglasSinCondicionPorOpcion(reglas, rt.Elementos)
	if fallas := evaluarValidacionReglas(valores, reglasGlobales); len(fallas) > 0 {
		mensajes := []string{}
		for _, f := range fallas {
			mensajes = append(mensajes, f.Mensaje)
		}
		return nil, errorSalida(strings.Join(mensajes, " "))
	}
	if len(mapa) > 0 {
		// Campos marcados como obligatorios en el Diseñador. En Borrador un
		// campo vacío es un pendiente; en cualquier otro estado, un error.
		// Se recorren en orden para que el mensaje no cambie entre guardados.
		estados := evaluarEstadoCamposRegla(valores, reglasGlobales)
		ids := make([]string, 0, len(els))
		for id := range els {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			requerido, _ := els[id]["requerido"].(bool)
			if !requerido || rt.Elementos[id].PadreOpcionesID != "" {
				continue
			}
			if estado, existe := estados[id]; existe && !estado.Visible {
				continue
			}
			if valorVacioSalida(valores[id]) {
				mensaje := mensajeCampoObligatorio(els, id)
				if !permitirPendientes {
					return nil, errorSalida(mensaje)
				}
				pendientes = append(pendientes, pendienteCotizacion{Mensaje: mensaje, Campos: []string{etiquetaElemento(els, id)}})
			}
		}
	}
	// Determinar primero la alternativa efectiva; los demás escenarios siguen
	// íntegros en el snapshot, pero no se agregan ni generan totales duplicados.
	efectivas := map[string]string{}
	for padreID, padre := range els {
		if padre["tipo"] != "OPCIONES_PROPUESTA" {
			continue
		}
		opciones, err := listarOpcionesPropuesta(ctx, tx, rt.CotizacionID, rt.Version, padreID)
		if err != nil {
			return nil, err
		}
		padre["opciones"] = opciones
		if len(opciones) == 1 {
			efectivas[padreID] = opciones[0].OpcionID
		} else {
			for _, op := range opciones {
				if op.EsRecomendada {
					if efectivas[padreID] != "" {
						return nil, errorSalida("Hay más de una opción recomendada; seleccione una sola.")
					}
					efectivas[padreID] = op.OpcionID
				}
			}
		}
	}
	// Modo COTIZACION: la opción efectiva es la que alimenta las salidas, así
	// que sus validaciones (BLOQUEAR_GUARDADO/CAMPO_REQUERIDO) tienen que
	// pasar. Las demás opciones ya se validaron al guardarse.
	if global := padreOpcionesCotizacion(rt.Estructura); global != "" && efectivas[global] != "" {
		if fallas := evaluarValidacionReglas(valoresPlanosOpcion(rt.Elementos, valores, global, efectivas[global]), reglas); len(fallas) > 0 {
			mensajes := []string{}
			for _, f := range fallas {
				mensajes = append(mensajes, f.Mensaje)
			}
			return nil, errorSalida(strings.Join(mensajes, " ") + " (opción recomendada)")
		}
	}
	for _, s := range mapa {
		if s.Activo && s.TipoFuente == "ESCENARIO" && efectivas[rt.Elementos[s.FuenteID].PadreOpcionesID] == "" {
			return nil, errorSalida(fmt.Sprintf("Para calcular %s hay que marcar una opción como recomendada antes de guardar.", nombreSalida(s.ClaveSalida, true)))
		}
	}
	externas, err := congelarFuentesSalidas(ctx, tx, els)
	if err != nil {
		return nil, err
	}
	// Una selección guardada anteriormente también debe seguir siendo válida;
	// no sumar silenciosamente solo los ítems restantes de una lista múltiple.
	for id, el := range els {
		if el["tipo"] != "LISTA_PRECIOS" && el["tipo"] != "CAMPO_CATALOGO" {
			continue
		}
		selecciones := []any{valores[id]}
		if rt.Elementos[id].PadreOpcionesID != "" {
			selecciones = nil
			porOpcion, _ := valores[id].(map[string]any)
			for _, v := range porOpcion {
				selecciones = append(selecciones, v)
			}
		}
		for _, v := range selecciones {
			if valorVacioSalida(v) {
				continue
			}
			if el["tipo"] == "CAMPO_CATALOGO" {
				existe := false
				for _, d := range externas[id] {
					if d["valor_sistema"] == v {
						existe = true
					}
				}
				if !existe {
					return nil, errorSalida(fmt.Sprintf("La opción elegida en «%s» ya no está activa; vuelva a seleccionarla.", etiquetaElemento(els, id)))
				}
			} else {
				cfg, _ := el["configuracion"].(map[string]any)
				seleccion, _ := v.(map[string]any)
				ids, e := itemIDsDesdeValorListaPrecios(fmt.Sprint(cfg["tipo_lista_precios"]), seleccion)
				if e != nil {
					return nil, errorSalida(e.Error())
				}
				for _, itemID := range ids {
					existe := false
					for _, d := range externas[id] {
						if d["item_id"] == itemID {
							existe = true
						}
					}
					if !existe {
						return nil, errorSalida(fmt.Sprintf("Un ítem elegido en «%s» ya no está disponible; vuelva a seleccionarlo.", etiquetaElemento(els, id)))
					}
				}
			}
		}
	}
	resolverCamposCalculados(els, valores)
	resolverCamposCalculadosPorOpcionConReglas(els, rt.Elementos, valores, reglas)
	incluirValoresCajaValor(rt.Estructura, valores)
	incluirEstadoReglas(rt.Estructura, evaluarEstadoCamposRegla(valores, reglasGlobales))
	salidas, pendientesSalidas, err := resolverSalidas(mapa, els, rt.Elementos, valores, efectivas, externas, permitirPendientes)
	if err != nil {
		return nil, err
	}
	pendientes = append(pendientes, pendientesSalidas...)
	var moneda string
	if err := tx.QueryRow(ctx, `SELECT moneda FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2`, rt.CotizacionID, rt.Version).Scan(&moneda); err != nil {
		return nil, err
	}
	for _, s := range salidas {
		if s.ClaveSalida == "MONEDA" && s.ValorTexto != nil {
			moneda = *s.ValorTexto
		}
	}
	for i := range salidas {
		if salidas[i].TipoDato == "MONEDA" {
			salidas[i].Moneda = moneda
		}
	}
	items := generarItemsSalidas(els, rt.Elementos, valores, efectivas, externas, salidas, moneda)
	snapshot := snapshotCotizacion{rt.Version, rt.CompiladoID, rt.Estructura, valores, efectivas, externas, salidas, items, mensajesPendientes(pendientes), camposPrecioPendiente(pendientes)}
	return &resultadoSalidasVersion{snapshot: snapshot, mapa: mapa, salidas: salidas, items: items, pendientes: pendientes}, nil
}

func valorVacioSalida(v any) bool { return v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" }

func congelarFuentesSalidas(ctx context.Context, q consultadorRuntime, els map[string]map[string]any) (map[string][]map[string]any, error) {
	externas := map[string][]map[string]any{}
	for id, el := range els {
		cfg, _ := el["configuracion"].(map[string]any)
		if cfg == nil {
			cfg = map[string]any{}
			el["configuracion"] = cfg
		}
		switch el["tipo"] {
		case "LISTA_PRECIOS":
			rows, err := q.Query(ctx, `SELECT item_id::text,codigo,nombre,COALESCE(descripcion,''),precio,costo_interno,moneda FROM lista_precios_items WHERE elemento_id=$1 AND activo ORDER BY orden,item_id`, id)
			if err != nil {
				return nil, err
			}
			publicos := []any{}
			privados := []map[string]any{}
			for rows.Next() {
				var item, codigo, nombre, descripcion, moneda string
				var precio float64
				var costo *float64
				if err := rows.Scan(&item, &codigo, &nombre, &descripcion, &precio, &costo, &moneda); err != nil {
					rows.Close()
					return nil, err
				}
				publicos = append(publicos, map[string]any{"item_id": item, "codigo": codigo, "nombre": nombre, "descripcion": descripcion, "precio": precio, "moneda": moneda})
				privado := map[string]any{"item_id": item, "codigo": codigo, "nombre": nombre, "descripcion": descripcion, "precio": precio, "moneda": moneda}
				if costo != nil {
					privado["costo_interno"] = *costo
				}
				privados = append(privados, privado)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			cfg["items"] = publicos
			externas[id] = privados
		case "CAMPO_CATALOGO":
			rows, err := q.Query(ctx, `SELECT valor_id,COALESCE(clave,''),valor_sistema,texto_visible,valor_calculo FROM catalogo_valores WHERE catalogo_id=$1 AND activo ORDER BY orden,valor_id`, el["catalogo_id"])
			if err != nil {
				return nil, err
			}
			opciones := []any{}
			calculos := []any{}
			completos := []map[string]any{}
			for rows.Next() {
				var vid, clave, valor, etiqueta string
				var calculo *float64
				if err := rows.Scan(&vid, &clave, &valor, &etiqueta, &calculo); err != nil {
					rows.Close()
					return nil, err
				}
				dato := map[string]any{"valor_id": vid, "clave": clave, "valor_sistema": valor, "texto_visible": etiqueta}
				opciones = append(opciones, dato)
				completo := map[string]any{"valor_id": vid, "valor_sistema": valor, "texto_visible": etiqueta}
				if calculo != nil {
					calculos = append(calculos, map[string]any{"valor_sistema": valor, "valor_calculo": *calculo})
					completo["valor_calculo"] = *calculo
				}
				completos = append(completos, completo)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
			el["opciones"] = opciones
			cfg["catalogo_valores_calculo"] = calculos
			externas[id] = completos
		}
	}
	return externas, nil
}

func valorFuenteSalida(id string, els map[string]map[string]any, metas map[string]elementoRuntime, valores map[string]any, efectivas map[string]string) any {
	el := els[id]
	if el == nil {
		return nil
	}
	valor := valores[id]
	if el["tipo"] == "CAMPO_CALCULADO" || el["tipo"] == "LISTA_PRECIOS" || el["tipo"] == "TABLA" {
		if metas[id].PadreOpcionesID != "" {
			valor = el["valores_resueltos_por_opcion"]
		} else {
			valor = el["valor_resuelto"]
		}
	}
	if padre := metas[id].PadreOpcionesID; padre != "" {
		m, _ := valor.(map[string]any)
		return m[efectivas[padre]]
	}
	return valor
}

func valorSeleccionFuente(id string, metas map[string]elementoRuntime, valores map[string]any, efectivas map[string]string) any {
	valor := valores[id]
	if padre := metas[id].PadreOpcionesID; padre != "" {
		m, _ := valor.(map[string]any)
		valor = m[efectivas[padre]]
	}
	return valor
}

// resolverSalidas calcula cada salida activa del mapa. Con
// permitirPendientes (Borrador), una salida requerida que no se pudo
// resolver solo porque faltan datos de entrada se devuelve en pendientes en
// vez de cortar el guardado; un valor incompatible o un error de cálculo
// siguen siendo error en cualquier estado.
func resolverSalidas(mapa []salidaCotizador, els map[string]map[string]any, metas map[string]elementoRuntime, valores map[string]any, efectivas map[string]string, externas map[string][]map[string]any, permitirPendientes bool) ([]salidaNormalizada, []pendienteCotizacion, error) {
	if err := validarMapaSalidas(mapa, els, metas, false); err != nil {
		return nil, nil, errorSalida(err.Error())
	}
	pendientes := []pendienteCotizacion{}
	porClave := map[string]salidaCotizador{}
	resueltos := map[string]salidaNormalizada{}
	visitados := map[string]bool{}
	for _, s := range mapa {
		if s.Activo {
			porClave[s.ClaveSalida] = s
		}
	}
	// diagnosticar averigua por qué la salida clave quedó sin valor. Si su
	// fuente es otra salida, el motivo es el de esa otra salida. Las fuentes
	// dentro de Opciones de Propuesta se diagnostican con los valores de la
	// opción efectiva, que es la única que alimenta las salidas.
	var diagnosticar func(clave string, profundidad int) diagnosticoFuente
	diagnosticar = func(clave string, profundidad int) diagnosticoFuente {
		s, ok := porClave[clave]
		if !ok || profundidad > len(mapa) {
			return diagnosticoFuente{errorCalculo: true}
		}
		if s.TipoFuente == "SALIDA" {
			return diagnosticar(s.FuenteID, profundidad+1)
		}
		valoresFuente := valores
		if padre := metas[s.FuenteID].PadreOpcionesID; padre != "" {
			valoresFuente = valoresPlanosOpcion(metas, valores, padre, efectivas[padre])
		}
		if s.PropiedadFuente == "total_costo" {
			// Costo de una lista de precios: pendiente solo si todavía no
			// hay nada elegido; un ítem sin costo cargado es un error.
			if valorSinDatos(valoresFuente[s.FuenteID]) {
				return diagnosticoFuente{faltantes: []string{s.FuenteID}}
			}
			return diagnosticoFuente{errorCalculo: true}
		}
		return diagnosticarFuente(s.FuenteID, els, valoresFuente)
	}
	// etiquetaFuente es la etiqueta visible del elemento del que sale la
	// salida, o el nombre de la otra salida de la que depende.
	etiquetaFuente := func(s salidaCotizador) string {
		if s.TipoFuente == "SALIDA" {
			return nombreSalida(s.FuenteID, false)
		}
		return etiquetaElemento(els, s.FuenteID)
	}
	var resolver func(string) (salidaNormalizada, error)
	resolver = func(clave string) (salidaNormalizada, error) {
		if s, ok := resueltos[clave]; ok {
			return s, nil
		}
		s := porClave[clave]
		resultado := salidaNormalizada{ClaveSalida: clave, FuenteID: s.FuenteID, TipoDato: tiposSalidas[clave]}
		if visitados[clave] {
			return resultado, errorSalida("Las salidas del cotizador dependen unas de otras en círculo; revise la pantalla Salidas del Diseñador.")
		}
		visitados[clave] = true
		defer delete(visitados, clave)
		var valor any
		if s.TipoFuente == "SALIDA" {
			fuente, err := resolver(s.FuenteID)
			if err != nil {
				return resultado, err
			}
			if fuente.ValorNumero != nil {
				valor = *fuente.ValorNumero
			}
			if fuente.ValorTexto != nil {
				valor = *fuente.ValorTexto
			}
			resultado.ValorVisible = fuente.ValorVisible
		} else {
			valor = valorFuenteSalida(s.FuenteID, els, metas, valores, efectivas)
			if s.PropiedadFuente == "total_costo" {
				costos := map[string]float64{}
				for _, i := range externas[s.FuenteID] {
					if costo, ok := numeroDesdeValor(i["costo_interno"]); ok {
						costos[fmt.Sprint(i["item_id"])] = costo
					}
				}
				cfg, _ := els[s.FuenteID]["configuracion"].(map[string]any)
				seleccionado, _ := valorSeleccionFuente(s.FuenteID, metas, valores, efectivas).(map[string]any)
				ids, e := itemIDsDesdeValorListaPrecios(fmt.Sprint(cfg["tipo_lista_precios"]), seleccionado)
				completos := e == nil
				for _, id := range ids {
					if _, ok := costos[id]; !ok {
						completos = false
					}
				}
				if costo, ok := valorListaPrecios(fmt.Sprint(cfg["tipo_lista_precios"]), seleccionado, costos); ok && completos {
					valor = costo
				} else {
					valor = nil
				}
			}
			if els[s.FuenteID]["tipo"] == "CAMPO_CATALOGO" {
				for _, v := range externas[s.FuenteID] {
					if v["valor_sistema"] == valor {
						etiqueta := fmt.Sprint(v["texto_visible"])
						resultado.ValorVisible = &etiqueta
					}
				}
			}
		}
		if resultado.TipoDato == "TEXTO" {
			if texto, ok := valor.(string); ok && strings.TrimSpace(texto) != "" {
				texto = strings.TrimSpace(texto)
				resultado.ValorTexto = &texto
			}
		} else if numero, ok := numeroDesdeValor(valor); ok && !math.IsInf(numero, 0) && !math.IsNaN(numero) {
			resultado.ValorNumero = &numero
		}
		if resultado.ValorNumero == nil && resultado.ValorTexto == nil {
			if !valorVacioSalida(valor) {
				// Hay algo escrito pero no sirve para esta salida: siempre error.
				return resultado, errorSalida(fmt.Sprintf("El valor de «%s» no sirve para calcular %s; corríjalo.", etiquetaFuente(s), nombreSalida(clave, true)))
			}
			if s.Requerido {
				diag := diagnosticar(clave, 0)
				if !diag.soloFaltanDatos() {
					// Los datos están pero el cálculo falla (división entre
					// cero, rango, dato no numérico): error en cualquier estado.
					return resultado, errorSalida(fmt.Sprintf("No se pudo calcular %s con los valores ingresados; revise «%s» y los campos de los que depende (por ejemplo, una división entre cero).", nombreSalida(clave, true), etiquetaFuente(s)))
				}
				mensaje := mensajeFaltanDatosSalida(els, clave, diag.faltantes)
				if !permitirPendientes {
					return resultado, errorSalida(mensaje)
				}
				campos := []string{}
				for _, id := range diag.faltantes {
					campos = append(campos, etiquetaElemento(els, id))
				}
				pendientes = append(pendientes, pendienteCotizacion{Mensaje: mensaje, Campos: campos, EsPrecio: clave == "TOTAL_PRECIO"})
			}
			return resultado, nil
		}
		resueltos[clave] = resultado
		return resultado, nil
	}
	resultado := []salidaNormalizada{}
	for _, s := range mapa {
		if !s.Activo {
			continue
		}
		r, err := resolver(s.ClaveSalida)
		if err != nil {
			return nil, nil, err
		}
		if r.ValorNumero != nil || r.ValorTexto != nil {
			resultado = append(resultado, r)
		}
	}
	return resultado, pendientes, nil
}

func generarItemsSalidas(els map[string]map[string]any, metas map[string]elementoRuntime, valores map[string]any, efectivas map[string]string, externas map[string][]map[string]any, salidas []salidaNormalizada, moneda string) []itemCotizacionSnapshot {
	items := []itemCotizacionSnapshot{}
	ids := []string{}
	for id := range els {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		el := els[id]
		cfg, _ := el["configuracion"].(map[string]any)
		valor, _ := valorSeleccionFuente(id, metas, valores, efectivas).(map[string]any)
		if valor == nil {
			continue
		}
		categoria, _ := el["etiqueta"].(string)
		if categoria == "" {
			categoria = nombreInternoElemento(cfg)
		}
		if categoria == "" {
			categoria = id
		}
		opcion := efectivas[metas[id].PadreOpcionesID]
		switch el["tipo"] {
		case "LISTA_PRECIOS":
			filas := []any{valor}
			if cfg["tipo_lista_precios"] == "MULTIPLE" {
				filas, _ = valor["filas"].([]any)
			}
			for _, raw := range filas {
				fila, _ := raw.(map[string]any)
				for _, dato := range externas[id] {
					if dato["item_id"] != fila["item_id"] {
						continue
					}
					cantidad, ok := numeroDesdeValor(fila["cantidad"])
					if !ok {
						cantidad = 1
					}
					precio, _ := numeroDesdeValor(dato["precio"])
					total := precio * cantidad
					item := itemCotizacionSnapshot{FuenteID: id, OpcionID: opcion, Categoria: categoria, Descripcion: fmt.Sprint(dato["nombre"]), Cantidad: cantidad, PrecioUnitario: &precio, TotalPrecio: &total, Moneda: fmt.Sprint(dato["moneda"]), Detalle: map[string]any{"seleccion": fila, "fuente": dato}}
					if costo, ok := numeroDesdeValor(dato["costo_interno"]); ok {
						totalCosto := costo * cantidad
						item.TotalCosto = &totalCosto
					}
					items = append(items, item)
				}
			}
		case "TABLA":
			columnas, _ := cfg["columnas"].([]any)
			columna := primeraColumnaNumericaTabla(columnas)
			filas, _ := valor["filas"].([]any)
			for i, raw := range filas {
				fila, _ := raw.(map[string]any)
				item := itemCotizacionSnapshot{FuenteID: id, OpcionID: opcion, Categoria: categoria, Descripcion: fmt.Sprintf("%s · fila %d", categoria, i+1), Cantidad: 1, Moneda: moneda, Detalle: fila}
				if n, ok := numeroDesdeValor(fila[columna]); ok {
					item.TotalPrecio = &n
					item.PrecioUnitario = &n
				}
				items = append(items, item)
			}
		}
	}
	if len(items) == 0 {
		for _, s := range salidas {
			if s.ClaveSalida == "TOTAL_PRECIO" && s.ValorNumero != nil {
				items = append(items, itemCotizacionSnapshot{FuenteID: s.FuenteID, Categoria: "Total de la oferta", Descripcion: "Sin desglose por categorías en el cotizador", Cantidad: 1, PrecioUnitario: s.ValorNumero, TotalPrecio: s.ValorNumero, Moneda: moneda, Detalle: map[string]any{}})
			}
		}
	}
	return items
}
