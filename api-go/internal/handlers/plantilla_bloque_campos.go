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

// PlantillaBloqueCamposHandler administra los campos internos (pares
// Etiqueta/Valor) de los bloques que necesitan VARIOS datos, no una sola
// fuente como plantilla_vinculaciones (migración 0029, Ronda P1). Mismo
// criterio de validación que plantilla_vinculaciones.go: una fuente CAMPO
// tiene que existir y estar activa en ESA calculadora_id (asociada a la
// plantilla del bloque); COTIZACION_BASE tiene que ser una de
// fuentesCotizacionBase; VALOR_FIJO no lleva fuente_id. A diferencia de las
// vinculaciones, calculadora_id solo se guarda para CAMPO — ver el
// encabezado de la migración.
type PlantillaBloqueCamposHandler struct {
	DB *pgxpool.Pool
}

// tiposBloqueConCampos son los únicos tipos cuyo contenido es una lista de
// pares Etiqueta/Valor. Otro tipo de bloque no acepta campos: sus datos van
// en contenido (TEXTO, ENCABEZADO...), en una vinculación (LISTA_PRECIOS)
// o en columnas (TABLA_INVERSION, TABLA_DATOS, OPCIONES_PROPUESTA).
var tiposBloqueConCampos = map[string]bool{
	"PORTADA": true, "DATOS_CLIENTE": true, "GRUPO_INFORMACION": true,
	"CONDICIONES_COMERCIALES": true, "FIRMA_ACEPTACION": true,
}

var fuentesTipoCampoBloque = map[string]bool{"CAMPO": true, "COTIZACION_BASE": true, "VALOR_FIJO": true}

// campoBloquePredeterminado es un campo con el que un bloque nace al
// crearse (CrearBloque), para no obligar a quien arma la plantilla a
// reconstruir a mano la misma lista en cada propuesta. Todos son
// COTIZACION_BASE o VALOR_FIJO a propósito: no dependen de ningún
// cotizador, así que valen aunque la plantilla todavía no tenga uno.
type campoBloquePredeterminado struct {
	Etiqueta, FuenteTipo, FuenteID string
}

var camposPredeterminadosBloque = map[string][]campoBloquePredeterminado{
	// Título de la portada = titulo del bloque; subtítulo/viñetas = contenido.
	"PORTADA": {
		{"Cliente", "COTIZACION_BASE", "cliente"},
		{"Contacto", "COTIZACION_BASE", "contacto"},
		{"Fecha", "COTIZACION_BASE", "fecha_creacion"},
		{"Número de oferta", "COTIZACION_BASE", "codigo_oferta"},
	},
	"DATOS_CLIENTE": {
		{"Nombre del contacto", "COTIZACION_BASE", "contacto"},
		{"Empresa", "COTIZACION_BASE", "empresa"},
		{"Correo", "COTIZACION_BASE", "contacto_correo"},
		{"Teléfono", "COTIZACION_BASE", "contacto_telefono"},
		{"Cargo", "COTIZACION_BASE", "contacto_cargo"},
	},
	// Sin texto inventado: el equipo comercial llena validez, forma de pago,
	// plazo y observaciones; la moneda sí sale de la cotización.
	"CONDICIONES_COMERCIALES": {
		{"Validez de la propuesta", "VALOR_FIJO", ""},
		{"Forma de pago", "VALOR_FIJO", ""},
		{"Plazo de entrega", "VALOR_FIJO", ""},
		{"Moneda", "COTIZACION_BASE", "moneda"},
		{"Observaciones comerciales", "VALOR_FIJO", ""},
	},
	"FIRMA_ACEPTACION": {
		{"Nombre", "COTIZACION_BASE", "contacto"},
		{"Cargo", "COTIZACION_BASE", "contacto_cargo"},
	},
}

const (
	maxEtiquetaCampoBloque  = 120
	maxValorFijoCampoBloque = 2000
)

