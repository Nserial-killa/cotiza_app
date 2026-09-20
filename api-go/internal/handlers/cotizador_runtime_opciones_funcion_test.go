package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// fixtureFuncionCampoPorOpcion arma un cotizador con un CAMPO_CALCULADO
// (funcion_campo=TOTAL_PRECIO_OFERTA) anidado bajo Opciones de Propuesta —
// el caso que actualizarTotalesCotizacionVersion no manejaba antes de la
// migración 0026 (ver el comentario de esa función en cotizador_runtime.go).
// Mismo patrón de fixture que fixtureRuntimeOpciones (SQL directo, sin pasar
// por el Diseñador), pero con dos opciones con precios DISTINTOS para poder
// comprobar que cada una guarda su propio total, no el mismo repetido.
func fixtureFuncionCampoPorOpcion(t *testing.T) (fixture fixtureRuntime, padreID, precioID, calculadoID string) {
	t.Helper()
	pool := setupTestDB(t)
	cotizacionID, calculadoraID, _ := crearCotizacionPrueba(t, pool, "Borrador", "", "")
	sufijo := sufijoUnico()
	tabID := "TEST-FCO-TAB-" + sufijo
	padreID = "TEST-FCO-PADRE-" + sufijo
	precioID = "TEST-FCO-PRECIO-" + sufijo
	calculadoID = "TEST-FCO-CALC-" + sufijo
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id, calculadora_id, nombre, orden, activo) VALUES ($1,$2,'Planes',1,true)`, tabID, calculadoraID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
		VALUES ($1,$2,'OPCIONES_PROPUESTA','Planes',1,'{"cantidad_inicial":1}'::jsonb,true),
		       ($3,$2,'CAMPO','Precio',2,'{"tipo_campo":"MONEDA"}'::jsonb,true),
		       ($4,$2,'CAMPO_CALCULADO','Total oferta',3,$5,true)`,
		padreID, tabID, precioID, calculadoID,
		fmt.Sprintf(`{"tipo_formula":"SIMPLE","operacion":"SUMA","operandos":[%q,%q],"decimales":2}`, precioID, precioID)); err != nil {
		t.Fatal(err)
	}
	estructura := map[string]any{
		"calculadora_id": calculadoraID, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": tabID, "nombre": "Planes", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{map[string]any{
				"elemento_id": padreID, "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Opciones de propuesta",
				"columnas_ancho": 1, "orden": 1, "requerido": false, "configuracion": map[string]any{"cantidad_inicial": 1},
				"hijos": []any{
					map[string]any{
						"elemento_id": precioID, "tipo": "CAMPO", "etiqueta": "Precio", "columnas_ancho": 1,
						"orden": 2, "requerido": true, "configuracion": map[string]any{"tipo_campo": "MONEDA"},
					},
					map[string]any{
						"elemento_id": calculadoID, "tipo": "CAMPO_CALCULADO", "etiqueta": "Total oferta", "columnas_ancho": 1,
						"orden": 3, "funcion_campo": "TOTAL_PRECIO_OFERTA",
						"configuracion": map[string]any{"tipo_formula": "SIMPLE", "operacion": "SUMA", "operandos": []any{precioID, precioID}, "decimales": 2},
					},
				},
			}},
		}},
	}
	raw, err := json.Marshal(estructura)
	if err != nil {
		t.Fatal(err)
	}
	var compiladoID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO cotizadores_compilados (calculadora_id,version,estado,configuracion)
		VALUES ($1,1,'ACTIVA',$2) RETURNING compilado_id::text`, calculadoraID, string(raw)).Scan(&compiladoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE cotizaciones SET compilado_id_usado=NULL WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID)
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE compilado_id::text=$1`, compiladoID)
		pool.Exec(context.Background(), `DELETE FROM elementos_tab_cotizador WHERE elemento_id=ANY($1)`, []string{calculadoID, precioID, padreID})
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE tab_id=$1`, tabID)
	})
	return fixtureRuntime{Handler: &CotizadorRuntimeHandler{DB: pool}, CotizacionID: cotizacionID, CompiladoID: compiladoID}, padreID, precioID, calculadoID
}

// TestCotizadorRuntime_FuncionCampoPorOpcion_SeGuardaPorOpcionNoEnVersion
// verifica el arreglo del Paso 0 (migración 0026): antes, un CAMPO_CALCULADO
// con funcion_campo anidado bajo Opciones de Propuesta no actualizaba nada
// — ni cotizacion_versiones (recibía un mapa donde esperaba un escalar) ni
// ninguna otra parte. Ahora debe guardar un total DISTINTO por cada opción
// en cotizacion_opciones, sin tocar cotizacion_versiones.total_precio (que
// sigue siendo NULL/0: no hay un único total cuando el campo es por opción).
func TestCotizadorRuntime_FuncionCampoPorOpcion_SeGuardaPorOpcionNoEnVersion(t *testing.T) {
	fixture, padreID, precioID, _ := fixtureFuncionCampoPorOpcion(t)

	rec := getRuntime(t, fixture, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("obtener runtime: %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Estructura map[string]any `json:"estructura"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	padre := elementoPorIDEnEstructura(res.Estructura, padreID)
	opciones, _ := padre["opciones"].([]any)
	if len(opciones) != 1 {
		t.Fatalf("esperaba 1 opción inicial, hay %d", len(opciones))
	}
	opcion1, _ := opciones[0].(map[string]any)
	opcion1ID, _ := opcion1["opcion_id"].(string)

	agregar := postOpcionesRuntime(t, fixture, map[string]any{"version": 1, "elemento_padre_id": padreID, "accion": "AGREGAR", "nombre": "Premium"})
	if agregar.Code != http.StatusOK {
		t.Fatalf("agregar segunda opción: %d: %s", agregar.Code, agregar.Body.String())
	}
	var agregada struct {
		Opciones []cotizacionOpcion `json:"opciones"`
	}
	assertJSON(t, agregar.Body.Bytes(), &agregada)
	var opcion2ID string
	for _, o := range agregada.Opciones {
		if o.OpcionID != opcion1ID {
			opcion2ID = o.OpcionID
		}
	}
	if opcion2ID == "" {
		t.Fatal("no se encontró la segunda opción recién creada")
	}

	rec = postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores_por_opcion": []map[string]any{
		{"elemento_id": precioID, "opcion_id": opcion1ID, "valor": "100"},
		{"elemento_id": precioID, "opcion_id": opcion2ID, "valor": "500"},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("guardar valores por opción: %d: %s", rec.Code, rec.Body.String())
	}

	var totalOpcion1, totalOpcion2 *float64
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT total_precio FROM cotizacion_opciones WHERE opcion_id=$1`, opcion1ID).Scan(&totalOpcion1); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT total_precio FROM cotizacion_opciones WHERE opcion_id=$1`, opcion2ID).Scan(&totalOpcion2); err != nil {
		t.Fatal(err)
	}
	if totalOpcion1 == nil || totalOpcion2 == nil {
		t.Fatalf("los totales por opción no se guardaron: opcion1=%v opcion2=%v", totalOpcion1, totalOpcion2)
	}
	if *totalOpcion1 != 200 || *totalOpcion2 != 1000 {
		t.Fatalf("totales incorrectos o mezclados entre opciones: opcion1=%v (esperado 200) opcion2=%v (esperado 1000)", *totalOpcion1, *totalOpcion2)
	}

	// crearCotizacionPrueba siembra cotizacion_versiones con total_precio=1000
	// como dato de relleno (lo usan pruebas de otros archivos que no les
	// importa el valor exacto) — acá sirve justo para lo contrario: confirmar
	// que un funcion_campo por opción NO lo toca ni lo pisa (no cabe un solo
	// total en una fila que es por versión, no por escenario).
	var totalVersion float64
	if err := fixture.Handler.DB.QueryRow(context.Background(), `SELECT total_precio FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=1`, fixture.CotizacionID).Scan(&totalVersion); err != nil {
		t.Fatal(err)
	}
	if totalVersion != 1000 {
		t.Fatalf("un funcion_campo por opción no debe escribir en cotizacion_versiones (fila única por versión); el valor sembrado por crearCotizacionPrueba cambió a %v", totalVersion)
	}
}
