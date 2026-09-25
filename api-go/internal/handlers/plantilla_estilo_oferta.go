package handlers

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// plantilla_estilo_oferta.go — el estilo visual del paso 4 del Diseñador de
// Plantillas (plantilla_estilos, 0010 + 0016) tal como lo necesita el
// documento de la oferta: el enlace público (publico.html, que también es lo
// que se imprime como PDF) y la Vista Previa. Viaja dentro de
// plantillaRenderizada, así que la plantilla fijada de una cotización trae SU
// estilo, no el de la versión publicada hoy.

// estiloOferta llega ya resuelto: los tres colores de marca nunca vienen
// vacíos (un color sin configurar toma el del tema, igual que la vista previa
// del paso 4) y organizacion_nombre siempre trae un nombre para mostrar
// cuando no hay logo. logo_url solo viene si mostrar_logo está activo.
type estiloOferta struct {
	Tema          string `json:"tema"`
	FormatoPagina string `json:"formato_pagina"`
	// EspaciadoPagina es plantilla_estilos.margenes con otro nombre a
	// propósito: el documento público no puede contener la palabra
	// "margen" en ninguna clave (las pruebas de ListaPrecios la buscan como
	// señal de margen interno filtrado al cliente).
	EspaciadoPagina     string  `json:"espaciado_pagina"`
	DisenoPortada       string  `json:"diseno_portada"`
	EstiloTablas        string  `json:"estilo_tablas"`
	ColorPrimario       string  `json:"color_primario"`
	ColorSecundario     string  `json:"color_secundario"`
	ColorAcento         string  `json:"color_acento"`
	ColorTexto          *string `json:"color_texto"`
	ColorFondo          *string `json:"color_fondo"`
	FuenteTitulos       *string `json:"fuente_titulos"`
	FuenteTexto         *string `json:"fuente_texto"`
	LogoURL             *string `json:"logo_url"`
	LogoTamano          string  `json:"logo_tamano"`
	MostrarOrganizacion bool    `json:"mostrar_organizacion"`
	OrganizacionNombre  string  `json:"organizacion_nombre"`
	TextoEncabezado     *string `json:"texto_encabezado"`
	TextoPie            *string `json:"texto_pie"`
	NumerarPaginas      bool    `json:"numerar_paginas"`
	MarcaConfidencial   bool    `json:"marca_confidencial"`
}

// coloresTemaEstilo es copia a mano de temasEstiloPredefinidos en
// frontend/legacy-gas/plantillas_app.html (primario, secundario, acento) —
// los dos tienen que cambiar juntos, o el documento real deja de verse como
// la vista previa del paso 4. Un tema desconocido cae a PROFESIONAL, igual
// que temaEstiloPorClave allá.
var coloresTemaEstilo = map[string][3]string{
	"PROFESIONAL": {"#0B2F63", "#1F6FFF", "#20B8CD"},
	"MINIMALISTA": {"#111827", "#4B5563", "#9CA3AF"},
	"EJECUTIVO":   {"#1C2B3A", "#3D5A80", "#E0B354"},
	"EMPRESARIAL": {"#0F4C3A", "#1E8A5F", "#7FD1AE"},
	"MODERNO":     {"#0F2A4A", "#185FA5", "#25BCD2"},
}

// nombreOrganizacionPorDefecto es el que ya mostraba la cabecera fija de
// publico.html: se usa solo si la plantilla no tiene nombre visible ni
// organización asociada, para que la cabecera nunca quede vacía.
const nombreOrganizacionPorDefecto = "Exceltec Business Solutions"

// estiloOfertaPlantilla lee el estilo de UNA plantilla puntual (cualquier
// estado, Archivada incluida). Una plantilla sin fila en plantilla_estilos
// sale con los mismos defaults de la tabla.
func estiloOfertaPlantilla(ctx context.Context, db *pgxpool.Pool, plantillaID string) (*estiloOferta, error) {
	var e estiloOferta
	var primario, secundario, acento, logoURL, nombreVisible, nombreOrganizacion *string
	var mostrarLogo bool
	err := db.QueryRow(ctx, `
		SELECT COALESCE(pe.tema,'PROFESIONAL'), COALESCE(pe.formato_pagina,'CARTA'), COALESCE(pe.margenes,'NORMAL'),
		       COALESCE(pe.diseno_portada,'BANDA_SUPERIOR'), COALESCE(pe.estilo_tablas,'LINEAS'),
		       pe.color_primario, pe.color_secundario, pe.color_acento, pe.color_texto, pe.color_fondo,
		       pe.fuente_titulos, pe.fuente_texto, pe.logo_url, COALESCE(pe.logo_tamano,'MEDIANO'),
		       COALESCE(pe.mostrar_logo,false), COALESCE(pe.mostrar_organizacion,false),
		       pe.nombre_organizacion_visible, o.nombre, pe.texto_encabezado, pe.texto_pie,
		       COALESCE(pe.numerar_paginas,false), COALESCE(pe.marca_confidencial,false)
		  FROM plantillas p
		  LEFT JOIN plantilla_estilos pe ON pe.plantilla_id = p.plantilla_id
		  LEFT JOIN organizaciones o ON o.organizacion_id = p.organizacion_id
		 WHERE p.plantilla_id::text = $1`, plantillaID).Scan(
		&e.Tema, &e.FormatoPagina, &e.EspaciadoPagina, &e.DisenoPortada, &e.EstiloTablas,
		&primario, &secundario, &acento, &e.ColorTexto, &e.ColorFondo,
		&e.FuenteTitulos, &e.FuenteTexto, &logoURL, &e.LogoTamano,
		&mostrarLogo, &e.MostrarOrganizacion, &nombreVisible, &nombreOrganizacion,
		&e.TextoEncabezado, &e.TextoPie, &e.NumerarPaginas, &e.MarcaConfidencial)
	if err != nil {
		return nil, err
	}

	tema, ok := coloresTemaEstilo[strings.ToUpper(e.Tema)]
	if !ok {
		tema = coloresTemaEstilo["PROFESIONAL"]
	}
	e.ColorPrimario = textoOPorDefecto(primario, tema[0])
	e.ColorSecundario = textoOPorDefecto(secundario, tema[1])
	e.ColorAcento = textoOPorDefecto(acento, tema[2])
	for _, campo := range []**string{&e.ColorTexto, &e.ColorFondo, &e.FuenteTitulos, &e.FuenteTexto, &e.TextoEncabezado, &e.TextoPie} {
		*campo = textoNoVacio(*campo)
	}
	if mostrarLogo {
		e.LogoURL = textoNoVacio(logoURL)
	}
	e.OrganizacionNombre = textoOPorDefecto(nombreVisible, textoOPorDefecto(nombreOrganizacion, nombreOrganizacionPorDefecto))
	return &e, nil
}

func textoNoVacio(p *string) *string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

func textoOPorDefecto(p *string, defecto string) string {
	if v := textoNoVacio(p); v != nil {
		return *v
	}
	return defecto
}
