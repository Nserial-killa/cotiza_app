package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CatalogosHandler expone las lecturas necesarias para el diseñador de catálogos.
type CatalogosHandler struct {
	DB *pgxpool.Pool
}

type catalogoDesigner struct {
	CatalogoID      string  `json:"catalogo_id"`
	NombreCatalogo  string  `json:"nombre_catalogo"`
	Descripcion     *string `json:"descripcion,omitempty"`
	Alcance         *string `json:"alcance,omitempty"`
	CatalogoPadreID *string `json:"catalogo_padre_id,omitempty"`
	Orden           int     `json:"orden"`
	Activo          bool    `json:"activo"`
	TipoCalculo     string  `json:"tipo_calculo"`
}

type valorCatalogoDesigner struct {
	ValorID      string   `json:"valor_id"`
	CatalogoID   string   `json:"catalogo_id"`
	Clave        *string  `json:"clave,omitempty"`
	TextoVisible string   `json:"texto_visible"`
	ValorSistema string   `json:"valor_sistema"`
	Descripcion  *string  `json:"descripcion,omitempty"`
	ValorPadreID *string  `json:"valor_padre_id,omitempty"`
	Orden        int      `json:"orden"`
	Activo       bool     `json:"activo"`
	ValorCalculo *float64 `json:"valor_calculo,omitempty"`
}

type relacionCatalogoDesigner struct {
	RelacionID      string `json:"relacion_id"`
	CatalogoPadreID string `json:"catalogo_padre_id"`
	ValorPadreID    string `json:"valor_padre_id"`
	CatalogoHijoID  string `json:"catalogo_hijo_id"`
	ValorHijoID     string `json:"valor_hijo_id"`
	Orden           int    `json:"orden"`
	Activo          bool   `json:"activo"`
}

type enteroFlexible int

func (n *enteroFlexible) UnmarshalJSON(data []byte) error {
	texto := strings.TrimSpace(string(data))
	if texto == "" || texto == "null" || texto == `""` {
		*n = 0
		return nil
	}
	if strings.HasPrefix(texto, `"`) {
		var valor string
		if err := json.Unmarshal(data, &valor); err != nil {
			return err
		}
		texto = strings.TrimSpace(valor)
	}
	valor, err := strconv.Atoi(texto)
	if err != nil {
		return fmt.Errorf("orden debe ser un número entero")
	}
	*n = enteroFlexible(valor)
	return nil
}

type guardarCatalogoRequest struct {
	CatalogoID      string         `json:"catalogo_id"`
	NombreCatalogo  string         `json:"nombre_catalogo"`
	Alcance         string         `json:"alcance"`
	Descripcion     string         `json:"descripcion"`
	Activo          bool           `json:"activo"`
	CatalogoPadreID string         `json:"catalogo_padre_id"`
	Orden           enteroFlexible `json:"orden"`
	TipoCalculo     string         `json:"tipo_calculo"`
}

type guardarValorRequest struct {
	ValorID      string                `json:"valor_id"`
	CatalogoID   string                `json:"catalogo_id"`
	Clave        string                `json:"clave"`
	TextoVisible string                `json:"texto_visible"`
	ValorSistema string                `json:"valor_sistema"`
	Descripcion  string                `json:"descripcion"`
	ValorPadreID string                `json:"valor_padre_id,omitempty"`
	Orden        enteroFlexible        `json:"orden"`
	Activo       bool                  `json:"activo"`
	ValorCalculo numeroCalculoFlexible `json:"valor_calculo"`
}

// tiposCalculoValidos son los 3 valores que acepta catalogos.tipo_calculo
// (migración 0023): SIN_VALOR es el default para no romper catálogos ya
// existentes — un catálogo puramente descriptivo (ej. CAT_TIPO_AGENTE)
// nunca entra en una fórmula. NUMERO/PORCENTAJE obligan a que cada valor
// activo del catálogo traiga valor_calculo (ver validarValorCalculo).
var tiposCalculoValidos = map[string]bool{"SIN_VALOR": true, "NUMERO": true, "PORCENTAJE": true}

// numeroCalculoFlexible distingue "no vino valor_calculo en el request" de
// "vino un 0" — a diferencia de enteroFlexible (que siempre tiene un
// entero, nunca opcional), acá la ausencia es un estado válido y distinto
// de cero: un catálogo SIN_VALOR debe llegar sin valor_calculo, uno
// NUMERO/PORCENTAJE no puede quedarse sin él (ver validarValorCalculo).
type numeroCalculoFlexible struct {
	Valor    float64
	Definido bool
}

