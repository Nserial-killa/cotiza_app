package isacustom

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type Informe struct {
	Completo      bool             `json:"completo"`
	Diagnostico   bool             `json:"diagnostico"`
	CalculadoraID string           `json:"calculadora_id"`
	ClienteID     string           `json:"cliente_id"`
	CotizacionID  string           `json:"cotizacion_id"`
	PlantillaID   string           `json:"plantilla_id"`
	Compilado     bool             `json:"compilado"`
	Opciones      []map[string]any `json:"opciones"`
	Incidencias   []Incidencia     `json:"incidencias"`
}

// Sembrar conserva las seis tabs del PDF. Diagnostico permite publicar la
// estructura PARCIAL que el API aceptó para probar piezas independientes;
// jamás la declara completa. El CLI devuelve código 2 si hay incidencias.
func (c *Cliente) Sembrar(ctx context.Context, d Datos, diagnostico bool) Informe {
	r := Informe{Diagnostico: diagnostico, CalculadoraID: c.ID("CALC")}
	yaPublicado := false
	anteriores := c.Intentar(ctx, "Buscar cotización previa", "GET", "/api/cotizaciones?calculadora_id="+url.QueryEscape(r.CalculadoraID), nil)
	for _, cot := range Lista(anteriores.Datos["cotizaciones"]) {
		if cot["tipo_propuesta"] == "ISA Custom" {
			runtime, err := c.Llamar(ctx, "GET", "/api/cotizador/runtime/"+texto(cot["cotizacion_id"]), nil)
			yaPublicado = err == nil && runtime.OK()
			break
		}
	}
	c.Intentar(ctx, "Cotizador", "POST", "/api/calculadoras", map[string]any{"calculadora_id": r.CalculadoraID, "nombre_calculadora": "ISA Custom (" + c.Prefijo + ")", "linea_negocio": "Inteligencia Artificial", "servicio_base": "ISA Custom", "descripcion": "Caso de integración del PDF. Consultar el informe de auditoría antes de usar: puede estar incompleto."})
	for i, cat := range d.Catalogos {
		c.Intentar(ctx, cat.Codigo, "POST", "/api/catalogos", map[string]any{"catalogo_id": c.ID(cat.Codigo), "nombre_catalogo": cat.Codigo, "alcance": "COTIZADOR", "tipo_calculo": cat.TipoCalculo, "orden": i, "activo": true})
		for j, v := range cat.Valores {
			c.Intentar(ctx, cat.Codigo+"/"+texto(v[0]), "POST", "/api/catalogos/valores", c.ValorCatalogo(cat.Codigo, texto(v[0]), texto(v[1]), v[2], j))
		}
	}
	for i, seccion := range d.Secciones {
		c.Intentar(ctx, seccion, "POST", "/api/cotizador/tabs", map[string]any{"tab_id": c.ID(seccion), "calculadora_id": r.CalculadoraID, "nombre": seccion, "orden": i + 1, "alcance": "PROPIO", "activo": true})
		columnas := 2
		if seccion == "03_CHAT" || seccion == "05_IMPL" {
			columnas = 3
		}
		c.Intentar(ctx, "Distribución "+seccion, "POST", "/api/cotizador/elementos", map[string]any{"elemento_id": c.ID(seccion + "-GRID"), "tab_id": c.ID(seccion), "tipo": "CONTENEDOR", "etiqueta": seccion, "columnas_ancho": 4, "orden": 0, "activo": true, "configuracion": map[string]any{"columnas": columnas}})
	}
	padre := map[string]any{"elemento_id": c.ID("ESCENARIOS"), "tab_id": c.ID("01_CONFIG"), "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Opciones ISA Custom", "columnas_ancho": 4, "orden": 1, "activo": true, "configuracion": map[string]any{"cantidad_inicial": 3, "nombres_sugeridos": "Recomendada, Solo Chat, Multicanal", "vista_editar": "PESTANAS", "vista_resumen": "TABLA_COMPARATIVA", "vista_oferta": "TABLA_COMPARATIVA", "permitir_duplicar": true, "permitir_eliminar": true, "permitir_renombrar": true, "permitir_recomendado": true}}
	c.Intentar(ctx, "Escenarios", "POST", "/api/cotizador/elementos", padre)
	// Primero se registra la distribución original. El segundo paso intenta
	// incorporar esos mismos campos a las opciones, sin duplicar ni mover tabs.
	for i, campo := range d.Entradas() {
		body := c.Elemento(campo, i+2)
		body["componente_padre_id"] = c.ID(campo.Seccion + "-GRID")
		body["columnas_ancho"] = 1
		c.Intentar(ctx, "Campo "+campo.Codigo, "POST", "/api/cotizador/elementos", body)
		if campo.Codigo != "MONEDA" {
			body["componente_padre_id"] = c.ID("ESCENARIOS")
			c.Intentar(ctx, "Escenario/campo "+campo.Codigo, "POST", "/api/cotizador/elementos", body)
		}
	}
	formulas := append(append([]Formula{}, d.Formulas...), d.Derivados...)
	for i, f := range formulas {
		body := c.ElementoFormula(f, 100+i)
		s := c.Intentar(ctx, "Fórmula "+f.Codigo, "POST", "/api/cotizador/elementos", body)
		if s.OK() {
			body["componente_padre_id"] = c.ID("ESCENARIOS")
			c.Intentar(ctx, "Escenario/fórmula "+f.Codigo, "POST", "/api/cotizador/elementos", body)
		}
	}
	c.reglas(ctx)
	for _, salida := range []struct{ clave, fuente string }{{"TOTAL_PRECIO", "TOTAL_MENSUAL"}, {"TOTAL_COSTO", "TOTAL_COSTO"}, {"TOTAL_GANANCIA", "TOTAL_GANANCIA"}, {"MARGEN_TOTAL", "MARGEN_TOTAL"}, {"MONEDA", "MONEDA"}} {
		tipo := "ESCENARIO"
		if salida.clave == "MONEDA" {
			tipo = "CAMPO"
		}
		c.Intentar(ctx, "Salida "+salida.clave, "POST", "/api/cotizador/salidas", map[string]any{"calculadora_id": r.CalculadoraID, "clave_salida": salida.clave, "tipo_fuente": tipo, "fuente_id": c.ID(salida.fuente), "requerido": true, "activo": true})
	}
	bodyCalc := map[string]any{"calculadora_id": r.CalculadoraID}
	v := c.Intentar(ctx, "Validación", "POST", "/api/cotizador/validar", bodyCalc)
	if v.OK() && v.Datos["valido"] != true {
		c.Advertir("Validación", fmt.Sprint(v.Datos["errores"]))
	}
	if len(c.Incidencias) == 0 || diagnostico {
		if yaPublicado {
			r.Compilado = true
		} else {
			p := c.Intentar(ctx, "Publicación", "POST", "/api/cotizador/compilar", bodyCalc)
			r.Compilado = p.OK() && p.Datos["compilado"] == true
			if p.OK() && !r.Compilado {
				c.Advertir("Publicación", fmt.Sprint(p.Datos["errores"]))
			}
		}
	}
	r.ClienteID = c.ensureCliente(ctx)
	if r.Compilado && r.ClienteID != "" {
		res := c.Intentar(ctx, "Buscar cotización", "GET", "/api/cotizaciones?calculadora_id="+url.QueryEscape(r.CalculadoraID), nil)
		for _, cot := range Lista(res.Datos["cotizaciones"]) {
			if texto(cot["tipo_propuesta"]) == "ISA Custom" {
				r.CotizacionID = texto(cot["cotizacion_id"])
				break
			}
		}
		if r.CotizacionID == "" {
			r.CotizacionID = c.NuevaCotizacion(ctx, r.ClienteID)
		}
		if r.CotizacionID != "" {
			runtime := c.Intentar(ctx, "Abrir runtime", "GET", "/api/cotizador/runtime/"+r.CotizacionID, nil)
			r.Opciones = Lista(Elementos(runtime.Datos)[c.ID("ESCENARIOS")]["opciones"])
			if len(r.Opciones) == 3 {
				c.AccionOpcion(ctx, r.CotizacionID, "RECOMENDAR", texto(r.Opciones[0]["opcion_id"]), "", true)
				for i, op := range r.Opciones {
					c.Intentar(ctx, "Valores completos "+d.Escenarios[i].Nombre, "POST", "/api/cotizador/runtime/"+r.CotizacionID+"/valores", c.CuerpoEscenario(d, i, texto(op["opcion_id"])))
				}
			}
		}
	}
	r.PlantillaID = c.plantilla(ctx, diagnostico)
	r.Incidencias = append([]Incidencia{}, c.Incidencias...)
	r.Completo = len(r.Incidencias) == 0 && r.Compilado && r.CotizacionID != "" && r.PlantillaID != ""
	return r
}

