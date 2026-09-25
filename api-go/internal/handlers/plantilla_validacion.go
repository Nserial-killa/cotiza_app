package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// plantilla_validacion.go — Ronda P5, "Revisar y publicar". validarPlantilla
// recorre la plantilla completa y devuelve errores (impiden publicar) y
// advertencias (se puede publicar, pero conviene revisarlas), cada una
// nombrando el bloque, la sección y el cotizador exacto: una plantilla con
// dos cotizadores puede estar completa para uno e incompleta para el otro,
// porque vinculaciones y columnas van por cotizador. La usan GET
// /api/plantillas/{id}/validacion (el paso 5) y Publicar, así que lo que el
// paso 5 muestra es exactamente lo que Publicar exige.

type validacionPlantilla struct {
	Nivel         string `json:"nivel"` // ERROR | ADVERTENCIA
	Codigo        string `json:"codigo"`
	Mensaje       string `json:"mensaje"`
	SeccionID     string `json:"seccion_id,omitempty"`
	BloqueID      string `json:"bloque_id,omitempty"`
	CalculadoraID string `json:"calculadora_id,omitempty"`
}

type cotizadorResumenPlantilla struct {
	CalculadoraID string `json:"calculadora_id"`
	Nombre        string `json:"nombre"`
}

type resumenPlantilla struct {
	PlantillaID string `json:"plantilla_id"`
	Codigo      string `json:"codigo"`
	Estado      string `json:"estado"`
	Version     int    `json:"version"`
	Secciones   int    `json:"secciones"`
	Bloques     int    `json:"bloques"`
	// Vinculaciones cuenta cada dato conectado al cotizador: vinculaciones
	// de un bloque, columnas y campos con fuente (un VALOR_FIJO no cuenta:
	// es texto escrito en la plantilla), solo de los cotizadores asociados.
	Vinculaciones  int                         `json:"vinculaciones"`
	Cotizadores    []cotizadorResumenPlantilla `json:"cotizadores"`
	TiposPropuesta []string                    `json:"tipos_propuesta"`
	// Canales: cuántos bloques se ven en la propuesta web y en el PDF
	// (mostrar_web/mostrar_pdf del bloque Y de su sección).
	Canales struct {
		Web int `json:"web"`
		PDF int `json:"pdf"`
	} `json:"canales"`
	Errores      int `json:"errores"`
	Advertencias int `json:"advertencias"`
}

type resultadoValidacionPlantilla struct {
	Resumen       resumenPlantilla      `json:"resumen"`
	Validaciones  []validacionPlantilla `json:"validaciones"`
	PuedePublicar bool                  `json:"puede_publicar"`
}

func (r *resultadoValidacionPlantilla) filtrar(nivel string) []validacionPlantilla {
	resultado := make([]validacionPlantilla, 0)
	for _, v := range r.Validaciones {
		if v.Nivel == nivel {
			resultado = append(resultado, v)
		}
	}
	return resultado
}
func (r *resultadoValidacionPlantilla) errores() []validacionPlantilla { return r.filtrar("ERROR") }
func (r *resultadoValidacionPlantilla) advertencias() []validacionPlantilla {
	return r.filtrar("ADVERTENCIA")
}

// etiquetasTipoBloquePlantilla es el mismo texto de la paleta del paso 2
// (tiposBloqueEstructura en plantillas_app.html), para que los mensajes
// hablen como la pantalla.
var etiquetasTipoBloquePlantilla = map[string]string{
	"PORTADA": "Portada", "ENCABEZADO": "Encabezado", "TEXTO": "Texto", "IMAGEN": "Imagen",
	"DATOS_CLIENTE": "Datos del cliente", "RESUMEN_EJECUTIVO": "Resumen ejecutivo",
	"TABLA_INVERSION": "Tabla de Inversión", "TABLA_DATOS": "Tabla de datos",
	"OPCIONES_PROPUESTA": "Opciones de propuesta", "LISTA_PRECIOS": "Lista de precios",
	"GRUPO_INFORMACION": "Grupo de información", "CONDICIONES_COMERCIALES": "Condiciones comerciales",
	"FIRMA_ACEPTACION": "Firma y aceptación", "SALTO_PAGINA": "Salto de página",
	"LISTA": "Lista", "CAMPO_VINCULADO": "Campo vinculado", "CONDICIONES": "Condiciones",
}

