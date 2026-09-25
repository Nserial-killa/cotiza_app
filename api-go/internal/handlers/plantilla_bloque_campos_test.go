package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// tiposPaletaPlantilla son los 14 tipos de la paleta "Bloques disponibles"
// (plantillas_app.html), en el orden de la plantilla real de producción.
var tiposPaletaPlantilla = []string{
	"PORTADA", "ENCABEZADO", "TEXTO", "IMAGEN", "DATOS_CLIENTE", "RESUMEN_EJECUTIVO",
	"TABLA_INVERSION", "TABLA_DATOS", "OPCIONES_PROPUESTA", "LISTA_PRECIOS",
	"GRUPO_INFORMACION", "CONDICIONES_COMERCIALES", "FIRMA_ACEPTACION", "SALTO_PAGINA",
}

func crearBloqueTipoPrueba(t *testing.T, e *entornoPlantillasPrueba, seccionID, tipo string, extras map[string]any) string {
	t.Helper()
	body := map[string]any{"tipo_bloque": tipo, "nombre_interno": strings.ToLower(tipo) + "_" + sufijoUnico(), "columna": 0}
	for clave, valor := range extras {
		body[clave] = valor
	}
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques",
		"/api/plantillas/secciones/"+seccionID+"/bloques", e.estructura.CrearBloque, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear bloque %s: esperaba 201, dio %d: %s", tipo, rec.Code, rec.Body.String())
	}
	id, _ := respuestaPlantilla(t, rec)["bloque_id"].(string)
	return id
}

func agregarCampoBloquePrueba(t *testing.T, h *PlantillaBloqueCamposHandler, bloqueID string, body map[string]any) string {
	t.Helper()
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/campos",
		"/api/plantillas/bloques/"+bloqueID+"/campos", h.Agregar, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("agregar campo %v: esperaba 201, dio %d: %s", body["etiqueta"], rec.Code, rec.Body.String())
	}
	id, _ := respuestaPlantilla(t, rec)["campo_id"].(string)
	return id
}

func detalleBloquesPrueba(t *testing.T, e *entornoPlantillasPrueba, plantillaID string) []plantillaBloque {
	t.Helper()
	detalle, err := e.plantillas.consultarDetalle(context.Background(), plantillaID)
	if err != nil {
		t.Fatal(err)
	}
	bloques := make([]plantillaBloque, 0)
	for _, s := range detalle.Secciones {
		bloques = append(bloques, s.Bloques...)
	}
	return bloques
}

func buscarBloqueDetalle(bloques []plantillaBloque, id string) *plantillaBloque {
	for i := range bloques {
		if bloques[i].BloqueID == id {
			return &bloques[i]
		}
	}
	return nil
}

func TestPlantillaEstructura_LosCatorceTiposSeGuardanYVuelvenEnElDetalle(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla 14 tipos", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Todo")
	ids := make(map[string]string, len(tiposPaletaPlantilla))
	for _, tipo := range tiposPaletaPlantilla {
		ids[tipo] = crearBloqueTipoPrueba(t, e, seccionID, tipo, nil)
	}

	bloques := detalleBloquesPrueba(t, e, plantillaID)
	if len(bloques) != len(tiposPaletaPlantilla) {
		t.Fatalf("esperaba %d bloques en el detalle, hay %d", len(tiposPaletaPlantilla), len(bloques))
	}
	for i, tipo := range tiposPaletaPlantilla {
		if bloques[i].TipoBloque != tipo || bloques[i].BloqueID != ids[tipo] {
			t.Fatalf("bloque %d: esperaba %s (%s), dio %s (%s)", i, tipo, ids[tipo], bloques[i].TipoBloque, bloques[i].BloqueID)
		}
		if bloques[i].Campos == nil {
			t.Fatalf("%s: campos debe venir como lista (vacía si no aplica), no null", tipo)
		}
	}

	salto := buscarBloqueDetalle(bloques, ids["SALTO_PAGINA"])
	if len(salto.Vinculaciones) != 0 || len(salto.Campos) != 0 || len(salto.Columnas) != 0 || len(salto.Condiciones) != 0 {
		t.Fatalf("SALTO_PAGINA no necesita ninguna configuración y debe guardarse sin ella: %+v", salto)
	}
	opciones := buscarBloqueDetalle(bloques, ids["OPCIONES_PROPUESTA"])
	if opciones.OrigenFilas != "OPCIONES_PROPUESTA" {
		t.Fatalf("OPCIONES_PROPUESTA siempre es una fila por opción, dio origen_filas=%s", opciones.OrigenFilas)
	}
	if len(opciones.Columnas) != 2 || opciones.Columnas[0].FuenteTipo != "NOMBRE_ESCENARIO" || opciones.Columnas[1].FuenteTipo != "ES_RECOMENDADA" {
		t.Fatalf("OPCIONES_PROPUESTA debería nacer con Escenario y Recomendada para el cotizador asociado: %+v", opciones.Columnas)
	}
	firma := buscarBloqueDetalle(bloques, ids["FIRMA_ACEPTACION"])
	if firma.Contenido == nil || !strings.Contains(*firma.Contenido, "acepta") {
		t.Fatalf("FIRMA_ACEPTACION debería nacer con su texto de aceptación: %v", firma.Contenido)
	}
}

