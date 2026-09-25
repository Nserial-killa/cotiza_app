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

// PlantillaVinculacionesHandler administra las fuentes y vinculaciones del
// paso 3 para cada cotizador asociado a una plantilla.
type PlantillaVinculacionesHandler struct {
	DB *pgxpool.Pool
}

type fuentePlantilla struct {
	FuenteID     string  `json:"fuente_id"`
	FuenteTipo   string  `json:"fuente_tipo"`
	Nombre       string  `json:"nombre"`
	TipoElemento *string `json:"tipo_elemento,omitempty"`
	TabID        *string `json:"tab_id,omitempty"`
	TabNombre    *string `json:"tab_nombre,omitempty"`
	// TipoDato solo en una SALIDA_ESTANDAR (MONEDA, TEXTO...), el mismo
	// vocabulario de tiposSalidas.
	TipoDato *string `json:"tipo_dato,omitempty"`
}

type guardarVinculacionRequest struct {
	CalculadoraID string `json:"calculadora_id"`
	FuenteTipo    string `json:"fuente_tipo"`
	FuenteID      string `json:"fuente_id"`
}

var fuentesCotizacionBase = []fuentePlantilla{
	{FuenteID: "cliente", FuenteTipo: "COTIZACION_BASE", Nombre: "Cliente"},
	{FuenteID: "empresa", FuenteTipo: "COTIZACION_BASE", Nombre: "Empresa"},
	{FuenteID: "contacto", FuenteTipo: "COTIZACION_BASE", Nombre: "Contacto"},
	// Del mismo contacto que "contacto" (principal, o el activo más antiguo):
	// los usan Datos del cliente y Firma y aceptación (Ronda P1).
	{FuenteID: "contacto_correo", FuenteTipo: "COTIZACION_BASE", Nombre: "Correo del contacto"},
	{FuenteID: "contacto_telefono", FuenteTipo: "COTIZACION_BASE", Nombre: "Teléfono del contacto"},
	{FuenteID: "contacto_cargo", FuenteTipo: "COTIZACION_BASE", Nombre: "Cargo del contacto"},
	{FuenteID: "codigo_oferta", FuenteTipo: "COTIZACION_BASE", Nombre: "Código de oferta"},
	{FuenteID: "tipo_propuesta", FuenteTipo: "COTIZACION_BASE", Nombre: "Tipo de propuesta"},
	{FuenteID: "total_precio", FuenteTipo: "COTIZACION_BASE", Nombre: "Precio total"},
	{FuenteID: "moneda", FuenteTipo: "COTIZACION_BASE", Nombre: "Moneda"},
	{FuenteID: "fecha_creacion", FuenteTipo: "COTIZACION_BASE", Nombre: "Fecha de creación"},
	{FuenteID: "vendedor", FuenteTipo: "COTIZACION_BASE", Nombre: "Vendedor"},
	{FuenteID: "estado", FuenteTipo: "COTIZACION_BASE", Nombre: "Estado"},
}

