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
	ElementoID    string         `json:"elemento_id"`
	Tipo          string         `json:"tipo"`
	Etiqueta      *string        `json:"etiqueta"`
	CatalogoID    *string        `json:"catalogo_id"`
	ColumnasAncho int            `json:"columnas_ancho"`
	Orden         int            `json:"orden"`
	Requerido     bool           `json:"requerido"`
	Configuracion map[string]any `json:"configuracion"`
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
		SELECT t.tab_id, t.nombre, t.descripcion, t.alcance, t.orden,
		       e.elemento_id, e.tipo, e.etiqueta, e.catalogo_id,
		       e.columnas_ancho, e.orden, e.requerido, e.configuracion,
		       CASE WHEN e.catalogo_id IS NULL THEN NULL ELSE c.activo END
		FROM tabs_cotizador t
		LEFT JOIN elementos_tab_cotizador e ON e.tab_id=t.tab_id AND e.activo=true
		LEFT JOIN catalogos c ON c.catalogo_id=e.catalogo_id
		WHERE t.calculadora_id=$1 AND t.activo=true
		ORDER BY t.orden, t.tab_id, e.orden, e.elemento_id`, calculadoraID)
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
		var columnasAncho, elementoOrden *int
		var requerido *bool
		var configuracion map[string]any
		var catalogoActivo *bool
		if err := rows.Scan(&tabID, &nombre, &descripcion, &alcance, &orden, &elementoID, &tipo, &etiqueta, &catalogoID, &columnasAncho, &elementoOrden, &requerido, &configuracion, &catalogoActivo); err != nil {
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
		el := elementoCompilado{ElementoID: *elementoID, Tipo: valorString(tipo), Etiqueta: etiqueta, CatalogoID: catalogoID, ColumnasAncho: valorInt(columnasAncho), Orden: valorInt(elementoOrden), Requerido: valorBool(requerido), Configuracion: cfg}
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
	if err := h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM reglas WHERE activo=true`).Scan(&resultado.Resumen.Reglas); err != nil {
		return resultado, err
	}
	resultado.Valido = len(resultado.Errores) == 0
	return resultado, nil
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
