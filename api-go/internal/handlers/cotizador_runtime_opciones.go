package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type cotizacionOpcion struct {
	OpcionID        string `json:"opcion_id"`
	ElementoPadreID string `json:"elemento_padre_id"`
	Nombre          string `json:"nombre"`
	EsRecomendada   bool   `json:"es_recomendada"`
	Orden           int    `json:"orden"`
}

type administrarOpcionRuntimeRequest struct {
	Version         int    `json:"version"`
	ElementoPadreID string `json:"elemento_padre_id"`
	Accion          string `json:"accion"`
	OpcionID        string `json:"opcion_id"`
	Nombre          string `json:"nombre"`
	EsRecomendada   *bool  `json:"es_recomendada"`
}

// asegurarOpcionesPropuesta crea las instancias iniciales en la primera
// apertura y las incrusta en cada elemento padre del compilado. El advisory
// lock evita que dos GET simultáneos creen dos juegos de opciones.
func (h *CotizadorRuntimeHandler) asegurarOpcionesPropuesta(ctx context.Context, runtime *contextoRuntime) error {
	padres := elementosOpcionesPropuesta(runtime.Estructura)
	if len(padres) == 0 {
		return nil
	}
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, padre := range padres {
		padreID := strings.TrimSpace(fmt.Sprint(padre["elemento_id"]))
		if padreID == "" {
			continue
		}
		claveLock := runtime.CotizacionID + ":" + fmt.Sprint(runtime.Version) + ":" + padreID
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, claveLock); err != nil {
			return err
		}
		var cantidad int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM cotizacion_opciones
			WHERE cotizacion_id=$1 AND numero_version=$2 AND elemento_padre_id=$3`,
			runtime.CotizacionID, runtime.Version, padreID).Scan(&cantidad); err != nil {
			return err
		}
		if cantidad == 0 {
			cfg, _ := padre["configuracion"].(map[string]any)
			cantidadInicial, ok := enteroDesdeConfiguracion(cfg, "cantidad_inicial")
			if !ok || cantidadInicial <= 0 {
				cantidadInicial = 1
			}
			nombres, err := normalizarNombresSugeridos(cfg["nombres_sugeridos"])
			if err != nil {
				return err
			}
			for i := 1; i <= cantidadInicial; i++ {
				nombre := fmt.Sprintf("Opción %d", i)
				if i <= len(nombres) {
					nombre = nombres[i-1]
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO cotizacion_opciones
						(cotizacion_id, numero_version, elemento_padre_id, nombre, orden)
					VALUES ($1, $2, $3, $4, $5)`, runtime.CotizacionID, runtime.Version, padreID, nombre, i); err != nil {
					return err
				}
			}
		}
		opciones, err := listarOpcionesPropuesta(ctx, tx, runtime.CotizacionID, runtime.Version, padreID)
		if err != nil {
			return err
		}
		opciones, err = autoRecomendarUnicaOpcion(ctx, tx, opciones)
		if err != nil {
			return err
		}
		padre["opciones"] = opciones
	}
	return tx.Commit(ctx)
}

