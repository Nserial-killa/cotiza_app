package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixtureRenderizadorPlantilla arma el caso ISA Custom acotado: un cotizador
// con un CAMPO booleano (USA_TELEFONIA) y un OPCIONES_PROPUESTA con un CAMPO
// "Precio" anidado, una Plantilla Publicada con dos bloques (un TEXTO
// condicionado a USA_TELEFONIA, con un token [USA_TELEFONIA] en su
// contenido, y una TABLA_INVERSION con origen_filas=OPCIONES_PROPUESTA), y
// una cotización real con 3 escenarios de precios distintos. Mismo patrón de
// SQL directo que fixtureRuntimeOpciones/fixtureFuncionCampoPorOpcion: la
// estructura compilada se inserta a mano porque estas pruebas no ejercitan
// el compilador, solo el renderer.
type fixtureRenderizadorPlantilla struct {
	pool                *pgxpool.Pool
	cotizacionID        string
	campoUsaTelefoniaID string
	padreID             string
	precioID            string
	opciones            []string // IDs en orden: Starter, Premium, Enterprise (recomendada).
}

func crearFixtureRenderizadorPlantilla(t *testing.T) fixtureRenderizadorPlantilla {
	t.Helper()
	e := nuevoEntornoPlantillas(t)
	campoUsaTelefoniaID := crearCampoTextoPrueba(t, e, "USA_TELEFONIA")

	sufijo := sufijoUnico()
	tabID := "TEST-REND-TAB-" + sufijo
	padreID := "TEST-REND-PADRE-" + sufijo
	precioID := "TEST-REND-PRECIO-" + sufijo
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo) VALUES ($1,$2,'Planes',2,true)`,
		tabID, e.calculadora); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
		VALUES ($1,$2,'OPCIONES_PROPUESTA','Planes',1,'{}'::jsonb,true),
		       ($3,$2,'CAMPO','Precio',2,'{"tipo_campo":"MONEDA"}'::jsonb,true)`,
		padreID, tabID, precioID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE tab_id=$1`, tabID) })

	estructura := map[string]any{
		"calculadora_id": e.calculadora, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": tabID, "nombre": "Planes", "alcance": "PROPIO", "orden": 2,
			"elementos": []any{
				map[string]any{
					// nombre_interno es lo que resuelve el token [USA_TELEFONIA]
					// del bloque de texto (mismo criterio que Fórmula Avanzada,
					// ver nombreInternoElemento) — nunca la etiqueta ni el ID.
					"elemento_id": campoUsaTelefoniaID, "tipo": "CAMPO", "etiqueta": "Usa telefonía",
					"columnas_ancho": 1, "orden": 0, "requerido": false,
					"configuracion": map[string]any{"tipo_campo": "TEXTO", "nombre_interno": "USA_TELEFONIA"},
				},
				map[string]any{
					"elemento_id": padreID, "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Opciones de propuesta",
					"columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": map[string]any{},
					"hijos": []any{map[string]any{
						"elemento_id": precioID, "tipo": "CAMPO", "etiqueta": "Precio", "columnas_ancho": 1,
						"orden": 2, "requerido": true, "configuracion": map[string]any{"tipo_campo": "MONEDA"},
					}},
				},
			},
		}},
	}
	raw, err := json.Marshal(estructura)
	if err != nil {
		t.Fatal(err)
	}
	var compiladoID string
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO cotizadores_compilados (calculadora_id,version,estado,configuracion)
		VALUES ($1,1,'ACTIVA',$2) RETURNING compilado_id::text`, e.calculadora, string(raw)).Scan(&compiladoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
	})

	plantillaID := crearPlantillaPrueba(t, e, "Plantilla ISA renderer", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Resumen")
	bloqueTextoID := crearBloquePrueba(t, e, seccionID, "resumen_telefonia")
	if _, err := e.pool.Exec(ctx, `UPDATE plantilla_bloques SET contenido=$2 WHERE bloque_id::text=$1`,
		bloqueTextoID, "La solución contempla telefonía: [USA_TELEFONIA]."); err != nil {
		t.Fatal(err)
	}
	condiciones := &PlantillaCondicionesHandler{DB: e.pool}
	recCond := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/condicion",
		"/api/plantillas/bloques/"+bloqueTextoID+"/condicion", condiciones.Guardar, map[string]any{
			"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoUsaTelefoniaID,
			"operador": "IGUAL_A", "valor_comparacion": "Sí",
		})
	if recCond.Code != http.StatusOK {
		t.Fatalf("condición del bloque de texto: %d: %s", recCond.Code, recCond.Body.String())
	}

	bloqueTablaID := crearBloqueTablaPrueba(t, e, seccionID, "tabla_inversion")
	if _, err := e.pool.Exec(ctx, `UPDATE plantilla_bloques SET origen_filas='OPCIONES_PROPUESTA' WHERE bloque_id::text=$1`, bloqueTablaID); err != nil {
		t.Fatal(err)
	}
	columnas := &PlantillaTablaColumnasHandler{DB: e.pool}
	for _, columna := range []map[string]any{
		{"calculadora_id": e.calculadora, "titulo": "Concepto", "fuente_tipo": "NOMBRE_ESCENARIO"},
		{"calculadora_id": e.calculadora, "titulo": "Precio", "fuente_tipo": "CAMPO", "fuente_id": precioID},
		{"calculadora_id": e.calculadora, "titulo": "Recomendada", "fuente_tipo": "ES_RECOMENDADA"},
	} {
		rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas",
			"/api/plantillas/bloques/"+bloqueTablaID+"/columnas", columnas.Agregar, columna)
		if rec.Code != http.StatusCreated {
			t.Fatalf("agregar columna %v: %d: %s", columna["titulo"], rec.Code, rec.Body.String())
		}
	}
	if _, err := e.pool.Exec(ctx, `UPDATE plantillas SET estado='Publicada' WHERE plantilla_id::text=$1`, plantillaID); err != nil {
		t.Fatal(err)
	}

	// tipo_propuesta debe coincidir con el que crearPlantillaPrueba asocia a
	// la plantilla ("COMERCIAL") — la selección de plantilla del renderer
	// exige esa coincidencia cuando la plantilla restringe tipos.
	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(ctx, `UPDATE cotizaciones SET calculadora_id=$1, tipo_propuesta='COMERCIAL' WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}

	nombres := []struct {
		nombre      string
		precio      string
		recomendada bool
	}{{"Starter", "100.00", false}, {"Premium", "200.00", false}, {"Enterprise", "300.00", true}}
	opciones := make([]string, 0, 3)
	for i, o := range nombres {
		var opcionID string
		if err := e.pool.QueryRow(ctx, `
			INSERT INTO cotizacion_opciones (cotizacion_id,numero_version,elemento_padre_id,nombre,es_recomendada,orden)
			VALUES ($1,1,$2,$3,$4,$5) RETURNING opcion_id`,
			cotizacionID, padreID, o.nombre, o.recomendada, i+1).Scan(&opcionID); err != nil {
			t.Fatal(err)
		}
		opciones = append(opciones, opcionID)
		valorJSON, _ := json.Marshal(o.precio)
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO cotizacion_valores (cotizacion_id,version,elemento_id,opcion_id,valor)
			VALUES ($1,1,$2,$3,$4)`, cotizacionID, precioID, opcionID, string(valorJSON)); err != nil {
			t.Fatal(err)
		}
	}

	return fixtureRenderizadorPlantilla{
		pool: e.pool, cotizacionID: cotizacionID, campoUsaTelefoniaID: campoUsaTelefoniaID,
		padreID: padreID, precioID: precioID, opciones: opciones,
	}
}

