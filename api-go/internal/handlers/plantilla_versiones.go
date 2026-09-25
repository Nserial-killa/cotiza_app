package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// plantilla_versiones.go — Ronda P5, "Publicación controlada" (migración
// 0031). Una versión Publicada (o Archivada) no se edita directamente: cada
// endpoint que modifica una plantilla llama primero a plantillaNoEditable, y
// para hacer cambios se crea una versión nueva con NuevaVersion.

// consultasEstadoPlantilla resuelven el estado y la versión de la plantilla
// dueña de cada tipo de ID que llega por URL.
var consultasEstadoPlantilla = map[string]string{
	"plantilla": `SELECT p.estado, p.version FROM plantillas p WHERE p.plantilla_id::text=$1`,
	"seccion": `SELECT p.estado, p.version FROM plantilla_secciones s
		JOIN plantillas p ON p.plantilla_id=s.plantilla_id WHERE s.seccion_id::text=$1`,
	"bloque": `SELECT p.estado, p.version FROM plantilla_bloques b
		JOIN plantilla_secciones s ON s.seccion_id=b.seccion_id
		JOIN plantillas p ON p.plantilla_id=s.plantilla_id WHERE b.bloque_id::text=$1`,
	"columna": `SELECT p.estado, p.version FROM plantilla_tabla_columnas c
		JOIN plantilla_bloques b ON b.bloque_id=c.bloque_id
		JOIN plantilla_secciones s ON s.seccion_id=b.seccion_id
		JOIN plantillas p ON p.plantilla_id=s.plantilla_id WHERE c.columna_id::text=$1`,
	"campo": `SELECT p.estado, p.version FROM plantilla_bloque_campos c
		JOIN plantilla_bloques b ON b.bloque_id=c.bloque_id
		JOIN plantilla_secciones s ON s.seccion_id=b.seccion_id
		JOIN plantillas p ON p.plantilla_id=s.plantilla_id WHERE c.campo_id::text=$1`,
}

func mensajePlantillaBloqueada(estado string, version int) string {
	return fmt.Sprintf("La versión %d de esta plantilla está %s y quedó bloqueada: no se puede editar directamente. Cree una nueva versión para hacer cambios.", version, estado)
}

// plantillaNoEditable responde 409 (y devuelve true) si el ID pertenece a
// una plantilla que no está en Borrador. Si el ID no existe devuelve false
// sin responder: cada handler ya sabe contestar su propio 404. El chequeo
// va antes de la escritura y fuera de su transacción; una publicación que
// entre justo en medio es una carrera de milisegundos que se acepta a
// cambio de no reescribir cada handler alrededor de un bloqueo.
func plantillaNoEditable(w http.ResponseWriter, ctx context.Context, db *pgxpool.Pool, tipoID, id string) bool {
	var estado string
	var version int
	err := db.QueryRow(ctx, consultasEstadoPlantilla[tipoID], strings.TrimSpace(id)).Scan(&estado, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		log.Printf("plantillas: error validando si la plantilla es editable (%s %s): %v", tipoID, id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el estado de la plantilla."})
		return true
	}
	if estado == "Borrador" {
		return false
	}
	escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": mensajePlantillaBloqueada(estado, version), "estado": estado, "version": version})
	return true
}

// NuevaVersion responde POST /api/plantillas/{id}/nueva-version: copia una
// versión Publicada (o Archivada) completa — secciones, bloques, campos,
// vinculaciones, condiciones, columnas, estilo, cotizadores y tipos — a una
// versión nueva en Borrador, mismo codigo y version = la más alta + 1. Si
// ya hay un borrador de esa plantilla, no crea otro: devuelve 409 con su ID
// para que el editor lo abra (idx_plantillas_un_borrador_por_codigo).
func (h *PlantillasHandler) NuevaVersion(w http.ResponseWriter, r *http.Request) {
	origenID := strings.TrimSpace(chi.URLParam(r, "id"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		responderErrorPlantilla(w, "iniciar la nueva versión", err)
		return
	}
	defer tx.Rollback(ctx)

	var codigo, estado string
	err = tx.QueryRow(ctx, `SELECT codigo, estado FROM plantillas WHERE plantilla_id::text=$1 FOR UPDATE`, origenID).Scan(&codigo, &estado)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		responderErrorPlantilla(w, "validar la plantilla", err)
		return
	}
	if estado == "Borrador" {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Esta versión todavía es un borrador: edítela directamente, no hace falta una versión nueva."})
		return
	}
	var borradorID string
	err = tx.QueryRow(ctx, `SELECT plantilla_id::text FROM plantillas WHERE codigo=$1 AND estado='Borrador'`, codigo).Scan(&borradorID)
	if err == nil {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "plantilla_id": borradorID, "error": "Ya existe una versión en borrador de esta plantilla; ábrala para continuar sus cambios."})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		responderErrorPlantilla(w, "buscar un borrador existente", err)
		return
	}

	var nuevaID string
	var version int
	err = tx.QueryRow(ctx, `
		INSERT INTO plantillas (codigo, nombre, descripcion, estado, version, organizacion_id,
			disponible_nuevas_propuestas, permite_duplicar, version_anterior_id)
		SELECT p.codigo, p.nombre, p.descripcion, 'Borrador',
		       (SELECT MAX(v.version) FROM plantillas v WHERE v.codigo=p.codigo)+1,
		       p.organizacion_id, p.disponible_nuevas_propuestas, p.permite_duplicar, p.plantilla_id
		  FROM plantillas p WHERE p.plantilla_id::text=$1
		RETURNING plantilla_id::text, version`, origenID).Scan(&nuevaID, &version)
	if err != nil {
		responderErrorPlantilla(w, "crear la nueva versión", err)
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO plantilla_calculadoras (plantilla_id, calculadora_id)
		SELECT $1::uuid, calculadora_id FROM plantilla_calculadoras WHERE plantilla_id::text=$2`, nuevaID, origenID); err != nil {
		responderErrorPlantilla(w, "copiar los cotizadores", err)
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO plantilla_tipos_propuesta (plantilla_id, tipo_propuesta)
		SELECT $1::uuid, tipo_propuesta FROM plantilla_tipos_propuesta WHERE plantilla_id::text=$2`, nuevaID, origenID); err != nil {
		responderErrorPlantilla(w, "copiar los tipos de propuesta", err)
		return
	}
	if err := copiarContenidoPlantilla(ctx, tx, origenID, nuevaID, true); err != nil {
		responderErrorPlantilla(w, "copiar el contenido de la versión", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		responderErrorPlantilla(w, "confirmar la nueva versión", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "plantilla_id": nuevaID, "codigo": codigo, "version": version, "estado": "Borrador",
		"mensaje": fmt.Sprintf("Se creó la versión %d en borrador. La versión publicada sigue en uso hasta que publique esta.", version),
	})
}