// autoRecomendarUnicaOpcion aplica R09 del documento: con una sola opción
// de propuesta, se marca recomendada sola, sin que nadie tenga que
// marcarla a mano. No hace nada si ya hay 0 opciones (no debería pasar,
// ELIMINAR ya lo impide) o más de una.
func autoRecomendarUnicaOpcion(ctx context.Context, tx pgx.Tx, opciones []cotizacionOpcion) ([]cotizacionOpcion, error) {
	if len(opciones) != 1 || opciones[0].EsRecomendada {
		return opciones, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE cotizacion_opciones SET es_recomendada=true WHERE opcion_id=$1`, opciones[0].OpcionID); err != nil {
		return opciones, err
	}
	opciones[0].EsRecomendada = true
	return opciones, nil
}

// AdministrarOpciones atiende agregar, duplicar, renombrar, eliminar y
// recomendar. Todas las decisiones se toman contra la configuración
// compilada fijada para esta cotización, no contra el diseñador mutable.
func (h *CotizadorRuntimeHandler) AdministrarOpciones(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "cotizacion_id"))
	var req administrarOpcionRuntimeRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.ElementoPadreID = strings.ToUpper(strings.TrimSpace(req.ElementoPadreID))
	req.Accion = strings.ToUpper(strings.TrimSpace(req.Accion))
	req.OpcionID = strings.TrimSpace(req.OpcionID)
	req.Nombre = strings.TrimSpace(req.Nombre)
	if req.Version <= 0 || req.ElementoPadreID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar version y elemento_padre_id."})
		return
	}
	if !map[string]bool{"AGREGAR": true, "DUPLICAR": true, "RENOMBRAR": true, "ELIMINAR": true, "RECOMENDAR": true}[req.Accion] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "accion debe ser AGREGAR, DUPLICAR, RENOMBRAR, ELIMINAR o RECOMENDAR."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	runtime, err := h.cargarContexto(ctx, cotizacionID, req.Version, true)
	if err != nil {
		h.responderError(w, "administrando opciones", cotizacionID, err)
		return
	}
	if runtime.Historica {
		h.responderError(w, "administrando opciones", cotizacionID, &errorRuntime{409, "No se pueden modificar opciones de una versión histórica o cerrada."})
		return
	}
	if err := h.asegurarOpcionesPropuesta(ctx, &runtime); err != nil {
		log.Printf("cotizador runtime: error inicializando opciones de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible preparar las opciones de propuesta."})
		return
	}
	padre := buscarElementoEstructura(runtime.Estructura, req.ElementoPadreID)
	if padre == nil || strings.ToUpper(strings.TrimSpace(fmt.Sprint(padre["tipo"]))) != "OPCIONES_PROPUESTA" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "elemento_padre_id no corresponde a Opciones de Propuesta de esta cotización."})
		return
	}
	cfg, _ := padre["configuracion"].(map[string]any)
	if (req.Accion == "AGREGAR" || req.Accion == "DUPLICAR") && !boolDesdeConfiguracion(cfg, "permitir_duplicar", true) {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Este componente no permite agregar ni duplicar opciones."})
		return
	}
	if req.Accion == "RENOMBRAR" && !boolDesdeConfiguracion(cfg, "permitir_renombrar", true) {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Este componente no permite renombrar opciones."})
		return
	}
	if req.Accion == "ELIMINAR" && !boolDesdeConfiguracion(cfg, "permitir_eliminar", true) {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Este componente no permite eliminar opciones."})
		return
	}
	if req.Accion == "RECOMENDAR" && !boolDesdeConfiguracion(cfg, "permitir_recomendado", true) {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Este componente no permite marcar una opción como recomendada."})
		return
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la operación."})
		return
	}
	defer tx.Rollback(ctx)
	claveLock := cotizacionID + ":" + fmt.Sprint(req.Version) + ":" + req.ElementoPadreID
	if err := bloquearVersionEditable(ctx, tx, cotizacionID, req.Version); err != nil {
		h.responderError(w, "bloqueando versión", cotizacionID, err)
		return
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, claveLock); err != nil {
		h.responderError(w, "bloqueando opciones", cotizacionID, err)
		return
	}
	opciones, err := listarOpcionesPropuesta(ctx, tx, cotizacionID, req.Version, req.ElementoPadreID)
	if err != nil {
		h.responderError(w, "leyendo opciones", cotizacionID, err)
		return
	}
	porID := make(map[string]cotizacionOpcion, len(opciones))
	for _, opcion := range opciones {
		porID[opcion.OpcionID] = opcion
	}
	if req.Accion != "AGREGAR" {
		if req.OpcionID == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar opcion_id para esta acción."})
			return
		}
		if _, existe := porID[req.OpcionID]; !existe {
			escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La opción indicada no existe en este componente."})
			return
		}
	}

	maxOrden := 0
	for _, opcion := range opciones {
		if opcion.Orden > maxOrden {
			maxOrden = opcion.Orden
		}
	}
	switch req.Accion {
	case "AGREGAR":
		nombre := req.Nombre
		if nombre == "" {
			nombre = fmt.Sprintf("Opción %d", maxOrden+1)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO cotizacion_opciones (cotizacion_id, numero_version, elemento_padre_id, nombre, orden)
			VALUES ($1, $2, $3, $4, $5)`, cotizacionID, req.Version, req.ElementoPadreID, nombre, maxOrden+1)
	case "DUPLICAR":
		origen := porID[req.OpcionID]
		nombre := req.Nombre
		if nombre == "" {
			nombre = origen.Nombre + " copia"
		}
		var nuevaOpcionID string
		err = tx.QueryRow(ctx, `
			INSERT INTO cotizacion_opciones (cotizacion_id, numero_version, elemento_padre_id, nombre, orden)
			VALUES ($1, $2, $3, $4, $5) RETURNING opcion_id`,
			cotizacionID, req.Version, req.ElementoPadreID, nombre, maxOrden+1).Scan(&nuevaOpcionID)
		if err == nil {
			_, err = tx.Exec(ctx, `
				INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, opcion_id, valor)
				SELECT cotizacion_id, version, elemento_id, $1, valor
				FROM cotizacion_valores WHERE opcion_id=$2`, nuevaOpcionID, req.OpcionID)
		}
	case "RENOMBRAR":
		if req.Nombre == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre de la opción no puede quedar vacío."})
			return
		}
		_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET nombre=$2 WHERE opcion_id=$1`, req.OpcionID, req.Nombre)
	case "ELIMINAR":
		if len(opciones) <= 1 {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "No se puede eliminar la única opción de propuesta."})
			return
		}
		_, err = tx.Exec(ctx, `DELETE FROM cotizacion_opciones WHERE opcion_id=$1`, req.OpcionID)
	case "RECOMENDAR":
		recomendada := true
		if req.EsRecomendada != nil {
			recomendada = *req.EsRecomendada
		}
		if recomendada {
			_, err = tx.Exec(ctx, `
				UPDATE cotizacion_opciones SET es_recomendada=false
				WHERE cotizacion_id=$1 AND numero_version=$2 AND elemento_padre_id=$3 AND es_recomendada=true`,
				cotizacionID, req.Version, req.ElementoPadreID)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE cotizacion_opciones SET es_recomendada=$2 WHERE opcion_id=$1`, req.OpcionID, recomendada)
		}
	}
	if err != nil {
		log.Printf("cotizador runtime: error aplicando %s a %s: %v", req.Accion, req.OpcionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible actualizar las opciones de propuesta."})
		return
	}
	opciones, err = listarOpcionesPropuesta(ctx, tx, cotizacionID, req.Version, req.ElementoPadreID)
	if err == nil {
		opciones, err = autoRecomendarUnicaOpcion(ctx, tx, opciones)
	}
	// Después del primer guardado, cambiar la opción efectiva es un cambio
	// financiero: regenerar snapshot/salidas en esta misma transacción.
	if err == nil && runtime.Snapshot != nil {
		reglas, e := reglasCotizadorParaEvaluar(ctx, tx, runtime.CalculadoraID)
		if e != nil {
			err = e
		} else {
			err = h.persistirSalidasSnapshot(ctx, tx, &runtime, reglas)
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		h.responderError(w, "confirmando opciones y salidas", cotizacionID, err)
		return
	}

	respuesta := map[string]any{"ok": true, "opciones": opciones, "mensaje": "Opciones de propuesta actualizadas."}
	if len(opciones) > 1 {
		recomendadaExiste := false
		for _, opcion := range opciones {
			if opcion.EsRecomendada {
				recomendadaExiste = true
				break
			}
		}
		if !recomendadaExiste {
			respuesta["advertencia"] = "Hay más de una opción de propuesta y ninguna está marcada como recomendada."
		}
	}
	escribirJSON(w, http.StatusOK, respuesta)
}

func elementosOpcionesPropuesta(estructura map[string]any) []map[string]any {
	resultado := make([]map[string]any, 0)
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		recogerElementosOpciones(tab["elementos"], &resultado)
	}
	return resultado
}