type crearCampoBloqueRequest struct {
	CalculadoraID string `json:"calculadora_id"`
	Etiqueta      string `json:"etiqueta"`
	FuenteTipo    string `json:"fuente_tipo"`
	FuenteID      string `json:"fuente_id"`
	ValorFijo     string `json:"valor_fijo"`
}

type editarCampoBloqueRequest struct {
	CalculadoraID *string `json:"calculadora_id"`
	Etiqueta      *string `json:"etiqueta"`
	FuenteTipo    *string `json:"fuente_tipo"`
	FuenteID      *string `json:"fuente_id"`
	ValorFijo     *string `json:"valor_fijo"`
}

// campoBloqueNormalizado es el campo listo para escribirse: los valores que
// no aplican a su fuente_tipo ya quedaron vacíos (y se guardan como NULL).
type campoBloqueNormalizado struct {
	CalculadoraID, Etiqueta, FuenteTipo, FuenteID, ValorFijo string
}

// normalizarCampoBloque limpia y valida un campo contra la plantilla del
// bloque. Devuelve un mensaje de error de validación (400) o un error de
// base (500), nunca los dos.
func normalizarCampoBloque(ctx context.Context, db *pgxpool.Pool, plantillaID string, campo campoBloqueNormalizado) (campoBloqueNormalizado, string, error) {
	campo.Etiqueta = strings.TrimSpace(campo.Etiqueta)
	campo.FuenteTipo = strings.ToUpper(strings.TrimSpace(campo.FuenteTipo))
	campo.FuenteID = strings.TrimSpace(campo.FuenteID)
	campo.CalculadoraID = strings.TrimSpace(campo.CalculadoraID)
	campo.ValorFijo = strings.TrimSpace(campo.ValorFijo)
	if campo.Etiqueta == "" || len([]rune(campo.Etiqueta)) > maxEtiquetaCampoBloque {
		return campo, "La etiqueta es obligatoria y admite hasta 120 caracteres.", nil
	}
	if !fuentesTipoCampoBloque[campo.FuenteTipo] {
		return campo, "fuente_tipo debe ser CAMPO, COTIZACION_BASE o VALOR_FIJO.", nil
	}
	switch campo.FuenteTipo {
	case "VALOR_FIJO":
		campo.CalculadoraID, campo.FuenteID = "", ""
		if len([]rune(campo.ValorFijo)) > maxValorFijoCampoBloque {
			return campo, "El valor fijo admite hasta 2000 caracteres.", nil
		}
		return campo, "", nil
	case "COTIZACION_BASE":
		// Un dato base es común a cualquier cotizador: no se ata a uno.
		campo.CalculadoraID, campo.ValorFijo = "", ""
		if !idsCotizacionBase[campo.FuenteID] {
			return campo, "El dato de la cotización indicado no existe.", nil
		}
		return campo, "", nil
	}
	campo.ValorFijo = ""
	if campo.CalculadoraID == "" || campo.FuenteID == "" {
		return campo, "Un campo del cotizador necesita calculadora_id y fuente_id.", nil
	}
	var asociada bool
	if err := db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plantilla_calculadoras
		 WHERE plantilla_id::text=$1 AND calculadora_id=$2)`, plantillaID, campo.CalculadoraID).Scan(&asociada); err != nil {
		return campo, "", err
	}
	if !asociada {
		return campo, "El cotizador no está asociado a la plantilla del bloque.", nil
	}
	valida, err := fuenteCondicionValida(ctx, db, "CAMPO", campo.FuenteID, campo.CalculadoraID)
	if err != nil {
		return campo, "", err
	}
	if !valida {
		return campo, "La fuente indicada no existe o no está activa para ese cotizador.", nil
	}
	return campo, "", nil
}

// Agregar responde POST /api/plantillas/bloques/{bloque_id}/campos.
func (h *PlantillaBloqueCamposHandler) Agregar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "bloque", bloqueID) {
		return
	}
	var req crearCampoBloqueRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	plantillaID, tipoBloque, err := plantillaYTipoDeBloque(ctx, h.DB, bloqueID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	if err != nil {
		responderErrorCampoBloque(w, "validar el bloque", err)
		return
	}
	if !tiposBloqueConCampos[tipoBloque] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Este tipo de bloque no admite campos."})
		return
	}
	campo, mensaje, err := normalizarCampoBloque(ctx, h.DB, plantillaID, campoBloqueNormalizado{
		CalculadoraID: req.CalculadoraID, Etiqueta: req.Etiqueta, FuenteTipo: req.FuenteTipo,
		FuenteID: req.FuenteID, ValorFijo: req.ValorFijo,
	})
	if err != nil {
		responderErrorCampoBloque(w, "validar el campo", err)
		return
	}
	if mensaje != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": mensaje})
		return
	}
	var campoID string
	var orden int
	err = h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_bloque_campos (bloque_id,calculadora_id,etiqueta,fuente_tipo,fuente_id,valor_fijo,orden)
		SELECT $1::uuid, NULLIF($2::text,''), $3::text, $4::text, NULLIF($5::text,''),
		       CASE WHEN $4::text='VALOR_FIJO' THEN $6::text END, COALESCE(MAX(orden),-1)+1
		  FROM plantilla_bloque_campos WHERE bloque_id=$1::uuid
		RETURNING campo_id::text, orden`, bloqueID, campo.CalculadoraID, campo.Etiqueta,
		campo.FuenteTipo, campo.FuenteID, campo.ValorFijo).Scan(&campoID, &orden)
	if err != nil {
		responderErrorCampoBloque(w, "crear el campo", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "campo_id": campoID, "orden": orden, "mensaje": "Campo creado."})
}

