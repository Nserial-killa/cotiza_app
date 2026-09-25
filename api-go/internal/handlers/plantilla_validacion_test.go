package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// fixtureDosCotizadores: una plantilla asociada a DOS cotizadores ("Cotizador
// plantillas" del entorno y "Automatización"), con un Campo vinculado y una
// Tabla de Inversión completos solo para el primero.
type fixtureDosCotizadores struct {
	e                         *entornoPlantillasPrueba
	plantillaID, seccionID    string
	otro                      string // calculadora_id de "Automatización"
	campoPropio, campoOtro    string
	bloqueTitulo, bloqueTabla string
}

func crearFixtureDosCotizadores(t *testing.T) fixtureDosCotizadores {
	t.Helper()
	e := nuevoEntornoPlantillas(t)
	ctx := context.Background()
	sufijo := sufijoUnico()
	otro := "TEST-CALC-AUTO-" + sufijo
	if _, err := e.pool.Exec(ctx, `INSERT INTO calculadoras (calculadora_id,nombre_calculadora,estado) VALUES ($1,'Automatización','Activo')`, otro); err != nil {
		t.Fatal(err)
	}
	campoOtro := "TEST-AUTO-EL-" + sufijo
	tabOtro := "TEST-AUTO-TAB-" + sufijo
	if _, err := e.pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo) VALUES ($1,$2,'Datos',1,true)`, tabOtro, otro); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo) VALUES ($1,$2,'CAMPO','Nombre del proyecto',1,'{}'::jsonb,true)`, campoOtro, tabOtro); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		e.pool.Exec(c, `DELETE FROM plantillas WHERE organizacion_id=$1`, e.organizacion)
		e.pool.Exec(c, `DELETE FROM tabs_cotizador WHERE calculadora_id=$1`, otro)
		e.pool.Exec(c, `DELETE FROM calculadoras WHERE calculadora_id=$1`, otro)
	})
	campoPropio := crearCampoTextoPrueba(t, e, "Nombre del proyecto")

	plantillaID := crearPlantillaPrueba(t, e, "Plantilla dos cotizadores", map[string]any{"calculadora_ids": []string{e.calculadora, otro}})
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Portada")
	bloqueTitulo := crearBloqueTipoPrueba(t, e, seccionID, "CAMPO_VINCULADO", map[string]any{"titulo": "Título de la propuesta"})
	bloqueTabla := crearBloqueTipoPrueba(t, e, seccionID, "TABLA_INVERSION", map[string]any{"titulo": "Detalle de la Inversión"})
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion", "/api/plantillas/bloques/"+bloqueTitulo+"/vinculacion",
		e.vinculaciones.Guardar, map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": campoPropio})
	if rec.Code != http.StatusOK {
		t.Fatalf("vincular: %d: %s", rec.Code, rec.Body.String())
	}
	columnas := &PlantillaTablaColumnasHandler{DB: e.pool}
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", "/api/plantillas/bloques/"+bloqueTabla+"/columnas",
		columnas.Agregar, map[string]any{"calculadora_id": e.calculadora, "titulo": "Total", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "total_precio"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("columna: %d: %s", rec.Code, rec.Body.String())
	}
	return fixtureDosCotizadores{e: e, plantillaID: plantillaID, seccionID: seccionID, otro: otro,
		campoPropio: campoPropio, campoOtro: campoOtro, bloqueTitulo: bloqueTitulo, bloqueTabla: bloqueTabla}
}

