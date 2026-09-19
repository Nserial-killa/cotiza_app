package handlers

import (
	"fmt"
	"strconv"
	"strings"
)

// reglaCotizadorEval es la forma mínima de una fila de reglas_cotizador (+
// sus campos_objetivo ya resueltos) que necesita el motor de evaluación.
// Vive separada del struct de lectura/escritura de reglas_cotizador.go a
// propósito: esta es la que usa el motor puro (sin acceso a base de datos),
// así se puede probar con datos armados a mano en pruebas unitarias.
type reglaCotizadorEval struct {
	ReglaID          string
	CampoCondicionID string
	Operador         string
	ValorComparacion string
	Accion           string
	CamposObjetivo   []string
	ValorAccion      string
	Mensaje          string
	Activo           bool
}

// estadoCampoRegla es el resultado de aplicar todas las reglas de
// visibilidad/acción activas cuya condición se cumple, para UN campo
// objetivo. Un campo que ninguna regla toca no aparece en el mapa que
// devuelve evaluarEstadoCamposRegla — el llamador debe tratar esa ausencia
// como el default (visible, habilitado, sin mínimo, no requerido).
type estadoCampoRegla struct {
	Visible    bool     `json:"visible"`
	Habilitado bool     `json:"habilitado"`
	ForzarCero bool     `json:"forzar_cero"`
	Minimo     *float64 `json:"minimo,omitempty"`
	Requerido  bool     `json:"requerido"`
}

// errorValidacionRegla es un error de guardado producido por una regla de
// validación (BLOQUEAR_GUARDADO o CAMPO_REQUERIDO) cuya condición se
// cumplió contra los valores que se están por guardar.
type errorValidacionRegla struct {
	ReglaID string `json:"regla_id"`
	Mensaje string `json:"mensaje"`
}

// accionesValidacion son las dos acciones que NO cambian el estado visual
// de un campo sino que bloquean el guardado — se evalúan aparte
// (evaluarValidacionReglas), nunca dentro de evaluarEstadoCamposRegla.
var accionesValidacion = map[string]bool{"BLOQUEAR_GUARDADO": true, "CAMPO_REQUERIDO": true}

// acumuladorEstadoCampo junta, campo por campo, las banderas de todas las
// reglas que lo tocan antes de decidir el estado final — así el criterio
// "más restrictivo gana" no depende del orden en que llegan las reglas.
type acumuladorEstadoCampo struct {
	ocultar      bool
	deshabilitar bool
	forzarCero   bool
	requerido    bool
	minimo       *float64
}

// evaluarEstadoCamposRegla recorre las reglas activas de una calculadora y
// devuelve, por cada campo objetivo alcanzado por al menos una regla cuya
// condición se cumple, su estado final.
//
// Criterio de conflicto (documentado a propósito, es una decisión de
// producto, no un detalle técnico): si dos reglas contradictorias afectan
// el mismo campo — una lo OCULTA y otra lo MUESTRA, o una lo DESHABILITA y
// otra lo HABILITA — gana la más restrictiva (OCULTAR/DESHABILITAR). Por
// eso MOSTRAR/HABILITAR no "activan" nada por sí solos acá: el default de
// un campo ya es visible+habilitado, así que MOSTRAR/HABILITAR solo
// existen para que el Diseñador pueda expresar la intención explícita "no
// lo toques", nunca para revertir un OCULTAR/DESHABILITAR de otra regla.
func evaluarEstadoCamposRegla(valores map[string]any, reglas []reglaCotizadorEval) map[string]estadoCampoRegla {
	acumulado := make(map[string]*acumuladorEstadoCampo)
	for _, regla := range reglas {
		if !regla.Activo || accionesValidacion[regla.Accion] {
			continue
		}
		if !evaluarCondicionRegla(valores, regla) {
			continue
		}
		for _, campoID := range regla.CamposObjetivo {
			campoID = strings.TrimSpace(campoID)
			if campoID == "" {
				continue
			}
			a, existe := acumulado[campoID]
			if !existe {
				a = &acumuladorEstadoCampo{}
				acumulado[campoID] = a
			}
			switch regla.Accion {
			case "OCULTAR":
				a.ocultar = true
			case "DESHABILITAR":
				a.deshabilitar = true
			case "PONER_EN_CERO":
				a.forzarCero = true
			case "CAMPO_REQUERIDO":
				a.requerido = true
			case "EXIGIR_MINIMO":
				if minimo, err := strconv.ParseFloat(strings.TrimSpace(regla.ValorAccion), 64); err == nil {
					if a.minimo == nil || minimo > *a.minimo {
						a.minimo = &minimo
					}
				}
			}
			// MOSTRAR/HABILITAR: sin efecto, ver comentario de la función.
		}
	}

	resultado := make(map[string]estadoCampoRegla, len(acumulado))
	for campoID, a := range acumulado {
		resultado[campoID] = estadoCampoRegla{
			Visible:    !a.ocultar,
			Habilitado: !a.deshabilitar,
			ForzarCero: a.forzarCero,
			Minimo:     a.minimo,
			Requerido:  a.requerido,
		}
	}
	return resultado
}