func (c *Cliente) ensureCliente(ctx context.Context) string {
	nombre := "Cliente de prueba ISA Custom " + c.Prefijo
	r := c.Intentar(ctx, "Buscar cliente", "GET", "/api/clientes", nil)
	for _, cl := range Lista(r.Datos["clientes"]) {
		if cl["nombre_comercial"] == nombre {
			return texto(cl["cliente_id"])
		}
	}
	r = c.Intentar(ctx, "Crear cliente", "POST", "/api/clientes", map[string]any{"nombre_comercial": nombre, "razon_social": nombre + " S.A."})
	return texto(r.Datos["cliente_id"])
}

func (c *Cliente) NuevaCotizacion(ctx context.Context, cliente string) string {
	r := c.Intentar(ctx, "Crear cotización", "POST", "/api/cotizaciones", map[string]any{"cliente_id": cliente, "calculadora_id": c.ID("CALC"), "tipo_propuesta": "ISA Custom"})
	return texto(r.Datos["cotizacion_id"])
}

func (c *Cliente) CuerpoEscenario(d Datos, indice int, opcion string) map[string]any {
	filas := []map[string]any{}
	valores := d.ValoresEscenario(indice)
	for _, campo := range d.Entradas() {
		if campo.Codigo == "MONEDA" {
			continue
		}
		filas = append(filas, map[string]any{"elemento_id": c.ID(campo.Codigo), "opcion_id": opcion, "valor": valores[campo.Codigo]})
	}
	return map[string]any{"version": 1, "valores": map[string]any{c.ID("MONEDA"): "USD"}, "valores_por_opcion": filas}
}

