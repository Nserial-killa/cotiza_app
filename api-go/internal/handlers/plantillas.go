package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// PlantillasHandler administra los datos generales de las plantillas y arma
// el detalle completo que consumen tanto la vista previa como el wizard.
type PlantillasHandler struct {
	DB *pgxpool.Pool
}

type plantillaListado struct {
	PlantillaID                string   `json:"plantilla_id"`
	Codigo                     string   `json:"codigo"`
	Nombre                     string   `json:"nombre"`
	Descripcion                *string  `json:"descripcion"`
	Estado                     string   `json:"estado"`
	Version                    int      `json:"version"`
	OrganizacionID             *string  `json:"organizacion_id,omitempty"`
	DisponibleNuevasPropuestas bool     `json:"disponible_nuevas_propuestas"`
	PermiteDuplicar            bool     `json:"permite_duplicar"`
	CalculadoraIDs             []string `json:"calculadora_ids"`
	Cotizadores                []string `json:"cotizadores"`
	TiposPropuesta             []string `json:"tipos_propuesta"`
}

type contadoresPlantillas struct {
	Total      int `json:"total"`
	Publicadas int `json:"publicadas"`
	Borradores int `json:"borradores"`
	Estilos    int `json:"estilos"`
}

type organizacionOpcionPlantilla struct {
	OrganizacionID string `json:"organizacion_id"`
	Nombre         string `json:"nombre"`
}

type plantillaDetalle struct {
	PlantillaID                string             `json:"plantilla_id"`
	Codigo                     string             `json:"codigo"`
	Nombre                     string             `json:"nombre"`
	Descripcion                *string            `json:"descripcion"`
	Estado                     string             `json:"estado"`
	Version                    int                `json:"version"`
	OrganizacionID             *string            `json:"organizacion_id,omitempty"`
	DisponibleNuevasPropuestas bool               `json:"disponible_nuevas_propuestas"`
	PermiteDuplicar            bool               `json:"permite_duplicar"`
	CalculadoraIDs             []string           `json:"calculadora_ids"`
	TiposPropuesta             []string           `json:"tipos_propuesta"`
	Secciones                  []plantillaSeccion `json:"secciones"`
	Estilo                     *plantillaEstilo   `json:"estilo"`
}

type plantillaSeccion struct {
	SeccionID     string            `json:"seccion_id"`
	PlantillaID   string            `json:"plantilla_id"`
	Nombre        string            `json:"nombre"`
	Titulo        *string           `json:"titulo"`
	MostrarTitulo bool              `json:"mostrar_titulo"`
	Visibilidad   string            `json:"visibilidad"`
	DisenoBloques string            `json:"diseno_bloques"`
	MostrarWeb    bool              `json:"mostrar_web"`
	MostrarPDF    bool              `json:"mostrar_pdf"`
	Orden         int               `json:"orden"`
	Bloques       []plantillaBloque `json:"bloques"`
}

type plantillaBloque struct {
	BloqueID      string                 `json:"bloque_id"`
	SeccionID     string                 `json:"seccion_id"`
	TipoBloque    string                 `json:"tipo_bloque"`
	NombreInterno string                 `json:"nombre_interno"`
	Titulo        *string                `json:"titulo"`
	Contenido     *string                `json:"contenido"`
	Columna       int                    `json:"columna"`
	MostrarWeb    bool                   `json:"mostrar_web"`
	MostrarPDF    bool                   `json:"mostrar_pdf"`
	Orden         int                    `json:"orden"`
	Vinculaciones []plantillaVinculacion `json:"vinculaciones"`
}

type plantillaVinculacion struct {
	VinculacionID string `json:"vinculacion_id"`
	BloqueID      string `json:"bloque_id"`
	CalculadoraID string `json:"calculadora_id"`
	FuenteTipo    string `json:"fuente_tipo"`
	FuenteID      string `json:"fuente_id"`
}

