package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type entornoPlantillasPrueba struct {
	pool          *pgxpool.Pool
	plantillas    *PlantillasHandler
	estructura    *PlantillaEstructuraHandler
	vinculaciones *PlantillaVinculacionesHandler
	estilo        *PlantillaEstiloHandler
	organizacion  string
	calculadora   string
}

func nuevoEntornoPlantillas(t *testing.T) *entornoPlantillasPrueba {
	t.Helper()
	pool := setupTestDB(t)
	sufijo := sufijoUnico()
	organizacionID := "TEST-ORG-TPL-" + sufijo
	calculadoraID := "TEST-CALC-TPL-" + sufijo
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO organizaciones (organizacion_id,nombre,estado) VALUES ($1,'Organización plantillas','Activo')`, organizacionID); err != nil {
		t.Fatalf("no se pudo crear la organización de prueba: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO calculadoras (calculadora_id,nombre_calculadora,estado) VALUES ($1,'Cotizador plantillas','Activo')`, calculadoraID); err != nil {
		pool.Exec(ctx, `DELETE FROM organizaciones WHERE organizacion_id=$1`, organizacionID)
		t.Fatalf("no se pudo crear el cotizador de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM plantillas WHERE organizacion_id=$1`, organizacionID)
		pool.Exec(ctx, `DELETE FROM tabs_cotizador WHERE calculadora_id=$1`, calculadoraID)
		pool.Exec(ctx, `DELETE FROM calculadoras WHERE calculadora_id=$1`, calculadoraID)
		pool.Exec(ctx, `DELETE FROM organizaciones WHERE organizacion_id=$1`, organizacionID)
	})
	return &entornoPlantillasPrueba{
		pool: pool, plantillas: &PlantillasHandler{DB: pool},
		estructura:    &PlantillaEstructuraHandler{DB: pool},
		vinculaciones: &PlantillaVinculacionesHandler{DB: pool},
		estilo:        &PlantillaEstiloHandler{DB: pool},
		organizacion:  organizacionID, calculadora: calculadoraID,
	}
}

func llamarPlantilla(t *testing.T, metodo, patron, ruta string, handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	var lector *bytes.Reader
	if body == nil {
		lector = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("no se pudo serializar el body: %v", err)
		}
		lector = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(metodo, ruta, lector)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	router.MethodFunc(metodo, patron, handler)
	router.ServeHTTP(rec, req)
	return rec
}

func respuestaPlantilla(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var respuesta map[string]any
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	return respuesta
}

func crearPlantillaPrueba(t *testing.T, e *entornoPlantillasPrueba, nombre string, extras map[string]any) string {
	t.Helper()
	body := map[string]any{
		"nombre": nombre, "descripcion": "Descripción de prueba",
		"calculadora_ids":              []string{e.calculadora},
		"tipos_propuesta":              []string{"COMERCIAL"},
		"organizacion_id":              e.organizacion,
		"disponible_nuevas_propuestas": true, "permite_duplicar": true,
	}
	for clave, valor := range extras {
		body[clave] = valor
	}
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas", "/api/plantillas", e.plantillas.Crear, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear plantilla: esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	respuesta := respuestaPlantilla(t, rec)
	id, _ := respuesta["plantilla_id"].(string)
	if id == "" {
		t.Fatalf("crear plantilla no devolvió plantilla_id: %s", rec.Body.String())
	}
	return id
}

func crearSeccionPrueba(t *testing.T, e *entornoPlantillasPrueba, plantillaID, nombre string) string {
	t.Helper()
	ruta := "/api/plantillas/" + plantillaID + "/secciones"
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/secciones", ruta, e.estructura.CrearSeccion, map[string]any{"nombre": nombre})
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear sección: esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	id, _ := respuestaPlantilla(t, rec)["seccion_id"].(string)
	return id
}

