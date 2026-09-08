package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlantillaEstiloHandler administra el estilo visual único de cada plantilla.
type PlantillaEstiloHandler struct {
	DB *pgxpool.Pool
}

type editarEstiloRequest struct {
	Tema          *string `json:"tema"`
	FormatoPagina *string `json:"formato_pagina"`
	Margenes      *string `json:"margenes"`
	DisenoPortada *string `json:"diseno_portada"`
	EstiloTablas  *string `json:"estilo_tablas"`
}

var formatosPaginaValidos = map[string]bool{"CARTA": true, "A4": true}
var margenesValidos = map[string]bool{"COMPACTO": true, "NORMAL": true, "AMPLIO": true}
var portadasValidas = map[string]bool{"BANDA_SUPERIOR": true, "LATERAL": true, "MINIMALISTA": true, "BLOQUE": true}
var estilosTablaValidos = map[string]bool{"LINEAS": true, "SUAVE": true, "TARJETAS": true}

func (h *PlantillaEstiloHandler) Actualizar(w http.ResponseWriter, r *http.Request) {
	plantillaID := strings.TrimSpace(chi.URLParam(r, "id"))
	var req editarEstiloRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Tema == nil && req.FormatoPagina == nil && req.Margenes == nil &&
		req.DisenoPortada == nil && req.EstiloTablas == nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos una propiedad de estilo."})
		return
	}
	if req.Tema != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.Tema))
		if v == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El tema no puede quedar vacío."})
			return
		}
		req.Tema = &v
	}
	if err := normalizarOpcionEstilo(req.FormatoPagina, formatosPaginaValidos, "formato_pagina"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.Margenes, margenesValidos, "margenes"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.DisenoPortada, portadasValidas, "diseno_portada"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.EstiloTablas, estilosTablaValidos, "estilo_tablas"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, plantillaID).Scan(&existe); err != nil {
		log.Printf("plantilla estilo: error validando %s: %v", plantillaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la plantilla."})
		return
	}
	if !existe {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}

	var estilo plantillaEstilo
	err := h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_estilos
			(plantilla_id,tema,formato_pagina,margenes,diseno_portada,estilo_tablas)
		VALUES ($1,COALESCE($2::text,'PROFESIONAL'),COALESCE($3::text,'CARTA'),
		        COALESCE($4::text,'NORMAL'),COALESCE($5::text,'BANDA_SUPERIOR'),COALESCE($6::text,'LINEAS'))
		ON CONFLICT (plantilla_id) DO UPDATE SET
			tema=COALESCE($2::text,plantilla_estilos.tema),
			formato_pagina=COALESCE($3::text,plantilla_estilos.formato_pagina),
			margenes=COALESCE($4::text,plantilla_estilos.margenes),
			diseno_portada=COALESCE($5::text,plantilla_estilos.diseno_portada),
			estilo_tablas=COALESCE($6::text,plantilla_estilos.estilo_tablas)
		RETURNING estilo_id::text,plantilla_id::text,tema,formato_pagina,margenes,diseno_portada,estilo_tablas`,
		plantillaID, req.Tema, req.FormatoPagina, req.Margenes, req.DisenoPortada, req.EstiloTablas).Scan(
		&estilo.EstiloID, &estilo.PlantillaID, &estilo.Tema, &estilo.FormatoPagina,
		&estilo.Margenes, &estilo.DisenoPortada, &estilo.EstiloTablas,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		log.Printf("plantilla estilo: error actualizando %s: %v", plantillaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible actualizar el estilo."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "estilo": estilo, "mensaje": "Estilo actualizado."})
}

func normalizarOpcionEstilo(valor *string, permitidos map[string]bool, campo string) error {
	if valor == nil {
		return nil
	}
	v := strings.ToUpper(strings.TrimSpace(*valor))
	*valor = v
	if !permitidos[v] {
		return errors.New(campo + " no tiene un valor válido")
	}
	return nil
}
