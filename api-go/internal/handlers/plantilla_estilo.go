package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlantillaEstiloHandler administra el estilo visual único de cada plantilla.
type PlantillaEstiloHandler struct {
	DB *pgxpool.Pool
}

type editarEstiloRequest struct {
	Tema                      *string `json:"tema"`
	FormatoPagina             *string `json:"formato_pagina"`
	Margenes                  *string `json:"margenes"`
	DisenoPortada             *string `json:"diseno_portada"`
	EstiloTablas              *string `json:"estilo_tablas"`
	ColorPrimario             *string `json:"color_primario"`
	ColorSecundario           *string `json:"color_secundario"`
	ColorAcento               *string `json:"color_acento"`
	ColorTexto                *string `json:"color_texto"`
	ColorFondo                *string `json:"color_fondo"`
	FuenteTitulos             *string `json:"fuente_titulos"`
	FuenteTexto               *string `json:"fuente_texto"`
	LogoURL                   *string `json:"logo_url"`
	LogoTamano                *string `json:"logo_tamano"`
	MostrarLogo               *bool   `json:"mostrar_logo"`
	MostrarOrganizacion       *bool   `json:"mostrar_organizacion"`
	NombreOrganizacionVisible *string `json:"nombre_organizacion_visible"`
	TextoEncabezado           *string `json:"texto_encabezado"`
	TextoPie                  *string `json:"texto_pie"`
	NumerarPaginas            *bool   `json:"numerar_paginas"`
	MarcaConfidencial         *bool   `json:"marca_confidencial"`
}