func crearBloquePrueba(t *testing.T, e *entornoPlantillasPrueba, seccionID, nombre string) string {
	t.Helper()
	ruta := "/api/plantillas/secciones/" + seccionID + "/bloques"
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques", ruta, e.estructura.CrearBloque, map[string]any{
		"tipo_bloque": "TEXTO", "nombre_interno": nombre, "columna": 0,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("crear bloque: esperaba 201, dio %d: %s", rec.Code, rec.Body.String())
	}
	id, _ := respuestaPlantilla(t, rec)["bloque_id"].(string)
	return id
}

func TestPlantillas_CrearListarDetalleEditarYEliminar(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla desde cero", nil)

	var estado string
	var secciones int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT p.estado,(SELECT COUNT(*) FROM plantilla_secciones ps WHERE ps.plantilla_id=p.plantilla_id)
		FROM plantillas p WHERE p.plantilla_id::text=$1`, plantillaID).Scan(&estado, &secciones); err != nil {
		t.Fatal(err)
	}
	if estado != "Borrador" || secciones != 0 {
		t.Fatalf("alta desde cero inesperada: estado=%s secciones=%d", estado, secciones)
	}

	rutaPatch := "/api/plantillas/" + plantillaID
	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}", rutaPatch, e.plantillas.Editar, map[string]any{
		"nombre": "Plantilla editada", "descripcion": "Nueva descripción",
		"disponible_nuevas_propuestas": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar plantilla: %d: %s", rec.Code, rec.Body.String())
	}

	rutaLista := "/api/plantillas?busqueda=" + url.QueryEscape("editada") +
		"&calculadora_id=" + url.QueryEscape(e.calculadora) + "&tipo_propuesta=COMERCIAL&estado=Borrador"
	rec = llamarPlantilla(t, http.MethodGet, "/api/plantillas", rutaLista, e.plantillas.Listar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar plantillas: %d: %s", rec.Code, rec.Body.String())
	}
	var lista struct {
		OK         bool                 `json:"ok"`
		Plantillas []plantillaListado   `json:"plantillas"`
		Contadores contadoresPlantillas `json:"contadores"`
	}
	assertJSON(t, rec.Body.Bytes(), &lista)
	if !lista.OK || len(lista.Plantillas) != 1 || lista.Plantillas[0].PlantillaID != plantillaID || lista.Contadores.Total < 1 {
		t.Fatalf("listado inesperado: %+v", lista)
	}

	rec = llamarPlantilla(t, http.MethodGet, "/api/plantillas/{id}", rutaPatch, e.plantillas.Detalle, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detalle plantilla: %d: %s", rec.Code, rec.Body.String())
	}
	var detalle struct {
		OK        bool             `json:"ok"`
		Plantilla plantillaDetalle `json:"plantilla"`
	}
	assertJSON(t, rec.Body.Bytes(), &detalle)
	if detalle.Plantilla.Nombre != "Plantilla editada" || detalle.Plantilla.DisponibleNuevasPropuestas || len(detalle.Plantilla.Secciones) != 0 {
		t.Fatalf("detalle inesperado: %+v", detalle.Plantilla)
	}

	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/{id}", rutaPatch, e.plantillas.Eliminar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar borrador: %d: %s", rec.Code, rec.Body.String())
	}
	var existe bool
	e.pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, plantillaID).Scan(&existe)
	if existe {
		t.Fatal("la plantilla borrador no fue eliminada")
	}
}

func TestPlantillas_OpcionesDevuelveTiposYOrganizaciones(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	crearPlantillaPrueba(t, e, "Plantilla opciones", nil)
	rec := llamarPlantilla(t, http.MethodGet, "/api/plantillas/opciones", "/api/plantillas/opciones", e.plantillas.Opciones, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("consultar opciones: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		Tipos          []string                      `json:"tipos_propuesta"`
		Organizaciones []organizacionOpcionPlantilla `json:"organizaciones"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	if len(respuesta.Tipos) == 0 || len(respuesta.Organizaciones) == 0 {
		t.Fatalf("opciones incompletas: %+v", respuesta)
	}
	encontroOrganizacion := false
	for _, organizacion := range respuesta.Organizaciones {
		if organizacion.OrganizacionID == e.organizacion {
			encontroOrganizacion = true
		}
	}
	if !encontroOrganizacion {
		t.Fatalf("no se devolvió la organización de prueba: %+v", respuesta.Organizaciones)
	}
}