func (f fixtureRenderizadorPlantilla) fijarUsaTelefonia(t *testing.T, valor string) {
	t.Helper()
	valorJSON, _ := json.Marshal(valor)
	if _, err := f.pool.Exec(context.Background(), `
		INSERT INTO cotizacion_valores (cotizacion_id,version,elemento_id,opcion_id,valor)
		VALUES ($1,1,$2,NULL,$3)
		ON CONFLICT (cotizacion_id,version,elemento_id) WHERE opcion_id IS NULL DO UPDATE SET valor=EXCLUDED.valor`,
		f.cotizacionID, f.campoUsaTelefoniaID, string(valorJSON)); err != nil {
		t.Fatal(err)
	}
}

func bloquePorTipo(secciones []seccionRenderizada, tipo string) *bloqueRenderizado {
	for _, seccion := range secciones {
		for i := range seccion.Bloques {
			if seccion.Bloques[i].TipoBloque == tipo {
				return &seccion.Bloques[i]
			}
		}
	}
	return nil
}

func TestPlantillaRenderizador_CondicionMuestraYOcultaElBloque(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)

	f.fijarUsaTelefonia(t, "Sí")
	plantilla, err := renderizarPlantillaCotizacion(context.Background(), f.pool, f.cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plantilla == nil {
		t.Fatal("esperaba encontrar la plantilla publicada")
	}
	bloque := bloquePorTipo(plantilla.Secciones, "TEXTO")
	if bloque == nil {
		t.Fatal("el bloque de texto debería mostrarse con USA_TELEFONIA=Sí")
	}
	if strings.Contains(bloque.Contenido, "[") || !strings.Contains(bloque.Contenido, "Sí") {
		t.Fatalf("CP-12: no debe quedar ningún placeholder sin resolver: %q", bloque.Contenido)
	}

	f.fijarUsaTelefonia(t, "No")
	plantilla, err = renderizarPlantillaCotizacion(context.Background(), f.pool, f.cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bloquePorTipo(plantilla.Secciones, "TEXTO") != nil {
		t.Fatal("el bloque de texto no debería mostrarse con USA_TELEFONIA=No")
	}
}

func TestPlantillaRenderizador_TablaGeneraUnaFilaPorOpcionConSusPropiosValores(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "No") // Irrelevante para la tabla; solo oculta el bloque de texto.

	plantilla, err := renderizarPlantillaCotizacion(context.Background(), f.pool, f.cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	bloque := bloquePorTipo(plantilla.Secciones, "TABLA_INVERSION")
	if bloque == nil {
		t.Fatal("la tabla de inversión debería estar siempre visible (sin condición)")
	}
	if len(bloque.Filas) != 3 {
		t.Fatalf("esperaba exactamente 3 filas (una por opción), hay %d: %+v", len(bloque.Filas), bloque.Filas)
	}
	if len(bloque.Columnas) != 3 || bloque.Columnas[0] != "Concepto" || bloque.Columnas[1] != "Precio" || bloque.Columnas[2] != "Recomendada" {
		t.Fatalf("columnas inesperadas: %+v", bloque.Columnas)
	}

	nombres := map[string]bool{}
	precios := map[string]bool{}
	var recomendadas int
	for _, fila := range bloque.Filas {
		if len(fila) != 3 {
			t.Fatalf("fila con cantidad de columnas incorrecta: %+v", fila)
		}
		nombre := fmt.Sprint(fila[0])
		precio := fmt.Sprint(fila[1])
		nombres[nombre] = true
		precios[precio] = true
		if recomendada, _ := fila[2].(bool); recomendada {
			recomendadas++
			if nombre != "Enterprise" {
				t.Fatalf("la fila recomendada no es la esperada: %s", nombre)
			}
		}
	}
	if len(nombres) != 3 {
		t.Fatalf("las 3 filas deberían tener nombres de escenario distintos, dio: %v", nombres)
	}
	if len(precios) != 3 {
		t.Fatalf("CADA opción debe traer SU PROPIO precio, no el mismo repetido en las 3 filas: %v", precios)
	}
	if recomendadas != 1 {
		t.Fatalf("esperaba exactamente una fila recomendada, hubo %d", recomendadas)
	}
}

func TestPlantillaRenderizador_SinPlantillaPublicadaDevuelveNil(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}
	plantilla, err := renderizarPlantillaCotizacion(context.Background(), e.pool, cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plantilla != nil {
		t.Fatalf("sin plantilla publicada asociada al cotizador, esperaba nil, dio %+v", plantilla)
	}
}

func TestEnlacesPublicos_VerCotizacionIncluyePlantillaRenderizada(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "Sí")

	enlaces := &EnlacesPublicosHandler{DB: f.pool}
	router := chi.NewRouter()
	router.Post("/api/cotizaciones/{id}/enlace", enlaces.GenerarEnlace)
	router.Get("/api/publico/cotizacion/{token}", enlaces.VerCotizacion)

	recGenerar := postCatalogos(t, func(w http.ResponseWriter, r *http.Request) { router.ServeHTTP(w, r) },
		"/api/cotizaciones/"+f.cotizacionID+"/enlace", map[string]any{"version": 1})
	if recGenerar.Code != http.StatusOK {
		t.Fatalf("generar enlace: %d: %s", recGenerar.Code, recGenerar.Body.String())
	}
	var generado struct {
		Token string `json:"token"`
	}
	assertJSON(t, recGenerar.Body.Bytes(), &generado)
	if generado.Token == "" {
		t.Fatal("no se generó token")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/publico/cotizacion/"+generado.Token, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ver cotización pública: %d: %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		OK        bool                  `json:"ok"`
		Plantilla *plantillaRenderizada `json:"plantilla"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	if !respuesta.OK || respuesta.Plantilla == nil {
		t.Fatalf("el enlace público debería traer la plantilla renderizada: %s", rec.Body.String())
	}
	if bloquePorTipo(respuesta.Plantilla.Secciones, "TABLA_INVERSION") == nil {
		t.Fatal("la tabla de inversión no llegó en la respuesta pública")
	}
}
