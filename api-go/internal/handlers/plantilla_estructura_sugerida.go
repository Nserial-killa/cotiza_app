package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// plantilla_estructura_sugerida.go — Ronda P2: "Estructura sugerida", el
// punto de partida con que se puebla una plantilla nueva en el paso 2. Es la
// estructura de la plantilla real de producción de Exceltec: 12 secciones,
// 18 bloques, con los tipos de la Ronda P1.
//
// No es un molde: una vez aplicada, cada sección y bloque es una fila
// normal que se edita, reordena o borra como cualquier otra. Tampoco
// vincula ningún dato (eso es el paso 3): los textos son indicaciones para
// quien arma la plantilla, y los bloques con campos nacen con los mismos
// campos de fábrica que al crearlos a mano (camposPredeterminadosBloque).

type bloqueSugerido struct {
	Tipo, NombreInterno, Titulo, Contenido string
}

type seccionSugerida struct {
	Nombre        string
	MostrarTitulo bool
	Bloques       []bloqueSugerido
}

// seccionEncabezadoYTexto arma las 6 secciones narrativas del medio de la
// propuesta, todas con la misma forma: un ENCABEZADO con el nombre de la
// sección y un TEXTO con la indicación de qué escribir.
func seccionEncabezadoYTexto(nombre, clave, indicacion string) seccionSugerida {
	return seccionSugerida{Nombre: nombre, MostrarTitulo: true, Bloques: []bloqueSugerido{
		{Tipo: "ENCABEZADO", NombreInterno: clave + "_encabezado", Titulo: nombre},
		{Tipo: "TEXTO", NombreInterno: clave + "_texto", Titulo: nombre, Contenido: indicacion},
	}}
}

// estructuraSugeridaPlantilla es la referencia. Los textos no usan corchetes
// a propósito: "[...]" es la sintaxis de token [NOMBRE_INTERNO] del
// renderizador.
var estructuraSugeridaPlantilla = []seccionSugerida{
	{Nombre: "Portada", MostrarTitulo: false, Bloques: []bloqueSugerido{
		{Tipo: "PORTADA", NombreInterno: "portada", Titulo: "Propuesta comercial",
			Contenido: "Subtítulo de la propuesta\n• Beneficio principal para el cliente\n• Segundo beneficio destacado"},
	}},
	{Nombre: "Información del cliente", MostrarTitulo: true, Bloques: []bloqueSugerido{
		{Tipo: "DATOS_CLIENTE", NombreInterno: "datos_cliente", Titulo: "Datos del cliente"},
	}},
	{Nombre: "Resumen ejecutivo", MostrarTitulo: true, Bloques: []bloqueSugerido{
		{Tipo: "RESUMEN_EJECUTIVO", NombreInterno: "resumen_ejecutivo", Titulo: "Resumen ejecutivo",
			Contenido: "Resuma en pocas líneas la necesidad del cliente, la solución propuesta y el valor que obtiene."},
	}},
	seccionEncabezadoYTexto("Situación actual", "situacion_actual",
		"Describa el contexto del cliente: cómo opera hoy, qué problema enfrenta y qué impacto tiene."),
	seccionEncabezadoYTexto("Solución propuesta", "solucion_propuesta",
		"Explique la solución que se ofrece y cómo resuelve la situación descrita."),
	seccionEncabezadoYTexto("Alcance del proyecto", "alcance_proyecto",
		"Detalle qué incluye la propuesta y qué queda fuera del alcance.\n• Incluye: …\n• No incluye: …"),
	seccionEncabezadoYTexto("Metodología", "metodologia",
		"Describa las etapas de trabajo y cómo se ejecutará el proyecto."),
	seccionEncabezadoYTexto("Cronograma", "cronograma",
		"Indique las fases y los plazos estimados de cada una."),
	seccionEncabezadoYTexto("Equipo de trabajo", "equipo_trabajo",
		"Presente los roles que participarán y sus responsabilidades."),
	{Nombre: "Inversión", MostrarTitulo: true, Bloques: []bloqueSugerido{
		{Tipo: "TABLA_INVERSION", NombreInterno: "tabla_inversion", Titulo: "Inversión"},
	}},
	{Nombre: "Condiciones comerciales", MostrarTitulo: true, Bloques: []bloqueSugerido{
		{Tipo: "CONDICIONES_COMERCIALES", NombreInterno: "condiciones_comerciales", Titulo: "Condiciones comerciales"},
	}},
	{Nombre: "Aceptación", MostrarTitulo: true, Bloques: []bloqueSugerido{
		{Tipo: "FIRMA_ACEPTACION", NombreInterno: "firma_aceptacion", Titulo: "Firma y aceptación"},
	}},
}

