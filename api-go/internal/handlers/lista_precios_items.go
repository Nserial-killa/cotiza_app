package handlers

// ListaPreciosItemsHandler administra los ítems de un elemento
// LISTA_PRECIOS (Ronda 3 del Diseñador, migración 0019). A diferencia de
// los catálogos (catalogos/catalogo_valores, globales), los ítems viven
// DENTRO de un elemento puntual — cada Lista de Precios tiene los suyos
// propios. No hay GET dedicado: los ítems viajan embebidos en la
// respuesta de GET /api/cotizador/elementos (ver ListarElementos en
// cotizador_tabs.go).

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

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ListaPreciosItemsHandler struct {
	DB *pgxpool.Pool
}

// numeroFlexible tolera que el frontend mande un número o un string
// numérico (mismo problema que enteroFlexible resuelve para enteros, ver
// catalogos.go).
type numeroFlexible float64

func (n *numeroFlexible) UnmarshalJSON(data []byte) error {
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
		if texto == "" {
			*n = 0
			return nil
		}
	}
	valor, err := strconv.ParseFloat(texto, 64)
	if err != nil {
		return fmt.Errorf("se esperaba un número")
	}
	*n = numeroFlexible(valor)
	return nil
}

// listaPreciosItem es la forma completa de un ítem — incluye
// costo_interno/margen_porcentaje a propósito: la usan ListarElementos
// (Diseñador, quien configura el precio SÍ debe verlos) y este handler.
// compilador.go/cotizador_runtime.go arman su PROPIA versión sin esas dos
// claves para lo que llega al Motor de Ejecución (ver el comentario en
// incluirItemsListaPrecios).
type listaPreciosItem struct {
	ItemID           string  `json:"item_id"`
	ElementoID       string  `json:"elemento_id"`
	Codigo           string  `json:"codigo"`
	Nombre           string  `json:"nombre"`
	Descripcion      *string `json:"descripcion"`
	Precio           float64 `json:"precio"`
	Moneda           string  `json:"moneda"`
	UnidadCobro      *string `json:"unidad_cobro"`
	CostoInterno     float64 `json:"costo_interno"`
	MargenPorcentaje float64 `json:"margen_porcentaje"`
	Orden            int     `json:"orden"`
	Activo           bool    `json:"activo"`
}

type crearItemListaPreciosRequest struct {
	Codigo           string         `json:"codigo"`
	Nombre           string         `json:"nombre"`
	Descripcion      string         `json:"descripcion"`
	Precio           numeroFlexible `json:"precio"`
	Moneda           string         `json:"moneda"`
	UnidadCobro      string         `json:"unidad_cobro"`
	CostoInterno     numeroFlexible `json:"costo_interno"`
	MargenPorcentaje numeroFlexible `json:"margen_porcentaje"`
	Orden            enteroFlexible `json:"orden"`
	Activo           *bool          `json:"activo"`
}

// Crear agrega un ítem a un elemento LISTA_PRECIOS ya existente.
// POST /api/cotizador/elementos/{elemento_id}/items
func (h *ListaPreciosItemsHandler) Crear(w http.ResponseWriter, r *http.Request) {
	elementoID := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "elemento_id")))
	if elementoID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el elemento (Lista de Precios)."})
		return
	}
	var req crearItemListaPreciosRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Codigo = strings.ToUpper(strings.TrimSpace(req.Codigo))
	req.Nombre = strings.TrimSpace(req.Nombre)
	if req.Codigo == "" || req.Nombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar código y nombre del ítem."})
		return
	}
	req.Moneda = strings.TrimSpace(req.Moneda)
	if req.Moneda == "" {
		req.Moneda = "US$"
	}
	activo := true
	if req.Activo != nil {
		activo = *req.Activo
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var tipo string
	err := h.DB.QueryRow(ctx, `SELECT tipo FROM elementos_tab_cotizador WHERE elemento_id=$1`, elementoID).Scan(&tipo)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El elemento indicado no existe."})
		return
	}
	if err != nil {
		log.Printf("lista precios items: error validando elemento %s: %v", elementoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el elemento."})
		return
	}
	if tipo != "LISTA_PRECIOS" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El elemento indicado no es una Lista de Precios."})
		return
	}

	var itemID string
	err = h.DB.QueryRow(ctx, `
		INSERT INTO lista_precios_items
			(elemento_id, codigo, nombre, descripcion, precio, moneda, unidad_cobro, costo_interno, margen_porcentaje, orden, activo)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8, $9, $10, $11)
		RETURNING item_id::text`,
		elementoID, req.Codigo, req.Nombre, req.Descripcion, float64(req.Precio), req.Moneda, req.UnidadCobro,
		float64(req.CostoInterno), float64(req.MargenPorcentaje), int(req.Orden), activo,
	).Scan(&itemID)
	if err != nil {
		if _, dup := comoViolacionUnica(err); dup {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("Ya existe un ítem con código %s en esta Lista de Precios.", req.Codigo)})
			return
		}
		log.Printf("lista precios items: error creando ítem de %s: %v", elementoID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear el ítem."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Ítem creado.", "item_id": itemID})
}