func (n *numeroCalculoFlexible) UnmarshalJSON(data []byte) error {
	texto := strings.TrimSpace(string(data))
	if texto == "" || texto == "null" || texto == `""` {
		n.Definido = false
		n.Valor = 0
		return nil
	}
	if strings.HasPrefix(texto, `"`) {
		var valor string
		if err := json.Unmarshal(data, &valor); err != nil {
			return err
		}
		texto = strings.TrimSpace(valor)
		if texto == "" {
			n.Definido = false
			n.Valor = 0
			return nil
		}
	}
	valor, err := strconv.ParseFloat(texto, 64)
	if err != nil {
		return fmt.Errorf("valor_calculo debe ser un número")
	}
	n.Valor = valor
	n.Definido = true
	return nil
}

// Puntero devuelve nil cuando el request no trajo valor_calculo, o un
// *float64 con el valor recibido en caso contrario — la forma que espera
// upsertValor para escribir NULL o el número en catalogo_valores.
func (n numeroCalculoFlexible) Puntero() *float64 {
	if !n.Definido {
		return nil
	}
	valor := n.Valor
	return &valor
}

type guardarRelacionesRequest struct {
	guardarValorRequest
	CatalogoPadreID string   `json:"_catalogo_padre_id"`
	ValorPadreIDs   []string `json:"_valor_padre_ids"`
	Actualizar      bool     `json:"_actualizar_relaciones,omitempty"`
}

var errRelacionInvalida = errors.New("uno o más valores padre no pertenecen al catálogo indicado")

// ListarDesigner atiende los tres modos de lectura que usa la pantalla:
// solo_catalogos, valores_catalogo y relaciones_valor.
func (h *CatalogosHandler) ListarDesigner(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	modo := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("modo")))
	var respuesta map[string]any
	var err error

	switch modo {
	case "", "solo_catalogos":
		respuesta, err = h.listarCatalogos(ctx)
	case "valores_catalogo":
		respuesta, err = h.listarValores(ctx, strings.TrimSpace(r.URL.Query().Get("catalogo_id")))
	case "relaciones_valor":
		respuesta, err = h.listarRelaciones(ctx, r)
	default:
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Modo de consulta de catálogos no válido."})
		return
	}

	if err != nil {
		log.Printf("catalogos: error en modo %s: %v", modo, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los catálogos."})
		return
	}

	escribirJSON(w, http.StatusOK, respuesta)
}