// salidasPublicasPlantilla son las Salidas estándar (mapa_salidas_cotizador,
// 0027) que una propuesta puede mostrar, en el orden de clavesSalidasOrden y
// con las etiquetas del Diseñador (cotiza_script_salidas_cotizador.html).
// Faltan TOTAL_COSTO, TOTAL_GANANCIA y MARGEN_TOTAL A PROPÓSITO: son costo y
// margen internos, y la propuesta la lee el cliente — mismo criterio que
// costo_interno/margen_porcentaje en toda la aplicación. No se ofrecen, no
// se pueden guardar y no se resuelven al renderizar.
var salidasPublicasPlantilla = []fuentePlantilla{
	{FuenteID: "TOTAL_PRECIO", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Total del precio"},
	{FuenteID: "SUBTOTAL", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Subtotal"},
	{FuenteID: "DESCUENTO", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Descuento"},
	{FuenteID: "IMPUESTOS", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Impuestos"},
	{FuenteID: "MONEDA", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Moneda"},
	{FuenteID: "TIPO_CLIENTE", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Tipo de cliente"},
	{FuenteID: "TIPO_PROPUESTA", FuenteTipo: "SALIDA_ESTANDAR", Nombre: "Tipo de propuesta"},
}

var clavesSalidasPublicas = func() map[string]bool {
	resultado := make(map[string]bool, len(salidasPublicasPlantilla))
	for _, salida := range salidasPublicasPlantilla {
		resultado[salida.FuenteID] = true
	}
	return resultado
}()

// salidasMapeadasPlantilla devuelve las salidas públicas que el cotizador
// tiene mapeadas y activas: una salida sin mapear nunca llega a
// cotizacion_salidas, así que vincularla dejaría el bloque vacío siempre.
func salidasMapeadasPlantilla(ctx context.Context, db *pgxpool.Pool, calculadoraID string) ([]fuentePlantilla, error) {
	mapa, err := leerMapaSalidas(ctx, db, calculadoraID)
	if err != nil {
		return nil, err
	}
	activas := make(map[string]bool, len(mapa))
	for _, s := range mapa {
		if s.Activo {
			activas[s.ClaveSalida] = true
		}
	}
	resultado := make([]fuentePlantilla, 0, len(salidasPublicasPlantilla))
	for _, salida := range salidasPublicasPlantilla {
		if !activas[salida.FuenteID] {
			continue
		}
		tipo := tiposSalidas[salida.FuenteID]
		salida.TipoDato = &tipo
		resultado = append(resultado, salida)
	}
	return resultado, nil
}

var idsCotizacionBase = func() map[string]bool {
	resultado := make(map[string]bool, len(fuentesCotizacionBase))
	for _, fuente := range fuentesCotizacionBase {
		resultado[fuente.FuenteID] = true
	}
	return resultado
}()

// Fuentes devuelve campos activos del Diseñador (incluidos Campo Calculado,
// Lista de Precios y Tabla — plantilla_renderizador.go ya sabe resolver su
// valor_resuelto) y los datos base comunes. Sin calculadora_id devuelve solo
// los datos base: son los únicos que no dependen de un cotizador, y es lo
// que el editor de campos del paso 2 necesita para un campo común.
func (h *PlantillaVinculacionesHandler) Fuentes(w http.ResponseWriter, r *http.Request) {
	plantillaID := strings.TrimSpace(chi.URLParam(r, "id"))
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if plantillaID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar la plantilla."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if calculadoraID == "" {
		var existe bool
		if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, plantillaID).Scan(&existe); err != nil {
			responderErrorVinculacion(w, "validar la plantilla", err)
			return
		}
		if !existe {
			escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
			return
		}
		base := append([]fuentePlantilla(nil), fuentesCotizacionBase...)
		escribirJSON(w, http.StatusOK, map[string]any{
			"ok": true, "calculadora_id": "", "fuentes": base,
			"campos": []fuentePlantilla{}, "salidas": []fuentePlantilla{}, "datos_cotizacion": base,
		})
		return
	}
	var existe, asociada bool
	err := h.DB.QueryRow(ctx, `
		SELECT true, EXISTS(
			SELECT 1 FROM plantilla_calculadoras pc
			WHERE pc.plantilla_id=p.plantilla_id AND pc.calculadora_id=$2)
		FROM plantillas p WHERE p.plantilla_id::text=$1`, plantillaID, calculadoraID).Scan(&existe, &asociada)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		responderErrorVinculacion(w, "validar la plantilla", err)
		return
	}
	if !asociada {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador no está asociado a esta plantilla."})
		return
	}

	rows, err := h.DB.Query(ctx, `
		SELECT e.elemento_id, 'CAMPO', COALESCE(NULLIF(e.etiqueta,''),e.elemento_id),
		       e.tipo, t.tab_id, t.nombre
		  FROM tabs_cotizador t
		  JOIN elementos_tab_cotizador e ON e.tab_id=t.tab_id
		 WHERE t.calculadora_id=$1 AND t.activo=true AND e.activo=true
		   AND e.tipo IN ('CAMPO','CAMPO_CATALOGO','CAMPO_CALCULADO','LISTA_PRECIOS','TABLA')
		 ORDER BY t.orden, e.orden, e.elemento_id`, calculadoraID)
	if err != nil {
		responderErrorVinculacion(w, "consultar las fuentes", err)
		return
	}
	defer rows.Close()
	campos := make([]fuentePlantilla, 0)
	for rows.Next() {
		var fuente fuentePlantilla
		if err := rows.Scan(&fuente.FuenteID, &fuente.FuenteTipo, &fuente.Nombre,
			&fuente.TipoElemento, &fuente.TabID, &fuente.TabNombre); err != nil {
			responderErrorVinculacion(w, "leer las fuentes", err)
			return
		}
		campos = append(campos, fuente)
	}
	if err := rows.Err(); err != nil {
		responderErrorVinculacion(w, "leer las fuentes", err)
		return
	}
	salidas, err := salidasMapeadasPlantilla(ctx, h.DB, calculadoraID)
	if err != nil {
		responderErrorVinculacion(w, "consultar las salidas", err)
		return
	}
	base := append([]fuentePlantilla(nil), fuentesCotizacionBase...)
	todas := make([]fuentePlantilla, 0, len(campos)+len(salidas)+len(base))
	todas = append(todas, campos...)
	todas = append(todas, salidas...)
	todas = append(todas, base...)
	// campos trae tab_id/tab_nombre (la sección del Diseñador) en el orden de
	// las secciones, para que el paso 3 las agrupe y las busque por sección.
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "calculadora_id": calculadoraID, "fuentes": todas,
		"campos": campos, "salidas": salidas, "datos_cotizacion": base,
	})
}

func (h *PlantillaVinculacionesHandler) Guardar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "bloque", bloqueID) {
		return
	}
	var req guardarVinculacionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.FuenteTipo = strings.ToUpper(strings.TrimSpace(req.FuenteTipo))
	req.FuenteID = strings.TrimSpace(req.FuenteID)
	if req.FuenteTipo == "SALIDA_ESTANDAR" {
		req.FuenteID = strings.ToUpper(req.FuenteID)
	}
	if bloqueID == "" || req.CalculadoraID == "" || req.FuenteID == "" ||
		(req.FuenteTipo != "CAMPO" && req.FuenteTipo != "COTIZACION_BASE" && req.FuenteTipo != "SALIDA_ESTANDAR") {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque, calculadora_id, fuente_id y una fuente_tipo válida."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	plantillaID, tipoBloque, err := plantillaYTipoDeBloque(ctx, h.DB, bloqueID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	if err != nil {
		responderErrorVinculacion(w, "validar el bloque", err)
		return
	}
	var asociada bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plantilla_calculadoras
		 WHERE plantilla_id::text=$1 AND calculadora_id=$2)`, plantillaID, req.CalculadoraID).Scan(&asociada); err != nil {
		responderErrorVinculacion(w, "validar el cotizador", err)
		return
	}
	if !asociada {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador no está asociado a la plantilla del bloque."})
		return
	}
	valida, err := h.fuenteValida(ctx, req)
	if err != nil {
		responderErrorVinculacion(w, "validar la fuente", err)
		return
	}
	if !valida {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La fuente indicada no existe o no está activa para ese cotizador."})
		return
	}
	// Un bloque Lista de precios dibuja los ítems de UN elemento
	// LISTA_PRECIOS del Diseñador: cualquier otra fuente no tiene ítems.
	if tipoBloque == "LISTA_PRECIOS" {
		esLista, err := fuenteEsListaPrecios(ctx, h.DB, req)
		if err != nil {
			responderErrorVinculacion(w, "validar la fuente", err)
			return
		}
		if !esLista {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Un bloque Lista de precios solo se vincula a un elemento Lista de Precios del cotizador."})
			return
		}
	}
	var vinculacionID string
	err = h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_vinculaciones (bloque_id,calculadora_id,fuente_tipo,fuente_id)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (bloque_id,calculadora_id) DO UPDATE SET
			fuente_tipo=EXCLUDED.fuente_tipo, fuente_id=EXCLUDED.fuente_id
		RETURNING vinculacion_id::text`, bloqueID, req.CalculadoraID,
		req.FuenteTipo, req.FuenteID).Scan(&vinculacionID)
	if err != nil {
		responderErrorVinculacion(w, "guardar la vinculación", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "vinculacion_id": vinculacionID, "bloque_id": bloqueID,
		"calculadora_id": req.CalculadoraID, "mensaje": "Vinculación guardada.",
	})
}

