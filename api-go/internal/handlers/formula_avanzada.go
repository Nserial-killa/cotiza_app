package handlers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// La gramática es deliberadamente cerrada: números decimales sin signo,
// nombres ASCII, aritmética, paréntesis y SI(token; expresión; expresión).
// Los límites acotan también la profundidad del árbol que después se evalúa.
const longitudMaximaFormula = 4096
const profundidadMaximaFormula = 64

type usoTokenFormula struct {
	Numerico  bool
	Condicion bool
}

type formulaAvanzada struct {
	raiz   *nodoFormula
	Tokens []string
	Usos   map[string]usoTokenFormula
}

type nodoFormula struct {
	tipo               byte
	numero             float64
	nombre             string
	izquierda, derecha *nodoFormula
	profundidad        int
}

type tokenFormula struct {
	tipo     byte
	texto    string
	posicion int
}

type parserFormula struct {
	tokens []tokenFormula
	actual int
	nivel  int
	formulaAvanzada
}

func letraFormula(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}

func digitoFormula(c byte) bool { return c >= '0' && c <= '9' }

func tokenizarFormula(texto string) ([]tokenFormula, error) {
	if len(texto) > longitudMaximaFormula {
		return nil, fmt.Errorf("la fórmula no puede exceder %d caracteres", longitudMaximaFormula)
	}
	tokens := make([]tokenFormula, 0)
	for i := 0; i < len(texto); {
		inicio := i
		c := texto[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
			continue
		case letraFormula(c):
			i++
			for i < len(texto) && (letraFormula(texto[i]) || digitoFormula(texto[i])) {
				i++
			}
			tokens = append(tokens, tokenFormula{'t', texto[inicio:i], inicio + 1})
		case digitoFormula(c):
			for i < len(texto) && digitoFormula(texto[i]) {
				i++
			}
			if i < len(texto) && texto[i] == '.' {
				i++
				decimal := i
				for i < len(texto) && digitoFormula(texto[i]) {
					i++
				}
				if decimal == i {
					return nil, fmt.Errorf("número decimal incompleto en la posición %d", inicio+1)
				}
			}
			tokens = append(tokens, tokenFormula{'n', texto[inicio:i], inicio + 1})
		case strings.ContainsRune("+-*/();", rune(c)):
			tokens = append(tokens, tokenFormula{c, string(c), inicio + 1})
			i++
		default:
			return nil, fmt.Errorf("carácter no permitido en la posición %d: solo se admiten números, nombres de campos, +, -, *, /, paréntesis y SI(token; entonces; sino)", i+1)
		}
	}
	return append(tokens, tokenFormula{0, "fin de fórmula", len(texto) + 1}), nil
}

func compilarFormulaAvanzada(texto string) (*formulaAvanzada, error) {
	tokens, err := tokenizarFormula(texto)
	if err != nil {
		return nil, err
	}
	p := parserFormula{tokens: tokens, formulaAvanzada: formulaAvanzada{Tokens: []string{}, Usos: map[string]usoTokenFormula{}}}
	raiz, err := p.expresion()
	if err != nil {
		return nil, err
	}
	if p.ver().tipo != 0 {
		return nil, p.errorEsperado("fin de fórmula")
	}
	p.raiz = raiz
	return &p.formulaAvanzada, nil
}

func (p *parserFormula) ver() tokenFormula { return p.tokens[p.actual] }

func (p *parserFormula) errorEsperado(esperado string) error {
	t := p.ver()
	return fmt.Errorf("fórmula inválida en la posición %d: se esperaba %s; se encontró %q", t.posicion, esperado, t.texto)
}

func (p *parserFormula) consumir(tipo byte, descripcion string) error {
	if p.ver().tipo != tipo {
		return p.errorEsperado(descripcion)
	}
	p.actual++
	return nil
}

func (p *parserFormula) registrar(nombre string, condicion bool) {
	uso, existe := p.Usos[nombre]
	if !existe {
		p.Tokens = append(p.Tokens, nombre)
	}
	if condicion {
		uso.Condicion = true
	} else {
		uso.Numerico = true
	}
	p.Usos[nombre] = uso
}

func unirNodosFormula(tipo byte, nombre string, izquierda, derecha *nodoFormula) (*nodoFormula, error) {
	profundidad := 1 + max(izquierda.profundidad, derecha.profundidad)
	if profundidad > profundidadMaximaFormula {
		return nil, fmt.Errorf("la fórmula excede la profundidad máxima de %d niveles", profundidadMaximaFormula)
	}
	return &nodoFormula{tipo: tipo, nombre: nombre, izquierda: izquierda, derecha: derecha, profundidad: profundidad}, nil
}

func (p *parserFormula) expresion() (*nodoFormula, error) {
	izquierda, err := p.termino()
	for err == nil && (p.ver().tipo == '+' || p.ver().tipo == '-') {
		op := p.ver().tipo
		p.actual++
		var derecha *nodoFormula
		derecha, err = p.termino()
		if err == nil {
			izquierda, err = unirNodosFormula(op, "", izquierda, derecha)
		}
	}
	return izquierda, err
}