const mensajeEstructuraExistente = "La plantilla ya tiene secciones. Para volver a generar la estructura sugerida, primero elimine la estructura actual."

// AplicarEstructuraSugerida responde POST /api/plantillas/{id}/estructura-sugerida.
// Solo sobre una plantilla SIN secciones (409 si tiene alguna): no mezcla
// ni pisa lo que la persona ya armó. Todo va en una transacción con la
// plantilla bloqueada, así dos clics seguidos no la duplican.
func (h *PlantillaEstructuraHandler) AplicarEstructuraSugerida(w http.ResponseWriter, r *http.Request) {
	plantillaID := strings.TrimSpace(chi.URLParam(r, "id"))
	if plantillaNoEditable(w, r.Context(), h.DB, "plantilla", plantillaID) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		responderErrorEstructura(w, "iniciar la estructura sugerida", err)
		return
	}
	defer tx.Rollback(ctx)
	// Bloquear y verificar van en DOS sentencias a propósito: en READ
	// COMMITTED, un EXISTS dentro del mismo SELECT ... FOR UPDATE se evalúa
	// con la foto de antes de esperar el bloqueo y no ve las secciones que
	// acaba de confirmar quien lo tenía (dos clics terminaban con 24
	// secciones — TestPlantillaEstructuraSugerida_ConcurrenteSoloUnaGana).
	var bloqueada string
	err = tx.QueryRow(ctx, `SELECT plantilla_id::text FROM plantillas WHERE plantilla_id::text=$1 FOR UPDATE`, plantillaID).Scan(&bloqueada)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		responderErrorEstructura(w, "validar la plantilla", err)
		return
	}
	var tieneSecciones bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantilla_secciones WHERE plantilla_id::text=$1)`, plantillaID).Scan(&tieneSecciones); err != nil {
		responderErrorEstructura(w, "validar la plantilla", err)
		return
	}
	if tieneSecciones {
		escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": mensajeEstructuraExistente})
		return
	}

	bloques := 0
	for orden, seccion := range estructuraSugeridaPlantilla {
		var seccionID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO plantilla_secciones
				(plantilla_id,nombre,titulo,mostrar_titulo,visibilidad,diseno_bloques,mostrar_web,mostrar_pdf,orden)
			VALUES ($1::uuid,$2,$2,$3,'SIEMPRE','UNA',true,true,$4)
			RETURNING seccion_id::text`, plantillaID, seccion.Nombre, seccion.MostrarTitulo, orden).Scan(&seccionID); err != nil {
			responderErrorEstructura(w, "crear las secciones sugeridas", err)
			return
		}
		for _, b := range seccion.Bloques {
			req := crearBloqueRequest{
				TipoBloque: b.Tipo, NombreInterno: b.NombreInterno, Titulo: b.Titulo,
				Contenido: b.Contenido, OrigenFilas: "FIJO",
			}
			if req.Contenido == "" {
				req.Contenido = contenidoPredeterminadoBloque[req.TipoBloque]
			}
			if _, _, err := insertarBloqueTx(ctx, tx, plantillaID, seccionID, req); err != nil {
				responderErrorEstructura(w, "crear los bloques sugeridos", err)
				return
			}
			bloques++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		responderErrorEstructura(w, "confirmar la estructura sugerida", err)
		return
	}
	escribirJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "plantilla_id": plantillaID, "secciones": len(estructuraSugeridaPlantilla), "bloques": bloques,
		"mensaje": "Se cargó la estructura sugerida.",
	})
}