func (h *PlantillaVinculacionesHandler) fuenteValida(ctx context.Context, req guardarVinculacionRequest) (bool, error) {
	if req.FuenteTipo == "SALIDA_ESTANDAR" {
		if !clavesSalidasPublicas[req.FuenteID] {
			return false, nil
		}
		salidas, err := salidasMapeadasPlantilla(ctx, h.DB, req.CalculadoraID)
		if err != nil {
			return false, err
		}
		for _, salida := range salidas {
			if salida.FuenteID == req.FuenteID {
				return true, nil
			}
		}
		return false, nil
	}
	return fuenteCondicionValida(ctx, h.DB, req.FuenteTipo, req.FuenteID, req.CalculadoraID)
}

func fuenteEsListaPrecios(ctx context.Context, db *pgxpool.Pool, req guardarVinculacionRequest) (bool, error) {
	if req.FuenteTipo != "CAMPO" {
		return false, nil
	}
	var esLista bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM elementos_tab_cotizador e
			JOIN tabs_cotizador t ON t.tab_id=e.tab_id
			WHERE e.elemento_id=$1 AND t.calculadora_id=$2 AND e.tipo='LISTA_PRECIOS')`,
		req.FuenteID, req.CalculadoraID).Scan(&esLista)
	return esLista, err
}

func (h *PlantillaVinculacionesHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "bloque", bloqueID) {
		return
	}
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if bloqueID == "" || calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque y calculadora_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `
		DELETE FROM plantilla_vinculaciones
		 WHERE bloque_id::text=$1 AND calculadora_id=$2`, bloqueID, calculadoraID)
	if err != nil {
		responderErrorVinculacion(w, "eliminar la vinculación", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Vinculación no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "bloque_id": bloqueID, "calculadora_id": calculadoraID, "mensaje": "Vinculación eliminada."})
}

func responderErrorVinculacion(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantilla vinculaciones: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
