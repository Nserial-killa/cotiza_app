package handlers

// Pruebas de integración de GET /api/catalogos/designer. Mismo
// criterio que auth_test.go: contra Postgres real, con fixtures
// propios que se limpian solos.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func postCatalogos(t *testing.T, metodo http.HandlerFunc, ruta string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("no se pudo serializar el body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, ruta, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	metodo(rec, req)
	return rec
}

func getDesigner(t *testing.T, handler *CatalogosHandler, query string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/catalogos/designer"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	handler.ListarDesigner(rec, req)
	return rec
}

func deleteConRuta(t *testing.T, patron, ruta string, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Delete(patron, handler)
	req := httptest.NewRequest(http.MethodDelete, ruta, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCatalogosDesigner_ModoInvalido(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}

	rec := getDesigner(t, handler, "modo=esto_no_existe")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("esperaba 400 con modo inválido, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogosDesigner_SoloCatalogos(t *testing.T) {
	pool := setupTestDB(t)
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo de prueba (solo_catalogos)", "")

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler, "") // modo por defecto = solo_catalogos

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK        bool               `json:"ok"`
		Catalogos []catalogoDesigner `json:"catalogos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	encontrado := false
	for _, c := range res.Catalogos {
		if c.CatalogoID == catalogoID {
			encontrado = true
			break
		}
	}
	if !encontrado {
		t.Errorf("el catálogo de prueba %q no apareció en la lista de %d catálogos", catalogoID, len(res.Catalogos))
	}
}

// Un catálogo con activo=false no debería listarse — es el filtro
// que usa la pantalla para "ocultar" en vez de borrar.
func TestCatalogosDesigner_NoListaInactivos(t *testing.T) {
	pool := setupTestDB(t)
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo inactivo de prueba", "")

	// lo desactivamos después de crearlo (el helper siempre crea activo=true)
	_, err := pool.Exec(context.Background(), `UPDATE catalogos SET activo = false WHERE catalogo_id = $1`, catalogoID)
	if err != nil {
		t.Fatalf("no se pudo desactivar el catálogo de prueba: %v", err)
	}

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler, "")

	var res struct {
		Catalogos []catalogoDesigner `json:"catalogos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	for _, c := range res.Catalogos {
		if c.CatalogoID == catalogoID {
			t.Errorf("el catálogo inactivo %q no debería aparecer en la lista", catalogoID)
		}
	}
}

func TestCatalogosDesigner_ValoresDeCatalogo(t *testing.T) {
	pool := setupTestDB(t)
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo con valores", "")
	crearValorCatalogoPrueba(t, pool, catalogoID, "Valor Uno", "")
	crearValorCatalogoPrueba(t, pool, catalogoID, "Valor Dos", "")

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler, "modo=valores_catalogo&catalogo_id="+catalogoID)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK      bool                    `json:"ok"`
		Valores []valorCatalogoDesigner `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	if len(res.Valores) != 2 {
		t.Fatalf("esperaba 2 valores, dio %d", len(res.Valores))
	}
}

// Sin catalogo_id, la pantalla espera una lista vacía y ok:true —
// NO un error — porque "todavía no se seleccionó ningún catálogo" es
// un estado normal de la UI, no una falla.
func TestCatalogosDesigner_ValoresSinCatalogoID(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}

	rec := getDesigner(t, handler, "modo=valores_catalogo")

	var res struct {
		OK      bool                    `json:"ok"`
		Valores []valorCatalogoDesigner `json:"valores"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Error("sin catalogo_id esperaba ok:true (lista vacía), no un error")
	}
	if len(res.Valores) != 0 {
		t.Errorf("esperaba 0 valores sin catalogo_id, dio %d", len(res.Valores))
	}
}