func TestPlantillas_CrearDesdeOtraCopiaSeccionesBloquesYEstilo(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	baseID := crearPlantillaPrueba(t, e, "Plantilla base", nil)
	seccionBaseID := crearSeccionPrueba(t, e, baseID, "Alcance")
	bloqueBaseID := crearBloquePrueba(t, e, seccionBaseID, "resumen_alcance")

	rutaEstilo := "/api/plantillas/" + baseID + "/estilo"
	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}/estilo", rutaEstilo, e.estilo.Actualizar, map[string]any{
		"tema": "EJECUTIVO", "formato_pagina": "A4", "margenes": "AMPLIO",
		"diseno_portada": "LATERAL", "estilo_tablas": "TARJETAS",
		"color_primario": "#112233", "color_secundario": "#445566", "color_acento": "#778899",
		"color_texto": "#1A2B3C", "color_fondo": "#F4F5F6", "fuente_titulos": "Georgia",
		"fuente_texto": "Arial", "logo_url": "https://example.com/logo.png", "logo_tamano": "GRANDE",
		"mostrar_logo": true, "mostrar_organizacion": true, "nombre_organizacion_visible": "Exceltec",
		"texto_encabezado": "Propuesta comercial", "texto_pie": "Uso interno", "numerar_paginas": true,
		"marca_confidencial": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("preparar estilo base: %d: %s", rec.Code, rec.Body.String())
	}

	copiaID := crearPlantillaPrueba(t, e, "Plantilla copiada", map[string]any{"crear_desde": baseID})
	rec = llamarPlantilla(t, http.MethodGet, "/api/plantillas/{id}", "/api/plantillas/"+copiaID, e.plantillas.Detalle, nil)
	var detalle struct {
		Plantilla plantillaDetalle `json:"plantilla"`
	}
	assertJSON(t, rec.Body.Bytes(), &detalle)
	if rec.Code != http.StatusOK || len(detalle.Plantilla.Secciones) != 1 || len(detalle.Plantilla.Secciones[0].Bloques) != 1 {
		t.Fatalf("la copia no preservó estructura: %d %s", rec.Code, rec.Body.String())
	}
	seccionCopia := detalle.Plantilla.Secciones[0]
	if seccionCopia.SeccionID == seccionBaseID || seccionCopia.Bloques[0].BloqueID == bloqueBaseID {
		t.Fatal("la copia reutilizó IDs de sección o bloque")
	}
	if detalle.Plantilla.Estilo == nil || detalle.Plantilla.Estilo.Tema != "EJECUTIVO" || detalle.Plantilla.Estilo.FormatoPagina != "A4" {
		t.Fatalf("la copia no preservó el estilo: %+v", detalle.Plantilla.Estilo)
	}
	if detalle.Plantilla.Estilo.ColorPrimario == nil || *detalle.Plantilla.Estilo.ColorPrimario != "#112233" ||
		detalle.Plantilla.Estilo.LogoURL == nil || *detalle.Plantilla.Estilo.LogoURL != "https://example.com/logo.png" ||
		!detalle.Plantilla.Estilo.MostrarLogo || !detalle.Plantilla.Estilo.NumerarPaginas ||
		detalle.Plantilla.Estilo.LogoTamano != "GRANDE" {
		t.Fatalf("la copia no preservó la identidad visual: %+v", detalle.Plantilla.Estilo)
	}
}