// GuardarCatalogo crea o actualiza un catálogo usando catalogo_id como clave estable.
func (h *CatalogosHandler) GuardarCatalogo(w http.ResponseWriter, r *http.Request) {
	var req guardarCatalogoRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	normalizarCatalogoRequest(&req)
	if req.CatalogoID == "" || req.NombreCatalogo == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar catalogo_id y nombre_catalogo."})
		return
	}
	if req.CatalogoPadreID == req.CatalogoID {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un catálogo no puede ser su propio catálogo padre."})
		return
	}
	if !tiposCalculoValidos[req.TipoCalculo] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "tipo_calculo debe ser SIN_VALOR, NUMERO o PORCENTAJE."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Esta pantalla también puede desactivar un catálogo desde el
	// checkbox "Activo" del formulario, sin pasar por el botón
	// "Eliminar" dedicado (EliminarCatalogo) — así que la misma
	// validación tiene que aplicarse acá, o ese botón deja de ser el
	// único camino y la protección se puede saltear guardando el
	// formulario con "Activo" destildado.
	if !req.Activo {
		usos, err := catalogoEnUsoActivo(ctx, h.DB, req.CatalogoID)
		if err != nil {
			log.Printf("catalogos: error comprobando usos de %s: %v", req.CatalogoID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible comprobar el uso del catálogo."})
			return
		}
		if usos > 0 {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": fmt.Sprintf("No se puede desactivar el catálogo %s porque %d elemento(s) activo(s) del Diseñador todavía lo utilizan.", req.CatalogoID, usos)})
			return
		}
	}

	_, err := h.DB.Exec(ctx, `
		INSERT INTO catalogos
			(catalogo_id, nombre_catalogo, alcance, descripcion, activo, catalogo_padre_id, orden, tipo_calculo)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7, $8)
		ON CONFLICT (catalogo_id) DO UPDATE SET
			nombre_catalogo = EXCLUDED.nombre_catalogo,
			alcance = EXCLUDED.alcance,
			descripcion = EXCLUDED.descripcion,
			activo = EXCLUDED.activo,
			catalogo_padre_id = EXCLUDED.catalogo_padre_id,
			orden = EXCLUDED.orden,
			tipo_calculo = EXCLUDED.tipo_calculo`,
		req.CatalogoID, req.NombreCatalogo, req.Alcance, req.Descripcion,
		req.Activo, req.CatalogoPadreID, int(req.Orden), req.TipoCalculo)
	if err != nil {
		log.Printf("catalogos: error guardando catálogo %s: %v", req.CatalogoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar el catálogo."})
		return
	}

	respuesta := map[string]any{"ok": true, "mensaje": "Catálogo guardado.", "catalogo_id": req.CatalogoID}
	if req.TipoCalculo != "SIN_VALOR" {
		incompletos, err := valoresActivosSinValorCalculo(ctx, h.DB, req.CatalogoID)
		if err != nil {
			log.Printf("catalogos: error comprobando valor_calculo faltante en %s: %v", req.CatalogoID, err)
		} else if len(incompletos) > 0 {
			respuesta["advertencias"] = []string{fmt.Sprintf(
				"El catálogo %s quedó en tipo_calculo=%s pero tiene %d valor(es) activo(s) sin valor_calculo: %s.",
				req.CatalogoID, req.TipoCalculo, len(incompletos), strings.Join(incompletos, ", "))}
		}
	}
	escribirJSON(w, http.StatusOK, respuesta)
}

// valoresActivosSinValorCalculo lista los valor_id de los valores activos
// de un catálogo que todavía no tienen valor_calculo — usado por
// GuardarCatalogo para avisar (sin bloquear) cuando un catálogo pasa a
// tipo_calculo NUMERO/PORCENTAJE con datos ya cargados de antes.
func valoresActivosSinValorCalculo(ctx context.Context, db *pgxpool.Pool, catalogoID string) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT valor_id FROM catalogo_valores
		WHERE catalogo_id=$1 AND activo=true AND valor_calculo IS NULL
		ORDER BY COALESCE(orden, 0), valor_id`, catalogoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	incompletos := make([]string, 0)
	for rows.Next() {
		var valorID string
		if err := rows.Scan(&valorID); err != nil {
			return nil, err
		}
		incompletos = append(incompletos, valorID)
	}
	return incompletos, rows.Err()
}

// GuardarValor crea o actualiza un valor sin modificar sus relaciones explícitas.
func (h *CatalogosHandler) GuardarValor(w http.ResponseWriter, r *http.Request) {
	var req guardarValorRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	normalizarValorRequest(&req)
	if err := validarValorRequest(req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := validarValorCalculo(ctx, h.DB, req.CatalogoID, req.ValorCalculo); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Mismo motivo que en GuardarCatalogo: el checkbox "Activo" de
	// este formulario también puede desactivar el valor sin pasar por
	// el botón "Eliminar" dedicado (EliminarValor).
	if !req.Activo {
		relaciones, err := valorConRelacionesActivas(ctx, h.DB, req.ValorID)
		if err != nil {
			log.Printf("catalogos: error comprobando relaciones de %s: %v", req.ValorID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible comprobar las relaciones del valor."})
			return
		}
		if relaciones > 0 {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": fmt.Sprintf("No se puede desactivar el valor %s porque tiene %d relación(es) activa(s). Elimine primero esas relaciones.", req.ValorID, relaciones)})
			return
		}
	}

	if err := upsertValor(ctx, h.DB, req); err != nil {
		log.Printf("catalogos: error guardando valor %s: %v", req.ValorID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar el valor del catálogo."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Valor guardado.", "valor_id": req.ValorID})
}

// GuardarRelaciones actualiza el valor y reemplaza todas sus relaciones padre
// dentro de una única transacción.
func (h *CatalogosHandler) GuardarRelaciones(w http.ResponseWriter, r *http.Request) {
	var req guardarRelacionesRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	normalizarValorRequest(&req.guardarValorRequest)
	req.CatalogoPadreID = strings.TrimSpace(req.CatalogoPadreID)
	req.ValorPadreIDs = normalizarIDs(req.ValorPadreIDs)
	if err := validarValorRequest(req.guardarValorRequest); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.CatalogoPadreID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar _catalogo_padre_id."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if err := validarValorCalculo(ctx, h.DB, req.CatalogoID, req.ValorCalculo); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar la transacción."})
		return
	}
	defer tx.Rollback(ctx)

	if err = upsertValor(ctx, tx, req.guardarValorRequest); err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM catalogo_relaciones WHERE valor_hijo_id = $1`, req.ValorID)
	}
	for orden, valorPadreID := range req.ValorPadreIDs {
		if err != nil {
			break
		}
		var existe bool
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM catalogo_valores
				WHERE valor_id = $1 AND catalogo_id = $2
			)`, valorPadreID, req.CatalogoPadreID).Scan(&existe)
		if err == nil && !existe {
			err = errRelacionInvalida
			break
		}
		if err == nil {
			_, err = tx.Exec(ctx, `
				INSERT INTO catalogo_relaciones
					(catalogo_padre_id, valor_padre_id, catalogo_hijo_id, valor_hijo_id, orden, activo)
				VALUES ($1, $2, $3, $4, $5, true)`,
				req.CatalogoPadreID, valorPadreID, req.CatalogoID, req.ValorID, orden+1)
		}
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		status := http.StatusInternalServerError
		mensaje := "No fue posible guardar las relaciones del valor."
		if errors.Is(err, errRelacionInvalida) {
			status = http.StatusBadRequest
			mensaje = err.Error()
		} else {
			log.Printf("catalogos: error guardando relaciones de %s: %v", req.ValorID, err)
		}
		escribirJSON(w, status, map[string]any{"ok": false, "error": mensaje})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Relaciones guardadas.", "valor_id": req.ValorID, "relaciones": len(req.ValorPadreIDs)})
}

// EliminarCatalogo inactiva el catálogo si ningún elemento activo del
// diseñador lo utiliza. La fila se conserva para mantener el historial.
func (h *CatalogosHandler) EliminarCatalogo(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el catálogo a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	usos, err := catalogoEnUsoActivo(ctx, h.DB, id)
	if err != nil {
		log.Printf("catalogos: error comprobando usos de %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible comprobar el uso del catálogo."})
		return
	}
	if usos > 0 {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": fmt.Sprintf("No se puede eliminar el catálogo %s porque %d elemento(s) activo(s) del Diseñador todavía lo utilizan.", id, usos)})
		return
	}
	tag, err := h.DB.Exec(ctx, `UPDATE catalogos SET activo=false WHERE catalogo_id=$1`, id)
	if err != nil {
		log.Printf("catalogos: error inactivando %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar el catálogo."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "El catálogo indicado no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Catálogo eliminado.", "catalogo_id": id})
}

// EliminarValor inactiva un valor cuando ninguna relación activa depende de él.
func (h *CatalogosHandler) EliminarValor(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el valor a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	relaciones, err := valorConRelacionesActivas(ctx, h.DB, id)
	if err != nil {
		log.Printf("catalogos: error comprobando relaciones de %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible comprobar las relaciones del valor."})
		return
	}
	if relaciones > 0 {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": fmt.Sprintf("No se puede eliminar el valor %s porque tiene %d relación(es) activa(s). Elimine primero esas relaciones.", id, relaciones)})
		return
	}
	tag, err := h.DB.Exec(ctx, `UPDATE catalogo_valores SET activo=false WHERE valor_id=$1`, id)
	if err != nil {
		log.Printf("catalogos: error inactivando valor %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar el valor."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "El valor indicado no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Valor eliminado.", "valor_id": id})
}

// catalogoEnUsoActivo cuenta cuántos elementos activos del Diseñador
// (elementos_tab_cotizador, tipo CAMPO_CATALOGO) todavía apuntan a
// este catálogo. Usado tanto por EliminarCatalogo como por
// GuardarCatalogo — un catálogo en uso no se puede desactivar por
// ninguna de las dos rutas, no solo por el botón "Eliminar" dedicado.
func catalogoEnUsoActivo(ctx context.Context, db *pgxpool.Pool, catalogoID string) (int, error) {
	var usos int
	err := db.QueryRow(ctx, `SELECT COUNT(*) FROM elementos_tab_cotizador WHERE catalogo_id=$1 AND activo=true`, catalogoID).Scan(&usos)
	return usos, err
}

// valorConRelacionesActivas cuenta cuántas relaciones activas de
// catalogo_relaciones dependen de este valor (como padre o como
// hijo). Usado tanto por EliminarValor como por GuardarValor — mismo
// criterio que catalogoEnUsoActivo.
func valorConRelacionesActivas(ctx context.Context, db *pgxpool.Pool, valorID string) (int, error) {
	var relaciones int
	err := db.QueryRow(ctx, `SELECT COUNT(*) FROM catalogo_relaciones WHERE activo=true AND (valor_padre_id=$1 OR valor_hijo_id=$1)`, valorID).Scan(&relaciones)
	return relaciones, err
}

// EliminarRelacion borra físicamente una relación, que no tiene dependencias.
func (h *CatalogosHandler) EliminarRelacion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la relación a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM catalogo_relaciones WHERE relacion_id::text=$1`, id)
	if err != nil {
		log.Printf("catalogos: error eliminando relación %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar la relación."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La relación indicada no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Relación eliminada.", "relacion_id": id})
}

type ejecutorSQL interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func upsertValor(ctx context.Context, db ejecutorSQL, req guardarValorRequest) error {
	var valorCalculo any
	if ptr := req.ValorCalculo.Puntero(); ptr != nil {
		valorCalculo = *ptr
	}
	_, err := db.Exec(ctx, `
		INSERT INTO catalogo_valores
			(valor_id, catalogo_id, clave, texto_visible, valor_sistema, descripcion, valor_calculo, orden, activo)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7, $8, $9)
		ON CONFLICT (valor_id) DO UPDATE SET
			catalogo_id = EXCLUDED.catalogo_id,
			clave = EXCLUDED.clave,
			texto_visible = EXCLUDED.texto_visible,
			valor_sistema = EXCLUDED.valor_sistema,
			descripcion = EXCLUDED.descripcion,
			valor_calculo = EXCLUDED.valor_calculo,
			orden = EXCLUDED.orden,
			activo = EXCLUDED.activo`,
		req.ValorID, req.CatalogoID, req.Clave, req.TextoVisible,
		req.ValorSistema, req.Descripcion, valorCalculo, int(req.Orden), req.Activo)
	return err
}

func decodificarJSON(r *http.Request, destino any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destino); err != nil {
		return fmt.Errorf("el cuerpo de la solicitud no es JSON válido: %w", err)
	}
	return nil
}