type plantillaEstilo struct {
	EstiloID      string `json:"estilo_id"`
	PlantillaID   string `json:"plantilla_id"`
	Tema          string `json:"tema"`
	FormatoPagina string `json:"formato_pagina"`
	Margenes      string `json:"margenes"`
	DisenoPortada string `json:"diseno_portada"`
	EstiloTablas  string `json:"estilo_tablas"`
}

type crearPlantillaRequest struct {
	Nombre                     string   `json:"nombre"`
	Descripcion                string   `json:"descripcion"`
	CrearDesde                 string   `json:"crear_desde"`
	CalculadoraIDs             []string `json:"calculadora_ids"`
	TiposPropuesta             []string `json:"tipos_propuesta"`
	OrganizacionID             string   `json:"organizacion_id"`
	DisponibleNuevasPropuestas *bool    `json:"disponible_nuevas_propuestas"`
	PermiteDuplicar            *bool    `json:"permite_duplicar"`
}

// Los punteros distinguen un campo ausente de un valor vacío o false. Las
// asociaciones también son punteros: un arreglo vacío explícito las limpia.
type editarPlantillaRequest struct {
	Nombre                     *string   `json:"nombre"`
	Descripcion                *string   `json:"descripcion"`
	CalculadoraIDs             *[]string `json:"calculadora_ids"`
	TiposPropuesta             *[]string `json:"tipos_propuesta"`
	OrganizacionID             *string   `json:"organizacion_id"`
	DisponibleNuevasPropuestas *bool     `json:"disponible_nuevas_propuestas"`
	PermiteDuplicar            *bool     `json:"permite_duplicar"`
}

var estadosPlantillaValidos = map[string]bool{
	"Borrador":  true,
	"Publicada": true,
	"Archivada": true,
}

