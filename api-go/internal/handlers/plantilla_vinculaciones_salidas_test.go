package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// fijarMapaSalidaPlantillaPrueba agrega (o reemplaza) una fila de mapa_salidas_cotizador.
// La fuente da igual para estas pruebas: el valor que se prueba es el ya
// persistido en cotizacion_salidas.
func fijarMapaSalidaPlantillaPrueba(t *testing.T, e *entornoPlantillasPrueba, calculadoraID, clave string, activo bool) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO mapa_salidas_cotizador (calculadora_id,clave_salida,tipo_fuente,fuente_id,activo)
		VALUES ($1,$2,'CAMPO','TEST-FUENTE',$3)
		ON CONFLICT (calculadora_id,clave_salida) DO UPDATE SET activo=EXCLUDED.activo`,
		calculadoraID, clave, activo); err != nil {
		t.Fatal(err)
	}
}

// Un cotizador de 24 campos en 4 secciones (más ruido que NO debe aparecer:
// un campo inactivo, una sección inactiva y un elemento no vinculable).
func TestPlantillaVinculaciones_FuentesCompletasAgrupadasPorSeccionYConSalidas(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	ctx := context.Background()
	sufijo := sufijoUnico()
	secciones := []string{"Datos generales", "Canales", "Capacidad", "Inversión"}
	tipos := []string{"CAMPO", "CAMPO_CATALOGO", "CAMPO_CALCULADO", "CAMPO", "LISTA_PRECIOS", "TABLA"}
	esperados := make([]string, 0, 24)
	for i, nombre := range secciones {
		tabID := fmt.Sprintf("TEST-P3-TAB%d-%s", i, sufijo)
		if _, err := e.pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo) VALUES ($1,$2,$3,$4,true)`,
			tabID, e.calculadora, nombre, i+1); err != nil {
			t.Fatal(err)
		}
		for j, tipo := range tipos {
			id := fmt.Sprintf("TEST-P3-EL%d%d-%s", i, j, sufijo)
			if _, err := e.pool.Exec(ctx, `
				INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
				VALUES ($1,$2,$3,$4,$5,'{}'::jsonb,true)`, id, tabID, tipo, fmt.Sprintf("%s %d", nombre, j+1), j+1); err != nil {
				t.Fatal(err)
			}
			esperados = append(esperados, id)
		}
		// Ruido dentro de cada sección.
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo)
			VALUES ($1,$3,'CAMPO','Inactivo',90,'{}'::jsonb,false), ($2,$3,'TITULO','Un título',91,'{}'::jsonb,true)`,
			fmt.Sprintf("TEST-P3-INACT%d-%s", i, sufijo), fmt.Sprintf("TEST-P3-TIT%d-%s", i, sufijo), tabID); err != nil {
			t.Fatal(err)
		}
	}
	tabInactivo := "TEST-P3-TABX-" + sufijo
	if _, err := e.pool.Exec(ctx, `INSERT INTO tabs_cotizador (tab_id,calculadora_id,nombre,orden,activo) VALUES ($1,$2,'Oculta',9,false)`, tabInactivo, e.calculadora); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO elementos_tab_cotizador (elemento_id,tab_id,tipo,etiqueta,orden,configuracion,activo) VALUES ($1,$2,'CAMPO','En sección oculta',1,'{}'::jsonb,true)`,
		"TEST-P3-OCULTO-"+sufijo, tabInactivo); err != nil {
		t.Fatal(err)
	}
	// Salidas: dos públicas activas, una pública INACTIVA, y dos internas
	// activas que nunca deben ofrecerse.
	for _, s := range []struct {
		clave  string
		activo bool
	}{{"MONEDA", true}, {"TOTAL_PRECIO", true}, {"SUBTOTAL", false}, {"TOTAL_COSTO", true}, {"MARGEN_TOTAL", true}} {
		fijarMapaSalidaPlantillaPrueba(t, e, e.calculadora, s.clave, s.activo)
	}

	plantillaID := crearPlantillaPrueba(t, e, "Plantilla fuentes P3", nil)
	rec := llamarPlantilla(t, http.MethodGet, "/api/plantillas/{id}/fuentes",
		"/api/plantillas/"+plantillaID+"/fuentes?calculadora_id="+url.QueryEscape(e.calculadora), e.vinculaciones.Fuentes, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("fuentes: %d: %s", rec.Code, rec.Body.String())
	}
	var respuesta struct {
		Campos  []fuentePlantilla `json:"campos"`
		Salidas []fuentePlantilla `json:"salidas"`
		Base    []fuentePlantilla `json:"datos_cotizacion"`
		Fuentes []fuentePlantilla `json:"fuentes"`
	}
	assertJSON(t, rec.Body.Bytes(), &respuesta)

	if len(respuesta.Campos) != 24 {
		t.Fatalf("esperaba los 24 campos activos, llegaron %d", len(respuesta.Campos))
	}
	vistas := map[string]bool{}
	for i, c := range respuesta.Campos {
		if c.FuenteID != esperados[i] {
			t.Fatalf("campo %d: esperaba %s (orden de sección y elemento), dio %s", i, esperados[i], c.FuenteID)
		}
		seccion := secciones[i/len(tipos)]
		if c.TabNombre == nil || *c.TabNombre != seccion || c.TabID == nil || c.TipoElemento == nil {
			t.Fatalf("%s: debe traer su sección %q, tab_id y tipo: %+v", c.FuenteID, seccion, c)
		}
		if *c.TipoElemento != tipos[i%len(tipos)] {
			t.Fatalf("%s: tipo %s, esperaba %s", c.FuenteID, *c.TipoElemento, tipos[i%len(tipos)])
		}
		// Contiguos por sección: una vez que se sale de una, no vuelve.
		if i > 0 && *respuesta.Campos[i-1].TabNombre != seccion && vistas[seccion] {
			t.Fatalf("la sección %q aparece partida", seccion)
		}
		vistas[seccion] = true
	}
	if len(vistas) != 4 {
		t.Fatalf("esperaba 4 secciones, hay %d", len(vistas))
	}

	claves := []string{}
	for _, s := range respuesta.Salidas {
		claves = append(claves, s.FuenteID)
		if s.FuenteTipo != "SALIDA_ESTANDAR" || s.TipoDato == nil || s.Nombre == "" {
			t.Fatalf("salida incompleta: %+v", s)
		}
	}
	// Orden de salidasPublicasPlantilla; sin SUBTOTAL (inactiva) ni costo/margen.
	if strings.Join(claves, ",") != "TOTAL_PRECIO,MONEDA" {
		t.Fatalf("salidas ofrecidas: %v", claves)
	}
	if len(respuesta.Fuentes) != 24+2+len(fuentesCotizacionBase) {
		t.Fatalf("fuentes totales: %d", len(respuesta.Fuentes))
	}
	if strings.Contains(rec.Body.String(), "TOTAL_COSTO") || strings.Contains(rec.Body.String(), "MARGEN_TOTAL") {
		t.Fatalf("una salida interna se ofreció como fuente: %s", rec.Body.String())
	}
}