func normalizarCatalogoRequest(req *guardarCatalogoRequest) {
	req.CatalogoID = strings.TrimSpace(req.CatalogoID)
	req.NombreCatalogo = strings.TrimSpace(req.NombreCatalogo)
	req.Alcance = strings.ToUpper(strings.TrimSpace(req.Alcance))
	if req.Alcance == "" {
		req.Alcance = "COTIZADOR"
	}
	req.Descripcion = strings.TrimSpace(req.Descripcion)
	req.CatalogoPadreID = strings.TrimSpace(req.CatalogoPadreID)
	req.TipoCalculo = strings.ToUpper(strings.TrimSpace(req.TipoCalculo))
	if req.TipoCalculo == "" {
		req.TipoCalculo = "SIN_VALOR"
	}
}

func normalizarValorRequest(req *guardarValorRequest) {
	req.ValorID = strings.TrimSpace(req.ValorID)
	req.CatalogoID = strings.TrimSpace(req.CatalogoID)
	req.Clave = strings.ToUpper(strings.TrimSpace(req.Clave))
	req.TextoVisible = strings.TrimSpace(req.TextoVisible)
	req.ValorSistema = strings.TrimSpace(req.ValorSistema)
	if req.ValorSistema == "" {
		req.ValorSistema = req.Clave
	}
	req.Descripcion = strings.TrimSpace(req.Descripcion)
}