type editarItemListaPreciosRequest struct {
	Codigo           *string         `json:"codigo"`
	Nombre           *string         `json:"nombre"`
	Descripcion      *string         `json:"descripcion"`
	Precio           *numeroFlexible `json:"precio"`
	Moneda           *string         `json:"moneda"`
	UnidadCobro      *string         `json:"unidad_cobro"`
	CostoInterno     *numeroFlexible `json:"costo_interno"`
	MargenPorcentaje *numeroFlexible `json:"margen_porcentaje"`
	Orden            *enteroFlexible `json:"orden"`
	Activo           *bool           `json:"activo"`
}

// Editar hace una actualización parcial: solo toca las columnas cuyo
// puntero no vino nil (mismo patrón que UsuariosHandler.Editar).
// PATCH /api/cotizador/items/{item_id}
func (h *ListaPreciosItemsHandler) Editar(w http.ResponseWriter, r *http.Request) {
	itemID := strings.TrimSpace(chi.URLParam(r, "item_id"))
	if itemID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el ítem a editar."})
		return
	}
	var req editarItemListaPreciosRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	columnas := make([]string, 0, 9)
	valores := make([]any, 0, 9)
	agregar := func(columna string, valor any) {
		valores = append(valores, valor)
		columnas = append(columnas, columna+" = $"+strconv.Itoa(len(valores)))
	}
	if req.Codigo != nil {
		codigo := strings.ToUpper(strings.TrimSpace(*req.Codigo))
		if codigo == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El código no puede quedar vacío."})
			return
		}
		agregar("codigo", codigo)
	}
	if req.Nombre != nil {
		nombre := strings.TrimSpace(*req.Nombre)
		if nombre == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre no puede quedar vacío."})
			return
		}
		agregar("nombre", nombre)
	}
	if req.Descripcion != nil {
		agregar("descripcion", strings.TrimSpace(*req.Descripcion))
	}
	if req.Precio != nil {
		agregar("precio", float64(*req.Precio))
	}
	if req.Moneda != nil {
		moneda := strings.TrimSpace(*req.Moneda)
		if moneda == "" {
			moneda = "US$"
		}
		agregar("moneda", moneda)
	}
	if req.UnidadCobro != nil {
		agregar("unidad_cobro", strings.TrimSpace(*req.UnidadCobro))
	}
	if req.CostoInterno != nil {
		agregar("costo_interno", float64(*req.CostoInterno))
	}
	if req.MargenPorcentaje != nil {
		agregar("margen_porcentaje", float64(*req.MargenPorcentaje))
	}
	if req.Orden != nil {
		agregar("orden", int(*req.Orden))
	}
	if req.Activo != nil {
		agregar("activo", *req.Activo)
	}
	if len(columnas) == 0 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos un campo para editar."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	valores = append(valores, itemID)
	consulta := "UPDATE lista_precios_items SET " + strings.Join(columnas, ", ") + " WHERE item_id::text = $" + strconv.Itoa(len(valores))
	tag, err := h.DB.Exec(ctx, consulta, valores...)
	if err != nil {
		if _, dup := comoViolacionUnica(err); dup {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Ya existe otro ítem con ese código en esta Lista de Precios."})
			return
		}
		log.Printf("lista precios items: error editando %s: %v", itemID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible editar el ítem."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "El ítem indicado no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Ítem actualizado.", "item_id": itemID})
}

// Eliminar marca activo=false — nunca borra físicamente: si una cotización
// ya usó este ítem, tiene que seguir viéndose en su historial.
// DELETE /api/cotizador/items/{item_id}
func (h *ListaPreciosItemsHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	itemID := strings.TrimSpace(chi.URLParam(r, "item_id"))
	if itemID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el ítem a eliminar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `UPDATE lista_precios_items SET activo=false WHERE item_id::text=$1`, itemID)
	if err != nil {
		log.Printf("lista precios items: error eliminando %s: %v", itemID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible eliminar el ítem."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "El ítem indicado no existe."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "mensaje": "Ítem eliminado.", "item_id": itemID})
}