// textosEjemploSugeridos son los textos con que nace la Estructura
// sugerida (P2): si llegan así a publicarse, casi seguro nadie los editó.
var textosEjemploSugeridos = func() map[string]bool {
	resultado := map[string]bool{}
	for _, seccion := range estructuraSugeridaPlantilla {
		for _, b := range seccion.Bloques {
			if b.Contenido != "" {
				resultado[strings.TrimSpace(b.Contenido)] = true
			}
		}
	}
	return resultado
}()

var patronURLHTTPS = regexp.MustCompile(`(?i)^https://\S+$`)

// fuentesCotizadorValidacion es lo que un cotizador ofrece hoy como fuente,
// para detectar vinculaciones que apuntan a algo borrado o desactivado.
type fuentesCotizadorValidacion struct {
	campos  map[string]string // elemento_id -> etiqueta
	salidas map[string]bool
}

func cargarFuentesCotizadorValidacion(ctx context.Context, db *pgxpool.Pool, calculadoraID string) (fuentesCotizadorValidacion, error) {
	resultado := fuentesCotizadorValidacion{campos: map[string]string{}, salidas: map[string]bool{}}
	rows, err := db.Query(ctx, `
		SELECT e.elemento_id, COALESCE(NULLIF(e.etiqueta,''), e.elemento_id)
		  FROM tabs_cotizador t JOIN elementos_tab_cotizador e ON e.tab_id=t.tab_id
		 WHERE t.calculadora_id=$1 AND t.activo AND e.activo
		   AND e.tipo IN ('CAMPO','CAMPO_CATALOGO','CAMPO_CALCULADO','LISTA_PRECIOS','TABLA')`, calculadoraID)
	if err != nil {
		return resultado, err
	}
	for rows.Next() {
		var id, etiqueta string
		if err := rows.Scan(&id, &etiqueta); err != nil {
			rows.Close()
			return resultado, err
		}
		resultado.campos[id] = etiqueta
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return resultado, err
	}
	salidas, err := salidasMapeadasPlantilla(ctx, db, calculadoraID)
	if err != nil {
		return resultado, err
	}
	for _, s := range salidas {
		resultado.salidas[s.FuenteID] = true
	}
	return resultado, nil
}

func (f fuentesCotizadorValidacion) existe(fuenteTipo, fuenteID string) bool {
	switch fuenteTipo {
	case "CAMPO":
		_, ok := f.campos[fuenteID]
		return ok
	case "SALIDA_ESTANDAR":
		return f.salidas[fuenteID]
	case "COTIZACION_BASE":
		return idsCotizacionBase[fuenteID]
	}
	return true // VALOR_FIJO, NOMBRE_ESCENARIO, ES_RECOMENDADA: no dependen del cotizador.
}