func validarValorRequest(req guardarValorRequest) error {
	if req.ValorID == "" || req.CatalogoID == "" || req.Clave == "" || req.TextoVisible == "" {
		return errors.New("debe indicar valor_id, catalogo_id, clave y texto_visible")
	}
	return nil
}

// validarValorCalculo aplica la regla "o el catálogo calcula, o no calcula,
// sin términos medios" (documento de definición funcional, caso ISA
// Custom): un catálogo NUMERO/PORCENTAJE exige valor_calculo en cada uno
// de sus valores; uno SIN_VALOR lo rechaza, para no dejar datos ambiguos
// que después alguien confunda con un cálculo real.
func validarValorCalculo(ctx context.Context, db *pgxpool.Pool, catalogoID string, valorCalculo numeroCalculoFlexible) error {
	var tipoCalculo, nombreCatalogo string
	err := db.QueryRow(ctx, `SELECT tipo_calculo, nombre_catalogo FROM catalogos WHERE catalogo_id=$1`, catalogoID).Scan(&tipoCalculo, &nombreCatalogo)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("el catálogo %s no existe", catalogoID)
	}
	if err != nil {
		return err
	}
	if tipoCalculo != "SIN_VALOR" && !valorCalculo.Definido {
		return fmt.Errorf("el catálogo %s (%s) es de tipo_calculo=%s: debe indicar valor_calculo para este valor", catalogoID, nombreCatalogo, tipoCalculo)
	}
	if tipoCalculo == "SIN_VALOR" && valorCalculo.Definido {
		return fmt.Errorf("el catálogo %s (%s) es SIN_VALOR: no debe indicar valor_calculo para este valor", catalogoID, nombreCatalogo)
	}
	return nil
}