// Opciones alimenta los selectores del paso 1. No existe un catálogo maestro
// de tipos de propuesta: se usan los valores reales ya presentes en
// cotizaciones y plantillas. En una instalación nueva, todavía sin ninguna de
// las dos, se devuelve una base comercial mínima para no bloquear la primera
// plantilla; esos valores dejan de ser necesarios apenas existan datos reales.
func (h *PlantillasHandler) Opciones(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT tipo_propuesta
		  FROM (
			SELECT DISTINCT btrim(tipo_propuesta) AS tipo_propuesta
			  FROM cotizaciones WHERE btrim(COALESCE(tipo_propuesta,'')) <> ''
			UNION
			SELECT DISTINCT btrim(tipo_propuesta)
			  FROM plantilla_tipos_propuesta WHERE btrim(tipo_propuesta) <> ''
		  ) tipos
		 ORDER BY lower(tipo_propuesta), tipo_propuesta`)
	if err != nil {
		log.Printf("plantillas: error consultando tipos de propuesta: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las opciones de plantillas."})
		return
	}
	tipos := make([]string, 0)
	for rows.Next() {
		var tipo string
		if err := rows.Scan(&tipo); err != nil {
			rows.Close()
			log.Printf("plantillas: error leyendo tipo de propuesta: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las opciones de plantillas."})
			return
		}
		tipos = append(tipos, tipo)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.Printf("plantillas: error recorriendo tipos de propuesta: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las opciones de plantillas."})
		return
	}
	rows.Close()
	if len(tipos) == 0 {
		tipos = []string{"Comercial", "Técnica", "Renovación"}
	}

	organizaciones := make([]organizacionOpcionPlantilla, 0)
	orgRows, err := h.DB.Query(ctx, `
		SELECT organizacion_id, nombre FROM organizaciones
		 WHERE estado='Activo' ORDER BY nombre, organizacion_id`)
	if err != nil {
		log.Printf("plantillas: error consultando organizaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las opciones de plantillas."})
		return
	}
	defer orgRows.Close()
	for orgRows.Next() {
		var item organizacionOpcionPlantilla
		if err := orgRows.Scan(&item.OrganizacionID, &item.Nombre); err != nil {
			log.Printf("plantillas: error leyendo organización: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las opciones de plantillas."})
			return
		}
		organizaciones = append(organizaciones, item)
	}
	if err := orgRows.Err(); err != nil {
		log.Printf("plantillas: error recorriendo organizaciones: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las opciones de plantillas."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "tipos_propuesta": tipos, "organizaciones": organizaciones,
	})
}

// Listar devuelve resultados filtrados y KPIs globales de la galería. Los
// arreglos asociados se agregan dentro de PostgreSQL para evitar una fila por
// combinación cotizador/tipo de propuesta.
func (h *PlantillasHandler) Listar(w http.ResponseWriter, r *http.Request) {
	busqueda := strings.TrimSpace(r.URL.Query().Get("busqueda"))
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	tipoPropuesta := strings.TrimSpace(r.URL.Query().Get("tipo_propuesta"))
	estado := strings.TrimSpace(r.URL.Query().Get("estado"))
	if estado != "" && !estadosPlantillaValidos[estado] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "estado debe ser Borrador, Publicada o Archivada."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	var contadores contadoresPlantillas
	err := h.DB.QueryRow(ctx, `
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE p.estado='Publicada')::int,
		       COUNT(*) FILTER (WHERE p.estado='Borrador')::int,
		       COUNT(DISTINCT pe.tema) FILTER (WHERE p.estado <> 'Archivada')::int
		  FROM plantillas p
		  LEFT JOIN plantilla_estilos pe ON pe.plantilla_id=p.plantilla_id`).Scan(
		&contadores.Total, &contadores.Publicadas, &contadores.Borradores, &contadores.Estilos,
	)
	if err != nil {
		log.Printf("plantillas: error calculando contadores: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las plantillas."})
		return
	}

	rows, err := h.DB.Query(ctx, `
		SELECT p.plantilla_id::text, p.codigo, p.nombre, p.descripcion,
		       p.estado, p.version, p.organizacion_id,
		       p.disponible_nuevas_propuestas, p.permite_duplicar,
		       COALESCE((SELECT array_agg(pc.calculadora_id ORDER BY pc.calculadora_id)
		                   FROM plantilla_calculadoras pc WHERE pc.plantilla_id=p.plantilla_id), ARRAY[]::text[]),
		       COALESCE((SELECT array_agg(c.nombre_calculadora ORDER BY c.nombre_calculadora)
		                   FROM plantilla_calculadoras pc JOIN calculadoras c USING (calculadora_id)
		                  WHERE pc.plantilla_id=p.plantilla_id), ARRAY[]::text[]),
		       COALESCE((SELECT array_agg(pt.tipo_propuesta ORDER BY pt.tipo_propuesta)
		                   FROM plantilla_tipos_propuesta pt WHERE pt.plantilla_id=p.plantilla_id), ARRAY[]::text[])
		  FROM plantillas p
		 WHERE ($1='' OR p.nombre ILIKE '%' || $1 || '%' OR p.codigo ILIKE '%' || $1 || '%')
		   AND ($2='' OR EXISTS (SELECT 1 FROM plantilla_calculadoras pc WHERE pc.plantilla_id=p.plantilla_id AND pc.calculadora_id=$2))
		   AND ($3='' OR EXISTS (SELECT 1 FROM plantilla_tipos_propuesta pt WHERE pt.plantilla_id=p.plantilla_id AND pt.tipo_propuesta=$3))
		   AND ($4='' OR p.estado=$4)
		 ORDER BY p.fecha_actualizacion DESC, p.nombre`, busqueda, calculadoraID, tipoPropuesta, estado)
	if err != nil {
		log.Printf("plantillas: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las plantillas."})
		return
	}
	defer rows.Close()

	items := make([]plantillaListado, 0)
	for rows.Next() {
		var item plantillaListado
		if err := rows.Scan(&item.PlantillaID, &item.Codigo, &item.Nombre, &item.Descripcion,
			&item.Estado, &item.Version, &item.OrganizacionID, &item.DisponibleNuevasPropuestas,
			&item.PermiteDuplicar, &item.CalculadoraIDs, &item.Cotizadores, &item.TiposPropuesta); err != nil {
			log.Printf("plantillas: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las plantillas."})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("plantillas: error recorriendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar las plantillas."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "plantillas": items, "contadores": contadores,
		"total": contadores.Total, "publicadas": contadores.Publicadas,
		"borradores": contadores.Borradores, "estilos": contadores.Estilos,
	})
}

// Detalle devuelve secciones, bloques, vinculaciones y estilo en un solo árbol.
func (h *PlantillasHandler) Detalle(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la plantilla."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	detalle, err := h.consultarDetalle(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		log.Printf("plantillas: error consultando detalle %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar la plantilla."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "plantilla": detalle})
}

func (h *PlantillasHandler) consultarDetalle(ctx context.Context, id string) (*plantillaDetalle, error) {
	var item plantillaDetalle
	err := h.DB.QueryRow(ctx, `
		SELECT p.plantilla_id::text, p.codigo, p.nombre, p.descripcion, p.estado,
		       p.version, p.organizacion_id, p.disponible_nuevas_propuestas, p.permite_duplicar,
		       COALESCE((SELECT array_agg(pc.calculadora_id ORDER BY pc.calculadora_id)
		                   FROM plantilla_calculadoras pc WHERE pc.plantilla_id=p.plantilla_id), ARRAY[]::text[]),
		       COALESCE((SELECT array_agg(pt.tipo_propuesta ORDER BY pt.tipo_propuesta)
		                   FROM plantilla_tipos_propuesta pt WHERE pt.plantilla_id=p.plantilla_id), ARRAY[]::text[])
		  FROM plantillas p WHERE p.plantilla_id::text=$1`, id).Scan(
		&item.PlantillaID, &item.Codigo, &item.Nombre, &item.Descripcion, &item.Estado,
		&item.Version, &item.OrganizacionID, &item.DisponibleNuevasPropuestas, &item.PermiteDuplicar,
		&item.CalculadoraIDs, &item.TiposPropuesta,
	)
	if err != nil {
		return nil, err
	}

	item.Secciones = make([]plantillaSeccion, 0)
	rows, err := h.DB.Query(ctx, `
		SELECT seccion_id::text, plantilla_id::text, nombre, titulo, mostrar_titulo,
		       visibilidad, diseno_bloques, mostrar_web, mostrar_pdf, orden
		  FROM plantilla_secciones WHERE plantilla_id::text=$1
		 ORDER BY orden, seccion_id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var seccion plantillaSeccion
		if err := rows.Scan(&seccion.SeccionID, &seccion.PlantillaID, &seccion.Nombre,
			&seccion.Titulo, &seccion.MostrarTitulo, &seccion.Visibilidad, &seccion.DisenoBloques,
			&seccion.MostrarWeb, &seccion.MostrarPDF, &seccion.Orden); err != nil {
			rows.Close()
			return nil, err
		}
		seccion.Bloques = make([]plantillaBloque, 0)
		item.Secciones = append(item.Secciones, seccion)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range item.Secciones {
		bloques, err := h.consultarBloques(ctx, item.Secciones[i].SeccionID)
		if err != nil {
			return nil, err
		}
		item.Secciones[i].Bloques = bloques
	}

	var estilo plantillaEstilo
	err = h.DB.QueryRow(ctx, `
		SELECT estilo_id::text, plantilla_id::text, tema, formato_pagina, margenes, diseno_portada, estilo_tablas
		  FROM plantilla_estilos WHERE plantilla_id::text=$1`, id).Scan(
		&estilo.EstiloID, &estilo.PlantillaID, &estilo.Tema, &estilo.FormatoPagina,
		&estilo.Margenes, &estilo.DisenoPortada, &estilo.EstiloTablas,
	)
	if err == nil {
		item.Estilo = &estilo
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	return &item, nil
}

