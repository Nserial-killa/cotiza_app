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

// PlantillaCondicionesHandler administra "mostrar este bloque solo si..."
// (cuarto hueco del documento del jefe, caso ISA Custom). Mismo patrón que
// PlantillaVinculacionesHandler: una condición por (bloque, calculadora),
// porque una plantilla puede estar asociada a varios cotizadores con
// vocabularios de campos distintos. El motor de evaluación es el mismo que
// reglas_evaluacion.go — ver plantilla_renderizador.go.
type PlantillaCondicionesHandler struct {
	DB *pgxpool.Pool
}

// operadoresCondicionPlantilla es el mismo vocabulario que reglas_cotizador
// (migración 0024) — no se inventa uno nuevo para plantillas.
var operadoresCondicionPlantilla = map[string]bool{
	"IGUAL_A": true, "DISTINTO_DE": true, "MAYOR_QUE": true, "MENOR_QUE": true,
	"MAYOR_O_IGUAL_QUE": true, "MENOR_O_IGUAL_QUE": true,
	"ESTA_VACIO": true, "NO_ESTA_VACIO": true,
}

// operadoresCondicionSinValor son los dos operadores que no llevan
// valor_comparacion — igual que reglas_cotizador.
var operadoresCondicionSinValor = map[string]bool{"ESTA_VACIO": true, "NO_ESTA_VACIO": true}

type guardarCondicionBloqueRequest struct {
	CalculadoraID    string `json:"calculadora_id"`
	FuenteTipo       string `json:"fuente_tipo"`
	FuenteID         string `json:"fuente_id"`
	Operador         string `json:"operador"`
	ValorComparacion string `json:"valor_comparacion"`
}

// Guardar responde POST /api/plantillas/bloques/{bloque_id}/condicion.
// Upsert por (bloque_id, calculadora_id), mismo mecanismo ON CONFLICT que
// PlantillaVinculacionesHandler.Guardar.
func (h *PlantillaCondicionesHandler) Guardar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	var req guardarCondicionBloqueRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.FuenteTipo = strings.ToUpper(strings.TrimSpace(req.FuenteTipo))
	req.FuenteID = strings.TrimSpace(req.FuenteID)
	req.Operador = strings.ToUpper(strings.TrimSpace(req.Operador))
	req.ValorComparacion = strings.TrimSpace(req.ValorComparacion)

	if bloqueID == "" || req.CalculadoraID == "" || req.FuenteID == "" ||
		(req.FuenteTipo != "CAMPO" && req.FuenteTipo != "COTIZACION_BASE") {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque, calculadora_id, fuente_id y una fuente_tipo válida."})
		return
	}
	if !operadoresCondicionPlantilla[req.Operador] {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "operador no es válido."})
		return
	}
	if operadoresCondicionSinValor[req.Operador] {
		req.ValorComparacion = ""
	} else if req.ValorComparacion == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "valor_comparacion es obligatorio para este operador."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var plantillaID string
	err := h.DB.QueryRow(ctx, `
		SELECT ps.plantilla_id::text
		  FROM plantilla_bloques pb
		  JOIN plantilla_secciones ps ON ps.seccion_id=pb.seccion_id
		 WHERE pb.bloque_id::text=$1`, bloqueID).Scan(&plantillaID)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Bloque no encontrado."})
		return
	}
	if err != nil {
		responderErrorCondicion(w, "validar el bloque", err)
		return
	}
	var asociada bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM plantilla_calculadoras
		 WHERE plantilla_id::text=$1 AND calculadora_id=$2)`, plantillaID, req.CalculadoraID).Scan(&asociada); err != nil {
		responderErrorCondicion(w, "validar el cotizador", err)
		return
	}
	if !asociada {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cotizador no está asociado a la plantilla del bloque."})
		return
	}
	valida, err := fuenteCondicionValida(ctx, h.DB, req.FuenteTipo, req.FuenteID, req.CalculadoraID)
	if err != nil {
		responderErrorCondicion(w, "validar la fuente", err)
		return
	}
	if !valida {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "La fuente indicada no existe o no está activa para ese cotizador."})
		return
	}

	var condicionID string
	err = h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_bloque_condiciones (bloque_id,calculadora_id,fuente_tipo,fuente_id,operador,valor_comparacion)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
		ON CONFLICT (bloque_id,calculadora_id) DO UPDATE SET
			fuente_tipo=EXCLUDED.fuente_tipo, fuente_id=EXCLUDED.fuente_id,
			operador=EXCLUDED.operador, valor_comparacion=EXCLUDED.valor_comparacion
		RETURNING condicion_id::text`, bloqueID, req.CalculadoraID,
		req.FuenteTipo, req.FuenteID, req.Operador, req.ValorComparacion).Scan(&condicionID)
	if err != nil {
		responderErrorCondicion(w, "guardar la condición", err)
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{
		"ok": true, "condicion_id": condicionID, "bloque_id": bloqueID,
		"calculadora_id": req.CalculadoraID, "mensaje": "Condición guardada.",
	})
}

// fuenteCondicionValida es la validación compartida de "fuente" para
// vinculaciones, condiciones y columnas de tabla (PlantillaVinculaciones-
// Handler.fuenteValida delega acá en vez de duplicarla). CAMPO admite
// cualquier tipo de elemento que plantilla_renderizador.go sepa resolver
// como valor puntual — no solo CAMPO/CAMPO_CATALOGO: un CAMPO_CALCULADO
// (ej. TOTAL_MENSUAL, PRECIO_IMPLEMENTACION) es, en la práctica, el dato
// más importante de una oferta y ya se resolvía bien en tiempo de
// renderizado (resolverValorFuentePlantilla); la validación se había
// quedado corta y lo rechazaba antes de llegar ahí.
func fuenteCondicionValida(ctx context.Context, db *pgxpool.Pool, fuenteTipo, fuenteID, calculadoraID string) (bool, error) {
	if fuenteTipo == "COTIZACION_BASE" {
		return idsCotizacionBase[fuenteID], nil
	}
	var existe bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM elementos_tab_cotizador e
			JOIN tabs_cotizador t ON t.tab_id=e.tab_id
			WHERE e.elemento_id=$1 AND t.calculadora_id=$2
			  AND t.activo=true AND e.activo=true
			  AND e.tipo IN ('CAMPO','CAMPO_CATALOGO','CAMPO_CALCULADO','LISTA_PRECIOS','TABLA'))`,
		fuenteID, calculadoraID).Scan(&existe)
	return existe, err
}

// Eliminar responde DELETE /api/plantillas/bloques/{bloque_id}/condicion?calculadora_id=...
func (h *PlantillaCondicionesHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	bloqueID := strings.TrimSpace(chi.URLParam(r, "bloque_id"))
	calculadoraID := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	if bloqueID == "" || calculadoraID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar bloque y calculadora_id."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tag, err := h.DB.Exec(ctx, `
		DELETE FROM plantilla_bloque_condiciones
		 WHERE bloque_id::text=$1 AND calculadora_id=$2`, bloqueID, calculadoraID)
	if err != nil {
		responderErrorCondicion(w, "eliminar la condición", err)
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Condición no encontrada."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "bloque_id": bloqueID, "calculadora_id": calculadoraID, "mensaje": "Condición eliminada."})
}

func responderErrorCondicion(w http.ResponseWriter, accion string, err error) {
	log.Printf("plantilla condiciones: error al %s: %v", accion, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible " + accion + "."})
}