func recogerElementosOpciones(raw any, destino *[]map[string]any) {
	elementos, _ := raw.([]any)
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) == "OPCIONES_PROPUESTA" {
			*destino = append(*destino, elemento)
		}
		if hijos, ok := elemento["hijos"].([]any); ok {
			recogerElementosOpciones(hijos, destino)
		}
	}
}

func buscarElementoEstructura(estructura map[string]any, elementoID string) map[string]any {
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		if encontrado := buscarElementoEnLista(tab["elementos"], elementoID); encontrado != nil {
			return encontrado
		}
	}
	return nil
}

func buscarElementoEnLista(raw any, elementoID string) map[string]any {
	elementos, _ := raw.([]any)
	for _, elementoRaw := range elementos {
		elemento, _ := elementoRaw.(map[string]any)
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(elemento["elemento_id"])), elementoID) {
			return elemento
		}
		if encontrado := buscarElementoEnLista(elemento["hijos"], elementoID); encontrado != nil {
			return encontrado
		}
	}
	return nil
}

func listarOpcionesPropuesta(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, cotizacionID string, version int, elementoPadreID string) ([]cotizacionOpcion, error) {
	rows, err := q.Query(ctx, `
		SELECT opcion_id, elemento_padre_id, nombre, es_recomendada, orden
		FROM cotizacion_opciones
		WHERE cotizacion_id=$1 AND numero_version=$2 AND elemento_padre_id=$3
		ORDER BY orden, opcion_id`, cotizacionID, version, elementoPadreID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultado := make([]cotizacionOpcion, 0)
	for rows.Next() {
		var opcion cotizacionOpcion
		if err := rows.Scan(&opcion.OpcionID, &opcion.ElementoPadreID, &opcion.Nombre, &opcion.EsRecomendada, &opcion.Orden); err != nil {
			return nil, err
		}
		resultado = append(resultado, opcion)
	}
	return resultado, rows.Err()
}
