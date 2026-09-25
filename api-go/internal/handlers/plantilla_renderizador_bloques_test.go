package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Costo y margen con valores imposibles de confundir con cualquier otro
// número del documento: si aparecen en el JSON renderizado, se filtraron.
const (
	costoInternoFuga = 4321.09
	margenFuga       = 37.25
)

// fixtureCatorceBloques arma una plantilla Publicada con los 14 tipos de la
// paleta, cada uno configurado como lo haría el equipo (campos, columnas,
// vinculación), y una cotización real del mismo cotizador con contacto,
// valores, 2 opciones de propuesta y un ítem elegido de la Lista de Precios.
type fixtureCatorceBloques struct {
	e            *entornoPlantillasPrueba
	cotizacionID string
	codigoOferta string
	listaID      string
	itemElegido  string
	plantillaID  string
	// configLista es la configuración del elemento LISTA_PRECIOS dentro de
	// la estructura compilada; las pruebas pueden alterarla antes de
	// publicar el compilado.
	configLista map[string]any
}

func crearFixtureCatorceBloques(t *testing.T, contaminarCompilado bool) fixtureCatorceBloques {
	t.Helper()
	e := nuevoEntornoPlantillas(t)
	ctx := context.Background()
	sufijo := sufijoUnico()
	tabID := "TEST-P1-TAB-" + sufijo
	tipoAgenteID := "TEST-P1-TIPO-" + sufijo
	listaID := "TEST-P1-LISTA-" + sufijo
	padreID := "TEST-P1-PADRE-" + sufijo
	precioID := "TEST-P1-PRECIO-" + sufijo
	if _, err := e.pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo) VALUES ($1,$2,'Configuración',1,true)`,
		tabID, e.calculadora); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
		VALUES ($1,$5,'CAMPO','Tipo de agente',1,'{"tipo_campo":"TEXTO","nombre_interno":"TIPO_AGENTE"}'::jsonb,true),
		       ($2,$5,'LISTA_PRECIOS','Planes',2,'{"tipo_lista_precios":"UNICA"}'::jsonb,true),
		       ($3,$5,'OPCIONES_PROPUESTA','Escenarios',3,'{}'::jsonb,true),
		       ($4,$5,'CAMPO','Precio',4,'{"tipo_campo":"MONEDA"}'::jsonb,true)`,
		tipoAgenteID, listaID, padreID, precioID, tabID); err != nil {
		t.Fatal(err)
	}
	// Ítems con costo y margen REALES en la base: lo que se prueba es que
	// nunca salen de ahí.
	var itemElegido, itemOtro string
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO lista_precios_items (elemento_id,codigo,nombre,descripcion,precio,moneda,unidad_cobro,costo_interno,margen_porcentaje,orden)
		VALUES ($1,'AG-STD','Agente estándar','Hasta 5.000 conversaciones',1500,'US$','Mes',$2,$3,1)
		RETURNING item_id::text`, listaID, costoInternoFuga, margenFuga).Scan(&itemElegido); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO lista_precios_items (elemento_id,codigo,nombre,precio,moneda,costo_interno,margen_porcentaje,orden)
		VALUES ($1,'AG-PRO','Agente pro',2500,'US$',$2,$3,2)
		RETURNING item_id::text`, listaID, costoInternoFuga, margenFuga).Scan(&itemOtro); err != nil {
		t.Fatal(err)
	}

	// La configuración compilada de la Lista de Precios sale del MISMO
	// mecanismo que usa el compilador real (incluirItemsListaPrecios), no
	// armada a mano.
	resultado := resultadoValidacion{Tabs: []tabCompilado{{TabID: tabID, Elementos: []elementoCompilado{{
		ElementoID: listaID, Tipo: "LISTA_PRECIOS", Configuracion: map[string]any{"tipo_lista_precios": "UNICA"},
	}}}}}
	if err := (&CompiladorHandler{DB: e.pool}).incluirItemsListaPrecios(ctx, &resultado); err != nil {
		t.Fatal(err)
	}
	configLista := resultado.Tabs[0].Elementos[0].Configuracion
	if contaminarCompilado {
		// Simula una estructura compilada que SÍ trae costo y margen (un bug
		// futuro del compilador, o un snapshot viejo): el renderizador
		// igual no debe dejarlos pasar.
		for _, item := range configLista["items"].([]map[string]any) {
			item["costo_interno"] = costoInternoFuga
			item["margen_porcentaje"] = margenFuga
		}
	}

	estructura := map[string]any{
		"calculadora_id": e.calculadora, "version": 1,
		"tabs": []any{map[string]any{
			"tab_id": tabID, "nombre": "Configuración", "alcance": "PROPIO", "orden": 1,
			"elementos": []any{
				map[string]any{"elemento_id": tipoAgenteID, "tipo": "CAMPO", "etiqueta": "Tipo de agente", "orden": 1,
					"configuracion": map[string]any{"tipo_campo": "TEXTO", "nombre_interno": "TIPO_AGENTE"}},
				map[string]any{"elemento_id": listaID, "tipo": "LISTA_PRECIOS", "etiqueta": "Planes", "orden": 2,
					"configuracion": configLista},
				map[string]any{"elemento_id": padreID, "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Escenarios", "orden": 3,
					"configuracion": map[string]any{},
					"hijos": []any{map[string]any{"elemento_id": precioID, "tipo": "CAMPO", "etiqueta": "Precio", "orden": 4,
						"configuracion": map[string]any{"tipo_campo": "MONEDA"}}}},
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

	// ---- Plantilla con los 14 tipos ----
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla producción Exceltec", nil)
	portada := crearSeccionPrueba(t, e, plantillaID, "Portada")
	cuerpo := crearSeccionPrueba(t, e, plantillaID, "Propuesta")
	campos := &PlantillaBloqueCamposHandler{DB: e.pool}
	columnas := &PlantillaTablaColumnasHandler{DB: e.pool}
	agregarColumna := func(bloqueID string, body map[string]any) {
		t.Helper()
		body["calculadora_id"] = e.calculadora
		rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/columnas",
			"/api/plantillas/bloques/"+bloqueID+"/columnas", columnas.Agregar, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("agregar columna %v: %d: %s", body["titulo"], rec.Code, rec.Body.String())
		}
	}

	crearBloqueTipoPrueba(t, e, portada, "PORTADA", map[string]any{
		"titulo": "Propuesta de Agentes IA", "contenido": "Solución a la medida\n• Atención 24/7\n• Integración con [TIPO_AGENTE]",
	})
	crearBloqueTipoPrueba(t, e, portada, "SALTO_PAGINA", map[string]any{"titulo": "Salto de página"})
	crearBloqueTipoPrueba(t, e, cuerpo, "ENCABEZADO", map[string]any{"titulo": "1. Solución propuesta"})
	crearBloqueTipoPrueba(t, e, cuerpo, "TEXTO", map[string]any{"contenido": "Se propone un agente de [TIPO_AGENTE]."})
	crearBloqueTipoPrueba(t, e, cuerpo, "IMAGEN", map[string]any{"contenido": "https://example.com/diagrama.png"})
	crearBloqueTipoPrueba(t, e, cuerpo, "DATOS_CLIENTE", map[string]any{"titulo": "Datos del cliente"})
	crearBloqueTipoPrueba(t, e, cuerpo, "RESUMEN_EJECUTIVO", map[string]any{"contenido": "Resumen: agente de [TIPO_AGENTE]."})
	inversionID := crearBloqueTipoPrueba(t, e, cuerpo, "TABLA_INVERSION", nil)
	agregarColumna(inversionID, map[string]any{"titulo": "Total", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "total_precio"})
	datosID := crearBloqueTipoPrueba(t, e, cuerpo, "TABLA_DATOS", nil)
	agregarColumna(datosID, map[string]any{"titulo": "Tipo de agente", "fuente_tipo": "CAMPO", "fuente_id": tipoAgenteID})
	agregarColumna(datosID, map[string]any{"titulo": "Moneda", "fuente_tipo": "COTIZACION_BASE", "fuente_id": "moneda"})
	opcionesID := crearBloqueTipoPrueba(t, e, cuerpo, "OPCIONES_PROPUESTA", nil)
	agregarColumna(opcionesID, map[string]any{"titulo": "Precio", "fuente_tipo": "CAMPO", "fuente_id": precioID})
	listaBloqueID := crearBloqueTipoPrueba(t, e, cuerpo, "LISTA_PRECIOS", map[string]any{"titulo": "Planes disponibles"})
	grupoID := crearBloqueTipoPrueba(t, e, cuerpo, "GRUPO_INFORMACION", map[string]any{"titulo": "Configuración"})
	agregarCampoBloquePrueba(t, campos, grupoID, map[string]any{"etiqueta": "Tipo de agente", "fuente_tipo": "CAMPO", "fuente_id": tipoAgenteID, "calculadora_id": e.calculadora})
	agregarCampoBloquePrueba(t, campos, grupoID, map[string]any{"etiqueta": "Idioma", "fuente_tipo": "VALOR_FIJO", "valor_fijo": "Español"})
	agregarCampoBloquePrueba(t, campos, grupoID, map[string]any{"etiqueta": "Cantidad", "fuente_tipo": "VALOR_FIJO", "valor_fijo": "3"})
	condicionesID := crearBloqueTipoPrueba(t, e, cuerpo, "CONDICIONES_COMERCIALES", nil)
	crearBloqueTipoPrueba(t, e, cuerpo, "FIRMA_ACEPTACION", nil)

	// Validez de la propuesta (primer campo pre-poblado) se llena después.
	b := buscarBloqueDetalle(detalleBloquesPrueba(t, e, plantillaID), condicionesID)
	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/campos/{campo_id}", "/api/plantillas/campos/"+b.Campos[0].CampoID,
		campos.Editar, map[string]any{"valor_fijo": "30 días naturales"})
	if rec.Code != http.StatusOK {
		t.Fatalf("llenar validez: %d: %s", rec.Code, rec.Body.String())
	}

	// La Lista de precios solo se vincula a un elemento LISTA_PRECIOS.
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion",
		"/api/plantillas/bloques/"+listaBloqueID+"/vinculacion", e.vinculaciones.Guardar,
		map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": tipoAgenteID})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Lista de Precios") {
		t.Fatalf("vincular Lista de precios a un CAMPO de texto: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	rec = llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion",
		"/api/plantillas/bloques/"+listaBloqueID+"/vinculacion", e.vinculaciones.Guardar,
		map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "CAMPO", "fuente_id": listaID})
	if rec.Code != http.StatusOK {
		t.Fatalf("vincular Lista de precios: %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := e.pool.Exec(ctx, `UPDATE plantillas SET estado='Publicada' WHERE plantilla_id::text=$1`, plantillaID); err != nil {
		t.Fatal(err)
	}

	// ---- Cotización ----
	cotizacionID, _, clienteID := crearCotizacionPrueba(t, e.pool, "Borrador", "", "")
	if _, err := e.pool.Exec(ctx, `UPDATE cotizaciones SET calculadora_id=$1, tipo_propuesta='COMERCIAL' WHERE cotizacion_id=$2`, e.calculadora, cotizacionID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO cliente_contactos (contacto_id,cliente_id,nombre,cargo,correo,telefono,contacto_principal,estado)
		VALUES ($1,$2,'Ana Rojas','Gerente de TI','ana.rojas@example.com','+506 8888-0000',true,'Activo')`,
		"TEST-P1-CONT-"+sufijo, clienteID); err != nil {
		t.Fatal(err)
	}
	guardarValor := func(elementoID string, opcionID any, valor any) {
		t.Helper()
		raw, _ := json.Marshal(valor)
		if _, err := e.pool.Exec(ctx, `INSERT INTO cotizacion_valores (cotizacion_id,version,elemento_id,opcion_id,valor) VALUES ($1,1,$2,$3,$4)`,
			cotizacionID, elementoID, opcionID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	guardarValor(tipoAgenteID, nil, "Ventas")
	guardarValor(listaID, nil, map[string]any{"item_id": itemElegido})
	for i, o := range []struct {
		nombre      string
		precio      string
		recomendada bool
	}{{"Básico", "1000.00", false}, {"Completo", "2000.00", true}} {
		var opcionID string
		if err := e.pool.QueryRow(ctx, `
			INSERT INTO cotizacion_opciones (cotizacion_id,numero_version,elemento_padre_id,nombre,es_recomendada,orden)
			VALUES ($1,1,$2,$3,$4,$5) RETURNING opcion_id`, cotizacionID, padreID, o.nombre, o.recomendada, i+1).Scan(&opcionID); err != nil {
			t.Fatal(err)
		}
		guardarValor(precioID, opcionID, o.precio)
	}
	_ = itemOtro

	return fixtureCatorceBloques{
		e: e, cotizacionID: cotizacionID, codigoOferta: "CTZ-PRUEBA-" + cotizacionID, listaID: listaID,
		itemElegido: itemElegido, plantillaID: plantillaID, configLista: configLista,
	}
}

func camposComoMapa(t *testing.T, b *bloqueRenderizado) map[string]any {
	t.Helper()
	resultado := make(map[string]any, len(b.Campos))
	for _, c := range b.Campos {
		resultado[c.Etiqueta] = c.Valor
	}
	return resultado
}

func TestPlantillaRenderizador_LosCatorceTiposSeResuelven(t *testing.T) {
	f := crearFixtureCatorceBloques(t, false)
	plantilla, err := renderizarPlantillaCotizacion(context.Background(), f.e.pool, f.cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plantilla == nil || plantilla.PlantillaID != f.plantillaID {
		t.Fatalf("esperaba la plantilla publicada %s, dio %+v", f.plantillaID, plantilla)
	}
	orden := make([]string, 0)
	for _, s := range plantilla.Secciones {
		for _, b := range s.Bloques {
			orden = append(orden, b.TipoBloque)
		}
	}
	esperado := []string{"PORTADA", "SALTO_PAGINA", "ENCABEZADO", "TEXTO", "IMAGEN", "DATOS_CLIENTE", "RESUMEN_EJECUTIVO",
		"TABLA_INVERSION", "TABLA_DATOS", "OPCIONES_PROPUESTA", "LISTA_PRECIOS", "GRUPO_INFORMACION",
		"CONDICIONES_COMERCIALES", "FIRMA_ACEPTACION"}
	if strings.Join(orden, ",") != strings.Join(esperado, ",") {
		t.Fatalf("bloques renderizados:\n  %v\nesperados:\n  %v", orden, esperado)
	}

	portada := bloquePorTipo(plantilla.Secciones, "PORTADA")
	if portada.Titulo != "Propuesta de Agentes IA" || !strings.Contains(portada.Contenido, "• Integración con Ventas") {
		t.Fatalf("portada: título/viñetas no resueltos: %+v", portada)
	}
	pc := camposComoMapa(t, portada)
	if pc["Cliente"] != "Cliente de prueba" || pc["Contacto"] != "Ana Rojas" || pc["Número de oferta"] != f.codigoOferta || pc["Fecha"] == nil {
		t.Fatalf("portada: campos inesperados: %+v", pc)
	}

	salto := bloquePorTipo(plantilla.Secciones, "SALTO_PAGINA")
	if salto.Titulo != "" || salto.Contenido != "" || salto.Campos != nil || salto.Columnas != nil || salto.Items != nil {
		t.Fatalf("SALTO_PAGINA no debe traer nada visible: %+v", salto)
	}
	if b := bloquePorTipo(plantilla.Secciones, "ENCABEZADO"); b.Titulo != "1. Solución propuesta" {
		t.Fatalf("encabezado: %+v", b)
	}
	if b := bloquePorTipo(plantilla.Secciones, "TEXTO"); b.Contenido != "Se propone un agente de Ventas." {
		t.Fatalf("texto: %q", b.Contenido)
	}
	if b := bloquePorTipo(plantilla.Secciones, "RESUMEN_EJECUTIVO"); b.Contenido != "Resumen: agente de Ventas." {
		t.Fatalf("resumen ejecutivo: %q", b.Contenido)
	}
	if b := bloquePorTipo(plantilla.Secciones, "IMAGEN"); b.Contenido != "https://example.com/diagrama.png" {
		t.Fatalf("imagen: %q", b.Contenido)
	}

	dc := camposComoMapa(t, bloquePorTipo(plantilla.Secciones, "DATOS_CLIENTE"))
	if dc["Nombre del contacto"] != "Ana Rojas" || dc["Empresa"] != "Cliente de Prueba S.A." || dc["Correo"] != "ana.rojas@example.com" ||
		dc["Teléfono"] != "+506 8888-0000" || dc["Cargo"] != "Gerente de TI" {
		t.Fatalf("datos del cliente: %+v", dc)
	}

	inversion := bloquePorTipo(plantilla.Secciones, "TABLA_INVERSION")
	if len(inversion.Filas) != 1 || fmt.Sprint(inversion.Filas[0][0]) != "1000" {
		t.Fatalf("tabla de inversión: %+v", inversion)
	}
	datos := bloquePorTipo(plantilla.Secciones, "TABLA_DATOS")
	if strings.Join(datos.Columnas, ",") != "Tipo de agente,Moneda" || len(datos.Filas) != 1 ||
		datos.Filas[0][0] != "Ventas" || datos.Filas[0][1] != "US$" {
		t.Fatalf("tabla de datos: %+v", datos)
	}
	opciones := bloquePorTipo(plantilla.Secciones, "OPCIONES_PROPUESTA")
	if strings.Join(opciones.Columnas, ",") != "Escenario,Recomendada,Precio" || len(opciones.Filas) != 2 {
		t.Fatalf("opciones de propuesta: %+v", opciones)
	}
	if opciones.Filas[0][0] != "Básico" || opciones.Filas[1][0] != "Completo" || opciones.Filas[1][1] != true ||
		fmt.Sprint(opciones.Filas[0][2]) == fmt.Sprint(opciones.Filas[1][2]) {
		t.Fatalf("cada escenario debe traer su nombre, recomendada y SU precio: %+v", opciones.Filas)
	}

	lista := bloquePorTipo(plantilla.Secciones, "LISTA_PRECIOS")
	if len(lista.Items) != 2 {
		t.Fatalf("lista de precios: esperaba los 2 ítems activos, dio %+v", lista.Items)
	}
	if !lista.Items[0].Seleccionado || lista.Items[1].Seleccionado || lista.Items[0].Codigo != "AG-STD" ||
		lista.Items[0].Precio != 1500 || lista.Items[0].Moneda != "US$" || lista.Items[0].Descripcion == "" {
		t.Fatalf("lista de precios: ítems inesperados: %+v", lista.Items)
	}

	grupo := camposComoMapa(t, bloquePorTipo(plantilla.Secciones, "GRUPO_INFORMACION"))
	if grupo["Tipo de agente"] != "Ventas" || grupo["Idioma"] != "Español" || grupo["Cantidad"] != "3" {
		t.Fatalf("grupo de información: %+v", grupo)
	}
	cc := camposComoMapa(t, bloquePorTipo(plantilla.Secciones, "CONDICIONES_COMERCIALES"))
	if cc["Validez de la propuesta"] != "30 días naturales" || cc["Moneda"] != "US$" {
		t.Fatalf("condiciones comerciales: %+v", cc)
	}
	if v, existe := cc["Forma de pago"]; !existe || v != nil {
		t.Fatalf("un VALOR_FIJO sin llenar debe llegar como null (se muestra '—'), nunca inventado: %+v", cc)
	}
	firma := bloquePorTipo(plantilla.Secciones, "FIRMA_ACEPTACION")
	fc := camposComoMapa(t, firma)
	if !strings.Contains(firma.Contenido, "acepta") || fc["Nombre"] != "Ana Rojas" || fc["Cargo"] != "Gerente de TI" {
		t.Fatalf("firma y aceptación: %+v", firma)
	}
}

// verificarSinCostoNiMargen revisa el JSON tal como sale al cliente (el
// mismo json.Marshal que usa escribirJSON), no solo los structs: una clave
// nueva agregada sin pensar también se detectaría acá.
func verificarSinCostoNiMargen(t *testing.T, plantilla *plantillaRenderizada) {
	t.Helper()
	raw, err := json.Marshal(plantilla)
	if err != nil {
		t.Fatal(err)
	}
	texto := string(raw)
	for _, prohibido := range []string{"costo", "margen", fmt.Sprint(costoInternoFuga), fmt.Sprint(margenFuga)} {
		if strings.Contains(texto, prohibido) {
			t.Fatalf("la propuesta renderizada contiene %q — costo/margen interno filtrado al cliente:\n%s", prohibido, texto)
		}
	}
	if b := bloquePorTipo(plantilla.Secciones, "LISTA_PRECIOS"); b == nil || len(b.Items) == 0 {
		t.Fatal("la prueba no sirve si la lista de precios no trae ítems")
	}
}

func TestPlantillaRenderizador_ListaPreciosNuncaIncluyeCostoNiMargen(t *testing.T) {
	f := crearFixtureCatorceBloques(t, false)
	// Primera barrera: el compilado (incluirItemsListaPrecios, el mecanismo
	// del Diseñador) ya viene sin costo ni margen aunque la base los tenga.
	for _, item := range f.configLista["items"].([]map[string]any) {
		for clave := range item {
			if strings.Contains(clave, "costo") || strings.Contains(clave, "margen") {
				t.Fatalf("incluirItemsListaPrecios dejó pasar %q al compilado: %+v", clave, item)
			}
		}
	}
	plantilla, err := renderizarPlantillaCotizacion(context.Background(), f.e.pool, f.cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	verificarSinCostoNiMargen(t, plantilla)
}

func TestPlantillaRenderizador_ListaPreciosNoFiltraCostoAunqueElCompiladoLoTraiga(t *testing.T) {
	// Segunda barrera: aunque la estructura compilada trajera costo y margen
	// (bug futuro, snapshot viejo), el bloque los descarta.
	f := crearFixtureCatorceBloques(t, true)
	plantilla, err := renderizarPlantillaCotizacion(context.Background(), f.e.pool, f.cotizacionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	verificarSinCostoNiMargen(t, plantilla)
}