func TestCatalogosDesigner_RelacionesPadreHijo(t *testing.T) {
	pool := setupTestDB(t)
	padreID := crearCatalogoPrueba(t, pool, "Catálogo padre", "")
	hijoID := crearCatalogoPrueba(t, pool, "Catálogo hijo", padreID)
	valorPadreID := crearValorCatalogoPrueba(t, pool, padreID, "Valor Padre", "")
	valorHijoID := crearValorCatalogoPrueba(t, pool, hijoID, "Valor Hijo", "")
	crearRelacionPrueba(t, pool, padreID, valorPadreID, hijoID, valorHijoID)

	handler := &CatalogosHandler{DB: pool}
	rec := getDesigner(t, handler,
		"modo=relaciones_valor&catalogo_hijo_id="+hijoID+
			"&valor_hijo_id="+valorHijoID+
			"&catalogo_padre_id="+padreID)

	if rec.Code != http.StatusOK {
		t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		OK           bool                       `json:"ok"`
		Relaciones   []relacionCatalogoDesigner `json:"relaciones"`
		ValoresPadre []valorCatalogoDesigner    `json:"valores_padre"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)

	if !res.OK {
		t.Fatal("esperaba ok:true")
	}
	if len(res.Relaciones) != 1 {
		t.Fatalf("esperaba 1 relación, dio %d", len(res.Relaciones))
	}
	if res.Relaciones[0].ValorHijoID != valorHijoID {
		t.Errorf("valor_hijo_id de la relación = %q, esperaba %q", res.Relaciones[0].ValorHijoID, valorHijoID)
	}
	// El modo relaciones_valor también debería traer los valores
	// disponibles del catálogo padre, para poblar el selector.
	if len(res.ValoresPadre) != 1 {
		t.Errorf("esperaba 1 valor del catálogo padre para el selector, dio %d", len(res.ValoresPadre))
	}
}

func TestCatalogosGuardar_UpsertCatalogo(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}
	id := "TEST-CAT-WRITE-" + sufijoUnico()
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM catalogos WHERE catalogo_id = $1`, id) })

	rec := postCatalogos(t, handler.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": id, "nombre_catalogo": "Catálogo creado", "alcance": "COTIZADOR",
		"descripcion": "Primera versión", "activo": true, "orden": "7",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear catálogo: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": id, "nombre_catalogo": "Catálogo editado", "alcance": "GLOBAL",
		"descripcion": "Segunda versión", "activo": true, "orden": 9,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar catálogo: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var nombre, alcance string
	var orden int
	if err := pool.QueryRow(context.Background(), `SELECT nombre_catalogo, alcance, orden FROM catalogos WHERE catalogo_id=$1`, id).Scan(&nombre, &alcance, &orden); err != nil {
		t.Fatalf("no se pudo leer el catálogo guardado: %v", err)
	}
	if nombre != "Catálogo editado" || alcance != "GLOBAL" || orden != 9 {
		t.Errorf("upsert no persistió la edición: nombre=%q alcance=%q orden=%d", nombre, alcance, orden)
	}
}