func normalizarIDs(ids []string) []string {
	vistos := make(map[string]bool, len(ids))
	resultado := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !vistos[id] {
			vistos[id] = true
			resultado = append(resultado, id)
		}
	}
	return resultado
}

func (h *CatalogosHandler) listarCatalogos(ctx context.Context) (map[string]any, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT catalogo_id, nombre_catalogo, descripcion, alcance,
		       catalogo_padre_id, COALESCE(orden, 0), activo, tipo_calculo
		FROM catalogos
		WHERE activo = true
		ORDER BY COALESCE(orden, 0), nombre_catalogo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]catalogoDesigner, 0)
	for rows.Next() {
		var item catalogoDesigner
		if err := rows.Scan(&item.CatalogoID, &item.NombreCatalogo, &item.Descripcion, &item.Alcance, &item.CatalogoPadreID, &item.Orden, &item.Activo, &item.TipoCalculo); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return map[string]any{"ok": true, "catalogos": items}, rows.Err()
}

func (h *CatalogosHandler) listarValores(ctx context.Context, catalogoID string) (map[string]any, error) {
	if catalogoID == "" {
		return map[string]any{"ok": true, "valores": []valorCatalogoDesigner{}}, nil
	}
	items, err := h.consultarValores(ctx, catalogoID)
	return map[string]any{"ok": err == nil, "valores": items}, err
}

func (h *CatalogosHandler) consultarValores(ctx context.Context, catalogoID string) ([]valorCatalogoDesigner, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT valor_id, catalogo_id, clave, texto_visible, valor_sistema,
		       descripcion, valor_padre_id, COALESCE(orden, 0), activo, valor_calculo
		FROM catalogo_valores
		WHERE catalogo_id = $1 AND activo = true
		ORDER BY COALESCE(orden, 0), texto_visible`, catalogoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]valorCatalogoDesigner, 0)
	for rows.Next() {
		var item valorCatalogoDesigner
		if err := rows.Scan(&item.ValorID, &item.CatalogoID, &item.Clave, &item.TextoVisible, &item.ValorSistema, &item.Descripcion, &item.ValorPadreID, &item.Orden, &item.Activo, &item.ValorCalculo); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *CatalogosHandler) listarRelaciones(ctx context.Context, r *http.Request) (map[string]any, error) {
	hijoID := strings.TrimSpace(r.URL.Query().Get("catalogo_hijo_id"))
	valorHijoID := strings.TrimSpace(r.URL.Query().Get("valor_hijo_id"))
	padreID := strings.TrimSpace(r.URL.Query().Get("catalogo_padre_id"))

	rows, err := h.DB.Query(ctx, `
		SELECT relacion_id::text, catalogo_padre_id, valor_padre_id,
		       catalogo_hijo_id, valor_hijo_id, COALESCE(orden, 0), activo
		FROM catalogo_relaciones
		WHERE ($1 = '' OR catalogo_hijo_id = $1)
		  AND ($2 = '' OR valor_hijo_id = $2)
		  AND activo = true
		ORDER BY COALESCE(orden, 0)`, hijoID, valorHijoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relaciones := make([]relacionCatalogoDesigner, 0)
	for rows.Next() {
		var item relacionCatalogoDesigner
		if err := rows.Scan(&item.RelacionID, &item.CatalogoPadreID, &item.ValorPadreID, &item.CatalogoHijoID, &item.ValorHijoID, &item.Orden, &item.Activo); err != nil {
			return nil, err
		}
		relaciones = append(relaciones, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	valoresPadre := make([]valorCatalogoDesigner, 0)
	if padreID != "" {
		valoresPadre, err = h.consultarValores(ctx, padreID)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{"ok": true, "relaciones": relaciones, "valores_padre": valoresPadre}, nil
}