func (c *Cliente) AccionOpcion(ctx context.Context, cot, accion, opcion, nombre string, recomendada bool) Respuesta {
	return c.Intentar(ctx, "Opción "+accion, "POST", "/api/cotizador/runtime/"+cot+"/opciones", map[string]any{"version": 1, "elemento_padre_id": c.ID("ESCENARIOS"), "accion": accion, "opcion_id": opcion, "nombre": nombre, "es_recomendada": recomendada})
}

func (c *Cliente) reglas(ctx context.Context) {
	regla := func(codigo, campo, operador, comparacion, accion, objetivos, valor string) {
		ids := []string{}
		for _, v := range strings.Fields(objetivos) {
			ids = append(ids, c.ID(v))
		}
		c.Intentar(ctx, "Regla "+codigo, "POST", "/api/cotizador/reglas", map[string]any{"regla_id": c.ID(codigo), "calculadora_id": c.ID("CALC"), "nombre": codigo, "campo_condicion_id": c.ID(campo), "operador": operador, "valor_comparacion": comparacion, "accion": accion, "campos_objetivo": ids, "valor_accion": valor, "mensaje": codigo + ": revisar los campos dependientes de ISA Custom.", "activo": true})
	}
	regla("R01-OCULTAR", "USA_TELEFONIA", "IGUAL_A", "NO", "OCULTAR", "MINUTOS_MES MINUTOS_EXTRA", "")
	regla("R01-CERO", "USA_TELEFONIA", "IGUAL_A", "NO", "PONER_EN_CERO", "COSTO_VOZ PRECIO_VOZ CONSUMO_EXTRA_VOZ", "")
	regla("R02", "USA_TELEFONIA", "IGUAL_A", "SI", "MOSTRAR", "MINUTOS_MES MINUTOS_EXTRA", "")
	regla("R03-OCULTAR", "INTEGRACION_API", "IGUAL_A", "NO", "OCULTAR", "CANTIDAD_INTEGRACIONES", "")
	regla("R03-CERO", "INTEGRACION_API", "IGUAL_A", "NO", "PONER_EN_CERO", "PRECIO_INTEGRACIONES", "")
	regla("R04-MOSTRAR", "INTEGRACION_API", "IGUAL_A", "SI", "MOSTRAR", "CANTIDAD_INTEGRACIONES", "")
	regla("R04-MINIMO", "INTEGRACION_API", "IGUAL_A", "SI", "EXIGIR_MINIMO", "CANTIDAD_INTEGRACIONES", "1")
	regla("R05", "CONVERSACIONES_EXTRA", "IGUAL_A", "0", "PONER_EN_CERO", "CONSUMO_EXTRA_CHAT", "")
	regla("R06", "MINUTOS_EXTRA", "IGUAL_A", "0", "PONER_EN_CERO", "CONSUMO_EXTRA_VOZ", "")
	regla("R07", "CANTIDAD_AGENTES", "MENOR_QUE", "1", "BLOQUEAR_GUARDADO", "", "")
	regla("R08-CHAT", "MARGEN_CHAT", "ESTA_VACIO", "", "CAMPO_REQUERIDO", "MARGEN_CHAT", "")
	regla("R08-VOZ", "MARGEN_VOZ", "ESTA_VACIO", "", "CAMPO_REQUERIDO", "MARGEN_VOZ", "")
	// R09 trabaja con metadatos de opciones, no con un campo escalar. Ya existe
	// una validación nativa: CP-10/11 la ejercitan, sin inventar un campo falso.
	// R10 está implementada por el runtime al aplicar OCULTAR/PONER_EN_CERO.
}
