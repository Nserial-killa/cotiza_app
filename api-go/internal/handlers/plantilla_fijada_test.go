package handlers

// Pruebas de la plantilla fijada por versión de cotización (migración 0032)
// y del estilo de la plantilla en el documento de la oferta. La prueba
// central es la de la brecha #5: un enlace generado con la v2 de la
// plantilla sigue mostrando la v2 después de publicar la v3.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

type respuestaOfertaFijada struct {
	OK        bool                  `json:"ok"`
	Plantilla *plantillaRenderizada `json:"plantilla"`
}

// routerOfertaFijada monta enlace, lectura pública y Vista Previa en un
// solo router, como en main.go (sin el middleware de sesión).
func routerOfertaFijada(e *entornoPlantillasPrueba) http.Handler {
	enlaces := &EnlacesPublicosHandler{DB: e.pool}
	vista := &VistaPreviaOfertaHandler{DB: e.pool}
	router := chi.NewRouter()
	router.Post("/api/cotizaciones/{id}/enlace", enlaces.GenerarEnlace)
	router.Get("/api/publico/cotizacion/{token}", enlaces.VerCotizacion)
	router.Get("/api/cotizaciones/{id}/vista-previa-oferta", vista.Ver)
	return router
}

func generarEnlaceFijada(t *testing.T, router http.Handler, cotizacionID string) (token string, versionUsada *int) {
	t.Helper()
	rec := postCatalogos(t, router.ServeHTTP, "/api/cotizaciones/"+cotizacionID+"/enlace", map[string]any{"version": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("generar enlace: %d: %s", rec.Code, rec.Body.String())
	}
	var r struct {
		Token                 string `json:"token"`
		PlantillaVersionUsada *int   `json:"plantilla_version_usada"`
	}
	assertJSON(t, rec.Body.Bytes(), &r)
	if r.Token == "" {
		t.Fatalf("no se generó token: %s", rec.Body.String())
	}
	return r.Token, r.PlantillaVersionUsada
}

func getOfertaFijada(t *testing.T, router http.Handler, ruta string) respuestaOfertaFijada {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ruta, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d: %s", ruta, rec.Code, rec.Body.String())
	}
	var r respuestaOfertaFijada
	assertJSON(t, rec.Body.Bytes(), &r)
	if !r.OK || r.Plantilla == nil {
		t.Fatalf("GET %s: esperaba una plantilla renderizada: %s", ruta, rec.Body.String())
	}
	return r
}

// plantillaPublicadaDeCotizacion devuelve la versión Publicada hoy de la
// plantilla que usa el cotizador de la cotización.
func plantillaPublicadaDeCotizacion(t *testing.T, e *entornoPlantillasPrueba, cotizacionID string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(), `
		SELECT p.plantilla_id::text FROM plantillas p
		  JOIN plantilla_calculadoras pc ON pc.plantilla_id = p.plantilla_id
		  JOIN cotizaciones c ON c.calculadora_id = pc.calculadora_id
		 WHERE c.cotizacion_id = $1 AND p.estado = 'Publicada'`, cotizacionID).Scan(&id); err != nil {
		t.Fatalf("buscar la plantilla publicada: %v", err)
	}
	return id
}

// publicarVersionNueva crea una versión nueva de origenID con los endpoints
// reales (nueva-version → edición en Borrador → publicar), le pone
// tituloSeccion a todas sus secciones para poder distinguirla en el
// documento, aplica el estilo indicado (si hay) y devuelve su ID.
func publicarVersionNueva(t *testing.T, e *entornoPlantillasPrueba, origenID, tituloSeccion string, estilo map[string]any) string {
	t.Helper()
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/nueva-version",
		"/api/plantillas/"+origenID+"/nueva-version", e.plantillas.NuevaVersion, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("nueva versión: %d: %s", rec.Code, rec.Body.String())
	}
	nuevaID, _ := respuestaPlantilla(t, rec)["plantilla_id"].(string)
	if _, err := e.pool.Exec(context.Background(), `UPDATE plantilla_secciones SET nombre=$2, titulo=$2 WHERE plantilla_id::text=$1`, nuevaID, tituloSeccion); err != nil {
		t.Fatal(err)
	}
	if estilo != nil {
		recEstilo := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}/estilo",
			"/api/plantillas/"+nuevaID+"/estilo", e.estilo.Actualizar, estilo)
		if recEstilo.Code != http.StatusOK {
			t.Fatalf("estilo de la versión nueva: %d: %s", recEstilo.Code, recEstilo.Body.String())
		}
	}
	recPub := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/publicar",
		"/api/plantillas/"+nuevaID+"/publicar", e.plantillas.Publicar, nil)
	if recPub.Code != http.StatusOK {
		t.Fatalf("publicar la versión nueva: %d: %s", recPub.Code, recPub.Body.String())
	}
	return nuevaID
}

