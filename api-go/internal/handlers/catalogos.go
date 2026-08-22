package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

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
}

type valorCatalogoDesigner struct {
	ValorID      string  `json:"valor_id"`
	CatalogoID   string  `json:"catalogo_id"`
	Clave        *string `json:"clave,omitempty"`
	TextoVisible string  `json:"texto_visible"`
	ValorSistema string  `json:"valor_sistema"`
	Descripcion  *string `json:"descripcion,omitempty"`
	ValorPadreID *string `json:"valor_padre_id,omitempty"`
	Orden        int     `json:"orden"`
	Activo       bool    `json:"activo"`
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

func (h *CatalogosHandler) listarCatalogos(ctx context.Context) (map[string]any, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT catalogo_id, nombre_catalogo, descripcion, alcance,
		       catalogo_padre_id, COALESCE(orden, 0), activo
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
		if err := rows.Scan(&item.CatalogoID, &item.NombreCatalogo, &item.Descripcion, &item.Alcance, &item.CatalogoPadreID, &item.Orden, &item.Activo); err != nil {
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
		       descripcion, valor_padre_id, COALESCE(orden, 0), activo
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
		if err := rows.Scan(&item.ValorID, &item.CatalogoID, &item.Clave, &item.TextoVisible, &item.ValorSistema, &item.Descripcion, &item.ValorPadreID, &item.Orden, &item.Activo); err != nil {
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
