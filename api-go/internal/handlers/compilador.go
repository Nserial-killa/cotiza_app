package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CompiladorHandler valida y publica versiones inmutables de un cotizador.
type CompiladorHandler struct {
	DB *pgxpool.Pool
}

type compiladorRequest struct {
	CalculadoraID string `json:"calculadora_id"`
	CotizadorID   string `json:"cotizador_id"`
}

type resumenCompilacion struct {
	Servicios      int `json:"servicios"`
	Campos         int `json:"campos"`
	Tabs           int `json:"tabs"`
	ElementosTab   int `json:"elementos_tab"`
	ValoresCalculo int `json:"valores_calculo"`
	Reglas         int `json:"reglas"`
}

type elementoCompilado struct {
	ElementoID    string              `json:"elemento_id"`
	Tipo          string              `json:"tipo"`
	Etiqueta      *string             `json:"etiqueta"`
	CatalogoID    *string             `json:"catalogo_id"`
	FuncionCampo  string              `json:"funcion_campo,omitempty"`
	ColumnasAncho int                 `json:"columnas_ancho"`
	Orden         int                 `json:"orden"`
	Requerido     bool                `json:"requerido"`
	Configuracion map[string]any      `json:"configuracion"`
	Hijos         []elementoCompilado `json:"hijos,omitempty"`

	// componentePadreID no se serializa: solo sirve para armar el
	// anidado hijos/CONTENEDOR u OPCIONES_PROPUESTA en
	// anidarHijosCompilado antes de
	// devolver la respuesta.
	componentePadreID string
}

type tabCompilado struct {
	TabID       string              `json:"tab_id"`
	Nombre      string              `json:"nombre"`
	Descripcion *string             `json:"descripcion"`
	Alcance     string              `json:"alcance"`
	Orden       int                 `json:"orden"`
	Elementos   []elementoCompilado `json:"elementos"`
}

type configuracionCompilada struct {
	CalculadoraID string         `json:"calculadora_id"`
	Version       int            `json:"version"`
	Tabs          []tabCompilado `json:"tabs"`
}

type resultadoValidacion struct {
	CalculadoraID string
	Valido        bool
	Errores       []string
	Advertencias  []string
	Resumen       resumenCompilacion
	Tabs          []tabCompilado
}

func (h *CompiladorHandler) Validar(w http.ResponseWriter, r *http.Request) {
	calculadoraID, ok := leerCalculadoraCompilador(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resultado, err := h.validarConfiguracion(ctx, calculadoraID)
	if err != nil {
		log.Printf("compilador: error validando %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el cotizador."})
		return
	}
	escribirJSON(w, http.StatusOK, respuestaCompilador(resultado, false, "", ""))
}