func (h *PlantillasHandler) consultarBloques(ctx context.Context, seccionID string) ([]plantillaBloque, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT bloque_id::text, seccion_id::text, tipo_bloque, nombre_interno,
		       titulo, contenido, columna, mostrar_web, mostrar_pdf, orden
		  FROM plantilla_bloques WHERE seccion_id::text=$1
		 ORDER BY orden, bloque_id`, seccionID)
	if err != nil {
		return nil, err
	}
	bloques := make([]plantillaBloque, 0)
	for rows.Next() {
		var bloque plantillaBloque
		if err := rows.Scan(&bloque.BloqueID, &bloque.SeccionID, &bloque.TipoBloque,
			&bloque.NombreInterno, &bloque.Titulo, &bloque.Contenido, &bloque.Columna,
			&bloque.MostrarWeb, &bloque.MostrarPDF, &bloque.Orden); err != nil {
			rows.Close()
			return nil, err
		}
		bloque.Vinculaciones = make([]plantillaVinculacion, 0)
		bloques = append(bloques, bloque)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	for i := range bloques {
		vinculaciones, err := h.DB.Query(ctx, `
			SELECT vinculacion_id::text, bloque_id::text, calculadora_id, fuente_tipo, fuente_id
			  FROM plantilla_vinculaciones WHERE bloque_id::text=$1 ORDER BY calculadora_id`, bloques[i].BloqueID)
		if err != nil {
			return nil, err
		}
		for vinculaciones.Next() {
			var v plantillaVinculacion
			if err := vinculaciones.Scan(&v.VinculacionID, &v.BloqueID, &v.CalculadoraID, &v.FuenteTipo, &v.FuenteID); err != nil {
				vinculaciones.Close()
				return nil, err
			}
			bloques[i].Vinculaciones = append(bloques[i].Vinculaciones, v)
		}
		if err := vinculaciones.Err(); err != nil {
			vinculaciones.Close()
			return nil, err
		}
		vinculaciones.Close()
	}
	return bloques, nil
}

func (h *PlantillasHandler) Crear(w http.ResponseWriter, r *http.Request) {
	var req crearPlantillaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.Descripcion = strings.TrimSpace(req.Descripcion)
	req.CrearDesde = strings.TrimSpace(req.CrearDesde)
	req.OrganizacionID = strings.TrimSpace(req.OrganizacionID)
	req.CalculadoraIDs = normalizarIDs(req.CalculadoraIDs)
	req.TiposPropuesta = normalizarIDs(req.TiposPropuesta)
	if req.Nombre == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre de la plantilla es obligatorio."})
		return
	}
	if len([]rune(req.Nombre)) > 120 || len([]rune(req.Descripcion)) > 500 {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre admite 120 caracteres y la descripción 500."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		responderErrorPlantilla(w, "iniciar la creación", err)
		return
	}
	defer tx.Rollback(ctx)

	if err := validarReferenciasPlantilla(ctx, tx, req.OrganizacionID, req.CalculadoraIDs); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	disponible := boolPredeterminado(req.DisponibleNuevasPropuestas, true)
	permiteDuplicar := boolPredeterminado(req.PermiteDuplicar, true)
	codigo := generarCodigoPlantilla()
	var plantillaID string
	err = tx.QueryRow(ctx, `
		INSERT INTO plantillas
			(codigo, nombre, descripcion, estado, version, organizacion_id,
			 disponible_nuevas_propuestas, permite_duplicar)
		VALUES ($1,$2,NULLIF($3,''),'Borrador',1,NULLIF($4,''),$5,$6)
		RETURNING plantilla_id::text`, codigo, req.Nombre, req.Descripcion,
		req.OrganizacionID, disponible, permiteDuplicar).Scan(&plantillaID)
	if err != nil {
		responderErrorPlantilla(w, "crear la plantilla", err)
		return
	}
	if err := reemplazarAsociacionesPlantilla(ctx, tx, plantillaID, req.CalculadoraIDs, req.TiposPropuesta); err != nil {
		responderErrorPlantilla(w, "guardar las asociaciones", err)
		return
	}

	if req.CrearDesde != "" && req.CrearDesde != "PLANTILLA_VACIA" {
		if err := copiarContenidoPlantilla(ctx, tx, req.CrearDesde, plantillaID); errors.Is(err, pgx.ErrNoRows) {
			escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "La plantilla indicada como base no existe."})
			return
		} else if err != nil {
			responderErrorPlantilla(w, "copiar la plantilla base", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		responderErrorPlantilla(w, "confirmar la creación", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "plantilla_id": plantillaID, "codigo": codigo,
		"estado": "Borrador", "mensaje": "Plantilla creada.",
	})
}

func (h *PlantillasHandler) Editar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la plantilla."})
		return
	}
	var req editarPlantillaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Nombre != nil {
		v := strings.TrimSpace(*req.Nombre)
		req.Nombre = &v
		if v == "" || len([]rune(v)) > 120 {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El nombre es obligatorio y admite hasta 120 caracteres."})
			return
		}
	}
	if req.Descripcion != nil {
		v := strings.TrimSpace(*req.Descripcion)
		req.Descripcion = &v
		if len([]rune(v)) > 500 {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La descripción admite hasta 500 caracteres."})
			return
		}
	}
	if req.OrganizacionID != nil {
		v := strings.TrimSpace(*req.OrganizacionID)
		req.OrganizacionID = &v
	}
	if req.CalculadoraIDs != nil {
		v := normalizarIDs(*req.CalculadoraIDs)
		req.CalculadoraIDs = &v
	}
	if req.TiposPropuesta != nil {
		v := normalizarIDs(*req.TiposPropuesta)
		req.TiposPropuesta = &v
	}
	if req.Nombre == nil && req.Descripcion == nil && req.OrganizacionID == nil &&
		req.DisponibleNuevasPropuestas == nil && req.PermiteDuplicar == nil &&
		req.CalculadoraIDs == nil && req.TiposPropuesta == nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos un campo para editar."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		responderErrorPlantilla(w, "iniciar la edición", err)
		return
	}
	defer tx.Rollback(ctx)
	var existe bool
	if err := tx.QueryRow(ctx, `SELECT true FROM plantillas WHERE plantilla_id::text=$1 FOR UPDATE`, id).Scan(&existe); errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	} else if err != nil {
		responderErrorPlantilla(w, "validar la plantilla", err)
		return
	}
	organizacionID := ""
	if req.OrganizacionID != nil {
		organizacionID = *req.OrganizacionID
	}
	calculadoras := []string(nil)
	if req.CalculadoraIDs != nil {
		calculadoras = *req.CalculadoraIDs
	}
	if err := validarReferenciasPlantilla(ctx, tx, organizacionID, calculadoras); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	columnas := make([]string, 0, 5)
	valores := make([]any, 0, 6)
	agregar := func(columna string, valor any) {
		valores = append(valores, valor)
		columnas = append(columnas, columna+"=$"+strconv.Itoa(len(valores)))
	}
	if req.Nombre != nil {
		agregar("nombre", *req.Nombre)
	}
	if req.Descripcion != nil {
		agregar("descripcion", *req.Descripcion)
	}
	if req.OrganizacionID != nil {
		valores = append(valores, *req.OrganizacionID)
		columnas = append(columnas, "organizacion_id=NULLIF($"+strconv.Itoa(len(valores))+",'')")
	}
	if req.DisponibleNuevasPropuestas != nil {
		agregar("disponible_nuevas_propuestas", *req.DisponibleNuevasPropuestas)
	}
	if req.PermiteDuplicar != nil {
		agregar("permite_duplicar", *req.PermiteDuplicar)
	}
	if len(columnas) > 0 {
		valores = append(valores, id)
		consulta := "UPDATE plantillas SET " + strings.Join(columnas, ",") +
			" WHERE plantilla_id::text=$" + strconv.Itoa(len(valores))
		if _, err := tx.Exec(ctx, consulta, valores...); err != nil {
			responderErrorPlantilla(w, "editar la plantilla", err)
			return
		}
	}
	if req.CalculadoraIDs != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM plantilla_calculadoras WHERE plantilla_id::text=$1`, id); err != nil {
			responderErrorPlantilla(w, "actualizar los cotizadores", err)
			return
		}
		if err := insertarCalculadorasPlantilla(ctx, tx, id, *req.CalculadoraIDs); err != nil {
			responderErrorPlantilla(w, "actualizar los cotizadores", err)
			return
		}
	}
	if req.TiposPropuesta != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM plantilla_tipos_propuesta WHERE plantilla_id::text=$1`, id); err != nil {
			responderErrorPlantilla(w, "actualizar los tipos de propuesta", err)
			return
		}
		if err := insertarTiposPlantilla(ctx, tx, id, *req.TiposPropuesta); err != nil {
			responderErrorPlantilla(w, "actualizar los tipos de propuesta", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		responderErrorPlantilla(w, "confirmar la edición", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "plantilla_id": id, "mensaje": "Plantilla actualizada."})
}

func (h *PlantillasHandler) Publicar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe, tieneContenido bool
	err := h.DB.QueryRow(ctx, `
		SELECT true, EXISTS(
			SELECT 1 FROM plantilla_secciones ps
			JOIN plantilla_bloques pb ON pb.seccion_id=ps.seccion_id
			WHERE ps.plantilla_id=p.plantilla_id)
		FROM plantillas p WHERE p.plantilla_id::text=$1`, id).Scan(&existe, &tieneContenido)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		responderErrorPlantilla(w, "validar la publicación", err)
		return
	}
	if !tieneContenido {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La plantilla debe tener al menos una sección con al menos un bloque antes de publicarse."})
		return
	}
	if _, err := h.DB.Exec(ctx, `UPDATE plantillas SET estado='Publicada' WHERE plantilla_id::text=$1`, id); err != nil {
		responderErrorPlantilla(w, "publicar la plantilla", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "plantilla_id": id, "estado": "Publicada", "mensaje": "Plantilla publicada."})
}

func (h *PlantillasHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var estado string
	err := h.DB.QueryRow(ctx, `SELECT estado FROM plantillas WHERE plantilla_id::text=$1`, id).Scan(&estado)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		responderErrorPlantilla(w, "consultar la plantilla", err)
		return
	}
	if estado != "Borrador" {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Solo se puede eliminar una plantilla en estado Borrador; una plantilla publicada debe despublicarse primero."})
		return
	}
	if _, err := h.DB.Exec(ctx, `DELETE FROM plantillas WHERE plantilla_id::text=$1`, id); err != nil {
		responderErrorPlantilla(w, "eliminar la plantilla", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "plantilla_id": id, "mensaje": "Plantilla eliminada."})
}

type consultadorFila interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func validarReferenciasPlantilla(ctx context.Context, db consultadorFila, organizacionID string, calculadoraIDs []string) error {
	if organizacionID != "" {
		var existe bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organizaciones WHERE organizacion_id=$1)`, organizacionID).Scan(&existe); err != nil {
			return fmt.Errorf("no fue posible validar la organización: %w", err)
		}
		if !existe {
			return errors.New("la organización indicada no existe")
		}
	}
	if len(calculadoraIDs) > 0 {
		var cantidad int
		if err := db.QueryRow(ctx, `SELECT COUNT(*)::int FROM calculadoras WHERE calculadora_id=ANY($1)`, calculadoraIDs).Scan(&cantidad); err != nil {
			return fmt.Errorf("no fue posible validar los cotizadores: %w", err)
		}
		if cantidad != len(calculadoraIDs) {
			return errors.New("uno o más cotizadores indicados no existen")
		}
	}
	return nil
}