func tituloPrimeraSeccion(t *testing.T, p *plantillaRenderizada) string {
	t.Helper()
	if len(p.Secciones) == 0 {
		t.Fatalf("la plantilla %s v%d no trajo secciones", p.PlantillaID, p.Version)
	}
	return p.Secciones[0].Titulo
}

func columnaPlantillaUsada(t *testing.T, e *entornoPlantillasPrueba, cotizacionID string) (*string, *int) {
	t.Helper()
	var id *string
	var version *int
	if err := e.pool.QueryRow(context.Background(), `
		SELECT plantilla_id_usada::text, plantilla_version_usada FROM cotizacion_versiones
		 WHERE cotizacion_id=$1 AND numero_version=1`, cotizacionID).Scan(&id, &version); err != nil {
		t.Fatal(err)
	}
	return id, version
}

// Pruebas 8 y 9 de la ronda: enlace generado con la v2, se publica la v3,
// el MISMO enlace sigue en la v2; la Vista Previa resuelve en vivo mientras
// no hay enlace y pasa a la fijada apenas se genera.
func TestPlantillaFijada_EnlaceSigueEnLaVersionConLaQueSeGenero(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "Sí")
	e := &entornoPlantillasPrueba{pool: f.pool, plantillas: &PlantillasHandler{DB: f.pool}, estilo: &PlantillaEstiloHandler{DB: f.pool}}
	router := routerOfertaFijada(e)
	rutaVista := "/api/cotizaciones/" + f.cotizacionID + "/vista-previa-oferta?version=1"

	v1 := plantillaPublicadaDeCotizacion(t, e, f.cotizacionID)
	v2 := publicarVersionNueva(t, e, v1, "Contenido v2", nil)

	// Sin enlace todavía: la Vista Previa resuelve en vivo (v2) y no fija nada.
	antes := getOfertaFijada(t, router, rutaVista)
	if antes.Plantilla.Version != 2 || antes.Plantilla.Fijada {
		t.Fatalf("vista previa sin enlace: esperaba v2 en vivo, dio v%d fijada=%v", antes.Plantilla.Version, antes.Plantilla.Fijada)
	}
	if id, _ := columnaPlantillaUsada(t, e, f.cotizacionID); id != nil {
		t.Fatalf("la Vista Previa no debe fijar la plantilla, quedó %s", *id)
	}

	token, versionUsada := generarEnlaceFijada(t, router, f.cotizacionID)
	if versionUsada == nil || *versionUsada != 2 {
		t.Fatalf("generar enlace: plantilla_version_usada esperaba 2, dio %v", versionUsada)
	}
	idUsada, versionColumna := columnaPlantillaUsada(t, e, f.cotizacionID)
	if idUsada == nil || *idUsada != v2 || versionColumna == nil || *versionColumna != 2 {
		t.Fatalf("cotizacion_versiones debía quedar con la v2 (%s), quedó %v v%v", v2, idUsada, versionColumna)
	}

	v3 := publicarVersionNueva(t, e, v2, "Contenido v3", nil)
	var estadoV2 string
	if err := f.pool.QueryRow(context.Background(), `SELECT estado FROM plantillas WHERE plantilla_id::text=$1`, v2).Scan(&estadoV2); err != nil {
		t.Fatal(err)
	}
	if estadoV2 != "Archivada" {
		t.Fatalf("al publicar la v3 la v2 debía archivarse, quedó %s", estadoV2)
	}

	// Prueba central: el MISMO enlace sigue mostrando la v2.
	publica := getOfertaFijada(t, router, "/api/publico/cotizacion/"+token)
	if publica.Plantilla.PlantillaID != v2 || publica.Plantilla.Version != 2 || !publica.Plantilla.Fijada {
		t.Fatalf("el enlace debía seguir en la v2 fijada, dio %s v%d fijada=%v", publica.Plantilla.PlantillaID, publica.Plantilla.Version, publica.Plantilla.Fijada)
	}
	if titulo := tituloPrimeraSeccion(t, publica.Plantilla); titulo != "Contenido v2" {
		t.Fatalf("el enlace muestra %q, esperaba el contenido de la v2", titulo)
	}

	// La Vista Previa de esa cotización ya muestra lo mismo que el enlace.
	despues := getOfertaFijada(t, router, rutaVista)
	rawPublica, _ := json.Marshal(publica.Plantilla)
	rawVista, _ := json.Marshal(despues.Plantilla)
	if string(rawPublica) != string(rawVista) {
		t.Fatalf("preview y enlace deben resolver lo mismo sobre la fijada:\nenlace: %s\nvista:  %s", rawPublica, rawVista)
	}

	// Regenerar el enlace no vuelve a resolver: sigue la v2, no la v3.
	token2, versionUsada2 := generarEnlaceFijada(t, router, f.cotizacionID)
	if token2 != token || versionUsada2 == nil || *versionUsada2 != 2 {
		t.Fatalf("regenerar debía devolver el mismo token con la v2, dio %s v%v", token2, versionUsada2)
	}
	if idUsada, _ := columnaPlantillaUsada(t, e, f.cotizacionID); idUsada == nil || *idUsada != v2 {
		t.Fatalf("regenerar el enlace no debe cambiar la plantilla fijada, quedó %v", idUsada)
	}

	// Otra cotización del mismo cotizador, sin enlace: ve la v3 en vivo.
	otra, _, _ := crearCotizacionPrueba(t, f.pool, "Borrador", "", "")
	if _, err := f.pool.Exec(context.Background(), `
		UPDATE cotizaciones SET calculadora_id=(SELECT calculadora_id FROM cotizaciones WHERE cotizacion_id=$2), tipo_propuesta='COMERCIAL'
		 WHERE cotizacion_id=$1`, otra, f.cotizacionID); err != nil {
		t.Fatal(err)
	}
	enVivo := getOfertaFijada(t, router, "/api/cotizaciones/"+otra+"/vista-previa-oferta?version=1")
	if enVivo.Plantilla.PlantillaID != v3 || enVivo.Plantilla.Version != 3 || enVivo.Plantilla.Fijada {
		t.Fatalf("sin enlace, la vista previa debía resolver la v3 en vivo, dio %s v%d fijada=%v", enVivo.Plantilla.PlantillaID, enVivo.Plantilla.Version, enVivo.Plantilla.Fijada)
	}
	if titulo := tituloPrimeraSeccion(t, enVivo.Plantilla); titulo != "Contenido v3" {
		t.Fatalf("la vista previa en vivo muestra %q, esperaba el contenido de la v3", titulo)
	}
}