func TestPlantillaVinculaciones_SalidaEstandarValidaAlGuardar(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	for _, s := range []struct {
		clave  string
		activo bool
	}{{"TOTAL_PRECIO", true}, {"TOTAL_COSTO", true}, {"SUBTOTAL", false}} {
		fijarMapaSalidaPlantillaPrueba(t, e, e.calculadora, s.clave, s.activo)
	}
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla vinculación salidas", nil)
	seccionID := crearSeccionPrueba(t, e, plantillaID, "Inversión")
	bloqueID := crearBloqueTipoPrueba(t, e, seccionID, "CAMPO_VINCULADO", nil)
	listaID := crearBloqueTipoPrueba(t, e, seccionID, "LISTA_PRECIOS", nil)
	guardar := func(bloque, fuenteID string) (int, string) {
		rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion",
			"/api/plantillas/bloques/"+bloque+"/vinculacion", e.vinculaciones.Guardar,
			map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "SALIDA_ESTANDAR", "fuente_id": fuenteID})
		return rec.Code, rec.Body.String()
	}
	for _, caso := range []struct {
		nombre, bloque, fuente string
		codigo                 int
	}{
		{"salida pública mapeada (minúsculas se normalizan)", bloqueID, "total_precio", http.StatusOK},
		{"costo interno, aunque esté mapeado", bloqueID, "TOTAL_COSTO", http.StatusBadRequest},
		{"salida pública pero inactiva en el mapa", bloqueID, "SUBTOTAL", http.StatusBadRequest},
		{"salida pública sin mapear", bloqueID, "MONEDA", http.StatusBadRequest},
		{"clave inexistente", bloqueID, "INVENTADA", http.StatusBadRequest},
		{"una Lista de precios solo acepta un elemento Lista de Precios", listaID, "TOTAL_PRECIO", http.StatusBadRequest},
	} {
		if codigo, cuerpo := guardar(caso.bloque, caso.fuente); codigo != caso.codigo {
			t.Fatalf("%s: esperaba %d, dio %d: %s", caso.nombre, caso.codigo, codigo, cuerpo)
		}
	}
	var fuenteTipo, fuenteID string
	if err := e.pool.QueryRow(context.Background(), `SELECT fuente_tipo, fuente_id FROM plantilla_vinculaciones WHERE bloque_id::text=$1`, bloqueID).Scan(&fuenteTipo, &fuenteID); err != nil {
		t.Fatal(err)
	}
	if fuenteTipo != "SALIDA_ESTANDAR" || fuenteID != "TOTAL_PRECIO" {
		t.Fatalf("vinculación guardada: %s/%s", fuenteTipo, fuenteID)
	}
}