func reemplazarAsociacionesPlantilla(ctx context.Context, tx pgx.Tx, plantillaID string, calculadoras, tipos []string) error {
	if err := insertarCalculadorasPlantilla(ctx, tx, plantillaID, calculadoras); err != nil {
		return err
	}
	return insertarTiposPlantilla(ctx, tx, plantillaID, tipos)
}

func insertarCalculadorasPlantilla(ctx context.Context, tx pgx.Tx, plantillaID string, ids []string) error {
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO plantilla_calculadoras (plantilla_id, calculadora_id) VALUES ($1,$2)`, plantillaID, id); err != nil {
			return err
		}
	}
	return nil
}

func insertarTiposPlantilla(ctx context.Context, tx pgx.Tx, plantillaID string, tipos []string) error {
	for _, tipo := range tipos {
		if _, err := tx.Exec(ctx, `INSERT INTO plantilla_tipos_propuesta (plantilla_id, tipo_propuesta) VALUES ($1,$2)`, plantillaID, tipo); err != nil {
			return err
		}
	}
	return nil
}

func copiarContenidoPlantilla(ctx context.Context, tx pgx.Tx, origenID, destinoID string) error {
	var existe bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, origenID).Scan(&existe); err != nil {
		return err
	}
	if !existe {
		return pgx.ErrNoRows
	}

	type seccionOrigen struct {
		id, nombre, visibilidad, diseno       string
		titulo                                *string
		mostrarTitulo, mostrarWeb, mostrarPDF bool
		orden                                 int
	}
	rows, err := tx.Query(ctx, `
		SELECT seccion_id::text, nombre, titulo, mostrar_titulo, visibilidad,
		       diseno_bloques, mostrar_web, mostrar_pdf, orden
		  FROM plantilla_secciones WHERE plantilla_id::text=$1 ORDER BY orden, seccion_id`, origenID)
	if err != nil {
		return err
	}
	secciones := make([]seccionOrigen, 0)
	for rows.Next() {
		var s seccionOrigen
		if err := rows.Scan(&s.id, &s.nombre, &s.titulo, &s.mostrarTitulo, &s.visibilidad,
			&s.diseno, &s.mostrarWeb, &s.mostrarPDF, &s.orden); err != nil {
			rows.Close()
			return err
		}
		secciones = append(secciones, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, s := range secciones {
		var nuevaSeccionID string
		err := tx.QueryRow(ctx, `
			INSERT INTO plantilla_secciones
				(plantilla_id,nombre,titulo,mostrar_titulo,visibilidad,diseno_bloques,mostrar_web,mostrar_pdf,orden)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING seccion_id::text`,
			destinoID, s.nombre, s.titulo, s.mostrarTitulo, s.visibilidad,
			s.diseno, s.mostrarWeb, s.mostrarPDF, s.orden).Scan(&nuevaSeccionID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO plantilla_bloques
				(seccion_id,tipo_bloque,nombre_interno,titulo,contenido,columna,mostrar_web,mostrar_pdf,orden)
			SELECT $1, tipo_bloque, nombre_interno, titulo, contenido, columna, mostrar_web, mostrar_pdf, orden
			  FROM plantilla_bloques WHERE seccion_id::text=$2`, nuevaSeccionID, s.id)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO plantilla_estilos (plantilla_id,tema,formato_pagina,margenes,diseno_portada,estilo_tablas)
		SELECT $1, tema, formato_pagina, margenes, diseno_portada, estilo_tablas
		  FROM plantilla_estilos WHERE plantilla_id::text=$2`, destinoID, origenID)
	return err
}

func boolPredeterminado(valor *bool, predeterminado bool) bool {
	if valor == nil {
		return predeterminado
	}
	return *valor
}

func generarCodigoPlantilla() string {
	aleatorio := make([]byte, 5)
	if _, err := rand.Read(aleatorio); err != nil {
		return "PLT-" + time.Now().UTC().Format("20060102150405.000000000")
	}
	return "PLT-" + strings.ToUpper(hex.EncodeToString(aleatorio))
}

func responderErrorPlantilla(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantillas: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