func TestPlantillaEstructura_DatosClienteYCondicionesNacenPrePoblados(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla prepoblada", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Datos")
	datosID := crearBloqueTipoPrueba(t, e, seccionID, "DATOS_CLIENTE", nil)
	condicionesID := crearBloqueTipoPrueba(t, e, seccionID, "CONDICIONES_COMERCIALES", nil)
	portadaID := crearBloqueTipoPrueba(t, e, seccionID, "PORTADA", nil)
	grupoID := crearBloqueTipoPrueba(t, e, seccionID, "GRUPO_INFORMACION", nil)
	bloques := detalleBloquesPrueba(t, e, plantillaID)

	type esperado struct{ etiqueta, fuenteTipo, fuenteID string }
	verificar := func(bloqueID string, esperados []esperado) {
		t.Helper()
		b := buscarBloqueDetalle(bloques, bloqueID)
		if len(b.Campos) != len(esperados) {
			t.Fatalf("%s: esperaba %d campos, hay %d: %+v", b.TipoBloque, len(esperados), len(b.Campos), b.Campos)
		}
		for i, c := range b.Campos {
			fuenteID := ""
			if c.FuenteID != nil {
				fuenteID = *c.FuenteID
			}
			if c.Etiqueta != esperados[i].etiqueta || c.FuenteTipo != esperados[i].fuenteTipo || fuenteID != esperados[i].fuenteID {
				t.Fatalf("%s campo %d: esperaba %+v, dio %s/%s/%s", b.TipoBloque, i, esperados[i], c.Etiqueta, c.FuenteTipo, fuenteID)
			}
			if c.CalculadoraID != nil {
				t.Fatalf("%s: un campo pre-poblado es común a todos los cotizadores, no debe atarse a %s", b.TipoBloque, *c.CalculadoraID)
			}
			if c.Orden != i {
				t.Fatalf("%s: orden %d esperado, dio %d", b.TipoBloque, i, c.Orden)
			}
		}
	}
	verificar(datosID, []esperado{
		{"Nombre del contacto", "COTIZACION_BASE", "contacto"},
		{"Empresa", "COTIZACION_BASE", "empresa"},
		{"Correo", "COTIZACION_BASE", "contacto_correo"},
		{"Teléfono", "COTIZACION_BASE", "contacto_telefono"},
		{"Cargo", "COTIZACION_BASE", "contacto_cargo"},
	})
	verificar(condicionesID, []esperado{
		{"Validez de la propuesta", "VALOR_FIJO", ""},
		{"Forma de pago", "VALOR_FIJO", ""},
		{"Plazo de entrega", "VALOR_FIJO", ""},
		{"Moneda", "COTIZACION_BASE", "moneda"},
		{"Observaciones comerciales", "VALOR_FIJO", ""},
	})
	verificar(portadaID, []esperado{
		{"Cliente", "COTIZACION_BASE", "cliente"},
		{"Contacto", "COTIZACION_BASE", "contacto"},
		{"Fecha", "COTIZACION_BASE", "fecha_creacion"},
		{"Número de oferta", "COTIZACION_BASE", "codigo_oferta"},
	})
	verificar(grupoID, nil) // Grupo de información: pares 100% libres.

	// Las fuentes base pre-pobladas tienen que ser fuentes que Guardar
	// aceptaría — si no, el editor no podría volver a guardarlas.
	for _, id := range []string{"contacto_correo", "contacto_telefono", "contacto_cargo", "moneda"} {
		if !idsCotizacionBase[id] {
			t.Fatalf("%s no está en fuentesCotizacionBase", id)
		}
	}
}

