package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func crearBloqueTablaPrueba(t *testing.T, e *entornoPlantillasPrueba, seccionID, nombre string) string {
	t.Helper()
	ruta := "/api/plantillas/secciones/" + seccionID + "/bloques"
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques", ruta, e.estructura.CrearBloque, map[string]any{
		"tipo_bloque": "TABLA_INVERSION", "nombre_interno": nombre, "columna": 0,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear bloque tabla: esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	id, _ := respuestaPlantilla(t, rec)["bloque_id"].(string)
	return id
}

func TestPlantillaTablaColumnas_CRUDYOrden(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla tabla", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Inversión")
	bloqueID := crearBloqueTablaPrueba(t, e, seccionID, "tabla_inversion")
	campoID := crearCampoTextoPrueba(t, e, "PRECIO")
	handler := &PlantillaTablaColumnasHandler{DB: e.pool}

	rutaAgregar := "/api/plantillas/bloques/" + bloqueID + "/columnas"
	col1 := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", rutaAgregar, handler.Agregar, map[string]any{
		"calculadora_id": e.calculadora, "titulo": "Concepto", "fuente_tipo": "nombre_escenario",
	})
	if col1.Code != http.StatusCreated {
		t.Fatalf("agregar columna virtual: %d: %s", col1.Code, col1.Body.String())
	}
	col1ID, _ := respuestaPlantilla(t, col1)["columna_id"].(string)

	col2 := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", rutaAgregar, handler.Agregar, map[string]any{
		"calculadora_id": e.calculadora, "titulo": "Precio", "fuente_tipo": "CAMPO", "fuente_id": campoID,
	})
	if col2.Code != http.StatusCreated {
		t.Fatalf("agregar columna campo: %d: %s", col2.Code, col2.Body.String())
	}
	col2ID, _ := respuestaPlantilla(t, col2)["columna_id"].(string)

	var total int
	e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plantilla_tabla_columnas WHERE bloque_id::text=$1`, bloqueID).Scan(&total)
	if total != 2 {
		t.Fatalf("esperaba 2 columnas, hay %d", total)
	}

	// Editar: cambiar el título de la primera.
	rutaEditar := "/api/plantillas/columnas/{columna_id}"
	rec := llamarPlantilla(t, http.MethodPatch, rutaEditar, "/api/plantillas/columnas/"+col1ID, handler.Editar, map[string]any{"titulo": "Escenario"})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar columna: %d: %s", rec.Code, rec.Body.String())
	}
	var tituloActualizado string
	e.pool.QueryRow(context.Background(), `SELECT titulo FROM plantilla_tabla_columnas WHERE columna_id::text=$1`, col1ID).Scan(&tituloActualizado)
	if tituloActualizado != "Escenario" {
		t.Fatalf("título no se actualizó: %s", tituloActualizado)
	}

	// Reordenar: invertir el orden.
	rutaOrden := "/api/plantillas/bloques/" + bloqueID + "/columnas/orden?calculadora_id=" + e.calculadora
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas/orden", rutaOrden, handler.Ordenar, []string{col2ID, col1ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("reordenar columnas: %d: %s", rec.Code, rec.Body.String())
	}
	var ordenCol2, ordenCol1 int
	e.pool.QueryRow(context.Background(), `SELECT orden FROM plantilla_tabla_columnas WHERE columna_id::text=$1`, col2ID).Scan(&ordenCol2)
	e.pool.QueryRow(context.Background(), `SELECT orden FROM plantilla_tabla_columnas WHERE columna_id::text=$1`, col1ID).Scan(&ordenCol1)
	if ordenCol2 >= ordenCol1 {
		t.Fatalf("no se aplicó el nuevo orden: col2=%d col1=%d", ordenCol2, ordenCol1)
	}

	// Eliminar.
	rec = llamarPlantilla(t, http.MethodDelete, rutaEditar, "/api/plantillas/columnas/"+col2ID, handler.Eliminar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar columna: %d: %s", rec.Code, rec.Body.String())
	}
	e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plantilla_tabla_columnas WHERE bloque_id::text=$1`, bloqueID).Scan(&total)
	if total != 1 {
		t.Fatalf("esperaba 1 columna tras eliminar, hay %d", total)
	}
}

func TestPlantillaTablaColumnas_Validaciones(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla tabla validación", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Inversión")
	bloqueTexto := crearBloquePrueba(t, e, seccionID, "texto")
	bloqueTabla := crearBloqueTablaPrueba(t, e, seccionID, "tabla")
	handler := &PlantillaTablaColumnasHandler{DB: e.pool}

	for _, caso := range []struct {
		nombre    string
		bloqueID  string
		body      map[string]any
		codigo    int
		contenido string
	}{
		{"bloque no es tabla", bloqueTexto, map[string]any{"calculadora_id": e.calculadora, "titulo": "X", "fuente_tipo": "NOMBRE_ESCENARIO"}, http.StatusBadRequest, "TABLA_INVERSION"},
		{"campo sin fuente_id", bloqueTabla, map[string]any{"calculadora_id": e.calculadora, "titulo": "Precio", "fuente_tipo": "CAMPO"}, http.StatusBadRequest, "fuente_id"},
		{"fuente_tipo inválida", bloqueTabla, map[string]any{"calculadora_id": e.calculadora, "titulo": "X", "fuente_tipo": "RARO"}, http.StatusBadRequest, "fuente_tipo"},
		{"sin título", bloqueTabla, map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "NOMBRE_ESCENARIO"}, http.StatusBadRequest, "titulo"},
		{"campo inexistente", bloqueTabla, map[string]any{"calculadora_id": e.calculadora, "titulo": "Precio", "fuente_tipo": "CAMPO", "fuente_id": "NO-EXISTE"}, http.StatusBadRequest, "no existe"},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", "/api/plantillas/bloques/"+caso.bloqueID+"/columnas", handler.Agregar, caso.body)
			if rec.Code != caso.codigo || !strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(caso.contenido)) {
				t.Fatalf("esperaba %d/%q, dio %d: %s", caso.codigo, caso.contenido, rec.Code, rec.Body.String())
			}
		})
	}
}