func (f fixtureDosCotizadores) validar(t *testing.T) resultadoValidacionPlantilla {
	t.Helper()
	rec := llamarPlantilla(t, http.MethodGet, "/api/plantillas/{id}/validacion", "/api/plantillas/"+f.plantillaID+"/validacion", f.e.plantillas.Validacion, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("validación: %d: %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		Validacion resultadoValidacionPlantilla `json:"validacion"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)
	return respuesta.Validacion
}

func (f fixtureDosCotizadores) publicar(t *testing.T) (int, map[string]any) {
	t.Helper()
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/publicar", "/api/plantillas/"+f.plantillaID+"/publicar", f.e.plantillas.Publicar, nil)
	var cuerpo map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cuerpo)
	return rec.Code, cuerpo
}

func buscarValidacion(validaciones []validacionPlantilla, codigo string) []validacionPlantilla {
	resultado := []validacionPlantilla{}
	for _, v := range validaciones {
		if v.Codigo == codigo {
			resultado = append(resultado, v)
		}
	}
	return resultado
}

func TestPlantillaValidacion_NombraElBloqueYElCotizadorQueFalta(t *testing.T) {
	f := crearFixtureDosCotizadores(t)
	if _, err := f.e.pool.Exec(context.Background(), `
		INSERT INTO plantilla_estilos (plantilla_id, mostrar_logo, logo_url) VALUES ($1::uuid, true, 'http://inseguro.example.com/logo.png')`, f.plantillaID); err != nil {
		t.Fatal(err)
	}
	v := f.validar(t)

	sinVincular := buscarValidacion(v.Validaciones, "BLOQUE_SIN_VINCULAR")
	if len(sinVincular) != 1 {
		t.Fatalf("esperaba exactamente un bloque sin vincular (solo falta en Automatización), hay %d: %+v", len(sinVincular), v.Validaciones)
	}
	m := sinVincular[0]
	if m.Nivel != "ERROR" || m.CalculadoraID != f.otro || m.BloqueID != f.bloqueTitulo ||
		m.Mensaje != "Falta vincular «Título de la propuesta» (Campo vinculado, sección «Portada») para «Automatización»." {
		t.Fatalf("el error debe nombrar el bloque y SOLO el cotizador que falta: %+v", m)
	}
	if strings.Contains(m.Mensaje, "Cotizador plantillas") {
		t.Fatalf("el cotizador ya vinculado no debe aparecer: %s", m.Mensaje)
	}

	sinColumnas := buscarValidacion(v.Validaciones, "TABLA_SIN_COLUMNAS")
	if len(sinColumnas) != 1 || sinColumnas[0].CalculadoraID != f.otro ||
		sinColumnas[0].Mensaje != "El componente «Detalle de la Inversión» (Tabla de Inversión, sección «Portada») no tiene ninguna columna visible para «Automatización»." {
		t.Fatalf("tabla sin columnas: %+v", sinColumnas)
	}
	logo := buscarValidacion(v.Validaciones, "ESTILO_LOGO_SIN_URL")
	if len(logo) != 1 || logo[0].Nivel != "ERROR" || logo[0].Mensaje != "El estilo solicita mostrar el logotipo, pero no existe una URL HTTPS configurada." {
		t.Fatalf("logo sin URL HTTPS: %+v", logo)
	}
	if v.PuedePublicar || v.Resumen.Errores != 3 {
		t.Fatalf("con 3 errores no debe poder publicarse: puede=%v errores=%d %+v", v.PuedePublicar, v.Resumen.Errores, v.Validaciones)
	}

	// Resumen con los números reales.
	r := v.Resumen
	if r.Secciones != 1 || r.Bloques != 2 || r.Vinculaciones != 2 || len(r.Cotizadores) != 2 ||
		r.Canales.Web != 2 || r.Canales.PDF != 2 || len(r.TiposPropuesta) != 1 || r.Estado != "Borrador" || r.Version != 1 {
		t.Fatalf("resumen inesperado: %+v", r)
	}

	// Publicar exige lo mismo que muestra el paso 5.
	codigo, cuerpo := f.publicar(t)
	if codigo != http.StatusBadRequest || !strings.Contains(cuerpo["error"].(string), "3 errores") || cuerpo["validacion"] == nil {
		t.Fatalf("publicar con errores: esperaba 400 con la validación, dio %d: %v", codigo, cuerpo)
	}

	// Corregir los tres y publicar.
	e := f.e
	if rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion", "/api/plantillas/bloques/"+f.bloqueTitulo+"/vinculacion",
		e.vinculaciones.Guardar, map[string]any{"calculadora_id": f.otro, "fuente_tipo": "CAMPO", "fuente_id": f.campoOtro}); rec.Code != http.StatusOK {
		t.Fatalf("vincular en Automatización: %d: %s", rec.Code, rec.Body.String())
	}
	columnas := &PlantillaTablaColumnasHandler{DB: e.pool}
	if rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", "/api/plantillas/bloques/"+f.bloqueTabla+"/columnas",
		columnas.Agregar, map[string]any{"calculadora_id": f.otro, "titulo": "Total", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "total_precio"}); rec.Code != http.StatusCreated {
		t.Fatalf("columna en Automatización: %d: %s", rec.Code, rec.Body.String())
	}
	if rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}/estilo", "/api/plantillas/"+f.plantillaID+"/estilo",
		e.estilo.Actualizar, map[string]any{"mostrar_logo": false}); rec.Code != http.StatusOK {
		t.Fatalf("apagar logo: %d: %s", rec.Code, rec.Body.String())
	}
	if v = f.validar(t); !v.PuedePublicar || v.Resumen.Errores != 0 {
		t.Fatalf("corregida, debería poder publicarse: %+v", v.Validaciones)
	}
	if codigo, cuerpo = f.publicar(t); codigo != http.StatusOK {
		t.Fatalf("publicar corregida: %d: %v", codigo, cuerpo)
	}
}