func TestPlantillas_PublicarExigeSeccionConBloque(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla para publicar", nil)
	ruta := "/api/plantillas/" + plantillaID + "/publicar"
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/publicar", ruta, e.plantillas.Publicar, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("publicar sin secciones: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Resumen")
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/publicar", ruta, e.plantillas.Publicar, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("publicar sin bloques: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	crearBloquePrueba(t, e, seccionID, "resumen")
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/publicar", ruta, e.plantillas.Publicar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("publicar estructura válida: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/{id}", "/api/plantillas/"+plantillaID, e.plantillas.Eliminar, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("eliminar publicada: esperaba 409, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlantillaEstructura_CRUDYReordenamiento(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla estructura", nil)
	seccionA := crearSeccionPrueba(t, e, plantillaID, "A")
	seccionB := crearSeccionPrueba(t, e, plantillaID, "B")
	seccionC := crearSeccionPrueba(t, e, plantillaID, "C")

	rutaOrden := "/api/plantillas/secciones/" + seccionA + "/orden"
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/secciones/{seccion_id}/orden", rutaOrden,
		e.estructura.OrdenarSecciones, map[string]any{"seccion_ids": []string{seccionC, seccionA, seccionB}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reordenar secciones: %d: %s", rec.Code, rec.Body.String())
	}
	var primera string
	if err := e.pool.QueryRow(context.Background(), `SELECT seccion_id::text FROM plantilla_secciones WHERE plantilla_id::text=$1 ORDER BY orden LIMIT 1`, plantillaID).Scan(&primera); err != nil || primera != seccionC {
		t.Fatalf("orden de secciones no persistió: primera=%s err=%v", primera, err)
	}

	rutaSeccion := "/api/plantillas/secciones/" + seccionA
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/secciones/{seccion_id}", rutaSeccion, e.estructura.EditarSeccion, map[string]any{
		"nombre": "A editada", "titulo": "Título A", "mostrar_titulo": false,
		"visibilidad": "CONDICIONAL", "diseno_bloques": "DOS_30_70", "mostrar_web": false, "mostrar_pdf": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar sección: %d: %s", rec.Code, rec.Body.String())
	}

	bloqueA := crearBloquePrueba(t, e, seccionA, "bloque_a")
	bloqueB := crearBloquePrueba(t, e, seccionA, "bloque_b")
	bloqueC := crearBloquePrueba(t, e, seccionA, "bloque_c")
	rutaOrdenBloques := "/api/plantillas/bloques/" + bloqueA + "/orden"
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/orden", rutaOrdenBloques,
		e.estructura.OrdenarBloques, []string{bloqueC, bloqueB, bloqueA})
	if rec.Code != http.StatusOK {
		t.Fatalf("reordenar bloques: %d: %s", rec.Code, rec.Body.String())
	}
	if err := e.pool.QueryRow(context.Background(), `SELECT bloque_id::text FROM plantilla_bloques WHERE seccion_id::text=$1 ORDER BY orden LIMIT 1`, seccionA).Scan(&primera); err != nil || primera != bloqueC {
		t.Fatalf("orden de bloques no persistió: primero=%s err=%v", primera, err)
	}

	rutaBloque := "/api/plantillas/bloques/" + bloqueA
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}", rutaBloque, e.estructura.EditarBloque, map[string]any{
		"titulo": "Bloque editado", "contenido": "Contenido", "columna": 2, "mostrar_pdf": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("editar bloque: %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/"+bloqueB, e.estructura.EliminarBloque, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar bloque: %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/secciones/{seccion_id}", "/api/plantillas/secciones/"+seccionB, e.estructura.EliminarSeccion, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar sección: %d: %s", rec.Code, rec.Body.String())
	}
}

