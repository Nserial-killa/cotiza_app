package handlers

// No borrar plantilla en uso (PLA-012, Ronda D, Crítica). Antes
// PlantillasHandler.Eliminar solo validaba estado='Borrador'. Ahora
// también bloquea si existe AL MENOS UNA cotización de un cotizador
// vinculado a la plantilla (plantilla_calculadoras) — sin importar si esa
// cotización puntual terminaría resolviendo a ESTA plantilla o a otra
// asociada al mismo cotizador; ver el comentario del handler para el porqué
// de ese criterio deliberadamente más protector.

import (
	"context"
	"net/http"
	"testing"
)

func TestPlantillas_EliminarBloqueaSiExisteCotizacionDelCotizadorVinculado(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla en uso", nil)

	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}

	ruta := "/api/plantillas/" + plantillaID
	rec := llamarPlantilla(t, http.MethodDelete, "/api/plantillas/{id}", ruta, e.plantillas.Eliminar, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PLA-012: esperaba 409 al borrar una plantilla en uso, dio %d: %s", rec.Code, rec.Body.String())
	}

	var existe bool
	if err := e.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, plantillaID).Scan(&existe); err != nil {
		t.Fatal(err)
	}
	if !existe {
		t.Fatal("la plantilla en uso fue eliminada a pesar del bloqueo")
	}
}

// TestPlantillas_EliminarBloqueaAunqueLaCotizacionResuelvaAOtraPlantilla
// confirma el criterio "más protector" explícito de PLA-012: dos
// plantillas Publicadas para el mismo cotizador, una cotización real, y el
// borrado de CUALQUIERA de las dos queda bloqueado — no solo la que
// plantilla_renderizador.go elegiría para mostrar esa cotización.
func TestPlantillas_EliminarBloqueaAunqueLaCotizacionResuelvaAOtraPlantilla(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaA := crearPlantillaPrueba(t, e, "Plantilla A", nil)
	plantillaB := crearPlantillaPrueba(t, e, "Plantilla B", nil)

	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}

	for _, plantillaID := range []string{plantillaA, plantillaB} {
		ruta := "/api/plantillas/" + plantillaID
		rec := llamarPlantilla(t, http.MethodDelete, "/api/plantillas/{id}", ruta, e.plantillas.Eliminar, nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("plantilla %s: esperaba 409, dio %d: %s", plantillaID, rec.Code, rec.Body.String())
		}
	}
}

// TestPlantillas_EliminarPermiteSiLaCotizacionEsDeOtroCotizador confirma
// que el bloqueo está bien acotado al cotizador vinculado: una cotización
// de un cotizador SIN relación con la plantilla no debe impedir borrarla.
func TestPlantillas_EliminarPermiteSiLaCotizacionEsDeOtroCotizador(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla sin cotizaciones propias", nil)

	otraCalculadoraID := "TEST-CALC-OTRA-" + sufijoUnico()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id,nombre_calculadora,estado) VALUES ($1,'Otro cotizador','Activo')`, otraCalculadoraID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, otraCalculadoraID)
	})

	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, otraCalculadoraID, cotizacionID); err != nil {
		t.Fatal(err)
	}

	ruta := "/api/plantillas/" + plantillaID
	rec := llamarPlantilla(t, http.MethodDelete, "/api/plantillas/{id}", ruta, e.plantillas.Eliminar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("una cotización de otro cotizador no debería bloquear el borrado: %d: %s", rec.Code, rec.Body.String())
	}
}