func TestPlantillaEstructura_SoloUnaPortadaPorPlantillaYTipoValido(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla una portada", nil)
	seccionA := crearSeccionPrueba(t, e, plantillaID, "Portada")
	seccionB := crearSeccionPrueba(t, e, plantillaID, "Cuerpo")
	portadaID := crearBloqueTipoPrueba(t, e, seccionA, "PORTADA", nil)

	// Segunda portada en OTRA sección de la misma plantilla: 409.
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques",
		"/api/plantillas/secciones/"+seccionB+"/bloques", e.estructura.CrearBloque,
		map[string]any{"tipo_bloque": "PORTADA", "nombre_interno": "portada_2"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "portada") {
		t.Fatalf("segunda portada: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}
	// Tampoco convirtiendo otro bloque en portada...
	textoID := crearBloqueTipoPrueba(t, e, seccionB, "TEXTO", nil)
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}",
		"/api/plantillas/bloques/"+textoID, e.estructura.EditarBloque, map[string]any{"tipo_bloque": "PORTADA"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("convertir en segunda portada: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}
	// ...pero la portada existente sí puede re-guardarse como PORTADA.
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}",
		"/api/plantillas/bloques/"+portadaID, e.estructura.EditarBloque, map[string]any{"tipo_bloque": "PORTADA", "titulo": "Propuesta"})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar la propia portada: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	// Otra plantilla sí puede tener la suya.
	otraID := crearPlantillaPrueba(t, e, "Otra plantilla", nil)
	crearBloqueTipoPrueba(t, e, crearSeccionPrueba(t, e, otraID, "Portada"), "PORTADA", nil)

	for _, tipo := range []string{"NO_EXISTE", "tabla-rara"} {
		rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques",
			"/api/plantillas/secciones/"+seccionB+"/bloques", e.estructura.CrearBloque,
			map[string]any{"tipo_bloque": tipo, "nombre_interno": "x"})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "tipo_bloque") {
			t.Fatalf("tipo %q: esperaba 400, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}",
		"/api/plantillas/bloques/"+textoID, e.estructura.EditarBloque, map[string]any{"tipo_bloque": "NO_EXISTE"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("editar a tipo inválido: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlantillaBloqueCampos_CRUDYOrden(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	campoTipoAgente := crearCampoTextoPrueba(t, e, "Tipo de agente")
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla grupo información", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Configuración")
	bloqueID := crearBloqueTipoPrueba(t, e, seccionID, "GRUPO_INFORMACION", nil)
	h := &PlantillaBloqueCamposHandler{DB: e.pool}

	tipoID := agregarCampoBloquePrueba(t, h, bloqueID, map[string]any{
		"calculadora_id": e.calculadora, "etiqueta": "Tipo de agente", "fuente_tipo": "CAMPO", "fuente_id": campoTipoAgente,
	})
	// VALOR_FIJO ignora fuente_id y calculadora_id aunque vengan.
	idiomaID := agregarCampoBloquePrueba(t, h, bloqueID, map[string]any{
		"etiqueta": "Idioma", "fuente_tipo": "VALOR_FIJO", "valor_fijo": " Español ",
		"fuente_id": "basura", "calculadora_id": e.calculadora,
	})
	monedaID := agregarCampoBloquePrueba(t, h, bloqueID, map[string]any{
		"etiqueta": "Moneda", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "moneda", "calculadora_id": e.calculadora,
	})

	b := buscarBloqueDetalle(detalleBloquesPrueba(t, e, plantillaID), bloqueID)
	if len(b.Campos) != 3 || b.Campos[0].CampoID != tipoID || b.Campos[1].CampoID != idiomaID || b.Campos[2].CampoID != monedaID {
		t.Fatalf("campos inesperados: %+v", b.Campos)
	}
	if b.Campos[0].CalculadoraID == nil || *b.Campos[0].CalculadoraID != e.calculadora {
		t.Fatalf("un campo CAMPO se ata a su cotizador: %+v", b.Campos[0])
	}
	idioma := b.Campos[1]
	if idioma.FuenteID != nil || idioma.CalculadoraID != nil || idioma.ValorFijo == nil || *idioma.ValorFijo != "Español" {
		t.Fatalf("VALOR_FIJO debe guardar solo el valor (recortado): %+v", idioma)
	}
	if b.Campos[2].CalculadoraID != nil || b.Campos[2].ValorFijo != nil {
		t.Fatalf("COTIZACION_BASE es común a todos los cotizadores: %+v", b.Campos[2])
	}

	// Editar: pasar Idioma a dato base y cambiar la etiqueta.
	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/campos/{campo_id}", "/api/plantillas/campos/"+idiomaID,
		h.Editar, map[string]any{"etiqueta": "Tipo de propuesta", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "tipo_propuesta"})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar campo: %d: %s", rec.Code, rec.Body.String())
	}
	// Editar un CAMPO a un campo que no existe: 400 y sin tocar la fila.
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/campos/{campo_id}", "/api/plantillas/campos/"+tipoID,
		h.Editar, map[string]any{"fuente_id": "NO-EXISTE"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("editar a fuente inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/campos/orden",
		"/api/plantillas/bloques/"+bloqueID+"/campos/orden", h.Ordenar, map[string]any{"campo_ids": []string{monedaID, tipoID, idiomaID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reordenar: %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/campos/orden",
		"/api/plantillas/bloques/"+bloqueID+"/campos/orden", h.Ordenar, map[string]any{"campo_ids": []string{monedaID, tipoID}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("un orden incompleto debe rechazarse: %d: %s", rec.Code, rec.Body.String())
	}

	b = buscarBloqueDetalle(detalleBloquesPrueba(t, e, plantillaID), bloqueID)
	if b.Campos[0].CampoID != monedaID || b.Campos[1].CampoID != tipoID || b.Campos[2].CampoID != idiomaID {
		t.Fatalf("orden no aplicado: %+v", b.Campos)
	}
	if b.Campos[2].Etiqueta != "Tipo de propuesta" || b.Campos[2].FuenteTipo != "COTIZACION_BASE" || b.Campos[2].ValorFijo != nil {
		t.Fatalf("edición no aplicada o valor_fijo no limpiado: %+v", b.Campos[2])
	}
	if b.Campos[1].FuenteID == nil || *b.Campos[1].FuenteID != campoTipoAgente {
		t.Fatalf("la edición rechazada no debió tocar el campo: %+v", b.Campos[1])
	}

	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/campos/{campo_id}", "/api/plantillas/campos/"+monedaID, h.Eliminar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar: %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/campos/{campo_id}", "/api/plantillas/campos/"+monedaID, h.Eliminar, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("eliminar dos veces: esperaba 404, dio %d", rec.Code)
	}
	if b = buscarBloqueDetalle(detalleBloquesPrueba(t, e, plantillaID), bloqueID); len(b.Campos) != 2 {
		t.Fatalf("esperaba 2 campos tras eliminar, hay %d", len(b.Campos))
	}
}

func TestPlantillaBloqueCampos_Validaciones(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	campoID := crearCampoTextoPrueba(t, e, "Idioma")
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla campos validación", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Datos")
	grupoID := crearBloqueTipoPrueba(t, e, seccionID, "GRUPO_INFORMACION", nil)
	textoID := crearBloqueTipoPrueba(t, e, seccionID, "TEXTO", nil)
	saltoID := crearBloqueTipoPrueba(t, e, seccionID, "SALTO_PAGINA", nil)

	// Un cotizador que existe pero NO está asociado a esta plantilla.
	otro := e.calculadora + "-OTRO"
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id,nombre_calculadora,estado) VALUES ($1,'Otro','Activo')`, otro); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, otro) })

	h := &PlantillaBloqueCamposHandler{DB: e.pool}
	for _, caso := range []struct {
		nombre    string
		bloqueID  string
		body      map[string]any
		codigo    int
		contenido string
	}{
		{"bloque de texto no admite campos", textoID, map[string]any{"etiqueta": "X", "fuente_tipo": "VALOR_FIJO", "valor_fijo": "Y"}, http.StatusBadRequest, "no admite campos"},
		{"salto de página no admite campos", saltoID, map[string]any{"etiqueta": "X", "fuente_tipo": "VALOR_FIJO"}, http.StatusBadRequest, "no admite campos"},
		{"bloque inexistente", "00000000-0000-0000-0000-000000000000", map[string]any{"etiqueta": "X", "fuente_tipo": "VALOR_FIJO"}, http.StatusNotFound, "no encontrado"},
		{"sin etiqueta", grupoID, map[string]any{"etiqueta": "  ", "fuente_tipo": "VALOR_FIJO"}, http.StatusBadRequest, "etiqueta"},
		{"fuente_tipo inválida", grupoID, map[string]any{"etiqueta": "X", "fuente_tipo": "RARO"}, http.StatusBadRequest, "fuente_tipo"},
		{"campo sin calculadora", grupoID, map[string]any{"etiqueta": "Idioma", "fuente_tipo": "CAMPO", "fuente_id": campoID}, http.StatusBadRequest, "calculadora_id"},
		{"campo sin fuente_id", grupoID, map[string]any{"etiqueta": "Idioma", "fuente_tipo": "CAMPO", "calculadora_id": e.calculadora}, http.StatusBadRequest, "fuente_id"},
		{"cotizador no asociado", grupoID, map[string]any{"etiqueta": "Idioma", "fuente_tipo": "CAMPO", "fuente_id": campoID, "calculadora_id": otro}, http.StatusBadRequest, "no está asociado"},
		{"campo de otro cotizador", grupoID, map[string]any{"etiqueta": "Idioma", "fuente_tipo": "CAMPO", "fuente_id": "NO-EXISTE", "calculadora_id": e.calculadora}, http.StatusBadRequest, "no existe"},
		{"dato base inexistente", grupoID, map[string]any{"etiqueta": "X", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "costo_total"}, http.StatusBadRequest, "no existe"},
		{"valor fijo demasiado largo", grupoID, map[string]any{"etiqueta": "X", "fuente_tipo": "VALOR_FIJO", "valor_fijo": strings.Repeat("a", 2001)}, http.StatusBadRequest, "2000"},
		// VALOR_FIJO vacío sí es válido: es como nacen los pre-poblados.
		{"valor fijo vacío", grupoID, map[string]any{"etiqueta": "Observaciones", "fuente_tipo": "VALOR_FIJO"}, http.StatusCreated, "campo_id"},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/campos",
				"/api/plantillas/bloques/"+caso.bloqueID+"/campos", h.Agregar, caso.body)
			if rec.Code != caso.codigo || !strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(caso.contenido)) {
				t.Fatalf("esperaba %d/%q, dio %d: %s", caso.codigo, caso.contenido, rec.Code, rec.Body.String())
			}
		})
	}

	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/campos/{campo_id}",
		"/api/plantillas/campos/00000000-0000-0000-0000-000000000000", h.Editar, map[string]any{"etiqueta": "X"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("editar inexistente: esperaba 404, dio %d", rec.Code)
	}
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/campos/{campo_id}",
		"/api/plantillas/campos/00000000-0000-0000-0000-000000000000", h.Editar, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("editar sin propiedades: esperaba 400, dio %d", rec.Code)
	}
}

// Crear una plantilla a partir de otra copia los campos comunes y el
// origen_filas; los atados a un cotizador puntual no (igual que las
// vinculaciones y columnas).
func TestPlantillas_CrearDesdeOtraCopiaCamposComunes(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	campoID := crearCampoTextoPrueba(t, e, "Idioma")
	baseID := crearPlantillaPrueba(t, e, "Plantilla base campos", nil)
	seccionID := crearSeccionPrueba(t, e, baseID, "Datos")
	grupoID := crearBloqueTipoPrueba(t, e, seccionID, "GRUPO_INFORMACION", nil)
	crearBloqueTipoPrueba(t, e, seccionID, "OPCIONES_PROPUESTA", nil)
	h := &PlantillaBloqueCamposHandler{DB: e.pool}
	agregarCampoBloquePrueba(t, h, grupoID, map[string]any{"etiqueta": "Idioma", "fuente_tipo": "CAMPO", "fuente_id": campoID, "calculadora_id": e.calculadora})
	agregarCampoBloquePrueba(t, h, grupoID, map[string]any{"etiqueta": "Cantidad", "fuente_tipo": "VALOR_FIJO", "valor_fijo": "3"})

	copiaID := crearPlantillaPrueba(t, e, "Plantilla copia campos", map[string]any{"crear_desde": baseID})
	bloques := detalleBloquesPrueba(t, e, copiaID)
	if len(bloques) != 2 {
		t.Fatalf("esperaba 2 bloques copiados, hay %d", len(bloques))
	}
	grupo, opciones := bloques[0], bloques[1]
	if len(grupo.Campos) != 1 || grupo.Campos[0].Etiqueta != "Cantidad" || *grupo.Campos[0].ValorFijo != "3" {
		t.Fatalf("solo el campo común debe copiarse: %+v", grupo.Campos)
	}
	if opciones.OrigenFilas != "OPCIONES_PROPUESTA" {
		t.Fatalf("origen_filas debe copiarse: %s", opciones.OrigenFilas)
	}
}