func TestPlantillaValidacion_FuentesViejasYAdvertencias(t *testing.T) {
	f := crearFixtureDosCotizadores(t)
	ctx := context.Background()
	// El campo vinculado en el cotizador propio se desactiva en el Diseñador.
	if _, err := f.e.pool.Exec(ctx, `UPDATE elementos_tab_cotizador SET activo=false WHERE elemento_id=$1`, f.campoPropio); err != nil {
		t.Fatal(err)
	}
	condiciones := crearBloqueTipoPrueba(t, f.e, f.seccionID, "CONDICIONES_COMERCIALES", nil)
	crearBloqueTipoPrueba(t, f.e, f.seccionID, "IMAGEN", map[string]any{"contenido": "logo.png", "mostrar_web": false, "mostrar_pdf": false})
	crearSeccionPrueba(t, f.e, f.plantillaID, "Vacía")
	v := f.validar(t)

	vieja := buscarValidacion(v.Validaciones, "FUENTE_INEXISTENTE")
	if len(vieja) != 1 || vieja[0].Nivel != "ERROR" || vieja[0].CalculadoraID != f.e.calculadora ||
		!strings.Contains(vieja[0].Mensaje, "«Título de la propuesta»") || !strings.Contains(vieja[0].Mensaje, "«Cotizador plantillas»") {
		t.Fatalf("fuente desactivada: %+v", vieja)
	}
	// Los 4 VALOR_FIJO pre-poblados de Condiciones comerciales nacen vacíos.
	sinValor := buscarValidacion(v.Validaciones, "CAMPO_SIN_VALOR")
	if len(sinValor) != 4 || sinValor[0].BloqueID != condiciones || !strings.Contains(sinValor[0].Mensaje, "«Validez de la propuesta»") {
		t.Fatalf("campos sin valor: %+v", sinValor)
	}
	for _, codigo := range []string{"IMAGEN_SIN_URL", "BLOQUE_OCULTO", "SECCION_VACIA", "SIN_PORTADA", "ESTILO_SIN_CONFIGURAR"} {
		if got := buscarValidacion(v.Validaciones, codigo); len(got) != 1 || got[0].Nivel != "ADVERTENCIA" {
			t.Fatalf("%s: esperaba una advertencia, dio %+v", codigo, got)
		}
	}
	if v.Resumen.Canales.Web != v.Resumen.Bloques-1 || v.Resumen.Canales.PDF != v.Resumen.Bloques-1 {
		t.Fatalf("la imagen oculta no cuenta en ningún canal: %+v", v.Resumen)
	}

	// La estructura sugerida sin editar se avisa.
	sugerida := crearPlantillaPrueba(t, f.e, "Plantilla sugerida sin editar", nil)
	if codigo, cuerpo := aplicarEstructuraSugeridaPrueba(f.e, t, sugerida); codigo != http.StatusCreated {
		t.Fatalf("estructura sugerida: %d %s", codigo, cuerpo)
	}
	resultado, err := validarPlantilla(ctx, f.e.pool, sugerida)
	if err != nil {
		t.Fatal(err)
	}
	if got := buscarValidacion(resultado.Validaciones, "TEXTO_DE_EJEMPLO"); len(got) != 8 { // Portada, Resumen y los 6 textos.
		t.Fatalf("textos de ejemplo: esperaba 8, dio %d: %+v", len(got), got)
	}
	if resultado.Resumen.Secciones != 12 || resultado.Resumen.Bloques != 18 {
		t.Fatalf("resumen de la estructura sugerida: %+v", resultado.Resumen)
	}
}