func crearFuentesPrueba(t *testing.T, e *entornoPlantillasPrueba) (tabID, campoID, catalogoID string) {
	t.Helper()
	sufijo := sufijoUnico()
	tabID = "TEST-TAB-FUENTE-" + sufijo
	campoID = "TEST-CAMPO-FUENTE-" + sufijo
	catalogoID = "TEST-CAMPO-CATALOGO-" + sufijo
	_, err := e.pool.Exec(context.Background(), `
		INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo)
		VALUES ($1,$2,'Datos',0,true)`, tabID, e.calculadora)
	if err == nil {
		_, err = e.pool.Exec(context.Background(), `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,activo) VALUES
			($2,$1,'CAMPO','Campo activo',0,true),
			($3,$1,'CAMPO_CATALOGO','Catálogo activo',1,true),
			($4,$1,'LEYENDA','Leyenda',2,true),
			($5,$1,'CAMPO','Campo inactivo',3,false)`,
			tabID, campoID, catalogoID, "TEST-LEYENDA-"+sufijo, "TEST-INACTIVO-"+sufijo)
	}
	if err != nil {
		t.Fatalf("no se pudieron crear fuentes: %v", err)
	}
	return tabID, campoID, catalogoID
}

func TestPlantillaVinculaciones_FuentesIncluyenDisenadorYDatosBase(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla fuentes", nil)
	_, campoID, catalogoID := crearFuentesPrueba(t, e)
	ruta := "/api/plantillas/" + plantillaID + "/fuentes?calculadora_id=" + url.QueryEscape(e.calculadora)
	rec := llamarPlantilla(t, http.MethodGet, "/api/plantillas/{id}/fuentes", ruta, e.vinculaciones.Fuentes, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("obtener fuentes: %d: %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		Fuentes         []fuentePlantilla `json:"fuentes"`
		Campos          []fuentePlantilla `json:"campos"`
		DatosCotizacion []fuentePlantilla `json:"datos_cotizacion"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	if len(respuesta.Campos) != 2 || len(respuesta.DatosCotizacion) != len(fuentesCotizacionBase) || len(respuesta.Fuentes) != 2+len(fuentesCotizacionBase) {
		t.Fatalf("fuentes inesperadas: campos=%d base=%d total=%d", len(respuesta.Campos), len(respuesta.DatosCotizacion), len(respuesta.Fuentes))
	}
	vistos := map[string]bool{}
	for _, fuente := range respuesta.Campos {
		vistos[fuente.FuenteID] = true
	}
	if !vistos[campoID] || !vistos[catalogoID] {
		t.Fatalf("faltan campos del Diseñador: %+v", respuesta.Campos)
	}
}

func TestPlantillaVinculaciones_ValidaUpsertYElimina(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla vínculos", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Datos")
	bloqueID := crearBloquePrueba(t, e, seccionID, "cliente")
	_, campoID, _ := crearFuentesPrueba(t, e)
	ruta := "/api/plantillas/bloques/" + bloqueID + "/vinculacion"

	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion", ruta, e.vinculaciones.Guardar, map[string]any{
		"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("vincular campo válido: %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion", ruta, e.vinculaciones.Guardar, map[string]any{
		"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": "NO-EXISTE",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("vincular elemento inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion", ruta, e.vinculaciones.Guardar, map[string]any{
		"calculadora_id": e.calculadora, "fuente_tipo": "COTIZACION_BASE", "fuente_id": "cliente",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert a dato base: %d: %s", rec.Code, rec.Body.String())
	}
	var cantidad int
	var tipo, fuente string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT COUNT(*),MIN(fuente_tipo),MIN(fuente_id) FROM plantilla_vinculaciones
		WHERE bloque_id::text=$1 AND calculadora_id=$2`, bloqueID, e.calculadora).Scan(&cantidad, &tipo, &fuente); err != nil {
		t.Fatal(err)
	}
	if cantidad != 1 || tipo != "COTIZACION_BASE" || fuente != "cliente" {
		t.Fatalf("upsert inconsistente: cantidad=%d tipo=%s fuente=%s", cantidad, tipo, fuente)
	}
	rec = llamarPlantilla(t, http.MethodDelete, "/api/plantillas/bloques/{bloque_id}/vinculacion",
		ruta+"?calculadora_id="+url.QueryEscape(e.calculadora), e.vinculaciones.Eliminar, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar vinculación: %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPlantillaEstilo_UpsertYActualizacionParcial(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla estilo", nil)
	ruta := "/api/plantillas/" + plantillaID + "/estilo"
	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}/estilo", ruta, e.estilo.Actualizar, map[string]any{
		"tema": "CORPORATIVO", "formato_pagina": "A4", "margenes": "COMPACTO",
		"diseno_portada": "MINIMALISTA", "estilo_tablas": "SUAVE",
		"color_primario": "#0b2f63", "color_secundario": "#1F6FFF", "color_acento": "#20B8CD",
		"color_texto": "#172B4D", "color_fondo": "#FFFFFF", "fuente_titulos": "Georgia",
		"fuente_texto": "Arial", "logo_url": "https://example.com/marca.svg", "logo_tamano": "pequeno",
		"mostrar_logo": true, "mostrar_organizacion": true, "nombre_organizacion_visible": "Exceltec CR",
		"texto_encabezado": "Propuesta", "texto_pie": "Confidencial", "numerar_paginas": true,
		"marca_confidencial": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear estilo: %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}/estilo", ruta, e.estilo.Actualizar, map[string]any{
		"margenes": "AMPLIO", "texto_encabezado": "Propuesta actualizada",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("actualizar estilo parcial: %d: %s", rec.Code, rec.Body.String())
	}
	var tema, formato, margenes, portada, tablas string
	var primario, fuenteTitulos, logoURL, logoTamano, encabezado string
	var mostrarLogo, mostrarOrganizacion, numerar, confidencial bool
	if err := e.pool.QueryRow(context.Background(), `
		SELECT tema,formato_pagina,margenes,diseno_portada,estilo_tablas,
		       color_primario,fuente_titulos,logo_url,logo_tamano,mostrar_logo,
		       mostrar_organizacion,texto_encabezado,numerar_paginas,marca_confidencial
		FROM plantilla_estilos WHERE plantilla_id::text=$1`, plantillaID).Scan(
		&tema, &formato, &margenes, &portada, &tablas, &primario, &fuenteTitulos,
		&logoURL, &logoTamano, &mostrarLogo, &mostrarOrganizacion, &encabezado, &numerar, &confidencial); err != nil {
		t.Fatal(err)
	}
	if tema != "CORPORATIVO" || formato != "A4" || margenes != "AMPLIO" || portada != "MINIMALISTA" || tablas != "SUAVE" {
		t.Fatalf("estilo inesperado: %s %s %s %s %s", tema, formato, margenes, portada, tablas)
	}
	if primario != "#0B2F63" || fuenteTitulos != "Georgia" || logoURL != "https://example.com/marca.svg" ||
		logoTamano != "PEQUENO" || !mostrarLogo || !mostrarOrganizacion || encabezado != "Propuesta actualizada" ||
		!numerar || !confidencial {
		t.Fatalf("identidad visual inesperada: %s %s %s %s %t %t %s %t %t",
			primario, fuenteTitulos, logoURL, logoTamano, mostrarLogo, mostrarOrganizacion, encabezado, numerar, confidencial)
	}
}

func TestPlantillaEstilo_ValidaColoresLogoYTamano(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla validaciones de estilo", nil)
	ruta := "/api/plantillas/" + plantillaID + "/estilo"

	casos := []map[string]any{
		{"color_primario": "azul"},
		{"logo_url": "http://example.com/logo.png"},
		{"logo_url": "https:///sin-host.png"},
		{"logo_tamano": "ENORME"},
	}
	for _, body := range casos {
		rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}/estilo", ruta, e.estilo.Actualizar, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("se esperaba 400 para %+v, dio %d: %s", body, rec.Code, rec.Body.String())
		}
	}
}