func (h *CompiladorHandler) Compilar(w http.ResponseWriter, r *http.Request) {
	calculadoraID, ok := leerCalculadoraCompilador(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	resultado, err := h.validarConfiguracion(ctx, calculadoraID)
	if err != nil {
		log.Printf("compilador: error validando antes de compilar %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el cotizador."})
		return
	}
	if !resultado.Valido {
		escribirJSON(w, http.StatusOK, respuestaCompilador(resultado, false, "", ""))
		return
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la compilación."})
		return
	}
	defer tx.Rollback(ctx)

	var existe bool
	if err = tx.QueryRow(ctx, `SELECT true FROM calculadoras WHERE calculadora_id=$1 FOR UPDATE`, calculadoraID).Scan(&existe); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador indicado no existe."})
			return
		}
		log.Printf("compilador: error bloqueando calculadora %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la publicación."})
		return
	}
	var versionAnterior int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM cotizadores_compilados WHERE calculadora_id=$1`, calculadoraID).Scan(&versionAnterior); err != nil {
		log.Printf("compilador: error consultando versión de %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible calcular la nueva versión."})
		return
	}
	version := versionAnterior + 1
	configuracion := configuracionCompilada{CalculadoraID: calculadoraID, Version: version, Tabs: resultado.Tabs}
	configuracionJSON, err := json.Marshal(configuracion)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE cotizadores_compilados SET estado='ANTERIOR' WHERE calculadora_id=$1 AND estado='ACTIVA'`, calculadoraID)
	}
	var compiladoID string
	if err == nil {
		err = tx.QueryRow(ctx, `
			INSERT INTO cotizadores_compilados (calculadora_id, version, estado, configuracion)
			VALUES ($1, $2, 'ACTIVA', $3)
			RETURNING compilado_id::text`, calculadoraID, version, string(configuracionJSON)).Scan(&compiladoID)
	}
	versionTexto := strconv.Itoa(version)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE calculadoras SET version_actual=$2, estado='Publicado' WHERE calculadora_id=$1`, calculadoraID, versionTexto)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("compilador: error publicando %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible publicar el cotizador."})
		return
	}
	escribirJSON(w, http.StatusOK, respuestaCompilador(resultado, true, versionTexto, compiladoID))
}

func leerCalculadoraCompilador(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req compiladorRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return "", false
	}
	id := strings.ToUpper(strings.TrimSpace(req.CalculadoraID))
	if id == "" {
		id = strings.ToUpper(strings.TrimSpace(req.CotizadorID))
	}
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id."})
		return "", false
	}
	return id, true
}

func (h *CompiladorHandler) validarConfiguracion(ctx context.Context, calculadoraID string) (resultadoValidacion, error) {
	resultado := resultadoValidacion{
		CalculadoraID: calculadoraID,
		Errores:       make([]string, 0), Advertencias: make([]string, 0), Tabs: make([]tabCompilado, 0),
	}
	rows, err := h.DB.Query(ctx, `
		WITH tabs_disponibles AS (
			SELECT t.tab_id, 0 AS origen_orden
			FROM tabs_cotizador t
			WHERE t.calculadora_id=$1 AND t.activo=true
			UNION
			SELECT a.tab_id, 1 AS origen_orden
			FROM tabs_cotizador_asociaciones a
			JOIN tabs_cotizador t ON t.tab_id=a.tab_id
			WHERE a.calculadora_id=$1 AND t.activo=true
		)
		SELECT t.tab_id, t.nombre, t.descripcion, t.alcance, t.orden,
		       e.elemento_id, e.tipo, e.etiqueta, e.catalogo_id,
		       e.componente_padre_id, e.campo_fuente_id, e.funcion_campo,
		       e.columnas_ancho, e.orden, e.requerido, e.configuracion,
		       CASE WHEN e.catalogo_id IS NULL THEN NULL ELSE c.activo END,
		       CASE WHEN e.catalogo_id IS NULL THEN NULL ELSE c.tipo_calculo END
		FROM tabs_disponibles td
		JOIN tabs_cotizador t ON t.tab_id=td.tab_id
		-- SECCIONES_ADICIONALES es un selector de diseño. En ejecución se
		-- reemplaza por las tabs referenciadas, no por un control editable.
		LEFT JOIN elementos_tab_cotizador e
		       ON e.tab_id=t.tab_id AND e.activo=true
		      AND e.tipo<>'SECCIONES_ADICIONALES'
		LEFT JOIN catalogos c ON c.catalogo_id=e.catalogo_id
		ORDER BY td.origen_orden, t.orden, t.tab_id, e.orden, e.elemento_id`, calculadoraID)
	if err != nil {
		return resultado, err
	}
	defer rows.Close()
	tabsPorID := make(map[string]int)
	for rows.Next() {
		var tabID, nombre, alcance string
		var descripcion *string
		var orden int
		var elementoID, tipo *string
		var etiqueta, catalogoID *string
		var componentePadreID, campoFuenteID, funcionCampo *string
		var columnasAncho, elementoOrden *int
		var requerido *bool
		var configuracion map[string]any
		var catalogoActivo *bool
		var catalogoTipoCalculo *string
		if err := rows.Scan(&tabID, &nombre, &descripcion, &alcance, &orden, &elementoID, &tipo, &etiqueta, &catalogoID, &componentePadreID, &campoFuenteID, &funcionCampo, &columnasAncho, &elementoOrden, &requerido, &configuracion, &catalogoActivo, &catalogoTipoCalculo); err != nil {
			return resultado, err
		}
		indice, existe := tabsPorID[tabID]
		if !existe {
			indice = len(resultado.Tabs)
			tabsPorID[tabID] = indice
			resultado.Tabs = append(resultado.Tabs, tabCompilado{TabID: tabID, Nombre: nombre, Descripcion: descripcion, Alcance: alcance, Orden: orden, Elementos: make([]elementoCompilado, 0)})
		}
		if elementoID == nil {
			continue
		}
		cfg := configuracion
		if cfg == nil {
			cfg = map[string]any{}
		}
		funcion := valorString(funcionCampo)
		if funcion == "NORMAL" {
			funcion = ""
		}
		el := elementoCompilado{ElementoID: *elementoID, Tipo: valorString(tipo), Etiqueta: etiqueta, CatalogoID: catalogoID, FuncionCampo: funcion, ColumnasAncho: valorInt(columnasAncho), Orden: valorInt(elementoOrden), Requerido: valorBool(requerido), Configuracion: cfg, componentePadreID: valorString(componentePadreID)}
		if el.Tipo == "CAJA_VALOR" {
			if fuente := valorString(campoFuenteID); fuente != "" {
				el.Configuracion["campo_fuente_id"] = fuente
			} else {
				delete(el.Configuracion, "campo_fuente_id")
			}
		}
		if el.Tipo == "CAMPO_CATALOGO" {
			tipoCalculo := valorString(catalogoTipoCalculo)
			if tipoCalculo == "" {
				tipoCalculo = "SIN_VALOR"
			}
			el.Configuracion["catalogo_tipo_calculo"] = tipoCalculo
		}
		resultado.Tabs[indice].Elementos = append(resultado.Tabs[indice].Elementos, el)
		resultado.Resumen.ElementosTab++
		if el.Tipo == "CAMPO" {
			resultado.Resumen.Campos++
		}
		if el.Tipo == "CAMPO_CATALOGO" && (catalogoID == nil || catalogoActivo == nil || !*catalogoActivo) {
			catalogo := "sin catálogo"
			if catalogoID != nil && strings.TrimSpace(*catalogoID) != "" {
				catalogo = *catalogoID
			}
			resultado.Errores = append(resultado.Errores, fmt.Sprintf("El elemento %s apunta al catálogo %s, que no existe o está inactivo.", *elementoID, catalogo))
		}
	}
	if err := rows.Err(); err != nil {
		return resultado, err
	}
	resultado.Resumen.Tabs = len(resultado.Tabs)
	if resultado.Resumen.Tabs == 0 {
		resultado.Errores = append(resultado.Errores, "El cotizador debe tener al menos una sección activa.")
	}
	for _, tab := range resultado.Tabs {
		if len(tab.Elementos) == 0 {
			resultado.Advertencias = append(resultado.Advertencias, fmt.Sprintf("La sección %s (%s) no tiene elementos activos.", tab.Nombre, tab.TabID))
		}
	}
	if err := h.incluirItemsListaPrecios(ctx, &resultado); err != nil {
		return resultado, err
	}
	if err := h.incluirColumnasTabla(ctx, &resultado); err != nil {
		return resultado, err
	}
	if err := h.incluirValorCalculoCatalogo(ctx, &resultado); err != nil {
		return resultado, err
	}
	for i := range resultado.Tabs {
		resultado.Tabs[i].Elementos = anidarHijosCompilado(resultado.Tabs[i].Elementos, &resultado)
	}
	if err := h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM reglas WHERE activo=true`).Scan(&resultado.Resumen.Reglas); err != nil {
		return resultado, err
	}
	resultado.Valido = len(resultado.Errores) == 0
	return resultado, nil
}