// evaluarValidacionReglas recorre las reglas de validación (BLOQUEAR_
// GUARDADO, CAMPO_REQUERIDO) cuya condición se cumple contra los valores
// que se están por guardar, y devuelve un error por cada disparo — CERO
// errores significa que el guardado puede continuar. BLOQUEAR_GUARDADO no
// necesita campos_objetivo (la condición sola ya bloquea todo, ej. "R07:
// CANTIDAD_AGENTES < 1 → bloquear guardado"); CAMPO_REQUERIDO dispara un
// error por cada campo objetivo que todavía esté vacío.
func evaluarValidacionReglas(valores map[string]any, reglas []reglaCotizadorEval) []errorValidacionRegla {
	errores := make([]errorValidacionRegla, 0)
	for _, regla := range reglas {
		if !regla.Activo || !accionesValidacion[regla.Accion] {
			continue
		}
		if !evaluarCondicionRegla(valores, regla) {
			continue
		}
		switch regla.Accion {
		case "BLOQUEAR_GUARDADO":
			errores = append(errores, errorValidacionRegla{
				ReglaID: regla.ReglaID,
				Mensaje: mensajeReglaODefecto(regla, "No se puede guardar: se cumple una condición que bloquea el guardado."),
			})
		case "CAMPO_REQUERIDO":
			for _, campoID := range regla.CamposObjetivo {
				campoID = strings.TrimSpace(campoID)
				if campoID == "" {
					continue
				}
				if valorVacioRegla(valores[campoID]) {
					errores = append(errores, errorValidacionRegla{
						ReglaID: regla.ReglaID,
						Mensaje: mensajeReglaODefecto(regla, fmt.Sprintf("El campo %s es obligatorio.", campoID)),
					})
				}
			}
		}
	}
	return errores
}

func mensajeReglaODefecto(regla reglaCotizadorEval, defecto string) string {
	if strings.TrimSpace(regla.Mensaje) != "" {
		return strings.TrimSpace(regla.Mensaje)
	}
	return defecto
}

// evaluarCondicionRegla decide si la condición (campo_condicion_id +
// operador + valor_comparacion) de una regla se cumple contra "valores"
// (elemento_id -> valor guardado, tal como llega de cotizacion_valores:
// string para CAMPO/CAMPO_CATALOGO, float64 si ya viene numérico).
//
// Un campo que todavía no tiene valor guardado (el usuario no llegó a
// responderlo) solo puede disparar ESTA_VACIO — cualquier otro operador
// sobre un campo sin valor se considera "no cumple" en vez de arriesgar un
// falso positivo con un valor inventado.
func evaluarCondicionRegla(valores map[string]any, regla reglaCotizadorEval) bool {
	valor, existe := valores[regla.CampoCondicionID]
	vacio := !existe || valorVacioRegla(valor)

	switch regla.Operador {
	case "ESTA_VACIO":
		return vacio
	case "NO_ESTA_VACIO":
		return !vacio
	}
	if vacio {
		return false
	}

	switch regla.Operador {
	case "IGUAL_A":
		return compararIgualdadRegla(valor, regla.ValorComparacion)
	case "DISTINTO_DE":
		return !compararIgualdadRegla(valor, regla.ValorComparacion)
	case "MAYOR_QUE", "MENOR_QUE", "MAYOR_O_IGUAL_QUE", "MENOR_O_IGUAL_QUE":
		numeroActual, ok := numeroDesdeValor(valor)
		if !ok {
			return false
		}
		numeroComparado, err := strconv.ParseFloat(strings.TrimSpace(regla.ValorComparacion), 64)
		if err != nil {
			return false
		}
		switch regla.Operador {
		case "MAYOR_QUE":
			return numeroActual > numeroComparado
		case "MENOR_QUE":
			return numeroActual < numeroComparado
		case "MAYOR_O_IGUAL_QUE":
			return numeroActual >= numeroComparado
		case "MENOR_O_IGUAL_QUE":
			return numeroActual <= numeroComparado
		}
	}
	return false
}

func valorVacioRegla(valor any) bool {
	switch v := valor.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return false
	}
}

func compararIgualdadRegla(valor any, comparado string) bool {
	return strings.EqualFold(strings.TrimSpace(valorComoTextoRegla(valor)), strings.TrimSpace(comparado))
}

func valorComoTextoRegla(valor any) string {
	if valor == nil {
		return ""
	}
	if texto, ok := valor.(string); ok {
		return texto
	}
	return fmt.Sprint(valor)
}