func TestPlantillaPublicada_QuedaBloqueadaYSeVersiona(t *testing.T) {
	f := crearFixtureDosCotizadores(t)
	e := f.e
	ctx := context.Background()
	if rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion", "/api/plantillas/bloques/"+f.bloqueTitulo+"/vinculacion",
		e.vinculaciones.Guardar, map[string]any{"calculadora_id": f.otro, "fuente_tipo": "CAMPO", "fuente_id": f.campoOtro}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	columnas := &PlantillaTablaColumnasHandler{DB: e.pool}
	if rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", "/api/plantillas/bloques/"+f.bloqueTabla+"/columnas",
		columnas.Agregar, map[string]any{"calculadora_id": f.otro, "titulo": "Total", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "total_precio"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	grupo := crearBloqueTipoPrueba(t, e, f.seccionID, "DATOS_CLIENTE", nil)
	if codigo, cuerpo := f.publicar(t); codigo != http.StatusOK {
		t.Fatalf("publicar: %d %v", codigo, cuerpo)
	}
	var campoID string
	if err := e.pool.QueryRow(ctx, `SELECT campo_id::text FROM plantilla_bloque_campos WHERE bloque_id::text=$1 LIMIT 1`, grupo).Scan(&campoID); err != nil {
		t.Fatal(err)
	}

	// Cualquier edición directa de la versión publicada: 409.
	campos := &PlantillaBloqueCamposHandler{DB: e.pool}
	for _, intento := range []struct {
		nombre, metodo, patron, ruta string
		handler                      http.HandlerFunc
		body                         any
	}{
		{"editar datos básicos", http.MethodPatch, "/api/plantillas/{id}", "/api/plantillas/" + f.plantillaID, e.plantillas.Editar, map[string]any{"nombre": "Otro"}},
		{"nueva sección", http.MethodPost, "/api/plantillas/{id}/secciones", "/api/plantillas/" + f.plantillaID + "/secciones", e.estructura.CrearSeccion, map[string]any{"nombre": "X"}},
		{"editar sección", http.MethodPatch, "/api/plantillas/secciones/{seccion_id}", "/api/plantillas/secciones/" + f.seccionID, e.estructura.EditarSeccion, map[string]any{"nombre": "X"}},
		{"nuevo bloque", http.MethodPost, "/api/plantillas/secciones/{seccion_id}/bloques", "/api/plantillas/secciones/" + f.seccionID + "/bloques", e.estructura.CrearBloque, map[string]any{"tipo_bloque": "TEXTO", "nombre_interno": "x"}},
		{"editar bloque", http.MethodPatch, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/" + f.bloqueTitulo, e.estructura.EditarBloque, map[string]any{"titulo": "X"}},
		{"eliminar bloque", http.MethodDelete, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/" + f.bloqueTitulo, e.estructura.EliminarBloque, nil},
		{"vinculación", http.MethodDelete, "/api/plantillas/bloques/{bloque_id}/vinculacion", "/api/plantillas/bloques/" + f.bloqueTitulo + "/vinculacion?calculadora_id=" + f.otro, e.vinculaciones.Eliminar, nil},
		{"columna", http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas", "/api/plantillas/bloques/" + f.bloqueTabla + "/columnas", columnas.Agregar, map[string]any{"calculadora_id": f.otro, "titulo": "Y", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "moneda"}},
		{"campo", http.MethodPatch, "/api/plantillas/campos/{campo_id}", "/api/plantillas/campos/" + campoID, campos.Editar, map[string]any{"etiqueta": "X"}},
		{"estilo", http.MethodPatch, "/api/plantillas/{id}/estilo", "/api/plantillas/" + f.plantillaID + "/estilo", e.estilo.Actualizar, map[string]any{"tema": "EJECUTIVO"}},
		{"estructura sugerida", http.MethodPost, "/api/plantillas/{id}/estructura-sugerida", "/api/plantillas/" + f.plantillaID + "/estructura-sugerida", e.estructura.AplicarEstructuraSugerida, nil},
		{"publicar de nuevo", http.MethodPost, "/api/plantillas/{id}/publicar", "/api/plantillas/" + f.plantillaID + "/publicar", e.plantillas.Publicar, nil},
	} {
		rec := llamarPlantilla(t, intento.metodo, intento.patron, intento.ruta, intento.handler, intento.body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s sobre la versión publicada: esperaba 409, dio %d: %s", intento.nombre, rec.Code, rec.Body.String())
		}
	}
	// Las banderas de disponibilidad no cambian el documento: sí se permiten.
	if rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}", "/api/plantillas/"+f.plantillaID, e.plantillas.Editar,
		map[string]any{"disponible_nuevas_propuestas": false}); rec.Code != http.StatusOK {
		t.Fatalf("bandera de disponibilidad: %d %s", rec.Code, rec.Body.String())
	}

	// Nueva versión: copia completa en Borrador, v2, mismo código.
	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/nueva-version", "/api/plantillas/"+f.plantillaID+"/nueva-version", e.plantillas.NuevaVersion, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("nueva versión: %d %s", rec.Code, rec.Body.String())
	}
	v2 := respuestaPlantilla(t, rec)["plantilla_id"].(string)
	contar := func(id string) (n [6]int) {
		t.Helper()
		if err := e.pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM plantilla_secciones WHERE plantilla_id::text=$1),
			       (SELECT count(*) FROM plantilla_bloques b JOIN plantilla_secciones s USING(seccion_id) WHERE s.plantilla_id::text=$1),
			       (SELECT count(*) FROM plantilla_vinculaciones v JOIN plantilla_bloques b USING(bloque_id) JOIN plantilla_secciones s USING(seccion_id) WHERE s.plantilla_id::text=$1),
			       (SELECT count(*) FROM plantilla_tabla_columnas c JOIN plantilla_bloques b USING(bloque_id) JOIN plantilla_secciones s USING(seccion_id) WHERE s.plantilla_id::text=$1),
			       (SELECT count(*) FROM plantilla_bloque_campos c JOIN plantilla_bloques b USING(bloque_id) JOIN plantilla_secciones s USING(seccion_id) WHERE s.plantilla_id::text=$1),
			       (SELECT count(*) FROM plantilla_calculadoras WHERE plantilla_id::text=$1)`, id).Scan(&n[0], &n[1], &n[2], &n[3], &n[4], &n[5]); err != nil {
			t.Fatal(err)
		}
		return
	}
	if a, b := contar(f.plantillaID), contar(v2); a != b || a[2] != 2 || a[3] != 2 {
		t.Fatalf("la versión nueva debe ser copia completa (secciones, bloques, vinculaciones, columnas, campos, cotizadores): v1=%v v2=%v", a, b)
	}
	var codigo1, codigo2, estado2 string
	var version2 int
	e.pool.QueryRow(ctx, `SELECT codigo FROM plantillas WHERE plantilla_id::text=$1`, f.plantillaID).Scan(&codigo1)
	e.pool.QueryRow(ctx, `SELECT codigo, estado, version FROM plantillas WHERE plantilla_id::text=$1`, v2).Scan(&codigo2, &estado2, &version2)
	if codigo1 != codigo2 || estado2 != "Borrador" || version2 != 2 {
		t.Fatalf("v2: codigo %s/%s estado %s version %d", codigo1, codigo2, estado2, version2)
	}
	// Un solo borrador por plantilla.
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/nueva-version", "/api/plantillas/"+f.plantillaID+"/nueva-version", e.plantillas.NuevaVersion, nil)
	if rec.Code != http.StatusConflict || respuestaPlantilla(t, rec)["plantilla_id"] != v2 {
		t.Fatalf("segundo borrador: esperaba 409 con el ID del existente, dio %d %s", rec.Code, rec.Body.String())
	}

	// La v2 sí se edita, y la v1 publicada no cambia.
	var bloqueV2 string
	e.pool.QueryRow(ctx, `SELECT b.bloque_id::text FROM plantilla_bloques b JOIN plantilla_secciones s USING(seccion_id) WHERE s.plantilla_id::text=$1 AND b.tipo_bloque='CAMPO_VINCULADO'`, v2).Scan(&bloqueV2)
	if rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/"+bloqueV2,
		e.estructura.EditarBloque, map[string]any{"titulo": "Título nuevo"}); rec.Code != http.StatusOK {
		t.Fatalf("editar v2: %d %s", rec.Code, rec.Body.String())
	}
	var tituloV1 string
	e.pool.QueryRow(ctx, `SELECT titulo FROM plantilla_bloques WHERE bloque_id::text=$1`, f.bloqueTitulo).Scan(&tituloV1)
	if tituloV1 != "Título de la propuesta" {
		t.Fatalf("editar la v2 tocó la v1: %q", tituloV1)
	}

	// Publicar la v2 archiva la v1.
	f2 := f
	f2.plantillaID = v2
	if codigo, cuerpo := f2.publicar(t); codigo != http.StatusOK || !strings.Contains(cuerpo["mensaje"].(string), "archivada") {
		t.Fatalf("publicar v2: %d %v", codigo, cuerpo)
	}
	var estado1 string
	e.pool.QueryRow(ctx, `SELECT estado FROM plantillas WHERE plantilla_id::text=$1`, f.plantillaID).Scan(&estado1)
	var publicadas int
	e.pool.QueryRow(ctx, `SELECT count(*) FROM plantillas WHERE codigo=$1 AND estado='Publicada'`, codigo1).Scan(&publicadas)
	if estado1 != "Archivada" || publicadas != 1 {
		t.Fatalf("v1 debería quedar Archivada y una sola Publicada: v1=%s publicadas=%d", estado1, publicadas)
	}
	if rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/bloques/{bloque_id}", "/api/plantillas/bloques/"+f.bloqueTitulo,
		e.estructura.EditarBloque, map[string]any{"titulo": "X"}); rec.Code != http.StatusConflict {
		t.Fatalf("una versión archivada tampoco se edita: %d", rec.Code)
	}

	// Un borrador v3 se puede descartar aunque el cotizador ya tenga cotizaciones.
	cotizacionID, _, _ := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(ctx, `UPDATE cotizaciones SET calculadora_id=$1 WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/{id}/nueva-version", "/api/plantillas/"+v2+"/nueva-version", e.plantillas.NuevaVersion, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("v3: %d %s", rec.Code, rec.Body.String())
	}
	v3 := respuestaPlantilla(t, rec)["plantilla_id"].(string)
	if rec := llamarPlantilla(t, http.MethodDelete, "/api/plantillas/{id}", "/api/plantillas/"+v3, e.plantillas.Eliminar, nil); rec.Code != http.StatusOK {
		t.Fatalf("descartar el borrador v3: %d %s", rec.Code, rec.Body.String())
	}
}