// El valor de una Salida estándar sale de cotizacion_salidas de ESA versión
// — no de cotizacion_versiones — y llega así a la Vista Previa de la oferta.
func TestVistaPreviaOferta_ResuelveBloqueVinculadoASalidaEstandar(t *testing.T) {
	f := crearFixtureCatorceBloques(t, false)
	e := f.e
	ctx := context.Background()
	for _, clave := range []string{"TOTAL_PRECIO", "TIPO_CLIENTE", "SUBTOTAL"} {
		fijarMapaSalidaPlantillaPrueba(t, e, e.calculadora, clave, true)
	}
	// La fixture ya publicó la plantilla, y una versión publicada queda
	// bloqueada (Ronda P5): se vuelve a Borrador para agregar los bloques y
	// se republica al final.
	if _, err := e.pool.Exec(ctx, `UPDATE plantillas SET estado='Borrador' WHERE plantilla_id::text=$1`, f.plantillaID); err != nil {
		t.Fatal(err)
	}
	seccionID := crearSeccionPrueba(t, e, f.plantillaID, "Resumen de inversión")
	vincular := func(titulo, clave string) {
		t.Helper()
		bloqueID := crearBloqueTipoPrueba(t, e, seccionID, "CAMPO_VINCULADO", map[string]any{"titulo": titulo})
		rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas/bloques/{bloque_id}/vinculacion",
			"/api/plantillas/bloques/"+bloqueID+"/vinculacion", e.vinculaciones.Guardar,
			map[string]any{"calculadora_id": e.calculadora, "fuente_tipo": "SALIDA_ESTANDAR", "fuente_id": clave})
		if rec.Code != http.StatusOK {
			t.Fatalf("vincular %s: %d: %s", clave, rec.Code, rec.Body.String())
		}
	}
	vincular("Precio total", "TOTAL_PRECIO")
	vincular("Tipo de cliente", "TIPO_CLIENTE")
	vincular("Subtotal", "SUBTOTAL")
	// Una vinculación a TOTAL_COSTO metida por fuera del API (datos viejos,
	// SQL a mano): el renderizador igual no la resuelve.
	costoID := crearBloqueTipoPrueba(t, e, seccionID, "CAMPO_VINCULADO", map[string]any{"titulo": "Costo"})
	if _, err := e.pool.Exec(ctx, `INSERT INTO plantilla_vinculaciones (bloque_id,calculadora_id,fuente_tipo,fuente_id) VALUES ($1,$2,'SALIDA_ESTANDAR','TOTAL_COSTO')`,
		costoID, e.calculadora); err != nil {
		t.Fatal(err)
	}

	if _, err := e.pool.Exec(ctx, `UPDATE plantillas SET estado='Publicada' WHERE plantilla_id::text=$1`, f.plantillaID); err != nil {
		t.Fatal(err)
	}

	// cotizacion_versiones dice 1000 (crearCotizacionPrueba); la salida
	// persistida dice 1234.5 — el documento tiene que mostrar la salida.
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO cotizacion_salidas (cotizacion_id,numero_version,clave_salida,fuente_id,tipo_dato,valor_numero,valor_texto,valor_visible,moneda)
		VALUES ($1,1,'TOTAL_PRECIO','F1','MONEDA',1234.5,NULL,NULL,'US$'),
		       ($1,1,'TIPO_CLIENTE','F2','TEXTO',NULL,'NUEVO','Cliente nuevo',NULL),
		       ($1,1,'TOTAL_COSTO','F3','MONEDA',777.77,NULL,NULL,'US$')`, f.cotizacionID); err != nil {
		t.Fatal(err)
	}

	valores := func() map[string]any {
		t.Helper()
		rec := getVistaPreviaOferta(t, &VistaPreviaOfertaHandler{DB: e.pool}, f.cotizacionID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("vista previa: %d: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "777.77") {
			t.Fatalf("el costo interno llegó a la vista previa: %s", rec.Body.String())
		}
		var respuesta struct {
			Plantilla plantillaRenderizada `json:"plantilla"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &respuesta); err != nil {
			t.Fatal(err)
		}
		resultado := map[string]any{}
		for _, s := range respuesta.Plantilla.Secciones {
			for _, b := range s.Bloques {
				if b.TipoBloque == "CAMPO_VINCULADO" {
					resultado[b.Titulo] = b.Valor
				}
			}
		}
		return resultado
	}
	v := valores()
	if v["Precio total"] != 1234.5 {
		t.Fatalf("TOTAL_PRECIO debe salir de cotizacion_salidas (1234.5), dio %v", v["Precio total"])
	}
	if v["Tipo de cliente"] != "Cliente nuevo" {
		t.Fatalf("una salida de texto se muestra con su texto visible, dio %v", v["Tipo de cliente"])
	}
	if v["Subtotal"] != nil {
		t.Fatalf("una salida sin valor persistido queda vacía, sin inventar: %v", v["Subtotal"])
	}
	if _, existe := v["Costo"]; !existe || v["Costo"] != nil {
		t.Fatalf("el bloque vinculado a TOTAL_COSTO debe renderizarse vacío: %v", v)
	}

	// Sin salidas guardadas en esta versión: vacío, sin caer a cotizacion_versiones.
	if _, err := e.pool.Exec(ctx, `DELETE FROM cotizacion_salidas WHERE cotizacion_id=$1`, f.cotizacionID); err != nil {
		t.Fatal(err)
	}
	if v = valores(); v["Precio total"] != nil {
		t.Fatalf("sin cotizacion_salidas no debe usarse cotizacion_versiones.total_precio: %v", v["Precio total"])
	}
}
