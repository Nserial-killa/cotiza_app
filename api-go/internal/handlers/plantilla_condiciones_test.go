package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// crearCampoTextoPrueba inserta un CAMPO de texto activo directamente en la
// calculadora del entorno de plantillas — no hace falta pasar por el
// Diseñador completo para validar la fuente de una condición.
func crearCampoTextoPrueba(t *testing.T, e *entornoPlantillasPrueba, nombre string) string {
	t.Helper()
	sufijo := sufijoUnico()
	tabID := "TEST-TPL-TAB-" + sufijo
	elementoID := "TEST-TPL-EL-" + sufijo
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo) VALUES ($1,$2,'Datos',1,true)`,
		tabID, e.calculadora); err != nil {
		t.Fatalf("no se pudo crear el tab de prueba: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
		VALUES ($1,$2,'CAMPO',$3,1,'{"tipo_campo":"TEXTO"}'::jsonb,true)`, elementoID, tabID, nombre); err != nil {
		t.Fatalf("no se pudo crear el campo de prueba: %v", err)
	}
	t.Cleanup(func() {
		e.pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE tab_id=$1`, tabID)
	})
	return elementoID
}

func TestPlantillaCondiciones_GuardarEditarYEliminar(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla condiciones", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Alcance")
	bloqueID := crearBloquePrueba(t, e, seccionID, "resumen")
	campoID := crearCampoTextoPrueba(t, e, "USA_TELEFONIA")
	handler := &PlantillaCondicionesHandler{DB: e.pool}

	ruta := "/api/plantillas/bloques/" + bloqueID + "/condicion"
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/condicion", ruta, handler.Guardar, map[string]any{
		"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoID,
		"operador": "igual_a", "valor_comparacion": "Sí",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar condición: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var operador, valorComparacion string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT operador, valor_comparacion FROM plantilla_bloque_condiciones WHERE bloque_id::text=$1`, bloqueID).
		Scan(&operador, &valorComparacion); err != nil {
		t.Fatal(err)
	}
	if operador != "IGUAL_A" || valorComparacion != "Sí" {
		t.Fatalf("condición persistida incorrecta: operador=%s valor=%s", operador, valorComparacion)
	}

	// Upsert: repetir con otro operador reemplaza, no duplica.
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/condicion", ruta, handler.Guardar, map[string]any{
		"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoID, "operador": "NO_ESTA_VACIO",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("actualizar condición: %d: %s", rec.Code, rec.Body.String())
	}
	var total int
	e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plantilla_bloque_condiciones WHERE bloque_id::text=$1`, bloqueID).Scan(&total)
	if total != 1 {
		t.Fatalf("esperaba una sola fila (upsert), hay %d", total)
	}
	var valorComparacionNula *string
	e.pool.QueryRow(context.Background(), `SELECT valor_comparacion FROM plantilla_bloque_condiciones WHERE bloque_id::text=$1`, bloqueID).Scan(&valorComparacionNula)
	if valorComparacionNula != nil {
		t.Fatalf("ESTA_VACIO/NO_ESTA_VACIO no debe guardar valor_comparacion, dio %v", *valorComparacionNula)
	}

	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/bloques/{bloque_id}/condicion", ruta+"?calculadora_id="+e.calculadora, handler.Eliminar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar condición: %d: %s", rec.Code, rec.Body.String())
	}
	e.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM plantilla_bloque_condiciones WHERE bloque_id::text=$1`, bloqueID).Scan(&total)
	if total != 0 {
		t.Fatal("la condición no se eliminó")
	}
}

func TestPlantillaCondiciones_Validaciones(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla condiciones validación", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Alcance")
	bloqueID := crearBloquePrueba(t, e, seccionID, "resumen")
	campoID := crearCampoTextoPrueba(t, e, "USA_TELEFONIA")
	handler := &PlantillaCondicionesHandler{DB: e.pool}
	ruta := "/api/plantillas/bloques/{bloque_id}/condicion"

	for _, caso := range []struct {
		nombre string
		body   map[string]any
		error  string
	}{
		{"operador inválido", map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoID, "operador": "CONTIENE"}, "operador"},
		{"falta valor_comparacion", map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoID, "operador": "IGUAL_A"}, "valor_comparacion"},
		{"fuente_tipo inválido", map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "OTRA_COSA", "fuente_id": campoID, "operador": "ESTA_VACIO"}, "fuente_tipo"},
		{"campo inexistente", map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": "NO-EXISTE", "operador": "ESTA_VACIO"}, "no existe"},
		{"dato base inexistente", map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "COTIZACION_BASE", "fuente_id": "no_existe", "operador": "ESTA_VACIO"}, "no existe"},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			rec := llamarPlantilla(t, http.MethodPost, ruta, "/api/plantillas/bloques/"+bloqueID+"/condicion", handler.Guardar, caso.body)
			if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(rec.Body.String()), caso.error) {
				t.Fatalf("esperaba 400/%q, dio %d: %s", caso.error, rec.Code, rec.Body.String())
			}
		})
	}

	otraCalculadora := "TEST-CALC-TPL-OTRA-" + sufijoUnico()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id,nombre_calculadora,estado) VALUES ($1,'Otra','Activo')`, otraCalculadora); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, otraCalculadora)
	})
	rec := llamarPlantilla(t, http.MethodPost, ruta, "/api/plantillas/bloques/"+bloqueID+"/condicion", handler.Guardar, map[string]any{
		"calculadora_id": otraCalculadora, "fuente_tipo": "CAMPO", "fuente_id": campoID, "operador": "ESTA_VACIO",
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "asociado") {
		t.Fatalf("esperaba 400/asociado, dio %d: %s", rec.Code, rec.Body.String())
	}
}