// Sin ninguna plantilla que aplique, generar el enlace no fija nada y la
// oferta sigue saliendo de las tabs crudas.
func TestPlantillaFijada_SinPlantillaNoFijaNada(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(context.Background(), `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}
	_, versionUsada := generarEnlaceFijada(t, routerOfertaFijada(e), cotizacionID)
	if versionUsada != nil {
		t.Fatalf("sin plantilla publicada no debía fijarse ninguna, dio v%d", *versionUsada)
	}
	if id, _ := columnaPlantillaUsada(t, e, cotizacionID); id != nil {
		t.Fatalf("sin plantilla publicada la columna debía quedar vacía, quedó %s", *id)
	}
}

// Prueba 10 de la ronda: el enlace trae el estilo de la plantilla usada.
// Con logo configurado viaja el logo y los colores; sin logo, viaja el
// nombre de la organización para mostrarlo en su lugar.
func TestPlantillaFijada_EnlaceTraeElEstiloDeLaPlantilla(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	f.fijarUsaTelefonia(t, "Sí")
	e := &entornoPlantillasPrueba{pool: f.pool, plantillas: &PlantillasHandler{DB: f.pool}, estilo: &PlantillaEstiloHandler{DB: f.pool}}
	router := routerOfertaFijada(e)

	// v1 sin estilo configurado: colores del tema, sin logo, con el nombre
	// de la organización asociada a la plantilla.
	sinLogo := getOfertaFijada(t, router, "/api/cotizaciones/"+f.cotizacionID+"/vista-previa-oferta?version=1").Plantilla.Estilo
	if sinLogo == nil {
		t.Fatal("la plantilla sin fila de estilo debía traer el estilo por defecto")
	}
	if sinLogo.LogoURL != nil {
		t.Fatalf("sin logo configurado no debía viajar logo_url, dio %s", *sinLogo.LogoURL)
	}
	if sinLogo.OrganizacionNombre != "Organización plantillas" {
		t.Fatalf("sin logo debía viajar el nombre de la organización, dio %q", sinLogo.OrganizacionNombre)
	}
	if sinLogo.ColorPrimario != "#0B2F63" || sinLogo.ColorAcento != "#20B8CD" {
		t.Fatalf("sin colores propios debía usar los del tema PROFESIONAL, dio %s / %s", sinLogo.ColorPrimario, sinLogo.ColorAcento)
	}

	v1 := plantillaPublicadaDeCotizacion(t, e, f.cotizacionID)
	logo := "https://example.com/logo-cliente.png"
	publicarVersionNueva(t, e, v1, "Con estilo", map[string]any{
		"tema": "EJECUTIVO", "color_primario": "#112233", "color_acento": "#AA5500",
		"fuente_titulos": "Georgia", "fuente_texto": "Verdana",
		"logo_url": logo, "logo_tamano": "GRANDE", "mostrar_logo": true,
		"mostrar_organizacion": true, "nombre_organizacion_visible": "Cliente Visible S.A.",
		"texto_encabezado": "Propuesta confidencial", "texto_pie": "Pie de prueba",
		"numerar_paginas": true, "marca_confidencial": true,
	})
	token, _ := generarEnlaceFijada(t, router, f.cotizacionID)
	estilo := getOfertaFijada(t, router, "/api/publico/cotizacion/"+token).Plantilla.Estilo
	if estilo == nil || estilo.LogoURL == nil || *estilo.LogoURL != logo || estilo.LogoTamano != "GRANDE" {
		t.Fatalf("el enlace debía traer el logo configurado: %+v", estilo)
	}
	if estilo.ColorPrimario != "#112233" || estilo.ColorAcento != "#AA5500" {
		t.Fatalf("colores propios no llegaron: %s / %s", estilo.ColorPrimario, estilo.ColorAcento)
	}
	if estilo.ColorSecundario != "#3D5A80" {
		t.Fatalf("el color secundario sin configurar debía salir del tema EJECUTIVO, dio %s", estilo.ColorSecundario)
	}
	if estilo.FuenteTitulos == nil || *estilo.FuenteTitulos != "Georgia" || estilo.FuenteTexto == nil || *estilo.FuenteTexto != "Verdana" {
		t.Fatalf("tipografía no llegó: %+v", estilo)
	}
	if estilo.OrganizacionNombre != "Cliente Visible S.A." || !estilo.MostrarOrganizacion {
		t.Fatalf("nombre visible de la organización no llegó: %q", estilo.OrganizacionNombre)
	}
	if estilo.TextoEncabezado == nil || estilo.TextoPie == nil || !estilo.NumerarPaginas || !estilo.MarcaConfidencial {
		t.Fatalf("encabezado/pie/numeración/confidencial no llegaron: %+v", estilo)
	}
}

// Con mostrar_logo apagado, un logo_url guardado no viaja: la cabecera
// muestra el nombre, nunca un logo que el diseñador decidió ocultar.
func TestPlantillaFijada_LogoOcultoNoViaja(t *testing.T) {
	f := crearFixtureRenderizadorPlantilla(t)
	e := &entornoPlantillasPrueba{pool: f.pool, plantillas: &PlantillasHandler{DB: f.pool}, estilo: &PlantillaEstiloHandler{DB: f.pool}}
	v1 := plantillaPublicadaDeCotizacion(t, e, f.cotizacionID)
	publicarVersionNueva(t, e, v1, "Logo oculto", map[string]any{
		"logo_url": "https://example.com/oculto.png", "mostrar_logo": false,
	})
	estilo := getOfertaFijada(t, routerOfertaFijada(e), "/api/cotizaciones/"+f.cotizacionID+"/vista-previa-oferta?version=1").Plantilla.Estilo
	if estilo == nil || estilo.LogoURL != nil {
		t.Fatalf("con mostrar_logo=false no debía viajar logo_url: %+v", estilo)
	}
	if estilo.OrganizacionNombre == "" {
		t.Fatal("sin logo debía viajar un nombre de organización")
	}
}