func TestCatalogosGuardar_UpsertValor(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo para escritura", "")
	valorID := "TEST-VAL-WRITE-" + sufijoUnico()

	body := map[string]any{
		"valor_id": valorID, "catalogo_id": catalogoID, "clave": "CLAVE",
		"texto_visible": "Texto inicial", "valor_sistema": "VALOR", "descripcion": "Demo",
		"orden": "3", "activo": true, "valor_padre_id": "",
	}
	rec := postCatalogos(t, handler.GuardarValor, "/api/catalogos/valores", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("crear valor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	body["texto_visible"] = "Texto editado"
	body["orden"] = 5
	rec = postCatalogos(t, handler.GuardarValor, "/api/catalogos/valores", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("editar valor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	var texto string
	var orden int
	if err := pool.QueryRow(context.Background(), `SELECT texto_visible, orden FROM catalogo_valores WHERE valor_id=$1`, valorID).Scan(&texto, &orden); err != nil {
		t.Fatalf("no se pudo leer el valor guardado: %v", err)
	}
	if texto != "Texto editado" || orden != 5 {
		t.Errorf("upsert no persistió la edición: texto=%q orden=%d", texto, orden)
	}
}

func TestCatalogosGuardar_ReemplazaRelacionesEnTransaccion(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}
	padreID := crearCatalogoPrueba(t, pool, "Padre para reemplazo", "")
	hijoID := crearCatalogoPrueba(t, pool, "Hijo para reemplazo", padreID)
	padreValor1 := crearValorCatalogoPrueba(t, pool, padreID, "Padre Uno", "")
	padreValor2 := crearValorCatalogoPrueba(t, pool, padreID, "Padre Dos", "")
	hijoValorID := "TEST-VAL-CHILD-" + sufijoUnico()

	base := map[string]any{
		"valor_id": hijoValorID, "catalogo_id": hijoID, "clave": "HIJO",
		"texto_visible": "Valor hijo", "valor_sistema": "HIJO", "descripcion": "",
		"orden": "1", "activo": true, "_actualizar_relaciones": true,
		"_catalogo_padre_id": padreID,
		"_valor_padre_ids":   []string{padreValor1, padreValor2},
	}
	rec := postCatalogos(t, handler.GuardarRelaciones, "/api/catalogos/relaciones", base)
	if rec.Code != http.StatusOK {
		t.Fatalf("crear relaciones: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	base["_valor_padre_ids"] = []string{padreValor2}
	rec = postCatalogos(t, handler.GuardarRelaciones, "/api/catalogos/relaciones", base)
	if rec.Code != http.StatusOK {
		t.Fatalf("reemplazar relaciones: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rows, err := pool.Query(context.Background(), `SELECT valor_padre_id FROM catalogo_relaciones WHERE valor_hijo_id=$1`, hijoValorID)
	if err != nil {
		t.Fatalf("no se pudieron consultar las relaciones: %v", err)
	}
	defer rows.Close()
	var encontrados []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		encontrados = append(encontrados, id)
	}
	if len(encontrados) != 1 || encontrados[0] != padreValor2 {
		t.Fatalf("esperaba solo la relación reemplazada %q, obtuvo %v", padreValor2, encontrados)
	}
}

func TestCatalogosEliminar_SinUsoQuedaInactivo(t *testing.T) {
	pool := setupTestDB(t)
	id := crearCatalogoPrueba(t, pool, "Catálogo eliminable", "")
	rec := deleteConRuta(t, "/api/catalogos/{id}", "/api/catalogos/"+id, (&CatalogosHandler{DB: pool}).EliminarCatalogo)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar catálogo: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var activo bool
	if err := pool.QueryRow(context.Background(), `SELECT activo FROM catalogos WHERE catalogo_id=$1`, id).Scan(&activo); err != nil {
		t.Fatal(err)
	}
	if activo {
		t.Fatal("el catálogo debía conservarse con activo=false")
	}
}

// Cubre el ciclo completo que pidió el reporte: rechazar mientras el
// catálogo está en uso, y permitir el borrado en cuanto ese uso deja
// de existir (acá, desactivando el elemento que lo usaba).
func TestCatalogosEliminar_RechazaCatalogoUsadoPorElementoActivo(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	pool := tabsHandler.DB
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo en uso", "")
	tabID := "TEST-TAB-CAT-USO-" + sufijoUnico()
	elementoID := "TEST-EL-CAT-USO-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Uso catálogo", "activo": true,
	})
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID,
		"tipo": "CAMPO_CATALOGO", "etiqueta": "Catálogo", "catalogo_id": catalogoID, "activo": true,
	})

	catalogosHandler := &CatalogosHandler{DB: pool}
	rec := deleteConRuta(t, "/api/catalogos/{id}", "/api/catalogos/"+catalogoID, catalogosHandler.EliminarCatalogo)
	if rec.Code != http.StatusConflict {
		t.Fatalf("catálogo en uso: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}
	var activo bool
	if err := pool.QueryRow(context.Background(), `SELECT activo FROM catalogos WHERE catalogo_id=$1`, catalogoID).Scan(&activo); err != nil || !activo {
		t.Fatalf("el catálogo rechazado debía seguir activo: activo=%v err=%v", activo, err)
	}

	// Desactivar el elemento que lo usaba: ahora sí debe poder eliminarse.
	recEl := deleteConRuta(t, "/api/cotizador/elementos/{id}", "/api/cotizador/elementos/"+elementoID, tabsHandler.EliminarElemento)
	if recEl.Code != http.StatusOK {
		t.Fatalf("desactivar el elemento: esperaba 200, dio %d: %s", recEl.Code, recEl.Body.String())
	}

	recSegundoIntento := deleteConRuta(t, "/api/catalogos/{id}", "/api/catalogos/"+catalogoID, catalogosHandler.EliminarCatalogo)
	if recSegundoIntento.Code != http.StatusOK {
		t.Fatalf("catálogo ya sin uso: esperaba 200, dio %d: %s", recSegundoIntento.Code, recSegundoIntento.Body.String())
	}
	if err := pool.QueryRow(context.Background(), `SELECT activo FROM catalogos WHERE catalogo_id=$1`, catalogoID).Scan(&activo); err != nil || activo {
		t.Fatalf("el catálogo debía quedar inactivo tras el segundo intento: activo=%v err=%v", activo, err)
	}
}

// Mismo bug, pero por el otro camino: guardar el formulario del
// catálogo con el checkbox "Activo" destildado (POST /api/catalogos
// con activo:false) en vez de usar el botón "Eliminar" dedicado. Este
// caso es el que realmente se coló: EliminarCatalogo ya validaba
// esto, pero GuardarCatalogo no.
func TestCatalogosGuardar_RechazaDesactivarCatalogoEnUso(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	pool := tabsHandler.DB
	catalogoID := crearCatalogoPrueba(t, pool, "Catálogo en uso (guardar)", "")
	tabID := "TEST-TAB-CAT-GUARDAR-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Uso catálogo", "activo": true,
	})
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAT-GUARDAR-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO_CATALOGO", "etiqueta": "Catálogo", "catalogo_id": catalogoID, "activo": true,
	})

	catalogosHandler := &CatalogosHandler{DB: pool}
	rec := postCatalogos(t, catalogosHandler.GuardarCatalogo, "/api/catalogos", map[string]any{
		"catalogo_id": catalogoID, "nombre_catalogo": "Catálogo en uso (guardar)",
		"alcance": "COTIZADOR", "activo": false,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("guardar con activo:false estando en uso: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}

	var activo bool
	if err := pool.QueryRow(context.Background(), `SELECT activo FROM catalogos WHERE catalogo_id=$1`, catalogoID).Scan(&activo); err != nil || !activo {
		t.Fatalf("el catálogo rechazado debía seguir activo: activo=%v err=%v", activo, err)
	}
}

func TestCatalogosEliminar_RelacionDesapareceFisicamente(t *testing.T) {
	pool := setupTestDB(t)
	padreID := crearCatalogoPrueba(t, pool, "Padre eliminación", "")
	hijoID := crearCatalogoPrueba(t, pool, "Hijo eliminación", padreID)
	valorPadreID := crearValorCatalogoPrueba(t, pool, padreID, "Padre", "")
	valorHijoID := crearValorCatalogoPrueba(t, pool, hijoID, "Hijo", "")
	relacionID := crearRelacionPrueba(t, pool, padreID, valorPadreID, hijoID, valorHijoID)
	rec := deleteConRuta(t, "/api/catalogos/relaciones/{id}", "/api/catalogos/relaciones/"+relacionID, (&CatalogosHandler{DB: pool}).EliminarRelacion)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar relación: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var existe bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM catalogo_relaciones WHERE relacion_id::text=$1)`, relacionID).Scan(&existe); err != nil {
		t.Fatal(err)
	}
	if existe {
		t.Fatal("la relación debía desaparecer físicamente")
	}
}

// Cubre el ciclo completo: rechazar mientras la relación activa
// existe, y permitir el borrado en cuanto esa relación desaparece.
func TestCatalogosEliminar_ValorConRelacionActivaSeRechaza(t *testing.T) {
	pool := setupTestDB(t)
	padreID := crearCatalogoPrueba(t, pool, "Padre valor usado", "")
	hijoID := crearCatalogoPrueba(t, pool, "Hijo valor usado", padreID)
	valorPadreID := crearValorCatalogoPrueba(t, pool, padreID, "Padre", "")
	valorHijoID := crearValorCatalogoPrueba(t, pool, hijoID, "Hijo", "")
	relacionID := crearRelacionPrueba(t, pool, padreID, valorPadreID, hijoID, valorHijoID)

	handler := &CatalogosHandler{DB: pool}
	rec := deleteConRuta(t, "/api/catalogos/valores/{id}", "/api/catalogos/valores/"+valorPadreID, handler.EliminarValor)
	if rec.Code != http.StatusConflict {
		t.Fatalf("valor relacionado: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}

	// Quitar la relación: ahora sí debe poder eliminarse el valor.
	recRel := deleteConRuta(t, "/api/catalogos/relaciones/{id}", "/api/catalogos/relaciones/"+relacionID, handler.EliminarRelacion)
	if recRel.Code != http.StatusOK {
		t.Fatalf("eliminar la relación: esperaba 200, dio %d: %s", recRel.Code, recRel.Body.String())
	}

	recSegundoIntento := deleteConRuta(t, "/api/catalogos/valores/{id}", "/api/catalogos/valores/"+valorPadreID, handler.EliminarValor)
	if recSegundoIntento.Code != http.StatusOK {
		t.Fatalf("valor ya sin relaciones: esperaba 200, dio %d: %s", recSegundoIntento.Code, recSegundoIntento.Body.String())
	}
}

// Mismo bug que con el catálogo, pero en el formulario del valor:
// guardar con el checkbox "Activo" destildado (POST
// /api/catalogos/valores con activo:false) debía poder saltarse la
// validación de EliminarValor antes de este fix.
func TestCatalogosGuardar_RechazaDesactivarValorConRelacionActiva(t *testing.T) {
	pool := setupTestDB(t)
	padreID := crearCatalogoPrueba(t, pool, "Padre valor guardar", "")
	hijoID := crearCatalogoPrueba(t, pool, "Hijo valor guardar", padreID)
	valorPadreID := crearValorCatalogoPrueba(t, pool, padreID, "Padre", "")
	valorHijoID := crearValorCatalogoPrueba(t, pool, hijoID, "Hijo", "")
	crearRelacionPrueba(t, pool, padreID, valorPadreID, hijoID, valorHijoID)

	handler := &CatalogosHandler{DB: pool}
	rec := postCatalogos(t, handler.GuardarValor, "/api/catalogos/valores", map[string]any{
		"valor_id": valorPadreID, "catalogo_id": padreID, "clave": "PADRE",
		"texto_visible": "Padre", "activo": false,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("guardar con activo:false teniendo relación activa: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}

	var activo bool
	if err := pool.QueryRow(context.Background(), `SELECT activo FROM catalogo_valores WHERE valor_id=$1`, valorPadreID).Scan(&activo); err != nil || !activo {
		t.Fatalf("el valor rechazado debía seguir activo: activo=%v err=%v", activo, err)
	}
}
