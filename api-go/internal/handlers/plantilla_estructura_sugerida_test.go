package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// estructuraSugeridaEsperada es la referencia de la plantilla real de
// Exceltec escrita a mano (no derivada de estructuraSugeridaPlantilla): si
// alguien cambia la definición, esta prueba tiene que cambiar a la par.
var estructuraSugeridaEsperada = []struct {
	seccion string
	bloques []string
}{
	{"Portada", []string{"PORTADA"}},
	{"Información del cliente", []string{"DATOS_CLIENTE"}},
	{"Resumen ejecutivo", []string{"RESUMEN_EJECUTIVO"}},
	{"Situación actual", []string{"ENCABEZADO", "TEXTO"}},
	{"Solución propuesta", []string{"ENCABEZADO", "TEXTO"}},
	{"Alcance del proyecto", []string{"ENCABEZADO", "TEXTO"}},
	{"Metodología", []string{"ENCABEZADO", "TEXTO"}},
	{"Cronograma", []string{"ENCABEZADO", "TEXTO"}},
	{"Equipo de trabajo", []string{"ENCABEZADO", "TEXTO"}},
	{"Inversión", []string{"TABLA_INVERSION"}},
	{"Condiciones comerciales", []string{"CONDICIONES_COMERCIALES"}},
	{"Aceptación", []string{"FIRMA_ACEPTACION"}},
}

func aplicarEstructuraSugeridaPrueba(e *entornoPlantillasPrueba, t *testing.T, plantillaID string) (int, string) {
	t.Helper()
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/estructura-sugerida",
		"/api/plantillas/"+plantillaID+"/estructura-sugerida", e.estructura.AplicarEstructuraSugerida, nil)
	return rec.Code, rec.Body.String()
}