func validarPlantilla(ctx context.Context, db *pgxpool.Pool, plantillaID string) (*resultadoValidacionPlantilla, error) {
	detalle, err := (&PlantillasHandler{DB: db}).consultarDetalle(ctx, plantillaID)
	if err != nil {
		return nil, err
	}
	res := &resultadoValidacionPlantilla{Validaciones: make([]validacionPlantilla, 0)}
	res.Resumen.PlantillaID, res.Resumen.Codigo = detalle.PlantillaID, detalle.Codigo
	res.Resumen.Estado, res.Resumen.Version = detalle.Estado, detalle.Version
	res.Resumen.TiposPropuesta = detalle.TiposPropuesta
	agregar := func(nivel, codigo, mensaje, seccionID, bloqueID, calculadoraID string) {
		res.Validaciones = append(res.Validaciones, validacionPlantilla{nivel, codigo, mensaje, seccionID, bloqueID, calculadoraID})
	}

	// Cotizadores asociados, con su nombre para los mensajes.
	nombres := map[string]string{}
	res.Resumen.Cotizadores = make([]cotizadorResumenPlantilla, 0, len(detalle.CalculadoraIDs))
	if len(detalle.CalculadoraIDs) > 0 {
		rows, err := db.Query(ctx, `SELECT calculadora_id, nombre_calculadora FROM calculadoras WHERE calculadora_id = ANY($1)`, detalle.CalculadoraIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, nombre string
			if err := rows.Scan(&id, &nombre); err != nil {
				rows.Close()
				return nil, err
			}
			nombres[id] = nombre
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	fuentes := map[string]fuentesCotizadorValidacion{}
	for _, id := range detalle.CalculadoraIDs {
		if nombres[id] == "" {
			nombres[id] = id
		}
		res.Resumen.Cotizadores = append(res.Resumen.Cotizadores, cotizadorResumenPlantilla{CalculadoraID: id, Nombre: nombres[id]})
		if fuentes[id], err = cargarFuentesCotizadorValidacion(ctx, db, id); err != nil {
			return nil, err
		}
	}
	asociado := func(id string) bool { _, ok := fuentes[id]; return ok }

	if len(detalle.CalculadoraIDs) == 0 {
		agregar("ERROR", "SIN_COTIZADORES", "La plantilla no tiene ningún cotizador asociado: no se usaría en ninguna propuesta.", "", "", "")
	}
	if len(detalle.TiposPropuesta) == 0 {
		agregar("ADVERTENCIA", "SIN_TIPOS_PROPUESTA", "La plantilla no restringe tipos de propuesta: aplicará a cualquier tipo de los cotizadores asociados.", "", "", "")
	}

	tienePortada := false
	for _, seccion := range detalle.Secciones {
		res.Resumen.Secciones++
		nombreSeccion := seccion.Nombre
		if seccion.Titulo != nil && strings.TrimSpace(*seccion.Titulo) != "" {
			nombreSeccion = *seccion.Titulo
		}
		if len(seccion.Bloques) == 0 {
			agregar("ADVERTENCIA", "SECCION_VACIA", fmt.Sprintf("La sección «%s» no tiene bloques.", nombreSeccion), seccion.SeccionID, "", "")
		}
		if !seccion.MostrarWeb && !seccion.MostrarPDF && len(seccion.Bloques) > 0 {
			agregar("ADVERTENCIA", "SECCION_OCULTA", fmt.Sprintf("La sección «%s» está oculta en la propuesta web y en el PDF.", nombreSeccion), seccion.SeccionID, "", "")
		}
		for _, b := range seccion.Bloques {
			res.Resumen.Bloques++
			if seccion.MostrarWeb && b.MostrarWeb {
				res.Resumen.Canales.Web++
			}
			if seccion.MostrarPDF && b.MostrarPDF {
				res.Resumen.Canales.PDF++
			}
			validarBloquePlantilla(res, agregar, detalle.CalculadoraIDs, nombres, fuentes, asociado, seccion, nombreSeccion, b)
			if b.TipoBloque == "PORTADA" {
				tienePortada = true
			}
		}
	}
	if res.Resumen.Bloques == 0 {
		agregar("ERROR", "SIN_CONTENIDO", "La plantilla debe tener al menos una sección con al menos un bloque antes de publicarse.", "", "", "")
	} else if !tienePortada {
		agregar("ADVERTENCIA", "SIN_PORTADA", "La plantilla no tiene un bloque Portada.", "", "", "")
	}

	// Estilo visual.
	if e := detalle.Estilo; e == nil {
		agregar("ADVERTENCIA", "ESTILO_SIN_CONFIGURAR", "El estilo visual no está configurado: se usarán los valores predeterminados.", "", "", "")
	} else {
		if e.MostrarLogo && (e.LogoURL == nil || !patronURLHTTPS.MatchString(strings.TrimSpace(*e.LogoURL))) {
			agregar("ERROR", "ESTILO_LOGO_SIN_URL", "El estilo solicita mostrar el logotipo, pero no existe una URL HTTPS configurada.", "", "", "")
		}
		if e.MostrarOrganizacion && (e.NombreOrganizacionVisible == nil || strings.TrimSpace(*e.NombreOrganizacionVisible) == "") {
			agregar("ADVERTENCIA", "ESTILO_ORGANIZACION_SIN_NOMBRE", "El estilo solicita mostrar el nombre de la organización, pero el nombre visible está vacío.", "", "", "")
		}
	}

	res.Resumen.Errores = len(res.errores())
	res.Resumen.Advertencias = len(res.advertencias())
	res.PuedePublicar = res.Resumen.Errores == 0 && res.Resumen.Estado == "Borrador"
	return res, nil
}

func validarBloquePlantilla(res *resultadoValidacionPlantilla, agregar func(nivel, codigo, mensaje, seccionID, bloqueID, calculadoraID string),
	calculadoras []string, nombres map[string]string, fuentes map[string]fuentesCotizadorValidacion, asociado func(string) bool,
	seccion plantillaSeccion, nombreSeccion string, b plantillaBloque,
) {
	nombre := b.NombreInterno
	if b.Titulo != nil && strings.TrimSpace(*b.Titulo) != "" {
		nombre = strings.TrimSpace(*b.Titulo)
	}
	tipo := etiquetasTipoBloquePlantilla[b.TipoBloque]
	if tipo == "" {
		tipo = b.TipoBloque
	}
	donde := fmt.Sprintf("«%s» (%s, sección «%s»)", nombre, tipo, nombreSeccion)
	contenido := ""
	if b.Contenido != nil {
		contenido = strings.TrimSpace(*b.Contenido)
	}
	nombreFuente := func(calculadoraID, fuenteTipo, fuenteID string) string {
		if fuenteTipo == "CAMPO" {
			if etiqueta, ok := fuentes[calculadoraID].campos[fuenteID]; ok {
				return etiqueta
			}
		}
		return fuenteID
	}

	if !b.MostrarWeb && !b.MostrarPDF && b.TipoBloque != "SALTO_PAGINA" {
		agregar("ADVERTENCIA", "BLOQUE_OCULTO", fmt.Sprintf("%s está oculto en la propuesta web y en el PDF.", donde), seccion.SeccionID, b.BloqueID, "")
	}

	// Bloques de una sola fuente: tienen que estar vinculados en CADA
	// cotizador asociado.
	if b.TipoBloque == "CAMPO_VINCULADO" || b.TipoBloque == "LISTA_PRECIOS" {
		for _, calc := range calculadoras {
			tiene := false
			for _, v := range b.Vinculaciones {
				if v.CalculadoraID == calc {
					tiene = true
				}
			}
			if !tiene {
				agregar("ERROR", "BLOQUE_SIN_VINCULAR", fmt.Sprintf("Falta vincular %s para «%s».", donde, nombres[calc]), seccion.SeccionID, b.BloqueID, calc)
			}
		}
	}
	for _, v := range b.Vinculaciones {
		if !asociado(v.CalculadoraID) {
			agregar("ADVERTENCIA", "VINCULACION_COTIZADOR_NO_ASOCIADO", fmt.Sprintf("%s tiene una vinculación para el cotizador %s, que ya no está asociado a la plantilla: se ignora.", donde, v.CalculadoraID), seccion.SeccionID, b.BloqueID, v.CalculadoraID)
			continue
		}
		res.Resumen.Vinculaciones++
		if !fuentes[v.CalculadoraID].existe(v.FuenteTipo, v.FuenteID) {
			agregar("ERROR", "FUENTE_INEXISTENTE", fmt.Sprintf("%s está vinculado a «%s», que ya no existe o no está activo en «%s».", donde, v.FuenteID, nombres[v.CalculadoraID]), seccion.SeccionID, b.BloqueID, v.CalculadoraID)
		}
	}
	for _, c := range b.Condiciones {
		if asociado(c.CalculadoraID) && !fuentes[c.CalculadoraID].existe(c.FuenteTipo, c.FuenteID) {
			agregar("ERROR", "FUENTE_INEXISTENTE", fmt.Sprintf("La condición de %s usa «%s», que ya no existe o no está activo en «%s».", donde, c.FuenteID, nombres[c.CalculadoraID]), seccion.SeccionID, b.BloqueID, c.CalculadoraID)
		}
	}

	// Tablas: al menos una columna por cotizador.
	if tiposBloqueConColumnas[b.TipoBloque] {
		for _, calc := range calculadoras {
			columnas, conValores := 0, 0
			for _, c := range b.Columnas {
				if c.CalculadoraID != calc {
					continue
				}
				columnas++
				if !fuentesVirtualesColumnaTabla[c.FuenteTipo] {
					conValores++
				}
				fuenteID := ""
				if c.FuenteID != nil {
					fuenteID = *c.FuenteID
				}
				if !fuentes[calc].existe(c.FuenteTipo, fuenteID) {
					agregar("ERROR", "FUENTE_INEXISTENTE", fmt.Sprintf("La columna «%s» de %s usa «%s», que ya no existe o no está activo en «%s».", c.Titulo, donde, nombreFuente(calc, c.FuenteTipo, fuenteID), nombres[calc]), seccion.SeccionID, b.BloqueID, calc)
				}
			}
			res.Resumen.Vinculaciones += conValores
			switch {
			case columnas == 0:
				agregar("ERROR", "TABLA_SIN_COLUMNAS", fmt.Sprintf("El componente %s no tiene ninguna columna visible para «%s».", donde, nombres[calc]), seccion.SeccionID, b.BloqueID, calc)
			case conValores == 0:
				agregar("ADVERTENCIA", "TABLA_SIN_VALORES", fmt.Sprintf("El componente %s no tiene valores visibles seleccionados para «%s»: solo muestra el nombre del escenario y si es recomendado.", donde, nombres[calc]), seccion.SeccionID, b.BloqueID, calc)
			}
		}
	}

	// Pares Etiqueta/Valor.
	if tiposBloqueConCampos[b.TipoBloque] {
		if len(b.Campos) == 0 && b.TipoBloque != "PORTADA" && b.TipoBloque != "FIRMA_ACEPTACION" {
			agregar("ADVERTENCIA", "BLOQUE_SIN_CAMPOS", fmt.Sprintf("%s no tiene campos: no mostrará ningún dato.", donde), seccion.SeccionID, b.BloqueID, "")
		}
		for _, c := range b.Campos {
			calc := ""
			if c.CalculadoraID != nil {
				calc = *c.CalculadoraID
			}
			switch c.FuenteTipo {
			case "VALOR_FIJO":
				if c.ValorFijo == nil || strings.TrimSpace(*c.ValorFijo) == "" {
					agregar("ADVERTENCIA", "CAMPO_SIN_VALOR", fmt.Sprintf("«%s» de %s no tiene valor: se mostrará «—».", c.Etiqueta, donde), seccion.SeccionID, b.BloqueID, "")
				}
			case "CAMPO":
				if !asociado(calc) {
					continue
				}
				res.Resumen.Vinculaciones++
				if fuenteID := valorTexto(c.FuenteID); !fuentes[calc].existe("CAMPO", fuenteID) {
					agregar("ERROR", "FUENTE_INEXISTENTE", fmt.Sprintf("«%s» de %s usa «%s», que ya no existe o no está activo en «%s».", c.Etiqueta, donde, fuenteID, nombres[calc]), seccion.SeccionID, b.BloqueID, calc)
				}
			default:
				res.Resumen.Vinculaciones++
			}
		}
	}

	switch b.TipoBloque {
	case "IMAGEN":
		if !patronURLHTTPS.MatchString(contenido) {
			agregar("ADVERTENCIA", "IMAGEN_SIN_URL", fmt.Sprintf("%s no tiene una URL HTTPS de imagen: se mostrará el texto en su lugar.", donde), seccion.SeccionID, b.BloqueID, "")
		}
	case "TEXTO", "RESUMEN_EJECUTIVO", "PORTADA":
		if textosEjemploSugeridos[contenido] {
			agregar("ADVERTENCIA", "TEXTO_DE_EJEMPLO", fmt.Sprintf("%s todavía tiene el texto de ejemplo de la estructura sugerida.", donde), seccion.SeccionID, b.BloqueID, "")
		}
	}
}

// Validacion responde GET /api/plantillas/{id}/validacion.
func (h *PlantillasHandler) Validacion(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	resultado, err := validarPlantilla(ctx, h.DB, id)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		log.Printf("plantillas: error validando %s: %v", id, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la plantilla."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "validacion": resultado})
}