func (p *parserFormula) termino() (*nodoFormula, error) {
	izquierda, err := p.factor()
	for err == nil && (p.ver().tipo == '*' || p.ver().tipo == '/') {
		op := p.ver().tipo
		p.actual++
		var derecha *nodoFormula
		derecha, err = p.factor()
		if err == nil {
			izquierda, err = unirNodosFormula(op, "", izquierda, derecha)
		}
	}
	return izquierda, err
}

func (p *parserFormula) factor() (*nodoFormula, error) {
	p.nivel++
	defer func() { p.nivel-- }()
	if p.nivel > profundidadMaximaFormula {
		return nil, fmt.Errorf("la fórmula excede la profundidad máxima de %d niveles", profundidadMaximaFormula)
	}
	t := p.ver()
	switch t.tipo {
	case 'n':
		p.actual++
		numero, err := strconv.ParseFloat(t.texto, 64)
		if err != nil || math.IsInf(numero, 0) || math.IsNaN(numero) {
			return nil, fmt.Errorf("número fuera de rango en la posición %d", t.posicion)
		}
		return &nodoFormula{tipo: 'n', numero: numero, profundidad: 1}, nil
	case 't':
		p.actual++
		if strings.EqualFold(t.texto, "SI") {
			if err := p.consumir('(', "'(' después de SI"); err != nil {
				return nil, err
			}
			condicion := p.ver()
			if condicion.tipo != 't' || strings.EqualFold(condicion.texto, "SI") {
				return nil, p.errorEsperado("un nombre de campo como condición de SI")
			}
			p.actual++
			p.registrar(condicion.texto, true)
			if err := p.consumir(';', "';' después de la condición"); err != nil {
				return nil, err
			}
			entonces, err := p.expresion()
			if err != nil {
				return nil, err
			}
			if err := p.consumir(';', "';' entre las dos ramas de SI"); err != nil {
				return nil, err
			}
			sino, err := p.expresion()
			if err != nil {
				return nil, err
			}
			if err := p.consumir(')', "')' al cerrar SI"); err != nil {
				return nil, err
			}
			return unirNodosFormula('s', condicion.texto, entonces, sino)
		}
		p.registrar(t.texto, false)
		return &nodoFormula{tipo: 't', nombre: t.texto, profundidad: 1}, nil
	case '(':
		p.actual++
		nodo, err := p.expresion()
		if err != nil {
			return nil, err
		}
		return nodo, p.consumir(')', "')'")
	default:
		return nil, p.errorEsperado("un número, un nombre de campo, '(' o SI(token; entonces; sino)")
	}
}

// condicionFormula respeta el contrato de SI: únicamente Sí/SI/true/1
// son verdaderos. No interpreta cualquier número distinto de cero como sí.
func condicionFormula(valor any) bool {
	switch strings.ToUpper(strings.TrimSpace(fmt.Sprint(valor))) {
	case "SÍ", "SI", "TRUE", "1":
		return true
	default:
		return false
	}
}

func (f *formulaAvanzada) evaluar(valores map[string]any) (float64, error) {
	return f.evaluarConResolver(func(nombre string, condicion bool) (any, bool) {
		valor, existe := valores[nombre]
		return valor, existe || condicion
	})
}

// La resolución es perezosa: SI evalúa solo la rama elegida. Un dato aún
// vacío o una división entre cero en la otra rama no bloquean el resultado.
func (f *formulaAvanzada) evaluarConResolver(resolver func(string, bool) (any, bool)) (float64, error) {
	var evaluar func(*nodoFormula) (float64, error)
	evaluar = func(n *nodoFormula) (float64, error) {
		switch n.tipo {
		case 'n':
			return n.numero, nil
		case 't':
			valor, existe := resolver(n.nombre, false)
			numero, numerico := numeroDesdeValor(valor)
			if !existe || !numerico || math.IsInf(numero, 0) || math.IsNaN(numero) {
				return 0, fmt.Errorf("el campo %s no tiene un valor numérico resuelto", n.nombre)
			}
			return numero, nil
		case 's':
			valor, disponible := resolver(n.nombre, true)
			if !disponible {
				return 0, fmt.Errorf("no se pudo resolver la condición %s", n.nombre)
			}
			if condicionFormula(valor) {
				return evaluar(n.izquierda)
			}
			return evaluar(n.derecha)
		}
		izquierda, err := evaluar(n.izquierda)
		if err != nil {
			return 0, err
		}
		derecha, err := evaluar(n.derecha)
		if err != nil {
			return 0, err
		}
		var resultado float64
		switch n.tipo {
		case '+':
			resultado = izquierda + derecha
		case '-':
			resultado = izquierda - derecha
		case '*':
			resultado = izquierda * derecha
		case '/':
			if derecha == 0 {
				return 0, errDivisionEntreCero
			}
			resultado = izquierda / derecha
		}
		if math.IsInf(resultado, 0) || math.IsNaN(resultado) {
			return 0, fmt.Errorf("el resultado de la fórmula está fuera del rango numérico")
		}
		return resultado, nil
	}
	return evaluar(f.raiz)
}