// incluirItemsListaPrecios agrega, a la configuracion de cada elemento
// LISTA_PRECIOS, su lista de ítems ACTIVOS bajo la clave "items" —
// código, nombre, descripción, precio, moneda, unidad_cobro. A propósito
// NUNCA incluye costo_interno ni margen_porcentaje: son precio interno, y
// esta es la estructura que después lee cualquiera que abra el Motor de
// Ejecución (cotizador_runtime.go), sin importar su rol — el mismo
// criterio de "costo/margen no viajan a quien no tiene permiso" que ya
// aplica sesionPuedeVerPrice en cotizaciones.go, pero aplicado acá de forma
// estructural (nunca entran al JSON compilado) en vez de por sesión, porque
// este JSON se guarda una vez y lo leen sesiones distintas después.
func (h *CompiladorHandler) incluirItemsListaPrecios(ctx context.Context, resultado *resultadoValidacion) error {
	ids := make([]string, 0)
	for _, tab := range resultado.Tabs {
		for _, el := range tab.Elementos {
			if el.Tipo == "LISTA_PRECIOS" {
				ids = append(ids, el.ElementoID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT elemento_id, item_id::text, codigo, nombre, COALESCE(descripcion, ''), precio, moneda, COALESCE(unidad_cobro, '')
		FROM lista_precios_items
		WHERE elemento_id = ANY($1) AND activo = true
		ORDER BY elemento_id, orden, codigo`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	itemsPorElemento := make(map[string][]map[string]any)
	for rows.Next() {
		var elementoID, itemID, codigo, nombre, descripcion, moneda, unidadCobro string
		var precio float64
		if err := rows.Scan(&elementoID, &itemID, &codigo, &nombre, &descripcion, &precio, &moneda, &unidadCobro); err != nil {
			return err
		}
		itemsPorElemento[elementoID] = append(itemsPorElemento[elementoID], map[string]any{
			"item_id": itemID, "codigo": codigo, "nombre": nombre, "descripcion": descripcion,
			"precio": precio, "moneda": moneda, "unidad_cobro": unidadCobro,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range resultado.Tabs {
		for j := range resultado.Tabs[i].Elementos {
			el := &resultado.Tabs[i].Elementos[j]
			if el.Tipo != "LISTA_PRECIOS" {
				continue
			}
			items := itemsPorElemento[el.ElementoID]
			if items == nil {
				items = make([]map[string]any, 0)
			}
			el.Configuracion["items"] = items
		}
	}
	return nil
}

// incluirColumnasTabla agrega, a la configuracion de cada elemento TABLA,
// su lista de columnas bajo la clave "columnas" — cada una ya normalizada
// a la misma forma {columna_id, origen, campo_existente_id, tipo_dato,
// etiqueta, orden}, sea CAMPO_EXISTENTE (tipo_dato/etiqueta resueltos del
// campo que referencia) o PROPIA (los suyos). Normalizar acá evita que el
// Motor de Ejecución y valorTotalTabla (calculo.go) tengan que distinguir
// el origen de cada columna.
func (h *CompiladorHandler) incluirColumnasTabla(ctx context.Context, resultado *resultadoValidacion) error {
	ids := make([]string, 0)
	for _, tab := range resultado.Tabs {
		for _, el := range tab.Elementos {
			if el.Tipo == "TABLA" {
				ids = append(ids, el.ElementoID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT tc.elemento_id, tc.columna_id::text, tc.origen, tc.campo_existente_id, tc.tipo_dato, tc.etiqueta, tc.orden,
		       ref.tipo, ref.etiqueta, ref.configuracion
		FROM tabla_columnas tc
		LEFT JOIN elementos_tab_cotizador ref ON ref.elemento_id = tc.campo_existente_id
		WHERE tc.elemento_id = ANY($1)
		ORDER BY tc.elemento_id, tc.orden`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	columnasPorElemento := make(map[string][]map[string]any)
	for rows.Next() {
		var elementoID, columnaID, origen string
		var campoExistenteID, tipoDato, etiqueta *string
		var orden int
		var refTipo, refEtiqueta *string
		var refConfig map[string]any
		if err := rows.Scan(&elementoID, &columnaID, &origen, &campoExistenteID, &tipoDato, &etiqueta, &orden, &refTipo, &refEtiqueta, &refConfig); err != nil {
			return err
		}
		tipoDatoFinal := valorString(tipoDato)
		etiquetaFinal := valorString(etiqueta)
		if origen == "CAMPO_EXISTENTE" {
			tipoDatoFinal = tipoDatoDesdeElementoReferenciado(valorString(refTipo), refConfig)
			etiquetaFinal = valorString(refEtiqueta)
		}
		columnasPorElemento[elementoID] = append(columnasPorElemento[elementoID], map[string]any{
			"columna_id": columnaID, "origen": origen, "campo_existente_id": valorString(campoExistenteID),
			"tipo_dato": tipoDatoFinal, "etiqueta": etiquetaFinal, "orden": orden,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range resultado.Tabs {
		for j := range resultado.Tabs[i].Elementos {
			el := &resultado.Tabs[i].Elementos[j]
			if el.Tipo != "TABLA" {
				continue
			}
			columnas := columnasPorElemento[el.ElementoID]
			if columnas == nil {
				columnas = make([]map[string]any, 0)
			}
			el.Configuracion["columnas"] = columnas
		}
	}
	return nil
}

// incluirValorCalculoCatalogo agrega, a la configuracion de cada elemento
// CAMPO_CATALOGO, el valor_calculo de cada valor ACTIVO de su catálogo bajo
// la clave "catalogo_valores_calculo" ([{valor_sistema, valor_calculo}]) —
// "catalogo_tipo_calculo" ya se resolvió arriba, en el mismo SELECT que
// valida catalogoActivo. Mismo patrón que incluirItemsListaPrecios: se
// congela en el momento de compilar, así resolverCamposCalculados
// (cotizador_runtime.go) no necesita otra consulta ni depende de que el
// catálogo no cambie después de compilado — igual que el precio de un
// ítem de Lista de Precios.
func (h *CompiladorHandler) incluirValorCalculoCatalogo(ctx context.Context, resultado *resultadoValidacion) error {
	ids := make([]string, 0)
	for _, tab := range resultado.Tabs {
		for _, el := range tab.Elementos {
			if el.Tipo == "CAMPO_CATALOGO" && el.CatalogoID != nil && strings.TrimSpace(*el.CatalogoID) != "" {
				ids = append(ids, strings.TrimSpace(*el.CatalogoID))
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT catalogo_id, valor_sistema, valor_calculo
		FROM catalogo_valores
		WHERE catalogo_id = ANY($1) AND activo = true
		ORDER BY catalogo_id, orden, valor_sistema`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	valoresPorCatalogo := make(map[string][]map[string]any)
	for rows.Next() {
		var catalogoID, valorSistema string
		var valorCalculo *float64
		if err := rows.Scan(&catalogoID, &valorSistema, &valorCalculo); err != nil {
			return err
		}
		var vc any
		if valorCalculo != nil {
			vc = *valorCalculo
		}
		valoresPorCatalogo[catalogoID] = append(valoresPorCatalogo[catalogoID], map[string]any{
			"valor_sistema": valorSistema, "valor_calculo": vc,
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range resultado.Tabs {
		for j := range resultado.Tabs[i].Elementos {
			el := &resultado.Tabs[i].Elementos[j]
			if el.Tipo != "CAMPO_CATALOGO" || el.CatalogoID == nil {
				continue
			}
			valores := valoresPorCatalogo[strings.TrimSpace(*el.CatalogoID)]
			if valores == nil {
				valores = make([]map[string]any, 0)
			}
			el.Configuracion["catalogo_valores_calculo"] = valores
		}
	}
	return nil
}

// tipoDatoDesdeElementoReferenciado resuelve el "tipo_dato" equivalente de
// una columna CAMPO_EXISTENTE a partir del elemento que integra: CAMPO y
// CAMPO_CATALOGO ya guardan su tipo en configuracion.tipo_campo (Sprint 2 /
// Ronda 2), y CAMPO_CALCULADO en configuracion.tipo_resultado (Ronda 2).
func tipoDatoDesdeElementoReferenciado(tipo string, cfg map[string]any) string {
	switch tipo {
	case "CAMPO", "CAMPO_CATALOGO":
		return strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["tipo_campo"])))
	case "CAMPO_CALCULADO":
		return strings.ToUpper(strings.TrimSpace(fmt.Sprint(cfg["tipo_resultado"])))
	}
	return ""
}

// anidarHijosCompilado saca de la lista plana de una sección los elementos
// que tienen componente_padre_id y los mueve al array "hijos" del
// padre correspondiente (CONTENEDOR u OPCIONES_PROPUESTA). Solo hay un nivel
// de anidado: los componentes padre no pueden estar dentro de otro padre. Un
// padre inexistente, inactivo o de otro tipo se reporta como error y el hijo
// queda arriba para no perderlo silenciosamente.
func anidarHijosCompilado(elementos []elementoCompilado, resultado *resultadoValidacion) []elementoCompilado {
	porID := make(map[string]int, len(elementos))
	for i, el := range elementos {
		porID[el.ElementoID] = i
	}
	hijosPorPadre := make(map[string][]elementoCompilado)
	top := make([]elementoCompilado, 0, len(elementos))
	for _, el := range elementos {
		if el.componentePadreID == "" {
			top = append(top, el)
			continue
		}
		indicePadre, existe := porID[el.componentePadreID]
		tipoPadre := ""
		if existe {
			tipoPadre = elementos[indicePadre].Tipo
		}
		if !existe || (tipoPadre != "CONTENEDOR" && tipoPadre != "OPCIONES_PROPUESTA") {
			resultado.Errores = append(resultado.Errores, fmt.Sprintf("El elemento %s referencia un componente padre (%s) que no existe o no es un Contenedor/Opciones de Propuesta activo.", el.ElementoID, el.componentePadreID))
			top = append(top, el)
			continue
		}
		hijosPorPadre[el.componentePadreID] = append(hijosPorPadre[el.componentePadreID], el)
	}
	for i := range top {
		if hijos, ok := hijosPorPadre[top[i].ElementoID]; ok {
			top[i].Hijos = hijos
		}
	}
	return top
}

func respuestaCompilador(resultado resultadoValidacion, compilado bool, version, compiladoID string) map[string]any {
	respuesta := map[string]any{
		"ok": true, "compilado": compilado, "valido": resultado.Valido,
		"cotizador_id": resultado.CalculadoraID, "errores": resultado.Errores,
		"advertencias": resultado.Advertencias, "resumen": resultado.Resumen,
	}
	if compilado {
		respuesta["version_configuracion"] = version
		respuesta["compilado_id"] = compiladoID
	}
	return respuesta
}

func valorString(valor *string) string {
	if valor == nil {
		return ""
	}
	return *valor
}

func valorInt(valor *int) int {
	if valor == nil {
		return 0
	}
	return *valor
}

func valorBool(valor *bool) bool {
	return valor != nil && *valor
}
