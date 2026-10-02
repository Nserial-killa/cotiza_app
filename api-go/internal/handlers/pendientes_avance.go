package handlers

// Exigir el precio al AVANZAR (ver docs/DECISION_GUARDADO_BORRADOR.md).
//
// Un Borrador se puede guardar incompleto; lo que no se puede es mandarlo a
// revisión, al cliente o darlo por aceptado/ganado sin precio. Este archivo
// recalcula —sin escribir nada— lo que le falta a una versión, con el mismo
// cálculo que usa el guardado (calcularSalidasVersion).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// estadosQueExigenPrecio son los destinos de CambiarEstado que necesitan la
// versión completa. Cancelada, Perdida y Vencida no están a propósito: un
// borrador incompleto se tiene que poder descartar. Los estados que dispara
// el cliente desde el link público (Vista por el Cliente, Cambios
// solicitados) tampoco pasan por acá.
var estadosQueExigenPrecio = map[string]bool{
	"Revisión Comercial": true, "Enviada al Cliente": true, "Aceptada": true, "Ganada": true,
}

// errorAvance es el rechazo legible que se devuelve con 409.
type errorAvance struct{ mensaje string }

func (e *errorAvance) Error() string { return e.mensaje }

// verificarVersionCompleta devuelve un *errorAvance si la versión tiene
// salidas requeridas sin resolver, campos obligatorios vacíos o un cálculo
// que no cierra; nil si está lista para avanzar. accion completa la frase:
// "pasar a «Enviada al Cliente»", "generar el enlace para el cliente".
//
// Una cotización sin ninguna estructura compilada (ni snapshot, ni compilado
// fijado, ni un compilado activo en su cotizador: datos migrados o heredados
// de antes del Motor de Ejecución) no tiene salidas que revisar, así que no
// se le exige nada: hoy ninguna cotización nueva puede nacer así, porque
// crear una exige un cotizador publicado con precio.
func verificarVersionCompleta(ctx context.Context, tx pgx.Tx, h *CotizadorRuntimeHandler, cotizacionID string, version int, accion string) error {
	var tieneEstructura bool
	if err := tx.QueryRow(ctx, `
		SELECT cv.snapshot_json IS NOT NULL OR c.compilado_id_usado IS NOT NULL OR EXISTS (
		         SELECT 1 FROM cotizadores_compilados cc WHERE cc.calculadora_id=c.calculadora_id AND cc.estado='ACTIVA')
		  FROM cotizaciones c
		  JOIN cotizacion_versiones cv ON cv.cotizacion_id=c.cotizacion_id AND cv.numero_version=$2
		 WHERE c.cotizacion_id=$1`, cotizacionID, version).Scan(&tieneEstructura); err != nil {
		return err
	}
	if !tieneEstructura {
		return nil
	}
	// fijar=false: revisar nunca debe fijar el compilado de la cotización.
	rt, err := h.cargarContextoConDB(ctx, tx, cotizacionID, version, false)
	if err != nil {
		return err
	}
	reglas, err := reglasCotizadorParaEvaluar(ctx, tx, rt.CalculadoraID)
	if err != nil {
		return err
	}
	// permitirPendientes=true para juntar TODO lo que falta en un solo
	// mensaje en vez de cortar en el primero.
	calculo, err := h.calcularSalidasVersion(ctx, tx, &rt, reglas, true)
	if err != nil {
		var errRT *errorRuntime
		if errors.As(err, &errRT) && errRT.status < 500 {
			return &errorAvance{fmt.Sprintf("No se puede %s: %s", accion, errRT.mensaje)}
		}
		return err
	}
	if mensajes := mensajesPendientes(calculo.pendientes); len(mensajes) > 0 {
		return &errorAvance{fmt.Sprintf("No se puede %s todavía. %s", accion, strings.Join(mensajes, " "))}
	}
	return nil
}

// responderErrorAvance escribe el 409 legible o el 500 genérico. Devuelve
// false si hubo error (el handler tiene que cortar).
func responderErrorAvance(w http.ResponseWriter, err error, contexto string) bool {
	if err == nil {
		return true
	}
	var errAv *errorAvance
	if errors.As(err, &errAv) {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": errAv.mensaje})
		return false
	}
	log.Printf("%s: error revisando pendientes: %v", contexto, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible revisar si la cotización está completa."})
	return false
}