func TestPlantillaEstructuraSugerida_Crea12SeccionesY18Bloques(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla con estructura sugerida", nil)
	codigo, cuerpo := aplicarEstructuraSugeridaPrueba(e, t, plantillaID)
	if codigo != http.StatusCreated || !strings.Contains(cuerpo, `"secciones":12`) || !strings.Contains(cuerpo, `"bloques":18`) {
		t.Fatalf("aplicar: esperaba 201 con 12/18, dio %d: %s", codigo, cuerpo)
	}

	detalle, err := e.plantillas.consultarDetalle(context.Background(), plantillaID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detalle.Secciones) != len(estructuraSugeridaEsperada) {
		t.Fatalf("esperaba 12 secciones, hay %d", len(detalle.Secciones))
	}
	totalBloques := 0
	nombresInternos := map[string]bool{}
	for i, esperada := range estructuraSugeridaEsperada {
		s := detalle.Secciones[i]
		if s.Nombre != esperada.seccion || s.Orden != i {
			t.Fatalf("sección %d: esperaba %q (orden %d), dio %q (orden %d)", i, esperada.seccion, i, s.Nombre, s.Orden)
		}
		if len(s.Bloques) != len(esperada.bloques) {
			t.Fatalf("%s: esperaba %d bloques, hay %d", s.Nombre, len(esperada.bloques), len(s.Bloques))
		}
		for j, tipo := range esperada.bloques {
			b := s.Bloques[j]
			if b.TipoBloque != tipo || b.Orden != j {
				t.Fatalf("%s bloque %d: esperaba %s (orden %d), dio %s (orden %d)", s.Nombre, j, tipo, j, b.TipoBloque, b.Orden)
			}
			if nombresInternos[b.NombreInterno] {
				t.Fatalf("nombre_interno repetido: %s", b.NombreInterno)
			}
			nombresInternos[b.NombreInterno] = true
			// Sin vincular nada: eso es el paso 3.
			if len(b.Vinculaciones) != 0 || len(b.Condiciones) != 0 || len(b.Columnas) != 0 {
				t.Fatalf("%s/%s no debe nacer vinculado: %+v", s.Nombre, tipo, b)
			}
			if b.Contenido != nil && strings.ContainsAny(*b.Contenido, "[]") {
				t.Fatalf("%s/%s: el texto de ejemplo no debe usar corchetes (sintaxis de token): %q", s.Nombre, tipo, *b.Contenido)
			}
			if tipo == "ENCABEZADO" && (b.Titulo == nil || *b.Titulo != s.Nombre) {
				t.Fatalf("%s: el encabezado debe llevar el nombre de la sección: %v", s.Nombre, b.Titulo)
			}
			if tipo == "TEXTO" && (b.Contenido == nil || *b.Contenido == "") {
				t.Fatalf("%s: el texto debe traer una indicación de qué escribir", s.Nombre)
			}
			totalBloques++
		}
	}
	if totalBloques != 18 {
		t.Fatalf("esperaba 18 bloques en total, hay %d", totalBloques)
	}

	// Los bloques con campos nacen con los de fábrica, igual que a mano.
	porTipo := map[string]plantillaBloque{}
	for _, s := range detalle.Secciones {
		for _, b := range s.Bloques {
			porTipo[b.TipoBloque] = b
		}
	}
	for tipo, cantidad := range map[string]int{"PORTADA": 4, "DATOS_CLIENTE": 5, "CONDICIONES_COMERCIALES": 5, "FIRMA_ACEPTACION": 2} {
		if got := len(porTipo[tipo].Campos); got != cantidad {
			t.Fatalf("%s: esperaba %d campos pre-poblados, hay %d", tipo, cantidad, got)
		}
	}
	if c := porTipo["FIRMA_ACEPTACION"].Contenido; c == nil || !strings.Contains(*c, "acepta") {
		t.Fatalf("la firma debe traer su texto de aceptación: %v", c)
	}

	// Punto de partida, no molde: se edita y se borra como cualquier otra.
	textoID := detalle.Secciones[3].Bloques[1].BloqueID
	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/"+textoID,
		e.estructura.EditarBloque, map[string]any{"contenido": "El cliente factura a mano."})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar un bloque sugerido: %d: %s", rec.Code, rec.Body.String())
	}
	seccionID := detalle.Secciones[8].SeccionID
	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/secciones/{seccion_id}", "/api/plantillas/secciones/"+seccionID,
		e.estructura.EliminarSeccion, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar una sección sugerida: %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlantillaEstructuraSugerida_RechazaSiYaHaySecciones(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	contar := func(plantillaID string) (secciones, bloques int) {
		t.Helper()
		if err := e.pool.QueryRow(context.Background(), `
			SELECT (SELECT count(*) FROM plantilla_secciones WHERE plantilla_id::text=$1),
			       (SELECT count(*) FROM plantilla_bloques pb JOIN plantilla_secciones ps USING(seccion_id) WHERE ps.plantilla_id::text=$1)`,
			plantillaID).Scan(&secciones, &bloques); err != nil {
			t.Fatal(err)
		}
		return
	}

	// Aplicada dos veces: la segunda se rechaza y no duplica nada.
	sugerida := crearPlantillaPrueba(t, e, "Plantilla sugerida dos veces", nil)
	if codigo, cuerpo := aplicarEstructuraSugeridaPrueba(e, t, sugerida); codigo != http.StatusCreated {
		t.Fatalf("primera vez: %d: %s", codigo, cuerpo)
	}
	codigo, cuerpo := aplicarEstructuraSugeridaPrueba(e, t, sugerida)
	if codigo != http.StatusConflict || !strings.Contains(cuerpo, "elimine la estructura actual") {
		t.Fatalf("segunda vez: esperaba 409 pidiendo vaciar la estructura, dio %d: %s", codigo, cuerpo)
	}
	if s, b := contar(sugerida); s != 12 || b != 18 {
		t.Fatalf("el rechazo no debe tocar nada: %d secciones, %d bloques", s, b)
	}

	// Una plantilla armada a mano (aunque sea una sección vacía) no se pisa.
	aMano := crearPlantillaPrueba(t, e, "Plantilla armada a mano", nil)
	seccionID := crearSeccionPrueba(t, e, aMano, "Mi sección")
	crearBloqueTipoPrueba(t, e, seccionID, "TEXTO", nil)
	if codigo, cuerpo := aplicarEstructuraSugeridaPrueba(e, t, aMano); codigo != http.StatusConflict {
		t.Fatalf("plantilla con secciones: esperaba 409, dio %d: %s", codigo, cuerpo)
	}
	if s, b := contar(aMano); s != 1 || b != 1 {
		t.Fatalf("la plantilla armada a mano cambió: %d secciones, %d bloques", s, b)
	}

	// Vaciada la estructura, se puede volver a generar.
	rec := llamarPlantilla(t, http.MethodDelete, "/api/plantillas/secciones/{seccion_id}", "/api/plantillas/secciones/"+seccionID,
		e.estructura.EliminarSeccion, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("vaciar: %d", rec.Code)
	}
	if codigo, cuerpo := aplicarEstructuraSugeridaPrueba(e, t, aMano); codigo != http.StatusCreated {
		t.Fatalf("tras vaciar: esperaba 201, dio %d: %s", codigo, cuerpo)
	}

	if codigo, _ := aplicarEstructuraSugeridaPrueba(e, t, "00000000-0000-0000-0000-000000000000"); codigo != http.StatusNotFound {
		t.Fatalf("plantilla inexistente: esperaba 404, dio %d", codigo)
	}
}

// Dos clics seguidos (o dos pestañas) no deben terminar con 24 secciones:
// la plantilla se bloquea mientras se aplica.
func TestPlantillaEstructuraSugerida_ConcurrenteSoloUnaGana(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla sugerida concurrente", nil)
	var wg sync.WaitGroup
	codigos := make([]int, 4)
	for i := range codigos {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codigos[i], _ = aplicarEstructuraSugeridaPrueba(e, t, plantillaID)
		}(i)
	}
	wg.Wait()
	creadas := 0
	for _, c := range codigos {
		switch c {
		case http.StatusCreated:
			creadas++
		case http.StatusConflict:
		default:
			t.Fatalf("código inesperado: %v", codigos)
		}
	}
	var secciones int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM plantilla_secciones WHERE plantilla_id::text=$1`, plantillaID).Scan(&secciones); err != nil {
		t.Fatal(err)
	}
	if creadas != 1 || secciones != 12 {
		t.Fatalf("esperaba un solo 201 y 12 secciones, dio %v y %d secciones", codigos, secciones)
	}
}
