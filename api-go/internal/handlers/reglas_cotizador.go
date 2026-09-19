package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReglasCotizadorHandler administra reglas_cotizador: condición sobre un
// campo + acción sobre uno o más campos objetivo, todo dentro de UNA
// calculadora. Distinto del catálogo genérico de reglas.go (ver el
// comentario ahí) — acá sí hay lógica real de condición/acción, evaluada
// por reglas_evaluacion.go.
type ReglasCotizadorHandler struct {
	DB *pgxpool.Pool
}

type reglaCotizadorDesigner struct {
	ReglaID          string   `json:"regla_id"`
	CalculadoraID    string   `json:"calculadora_id"`
	Nombre           *string  `json:"nombre,omitempty"`
	CampoCondicionID string   `json:"campo_condicion_id"`
	Operador         string   `json:"operador"`
	ValorComparacion *string  `json:"valor_comparacion,omitempty"`
	Accion           string   `json:"accion"`
	CamposObjetivo   []string `json:"campos_objetivo"`
	ValorAccion      *string  `json:"valor_accion,omitempty"`
	Mensaje          *string  `json:"mensaje,omitempty"`
	Orden            int      `json:"orden"`
	Activo           bool     `json:"activo"`
}

type guardarReglaCotizadorRequest struct {
	ReglaID          string         `json:"regla_id"`
	CalculadoraID    string         `json:"calculadora_id"`
	Nombre           string         `json:"nombre"`
	CampoCondicionID string         `json:"campo_condicion_id"`
	Operador         string         `json:"operador"`
	ValorComparacion string         `json:"valor_comparacion"`
	Accion           string         `json:"accion"`
	CamposObjetivo   []string       `json:"campos_objetivo"`
	ValorAccion      string         `json:"valor_accion"`
	Mensaje          string         `json:"mensaje"`
	Orden            enteroFlexible `json:"orden"`
	Activo           bool           `json:"activo"`
}

// operadoresReglaCotizadorValidos y accionesReglaCotizadorValidas reflejan
// los CHECK de 0024_reglas_cotizador.sql — se repiten acá para devolver un
// mensaje claro al frontend en vez de un error genérico de constraint.
var operadoresReglaCotizadorValidos = map[string]bool{
	"IGUAL_A": true, "DISTINTO_DE": true, "MAYOR_QUE": true, "MENOR_QUE": true,
	"MAYOR_O_IGUAL_QUE": true, "MENOR_O_IGUAL_QUE": true, "ESTA_VACIO": true, "NO_ESTA_VACIO": true,
}

var operadoresSinValorComparacion = map[string]bool{"ESTA_VACIO": true, "NO_ESTA_VACIO": true}

var accionesReglaCotizadorValidas = map[string]bool{
	"OCULTAR": true, "MOSTRAR": true, "DESHABILITAR": true, "HABILITAR": true,
	"PONER_EN_CERO": true, "EXIGIR_MINIMO": true, "CAMPO_REQUERIDO": true, "BLOQUEAR_GUARDADO": true,
}

// accionesSinCamposObjetivo: BLOQUEAR_GUARDADO no apunta a ningún campo, la
// condición sola ya bloquea el guardado completo.
var accionesSinCamposObjetivo = map[string]bool{"BLOQUEAR_GUARDADO": true}

