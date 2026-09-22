package handlers

// Columnas totalizables explícitas en Tabla (TAB-003, CTZ-TBL-002, Ronda
// D). Antes el total de una Tabla siempre inferia la primera columna
// NUMERO/MONEDA (calculo.go); ahora el diseñador puede marcar una o varias
// columnas como Totalizable — sin ninguna marcada, se sigue infiriendo (no
// romper cotizadores existentes) pero Validar avisa con el código
// TABLA_TOTALES_COLUMNAS_INFERIDAS, nombrando la tabla.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestTablaColumnas_TotalizableSePersisteAlCrearYEditar(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-TOT-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Tabla", "activo": true,
	})
	tablaID := crearElementoTablaPrueba(t, tabsHandler, tabID)
	columnasHandler := &TablaColumnasHandler{DB: tabsHandler.DB}

	rec, res := crearColumnaTabla(t, columnasHandler, tablaID, map[string]any{
		"origen": "PROPIA", "tipo_dato": "NUMERO", "etiqueta": "Cantidad", "orden": 1, "totalizable": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear columna totalizable: %d: %v", rec.Code, res)
	}
	columnaID := fmt.Sprint(res["columna_id"])

	var totalizable bool
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT totalizable FROM tabla_columnas WHERE columna_id::text=$1`, columnaID).Scan(&totalizable); err != nil || !totalizable {
		t.Fatalf("totalizable no se guardó al crear: valor=%v err=%v", totalizable, err)
	}

	if rec := patchColumnaTabla(t, columnasHandler, columnaID, map[string]any{"totalizable": false}); rec.Code != http.StatusOK {
		t.Fatalf("editar totalizable: %d: %s", rec.Code, rec.Body.String())
	}
	if err := tabsHandler.DB.QueryRow(context.Background(), `SELECT totalizable FROM tabla_columnas WHERE columna_id::text=$1`, columnaID).Scan(&totalizable); err != nil || totalizable {
		t.Fatalf("totalizable no se actualizó a false: valor=%v err=%v", totalizable, err)
	}
}

// fixtureTablaTotalizable arma una TABLA con dos columnas propias:
// "Cantidad" (NUMERO, primera de las dos) y "Precio" (MONEDA, segunda) —
// para distinguir claramente "se infirió la primera numérica" de "se usó
// la columna marcada", nunca deben coincidir en el mismo total.
type fixtureTablaTotalizable struct {
	f                          fixtureFormulaAvanzada
	tabla                      string
	colCantidadID, colPrecioID string
}

func crearFixtureTablaTotalizable(t *testing.T) fixtureTablaTotalizable {
	t.Helper()
	f := crearFixtureFormulaAvanzada(t)
	tabla := f.crear(t, "TABLA", "Consultoría", map[string]any{"permitir_agregar_filas": true, "permitir_eliminar_filas": true}, nil)
	colHandler := &TablaColumnasHandler{DB: f.handler.DB}
	_, colCantidad := crearColumnaTabla(t, colHandler, tabla, map[string]any{"origen": "PROPIA", "tipo_dato": "NUMERO", "etiqueta": "Cantidad", "orden": 1})
	_, colPrecio := crearColumnaTabla(t, colHandler, tabla, map[string]any{"origen": "PROPIA", "tipo_dato": "MONEDA", "etiqueta": "Precio", "orden": 2})
	return fixtureTablaTotalizable{
		f: f, tabla: tabla,
		colCantidadID: fmt.Sprint(colCantidad["columna_id"]),
		colPrecioID:   fmt.Sprint(colPrecio["columna_id"]),
	}
}

func (ft fixtureTablaTotalizable) marcarTotalizable(t *testing.T, columnaID string, totalizable bool) {
	t.Helper()
	colHandler := &TablaColumnasHandler{DB: ft.f.handler.DB}
	rec := patchColumnaTabla(t, colHandler, columnaID, map[string]any{"totalizable": totalizable})
	if rec.Code != http.StatusOK {
		t.Fatalf("marcar totalizable=%v en %s: %d: %s", totalizable, columnaID, rec.Code, rec.Body.String())
	}
}

func (ft fixtureTablaTotalizable) totalPrecioGuardado(t *testing.T) float64 {
	t.Helper()
	exigirGuardadoSalida(t, mapearSalidaPrueba(t, ft.f, "TOTAL_PRECIO", "TOTAL_TABLA", ft.tabla, "", true))
	rt := ft.f.runtime(t)
	exigirGuardadoSalida(t, postValoresRuntime(t, rt, map[string]any{"version": 1, "valores": map[string]any{ft.tabla: map[string]any{"filas": []any{
		map[string]any{ft.colCantidadID: 3, ft.colPrecioID: 100},
		map[string]any{ft.colCantidadID: 5, ft.colPrecioID: 200},
	}}}}))
	return leerSalidaNumeroPrueba(t, rt, 1, "TOTAL_PRECIO")
}

func TestTablaTotalizable_SinMarcarInfierePrimeraColumnaNumerica(t *testing.T) {
	ft := crearFixtureTablaTotalizable(t)
	// Ninguna columna marcada: cae a la primera numérica -> Cantidad (3+5=8).
	if total := ft.totalPrecioGuardado(t); total != 8 {
		t.Fatalf("sin marcar ninguna columna, esperaba inferir Cantidad (8), dio %v", total)
	}
}

func TestTablaTotalizable_ColumnaMarcadaTienePrioridadSobreLaPrimeraNumerica(t *testing.T) {
	ft := crearFixtureTablaTotalizable(t)
	ft.marcarTotalizable(t, ft.colPrecioID, true)
	// Precio (100+200=300) marcada explícitamente, NO Cantidad (que sería 8
	// si se siguiera infiriendo la primera numérica).
	if total := ft.totalPrecioGuardado(t); total != 300 {
		t.Fatalf("con Precio marcada totalizable, esperaba 300 (no la inferida 8), dio %v", total)
	}
}

func TestTablaTotalizable_SumaTodasLasColumnasMarcadas(t *testing.T) {
	ft := crearFixtureTablaTotalizable(t)
	ft.marcarTotalizable(t, ft.colCantidadID, true)
	ft.marcarTotalizable(t, ft.colPrecioID, true)
	// (3+100) + (5+200) = 308.
	if total := ft.totalPrecioGuardado(t); total != 308 {
		t.Fatalf("con ambas columnas marcadas, esperaba sumar las dos (308), dio %v", total)
	}
}

func TestTablaTotalizable_SinColumnaMarcadaAdvierteAlValidar(t *testing.T) {
	ft := crearFixtureTablaTotalizable(t)
	res := postCompilador(t, (&CompiladorHandler{DB: ft.f.handler.DB}).Validar, ft.f.calculadoraID)
	if !contieneAdvertenciaTablaInferida(res.Advertencias, ft.tabla) {
		t.Fatalf("esperaba la advertencia TABLA_TOTALES_COLUMNAS_INFERIDAS nombrando %s, dio: %v", ft.tabla, res.Advertencias)
	}
}

func TestTablaTotalizable_ConColumnaMarcadaNoAdvierteAlValidar(t *testing.T) {
	ft := crearFixtureTablaTotalizable(t)
	ft.marcarTotalizable(t, ft.colPrecioID, true)
	res := postCompilador(t, (&CompiladorHandler{DB: ft.f.handler.DB}).Validar, ft.f.calculadoraID)
	if contieneAdvertenciaTablaInferida(res.Advertencias, ft.tabla) {
		t.Fatalf("con una columna marcada totalizable, no debería advertir TABLA_TOTALES_COLUMNAS_INFERIDAS: %v", res.Advertencias)
	}
}

func contieneAdvertenciaTablaInferida(advertencias []string, tablaID string) bool {
	for _, adv := range advertencias {
		if strings.Contains(adv, "TABLA_TOTALES_COLUMNAS_INFERIDAS") && strings.Contains(adv, tablaID) {
			return true
		}
	}
	return false
}
