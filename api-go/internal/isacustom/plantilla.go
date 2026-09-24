package isacustom

import (
	"context"
	"net/url"
)

func (c *Cliente) plantilla(ctx context.Context, diagnostico bool) string {
	nombre := "Plantilla ISA Custom (" + c.Prefijo + ")"
	r := c.Intentar(ctx, "Buscar plantilla", "GET", "/api/plantillas?busqueda="+url.QueryEscape(nombre), nil)
	id := ""
	for _, p := range Lista(r.Datos["plantillas"]) {
		if p["nombre"] == nombre {
			id = texto(p["plantilla_id"])
			break
		}
	}
	if id == "" {
		r = c.Intentar(ctx, "Crear plantilla", "POST", "/api/plantillas", map[string]any{"nombre": nombre, "descripcion": "Caso ISA Custom del PDF. Auditar vinculaciones antes de uso comercial.", "calculadora_ids": []string{c.ID("CALC")}, "tipos_propuesta": []string{"ISA Custom"}, "disponible_nuevas_propuestas": true, "permite_duplicar": true})
		id = texto(r.Datos["plantilla_id"])
	}
	if id == "" {
		return ""
	}
	detalle := c.Intentar(ctx, "Detalle plantilla", "GET", "/api/plantillas/"+id, nil)
	secciones := Lista(mapa(detalle.Datos["plantilla"])["secciones"])
	seccion := func(nombre string) (string, []map[string]any) {
		for _, s := range secciones {
			if s["nombre"] == nombre {
				return texto(s["seccion_id"]), Lista(s["bloques"])
			}
		}
		r := c.Intentar(ctx, "Sección oferta "+nombre, "POST", "/api/plantillas/"+id+"/secciones", map[string]any{"nombre": nombre, "titulo": nombre, "diseno_bloques": "UNA", "mostrar_titulo": true})
		return texto(r.Datos["seccion_id"]), nil
	}
	bloque := func(sid string, existentes []map[string]any, tipo, nombre, titulo, contenido string) string {
		if sid == "" {
			return ""
		}
		body := map[string]any{"tipo_bloque": tipo, "nombre_interno": nombre, "titulo": titulo, "contenido": contenido, "columna": 1}
		if tipo == "TABLA_INVERSION" {
			body["origen_filas"] = "OPCIONES_PROPUESTA"
		}
		for _, b := range existentes {
			if b["nombre_interno"] == nombre {
				bid := texto(b["bloque_id"])
				c.Intentar(ctx, "Actualizar bloque "+nombre, "PATCH", "/api/plantillas/bloques/"+bid, body)
				return bid
			}
		}
		r := c.Intentar(ctx, "Bloque "+nombre, "POST", "/api/plantillas/secciones/"+sid+"/bloques", body)
		return texto(r.Datos["bloque_id"])
	}
	vincular := func(bid, tipo, fuente string) {
		if bid != "" {
			c.Intentar(ctx, "Vincular "+fuente, "POST", "/api/plantillas/bloques/"+bid+"/vinculacion", map[string]any{"calculadora_id": c.ID("CALC"), "fuente_tipo": tipo, "fuente_id": fuente})
		}
	}
	sid, bs := seccion("Portada")
	bloque(sid, bs, "TEXTO", "PORTADA", "ISA Custom", "Agentes de Inteligencia Artificial diseñados para su operación")
	for _, cab := range []struct{ nombre, titulo, fuente string }{{"EMPRESA", "Propuesta preparada para", "empresa"}, {"CONTACTO", "Contacto", "contacto"}, {"FECHA_COTIZACION", "Fecha", "fecha_creacion"}, {"NUMERO_COTIZACION", "Oferta", "codigo_oferta"}} {
		vincular(bloque(sid, bs, "CAMPO_VINCULADO", cab.nombre, cab.titulo, ""), "COTIZACION_BASE", cab.fuente)
	}
	sid, bs = seccion("1. Solución propuesta")
	bloque(sid, bs, "TEXTO", "SOLUCION", "Solución propuesta", "Se propone implementar [CANTIDAD_AGENTES] agente(s) de Inteligencia Artificial orientado(s) a [TIPO_AGENTE]. La solución [NOMBRE_SOLUCION] se configura de acuerdo con los canales, capacidad e integraciones seleccionadas.")
	sid, bs = seccion("2. Configuración")
	for _, campo := range []string{"TIPO_AGENTE", "CANTIDAD_AGENTES", "IDIOMA"} {
		vincular(bloque(sid, bs, "CAMPO_VINCULADO", campo, campo, ""), "CAMPO", c.ID(campo))
	}
	sid, bs = seccion("3. Canales")
	for _, canal := range []struct{ campo, nombre string }{{"USA_WHATSAPP", "WhatsApp"}, {"USA_CHATWEB", "Chat Web"}, {"USA_TEAMS", "Microsoft Teams"}, {"USA_TELEFONIA", "Telefonía IA"}} {
		bid := bloque(sid, bs, "LISTA", canal.campo, canal.nombre, canal.nombre)
		c.condicion(ctx, bid, canal.campo)
	}
	sid, bs = seccion("4. Capacidad")
	for _, campo := range []string{"CONVERSACIONES_MES", "MINUTOS_MES"} {
		vincular(bloque(sid, bs, "CAMPO_VINCULADO", campo, campo, ""), "CAMPO", c.ID(campo))
	}
	sid, bs = seccion("5. Integraciones")
	bid := bloque(sid, bs, "TEXTO", "INTEGRACIONES", "Integraciones", "La solución contempla [CANTIDAD_INTEGRACIONES] integración(es) con sistemas externos. El alcance técnico, disponibilidad de API, autenticación, seguridad y operaciones permitidas deberán validarse durante la implementación.")
	c.condicion(ctx, bid, "INTEGRACION_API")
	sid, bs = seccion("6. Opciones")
	bt := bloque(sid, bs, "TABLA_INVERSION", "TABLA_ESCENARIOS", "Opciones comerciales", "")
	columnas := []map[string]any{}
	for _, b := range bs {
		if texto(b["bloque_id"]) == bt {
			columnas = Lista(b["columnas"])
		}
	}
	for _, col := range []struct{ titulo, tipo, fuente string }{{"Concepto", "NOMBRE_ESCENARIO", ""}, {"Implementación", "CAMPO", c.ID("TOTAL_INICIAL")}, {"Mensualidad", "CAMPO", c.ID("TOTAL_MENSUAL")}, {"Recomendada", "ES_RECOMENDADA", ""}} {
		body := map[string]any{"calculadora_id": c.ID("CALC"), "titulo": col.titulo, "fuente_tipo": col.tipo, "fuente_id": col.fuente}
		colID := ""
		for _, v := range columnas {
			if v["titulo"] == col.titulo {
				colID = texto(v["columna_id"])
				break
			}
		}
		if bt == "" {
			continue
		}
		if colID != "" {
			delete(body, "calculadora_id")
			c.Intentar(ctx, "Columna "+col.titulo, "PATCH", "/api/plantillas/columnas/"+colID, body)
		} else {
			c.Intentar(ctx, "Columna "+col.titulo, "POST", "/api/plantillas/bloques/"+bt+"/columnas", body)
		}
	}
	sid, bs = seccion("7. Inversión")
	for _, campo := range []string{"PRECIO_IMPLEMENTACION", "TOTAL_MENSUAL", "TOTAL_PRIMER_MES"} {
		vincular(bloque(sid, bs, "CAMPO_VINCULADO", campo, campo, ""), "CAMPO", c.ID(campo))
	}
	sid, bs = seccion("8. Alcance")
	bloque(sid, bs, "CONDICIONES", "ALCANCE", "Supuestos de implementación", "Precios técnicos de prueba. Validar accesos, información, disponibilidad de API y alcance antes de la implementación.")
	sid, bs = seccion("9. Próximos pasos")
	bloque(sid, bs, "LISTA", "PROXIMOS_PASOS", "Próximos pasos", "Aprobación, configuración, pruebas y salida a producción.")
	c.Intentar(ctx, "Estilo ISA Custom", "PATCH", "/api/plantillas/"+id+"/estilo", map[string]any{"tema": "CORPORATIVO", "formato_pagina": "A4", "color_primario": "#0B2F63", "mostrar_organizacion": true, "nombre_organizacion_visible": "Exceltec", "texto_encabezado": "ISA Custom", "texto_pie": "Propuesta de prueba", "numerar_paginas": true})
	if len(c.Incidencias) == 0 || diagnostico {
		c.Intentar(ctx, "Publicar plantilla", "POST", "/api/plantillas/"+id+"/publicar", map[string]any{})
	}
	return id
}

func (c *Cliente) condicion(ctx context.Context, bloque, campo string) {
	if bloque == "" {
		return
	}
	c.Intentar(ctx, "Condición "+campo, "POST", "/api/plantillas/bloques/"+bloque+"/condicion", map[string]any{"calculadora_id": c.ID("CALC"), "fuente_tipo": "CAMPO", "fuente_id": c.ID(campo), "operador": "IGUAL_A", "valor_comparacion": "SI"})
}