// Editar responde PATCH /api/plantillas/campos/{campo_id}. Se revalida el
// campo completo (no solo lo que cambió) porque fuente_tipo, fuente_id y
// calculadora_id solo son válidos juntos.
func (h *PlantillaBloqueCamposHandler) Editar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "campo_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "campo", id) {
		return
	}
	var req editarCampoBloqueRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.CalculadoraID == nil && req.Etiqueta == nil && req.FuenteTipo == nil && req.FuenteID == nil && req.ValorFijo == nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos una propiedad para editar."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var plantillaID string
	var actual campoBloqueNormalizado
	err := h.DB.QueryRow(ctx, `
		SELECT ps.plantilla_id::text, COALESCE(c.calculadora_id,''), c.etiqueta, c.fuente_tipo,
		       COALESCE(c.fuente_id,''), COALESCE(c.valor_fijo,'')
		  FROM plantilla_bloque_campos c
		  JOIN plantilla_bloques pb ON pb.bloque_id=c.bloque_id
		  JOIN plantilla_secciones ps ON ps.seccion_id=pb.seccion_id
		 WHERE c.campo_id::text=$1`, id).Scan(&plantillaID, &actual.CalculadoraID, &actual.Etiqueta,
		&actual.FuenteTipo, &actual.FuenteID, &actual.ValorFijo)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Campo no encontrado."})
		return
	}
	if err != nil {
		responderErrorCampoBloque(w, "validar el campo", err)
		return
	}
	if req.CalculadoraID != nil {
		actual.CalculadoraID = *req.CalculadoraID
	}
	if req.Etiqueta != nil {
		actual.Etiqueta = *req.Etiqueta
	}
	if req.FuenteTipo != nil {
		actual.FuenteTipo = *req.FuenteTipo
	}
	if req.FuenteID != nil {
		actual.FuenteID = *req.FuenteID
	}
	if req.ValorFijo != nil {
		actual.ValorFijo = *req.ValorFijo
	}
	campo, mensaje, err := normalizarCampoBloque(ctx, h.DB, plantillaID, actual)
	if err != nil {
		responderErrorCampoBloque(w, "validar el campo", err)
		return
	}
	if mensaje != "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": mensaje})
		return
	}
	tag, err := h.DB.Exec(ctx, `
		UPDATE plantilla_bloque_campos
		   SET calculadora_id=NULLIF($2::text,''), etiqueta=$3::text, fuente_tipo=$4::text, fuente_id=NULLIF($5::text,''),
		       valor_fijo=CASE WHEN $4::text='VALOR_FIJO' THEN $6::text END
		 WHERE campo_id::text=$1`, id, campo.CalculadoraID, campo.Etiqueta, campo.FuenteTipo,
		campo.FuenteID, campo.ValorFijo)
	if err != nil {
		responderErrorCampoBloque(w, "editar el campo", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Campo no encontrado."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "campo_id": id, "mensaje": "Campo actualizado."})
}

// Eliminar responde DELETE /api/plantillas/campos/{campo_id}.
func (h *PlantillaBloqueCamposHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "campo_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "campo", id) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `DELETE FROM plantilla_bloque_campos WHERE campo_id::text=$1`, id)
	if err != nil {
		responderErrorCampoBloque(w, "eliminar el campo", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Campo no encontrado."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "campo_id": id, "mensaje": "Campo eliminado."})
}

// Ordenar responde POST /api/plantillas/bloques/{bloque_id}/campos/orden.
// El contenedor es el bloque completo — comunes y de cada cotizador en un
// solo orden, que es el que el renderizador respeta al mezclarlos.
func (h *PlantillaBloqueCamposHandler) Ordenar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "bloque", bloqueID) {
		return
	}
	ids, err := decodificarOrden(r, "campo_ids")
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	existentes, err := consultarIDs(ctx, h.DB, `SELECT campo_id::text FROM plantilla_bloque_campos WHERE bloque_id::text=$1`, bloqueID)
	if err != nil {
		responderErrorCampoBloque(w, "consultar los campos", err)
		return
	}
	if err := validarOrdenCompleto(ids, existentes); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := aplicarOrden(ctx, h.DB, "plantilla_bloque_campos", "campo_id", ids); err != nil {
		responderErrorCampoBloque(w, "reordenar los campos", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "campo_ids": ids, "mensaje": "Campos reordenados."})
}

func plantillaYTipoDeBloque(ctx context.Context, db consultadorFila, bloqueID string) (string, string, error) {
	var plantillaID, tipoBloque string
	err := db.QueryRow(ctx, `
		SELECT ps.plantilla_id::text, pb.tipo_bloque
		  FROM plantilla_bloques pb
		  JOIN plantilla_secciones ps ON ps.seccion_id=pb.seccion_id
		 WHERE pb.bloque_id::text=$1`, bloqueID).Scan(&plantillaID, &tipoBloque)
	return plantillaID, tipoBloque, err
}

// insertarCamposPredeterminados deja un bloque recién creado con sus campos
// de fábrica (camposPredeterminadosBloque). Va dentro de la misma
// transacción que crea el bloque: o nace completo, o no nace.
func insertarCamposPredeterminados(ctx context.Context, tx pgx.Tx, bloqueID, tipoBloque string) error {
	for orden, campo := range camposPredeterminadosBloque[tipoBloque] {
		if _, err := tx.Exec(ctx, `
			INSERT INTO plantilla_bloque_campos (bloque_id,fuente_tipo,etiqueta,fuente_id,valor_fijo,orden)
			VALUES ($1::uuid,$2::text,$3::text,NULLIF($4::text,''),CASE WHEN $2::text='VALOR_FIJO' THEN '' END,$5)`,
			bloqueID, campo.FuenteTipo, campo.Etiqueta, campo.FuenteID, orden); err != nil {
			return err
		}
	}
	return nil
}

func responderErrorCampoBloque(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantilla bloque campos: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
