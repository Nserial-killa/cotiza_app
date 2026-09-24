// Package isacustom configura el caso funcional exclusivamente por HTTP.
// No importa el paquete db ni pgx: ni el seed ni las pruebas fabrican SQL.
package isacustom

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed caso.json
var documento []byte

type Catalogo struct {
	Codigo      string  `json:"codigo"`
	TipoCalculo string  `json:"tipo_calculo"`
	Valores     [][]any `json:"valores"`
}
type Campo struct {
	Seccion  string `json:"seccion"`
	Codigo   string `json:"codigo"`
	Etiqueta string `json:"etiqueta"`
	Tipo     string `json:"tipo"`
	Catalogo string `json:"catalogo"`
	Valor    string `json:"valor"`
}
type Formula struct {
	Codigo        string   `json:"codigo"`
	Texto         string   `json:"texto"`
	Operacion     string   `json:"operacion"`
	Operandos     []string `json:"operandos"`
	TipoResultado string   `json:"tipo_resultado"`
}
type Escenario struct {
	Nombre      string            `json:"nombre"`
	Recomendada bool              `json:"recomendada"`
	Valores     map[string]string `json:"valores"`
}
type Datos struct {
	Secciones  []string          `json:"secciones"`
	Catalogos  []Catalogo        `json:"catalogos"`
	Campos     []Campo           `json:"campos"`
	Parametros map[string]string `json:"parametros"`
	Formulas   []Formula         `json:"formulas"`
	Derivados  []Formula         `json:"derivados_salidas"`
	Escenarios []Escenario       `json:"escenarios"`
}

func CargarDatos() (Datos, error) {
	var d Datos
	err := json.Unmarshal(documento, &d)
	return d, err
}

func (d Datos) Entradas() []Campo {
	r := append([]Campo{}, d.Campos...)
	claves := make([]string, 0, len(d.Parametros))
	for k := range d.Parametros {
		claves = append(claves, k)
	}
	sort.Strings(claves)
	for _, k := range claves {
		r = append(r, Campo{"06_COMERCIAL", k, k, "MONEDA", "", d.Parametros[k]})
	}
	return append(r, Campo{"06_COMERCIAL", "MONEDA", "Moneda", "TEXTO", "", "USD"})
}

func (d Datos) ValoresEscenario(indice int) map[string]string {
	valores := map[string]string{}
	for _, c := range d.Entradas() {
		valores[c.Codigo] = c.Valor
	}
	for k, v := range d.Escenarios[indice].Valores {
		valores[k] = v
	}
	return valores
}

func (c *Cliente) Elemento(campo Campo, orden int) map[string]any {
	tipo := "CAMPO"
	cfg := map[string]any{"nombre_interno": campo.Codigo, "nombre_elemento": campo.Codigo, "tipo_campo": campo.Tipo, "valor_por_defecto": campo.Valor}
	r := map[string]any{"elemento_id": c.ID(campo.Codigo), "tab_id": c.ID(campo.Seccion), "tipo": tipo,
		"etiqueta": campo.Etiqueta, "columnas_ancho": 2, "orden": orden, "activo": true, "configuracion": cfg}
	if campo.Catalogo != "" {
		r["tipo"] = "CAMPO_CATALOGO"
		r["catalogo_id"] = c.ID(campo.Catalogo)
	}
	return r
}

func (c *Cliente) ElementoFormula(f Formula, orden int) map[string]any {
	tipo := f.TipoResultado
	if tipo == "" {
		tipo = "MONEDA"
	}
	cfg := map[string]any{"nombre_interno": f.Codigo, "nombre_elemento": f.Codigo, "tipo_formula": "AVANZADA", "formula_texto": f.Texto, "tipo_resultado": tipo, "decimales": 2}
	if f.Operacion != "" {
		ops := []string{}
		for _, op := range f.Operandos {
			ops = append(ops, c.ID(op))
		}
		cfg["tipo_formula"] = "SIMPLE"
		cfg["operacion"] = f.Operacion
		cfg["operandos"] = ops
		delete(cfg, "formula_texto")
	}
	return map[string]any{"elemento_id": c.ID(f.Codigo), "tab_id": c.ID("06_COMERCIAL"), "tipo": "CAMPO_CALCULADO",
		"etiqueta": f.Codigo, "columnas_ancho": 2, "orden": orden, "activo": true, "configuracion": cfg}
}

func (c *Cliente) ValorCatalogo(catalogo, codigo, etiqueta string, calculo any, orden int) map[string]any {
	return map[string]any{"valor_id": c.ID(catalogo + "-" + codigo), "catalogo_id": c.ID(catalogo), "clave": codigo,
		"valor_sistema": codigo, "texto_visible": etiqueta, "valor_calculo": calculo, "orden": orden, "activo": true}
}

func mapa(v any) map[string]any { r, _ := v.(map[string]any); return r }
func texto(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func Lista(v any) []map[string]any {
	r := []map[string]any{}
	if items, ok := v.([]any); ok {
		for _, item := range items {
			if m := mapa(item); m != nil {
				r = append(r, m)
			}
		}
	}
	return r
}

func Elementos(runtime map[string]any) map[string]map[string]any {
	r := map[string]map[string]any{}
	var recorrer func(any)
	recorrer = func(v any) {
		for _, el := range Lista(v) {
			r[texto(el["elemento_id"])] = el
			recorrer(el["hijos"])
		}
	}
	for _, tab := range Lista(mapa(runtime["estructura"])["tabs"]) {
		recorrer(tab["elementos"])
	}
	return r
}