// Listar devuelve las reglas_cotizador de una calculadora (todas, activas e
// inactivas — el Diseñador necesita poder reactivarlas).
func (h *ReglasCotizadorHandler) Listar(w http.ResponseWriter, r *http.Request) {
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar calculadora_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT rc.regla_id, rc.calculadora_id, rc.nombre, rc.campo_condicion_id, rc.operador,
		       rc.valor_comparacion, rc.accion, rc.valor_accion, rc.mensaje, rc.orden, rc.activo,
		       COALESCE(array_agg(o.elemento_id ORDER BY o.orden) FILTER (WHERE o.elemento_id IS NOT NULL), '{}')
		FROM reglas_cotizador rc
		LEFT JOIN reglas_cotizador_campos_objetivo o ON o.regla_id = rc.regla_id
		WHERE rc.calculadora_id = $1
		GROUP BY rc.regla_id
		ORDER BY rc.orden, rc.regla_id`, calculadoraID)
	if err != nil {
		log.Printf("reglas_cotizador: error listando %s: %v", calculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las reglas del cotizador."})
		return
	}
	defer rows.Close()

	reglas := make([]reglaCotizadorDesigner, 0)
	for rows.Next() {
		var item reglaCotizadorDesigner
		if err := rows.Scan(&item.ReglaID, &item.CalculadoraID, &item.Nombre, &item.CampoCondicionID,
			&item.Operador, &item.ValorComparacion, &item.Accion, &item.ValorAccion, &item.Mensaje,
			&item.Orden, &item.Activo, &item.CamposObjetivo); err != nil {
			log.Printf("reglas_cotizador: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las reglas del cotizador."})
			return
		}
		reglas = append(reglas, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("reglas_cotizador: error leyendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las reglas del cotizador."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "reglas": reglas})
}

// Guardar crea o actualiza una regla_cotizador (regla_id como clave
// estable) y reemplaza sus campos_objetivo dentro de la misma transacción.
func (h *ReglasCotizadorHandler) Guardar(w http.ResponseWriter, r *http.Request) {
	var req guardarReglaCotizadorRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	normalizarReglaCotizadorRequest(&req)

	if req.ReglaID == "" || req.CalculadoraID == "" || req.CampoCondicionID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar regla_id, calculadora_id y campo_condicion_id."})
		return
	}
	if !operadoresReglaCotizadorValidos[req.Operador] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "operador no es válido."})
		return
	}
	if !accionesReglaCotizadorValidas[req.Accion] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "accion no es válida."})
		return
	}
	if !operadoresSinValorComparacion[req.Operador] && req.ValorComparacion == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El operador %s requiere valor_comparacion.", req.Operador)})
		return
	}
	if req.Accion == "EXIGIR_MINIMO" {
		if req.ValorAccion == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La acción EXIGIR_MINIMO requiere valor_accion."})
			return
		}
		if _, err := strconv.ParseFloat(req.ValorAccion, 64); err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "valor_accion debe ser un número."})
			return
		}
	}
	if !accionesSinCamposObjetivo[req.Accion] && len(req.CamposObjetivo) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("La acción %s requiere al menos un campo objetivo.", req.Accion)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var existeCalculadora bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM calculadoras WHERE calculadora_id=$1)`, req.CalculadoraID).Scan(&existeCalculadora); err != nil {
		log.Printf("reglas_cotizador: error validando calculadora %s: %v", req.CalculadoraID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la calculadora."})
		return
	}
	if !existeCalculadora {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La calculadora indicada no existe."})
		return
	}

	pertenece, err := elementoPerteneceCalculadora(ctx, h.DB, req.CampoCondicionID, req.CalculadoraID)
	if err != nil {
		log.Printf("reglas_cotizador: error validando campo_condicion_id %s: %v", req.CampoCondicionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar campo_condicion_id."})
		return
	}
	if !pertenece {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("campo_condicion_id (%s) debe ser un elemento activo de esta calculadora.", req.CampoCondicionID)})
		return
	}
	for _, campoObjetivoID := range req.CamposObjetivo {
		pertenece, err := elementoPerteneceCalculadora(ctx, h.DB, campoObjetivoID, req.CalculadoraID)
		if err != nil {
			log.Printf("reglas_cotizador: error validando campo objetivo %s: %v", campoObjetivoID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los campos objetivo."})
			return
		}
		if !pertenece {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El campo objetivo %s debe ser un elemento activo de esta calculadora.", campoObjetivoID)})
			return
		}
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la transacción."})
		return
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO reglas_cotizador
			(regla_id, calculadora_id, nombre, campo_condicion_id, operador, valor_comparacion,
			 accion, valor_accion, mensaje, orden, activo)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11)
		ON CONFLICT (regla_id) DO UPDATE SET
			calculadora_id = EXCLUDED.calculadora_id,
			nombre = EXCLUDED.nombre,
			campo_condicion_id = EXCLUDED.campo_condicion_id,
			operador = EXCLUDED.operador,
			valor_comparacion = EXCLUDED.valor_comparacion,
			accion = EXCLUDED.accion,
			valor_accion = EXCLUDED.valor_accion,
			mensaje = EXCLUDED.mensaje,
			orden = EXCLUDED.orden,
			activo = EXCLUDED.activo`,
		req.ReglaID, req.CalculadoraID, req.Nombre, req.CampoCondicionID, req.Operador, req.ValorComparacion,
		req.Accion, req.ValorAccion, req.Mensaje, int(req.Orden), req.Activo)
	if err != nil {
		log.Printf("reglas_cotizador: error guardando %s: %v", req.ReglaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar la regla."})
		return
	}

	if _, err = tx.Exec(ctx, `DELETE FROM reglas_cotizador_campos_objetivo WHERE regla_id=$1`, req.ReglaID); err != nil {
		log.Printf("reglas_cotizador: error limpiando campos objetivo de %s: %v", req.ReglaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los campos objetivo."})
		return
	}
	for orden, campoObjetivoID := range req.CamposObjetivo {
		if _, err = tx.Exec(ctx, `
			INSERT INTO reglas_cotizador_campos_objetivo (regla_id, elemento_id, orden)
			VALUES ($1, $2, $3)`, req.ReglaID, campoObjetivoID, orden+1); err != nil {
			log.Printf("reglas_cotizador: error insertando campo objetivo %s de %s: %v", campoObjetivoID, req.ReglaID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los campos objetivo."})
			return
		}
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("reglas_cotizador: error confirmando %s: %v", req.ReglaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar la regla."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Regla guardada.", "regla_id": req.ReglaID})
}

// Eliminar borra físicamente una regla_cotizador (sin dependencias de otras
// tablas: nada más referencia una regla_cotizador, a diferencia de un
// catálogo o un elemento del diseñador).
func (h *ReglasCotizadorHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	reglaID := strings.TrimSpace(chi.URLParam(r, "id"))
	if reglaID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la regla a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM reglas_cotizador WHERE regla_id=$1`, reglaID)
	if err != nil {
		log.Printf("reglas_cotizador: error eliminando %s: %v", reglaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar la regla."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La regla indicada no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Regla eliminada.", "regla_id": reglaID})
}