var formatosPaginaValidos = map[string]bool{"CARTA": true, "A4": true}
var margenesValidos = map[string]bool{"COMPACTO": true, "NORMAL": true, "AMPLIO": true}
var portadasValidas = map[string]bool{"BANDA_SUPERIOR": true, "LATERAL": true, "MINIMALISTA": true, "BLOQUE": true}
var estilosTablaValidos = map[string]bool{"LINEAS": true, "SUAVE": true, "TARJETAS": true}
var tamanosLogoValidos = map[string]bool{"PEQUENO": true, "MEDIANO": true, "GRANDE": true}
var colorHexEstilo = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func (h *PlantillaEstiloHandler) Actualizar(w http.ResponseWriter, r *http.Request) {
	plantillaID := strings.TrimSpace(chi.URLParam(r, "id"))
	var req editarEstiloRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Tema == nil && req.FormatoPagina == nil && req.Margenes == nil &&
		req.DisenoPortada == nil && req.EstiloTablas == nil && req.ColorPrimario == nil &&
		req.ColorSecundario == nil && req.ColorAcento == nil && req.ColorTexto == nil &&
		req.ColorFondo == nil && req.FuenteTitulos == nil && req.FuenteTexto == nil &&
		req.LogoURL == nil && req.LogoTamano == nil && req.MostrarLogo == nil &&
		req.MostrarOrganizacion == nil && req.NombreOrganizacionVisible == nil &&
		req.TextoEncabezado == nil && req.TextoPie == nil && req.NumerarPaginas == nil &&
		req.MarcaConfidencial == nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar al menos una propiedad de estilo."})
		return
	}
	if req.Tema != nil {
		v := strings.ToUpper(strings.TrimSpace(*req.Tema))
		if v == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El tema no puede quedar vacío."})
			return
		}
		req.Tema = &v
	}
	if err := normalizarOpcionEstilo(req.FormatoPagina, formatosPaginaValidos, "formato_pagina"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.Margenes, margenesValidos, "margenes"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.DisenoPortada, portadasValidas, "diseno_portada"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.EstiloTablas, estilosTablaValidos, "estilo_tablas"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := normalizarOpcionEstilo(req.LogoTamano, tamanosLogoValidos, "logo_tamano"); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	colores := []struct {
		valor *string
		campo string
	}{
		{req.ColorPrimario, "color_primario"},
		{req.ColorSecundario, "color_secundario"},
		{req.ColorAcento, "color_acento"},
		{req.ColorTexto, "color_texto"},
		{req.ColorFondo, "color_fondo"},
	}
	for _, color := range colores {
		if err := normalizarColorEstilo(color.valor, color.campo); err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if err := normalizarLogoURL(req.LogoURL); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	for _, texto := range []*string{
		req.FuenteTitulos, req.FuenteTexto, req.NombreOrganizacionVisible,
		req.TextoEncabezado, req.TextoPie,
	} {
		if texto != nil {
			*texto = strings.TrimSpace(*texto)
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plantillas WHERE plantilla_id::text=$1)`, plantillaID).Scan(&existe); err != nil {
		log.Printf("plantilla estilo: error validando %s: %v", plantillaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar la plantilla."})
		return
	}
	if !existe {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}

	var estilo plantillaEstilo
	err := h.DB.QueryRow(ctx, `
		INSERT INTO plantilla_estilos
			(plantilla_id,tema,formato_pagina,margenes,diseno_portada,estilo_tablas,
			 color_primario,color_secundario,color_acento,color_texto,color_fondo,
			 fuente_titulos,fuente_texto,logo_url,logo_tamano,mostrar_logo,
			 mostrar_organizacion,nombre_organizacion_visible,texto_encabezado,texto_pie,
			 numerar_paginas,marca_confidencial)
		VALUES ($1,COALESCE($2::text,'PROFESIONAL'),COALESCE($3::text,'CARTA'),
		        COALESCE($4::text,'NORMAL'),COALESCE($5::text,'BANDA_SUPERIOR'),COALESCE($6::text,'LINEAS'),
		        $7::text,$8::text,$9::text,$10::text,$11::text,$12::text,$13::text,$14::text,
		        COALESCE($15::text,'MEDIANO'),COALESCE($16::boolean,FALSE),COALESCE($17::boolean,FALSE),
		        $18::text,$19::text,$20::text,COALESCE($21::boolean,FALSE),COALESCE($22::boolean,FALSE))
		ON CONFLICT (plantilla_id) DO UPDATE SET
			tema=COALESCE($2::text,plantilla_estilos.tema),
			formato_pagina=COALESCE($3::text,plantilla_estilos.formato_pagina),
			margenes=COALESCE($4::text,plantilla_estilos.margenes),
			diseno_portada=COALESCE($5::text,plantilla_estilos.diseno_portada),
			estilo_tablas=COALESCE($6::text,plantilla_estilos.estilo_tablas),
			color_primario=COALESCE($7::text,plantilla_estilos.color_primario),
			color_secundario=COALESCE($8::text,plantilla_estilos.color_secundario),
			color_acento=COALESCE($9::text,plantilla_estilos.color_acento),
			color_texto=COALESCE($10::text,plantilla_estilos.color_texto),
			color_fondo=COALESCE($11::text,plantilla_estilos.color_fondo),
			fuente_titulos=COALESCE($12::text,plantilla_estilos.fuente_titulos),
			fuente_texto=COALESCE($13::text,plantilla_estilos.fuente_texto),
			logo_url=COALESCE($14::text,plantilla_estilos.logo_url),
			logo_tamano=COALESCE($15::text,plantilla_estilos.logo_tamano),
			mostrar_logo=COALESCE($16::boolean,plantilla_estilos.mostrar_logo),
			mostrar_organizacion=COALESCE($17::boolean,plantilla_estilos.mostrar_organizacion),
			nombre_organizacion_visible=COALESCE($18::text,plantilla_estilos.nombre_organizacion_visible),
			texto_encabezado=COALESCE($19::text,plantilla_estilos.texto_encabezado),
			texto_pie=COALESCE($20::text,plantilla_estilos.texto_pie),
			numerar_paginas=COALESCE($21::boolean,plantilla_estilos.numerar_paginas),
			marca_confidencial=COALESCE($22::boolean,plantilla_estilos.marca_confidencial)
		RETURNING estilo_id::text,plantilla_id::text,tema,formato_pagina,margenes,diseno_portada,estilo_tablas,
		          color_primario,color_secundario,color_acento,color_texto,color_fondo,
		          fuente_titulos,fuente_texto,logo_url,logo_tamano,mostrar_logo,
		          mostrar_organizacion,nombre_organizacion_visible,texto_encabezado,texto_pie,
		          numerar_paginas,marca_confidencial`,
		plantillaID, req.Tema, req.FormatoPagina, req.Margenes, req.DisenoPortada, req.EstiloTablas,
		req.ColorPrimario, req.ColorSecundario, req.ColorAcento, req.ColorTexto, req.ColorFondo,
		req.FuenteTitulos, req.FuenteTexto, req.LogoURL, req.LogoTamano, req.MostrarLogo,
		req.MostrarOrganizacion, req.NombreOrganizacionVisible, req.TextoEncabezado, req.TextoPie,
		req.NumerarPaginas, req.MarcaConfidencial).Scan(
		&estilo.EstiloID, &estilo.PlantillaID, &estilo.Tema, &estilo.FormatoPagina,
		&estilo.Margenes, &estilo.DisenoPortada, &estilo.EstiloTablas,
		&estilo.ColorPrimario, &estilo.ColorSecundario, &estilo.ColorAcento,
		&estilo.ColorTexto, &estilo.ColorFondo, &estilo.FuenteTitulos, &estilo.FuenteTexto,
		&estilo.LogoURL, &estilo.LogoTamano, &estilo.MostrarLogo, &estilo.MostrarOrganizacion,
		&estilo.NombreOrganizacionVisible, &estilo.TextoEncabezado, &estilo.TextoPie,
		&estilo.NumerarPaginas, &estilo.MarcaConfidencial,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Plantilla no encontrada."})
		return
	}
	if err != nil {
		log.Printf("plantilla estilo: error actualizando %s: %v", plantillaID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible actualizar el estilo."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "estilo": estilo, "mensaje": "Estilo actualizado."})
}

func normalizarOpcionEstilo(valor *string, permitidos map[string]bool, campo string) error {
	if valor == nil {
		return nil
	}
	v := strings.ToUpper(strings.TrimSpace(*valor))
	*valor = v
	if !permitidos[v] {
		return errors.New(campo + " no tiene un valor válido")
	}
	return nil
}

func normalizarColorEstilo(valor *string, campo string) error {
	if valor == nil {
		return nil
	}
	v := strings.ToUpper(strings.TrimSpace(*valor))
	if !colorHexEstilo.MatchString(v) {
		return errors.New(campo + " debe tener formato hexadecimal #RRGGBB")
	}
	*valor = v
	return nil
}

func normalizarLogoURL(valor *string) error {
	if valor == nil {
		return nil
	}
	v := strings.TrimSpace(*valor)
	*valor = v
	if v == "" {
		return nil
	}
	parsed, err := url.Parse(v)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("logo_url debe ser una URL https válida")
	}
	return nil
}