func normalizarReglaCotizadorRequest(req *guardarReglaCotizadorRequest) {
	req.ReglaID = strings.ToUpper(strings.TrimSpace(req.ReglaID))
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.CampoCondicionID = strings.TrimSpace(req.CampoCondicionID)
	req.Operador = strings.ToUpper(strings.TrimSpace(req.Operador))
	req.ValorComparacion = strings.TrimSpace(req.ValorComparacion)
	req.Accion = strings.ToUpper(strings.TrimSpace(req.Accion))
	req.ValorAccion = strings.TrimSpace(req.ValorAccion)
	req.Mensaje = strings.TrimSpace(req.Mensaje)

	// Limpiar campos que no aplican a la combinación operador/acción
	// elegida, para no dejar datos ambiguos dando vueltas (mismo
	// criterio que valor_calculo en catalogos.go).
	if operadoresSinValorComparacion[req.Operador] {
		req.ValorComparacion = ""
	}
	if req.Accion != "EXIGIR_MINIMO" {
		req.ValorAccion = ""
	}
	if accionesSinCamposObjetivo[req.Accion] {
		req.CamposObjetivo = nil
	}
	req.CamposObjetivo = normalizarIDs(req.CamposObjetivo)
}

// elementoPerteneceCalculadora comprueba que un elemento esté activo y
// pertenezca a la calculadora indicada — ya sea porque su tab es propio de
// esa calculadora, o porque el tab está asociado (tabs_cotizador_asociaciones,
// el mismo mecanismo de "tabs compartidos" que usa compilador.go).
func elementoPerteneceCalculadora(ctx context.Context, db *pgxpool.Pool, elementoID, calculadoraID string) (bool, error) {
	var existe bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM elementos_tab_cotizador e
			WHERE e.elemento_id = $1 AND e.activo = true AND (
				e.tab_id IN (SELECT tab_id FROM tabs_cotizador WHERE calculadora_id = $2 AND activo = true)
				OR e.tab_id IN (SELECT tab_id FROM tabs_cotizador_asociaciones WHERE calculadora_id = $2)
			)
		)`, elementoID, calculadoraID).Scan(&existe)
	return existe, err
}

// reglasCotizadorParaEvaluar consulta las reglas activas de una calculadora
// y las devuelve ya en la forma que usa el motor de evaluación
// (reglas_evaluacion.go) — usado tanto por cotizador_runtime.go (GET/POST)
// como por las pruebas de integración.
func reglasCotizadorParaEvaluar(ctx context.Context, db consultadorRuntime, calculadoraID string) ([]reglaCotizadorEval, error) {
	rows, err := db.Query(ctx, `
		SELECT rc.regla_id, rc.campo_condicion_id, rc.operador, COALESCE(rc.valor_comparacion, ''),
		       rc.accion, COALESCE(rc.valor_accion, ''), COALESCE(rc.mensaje, ''),
		       COALESCE(array_agg(o.elemento_id ORDER BY o.orden) FILTER (WHERE o.elemento_id IS NOT NULL), '{}')
		FROM reglas_cotizador rc
		LEFT JOIN reglas_cotizador_campos_objetivo o ON o.regla_id = rc.regla_id
		WHERE rc.calculadora_id = $1 AND rc.activo = true
		GROUP BY rc.regla_id
		ORDER BY rc.orden, rc.regla_id`, calculadoraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reglas := make([]reglaCotizadorEval, 0)
	for rows.Next() {
		var regla reglaCotizadorEval
		if err := rows.Scan(&regla.ReglaID, &regla.CampoCondicionID, &regla.Operador, &regla.ValorComparacion,
			&regla.Accion, &regla.ValorAccion, &regla.Mensaje, &regla.CamposObjetivo); err != nil {
			return nil, err
		}
		regla.Activo = true
		reglas = append(reglas, regla)
	}
	return reglas, rows.Err()
}
