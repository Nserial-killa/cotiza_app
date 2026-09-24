package handlers

import (
	"context"
	"fmt"
	"strings"
)

// nombreInternoElemento admite el nombre legado usado por el editor. No se
// usan etiquetas ni IDs como sustituto de un nombre interno ausente.
func nombreInternoElemento(cfg map[string]any) string {
	if nombre, ok := cfg["nombre_interno"].(string); ok && strings.TrimSpace(nombre) != "" {
		return strings.TrimSpace(nombre)
	}
	nombre, _ := cfg["nombre_elemento"].(string)
	return strings.TrimSpace(nombre)
}

// prepararFormulaAvanzada deriva las dependencias desde el texto: nunca
// confía en tokens/operandos enviados por el cliente. Se reutiliza al
// publicar para detectar nombres cambiados, ambiguos o campos desactivados.
//
// Ronda F2: los tokens se enlazan en el alcance del COTIZADOR (todas sus
// secciones activas más las asociadas como Secciones Adicionales), no solo
// en la sección del Campo Calculado: el documento ISA exige fórmulas como
// COSTO_CHAT que combinan 03_CHAT con 06_COMERCIAL. calculadoraID es el
// dueño de la sección al guardar, y el cotizador que publica al compilar.
func (h *CotizadorTabsHandler) prepararFormulaAvanzada(ctx context.Context, elementoID, calculadoraID string, cfg map[string]any) error {
	texto, ok := cfg["formula_texto"].(string)
	if !ok || strings.TrimSpace(texto) == "" {
		return fmt.Errorf("formula_texto es obligatorio para una fórmula AVANZADA")
	}
	formula, err := compilarFormulaAvanzada(texto)
	if err != nil {
		return err
	}
	type candidato struct {
		id, tipo, tipoCatalogo string
		activo                 bool
		config                 map[string]any
	}
	porNombre := make(map[string][]candidato)
	rows, err := h.DB.Query(ctx, `
		SELECT e.elemento_id, e.tipo, e.activo, e.configuracion, COALESCE(c.tipo_calculo,'SIN_VALOR')
		FROM elementos_tab_cotizador e
		JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		LEFT JOIN catalogos c ON c.catalogo_id=e.catalogo_id
		WHERE e.elemento_id<>$2 AND `+sqlTabEnAlcanceCotizador, calculadoraID, elementoID)
	if err != nil {
		return fmt.Errorf("no fue posible consultar los campos de la fórmula: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c candidato
		if err := rows.Scan(&c.id, &c.tipo, &c.activo, &c.config, &c.tipoCatalogo); err != nil {
			return err
		}
		nombre := nombreInternoElemento(c.config)
		porNombre[nombre] = append(porNombre[nombre], c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	operandos := make([]string, 0, len(formula.Tokens))
	mapa := make(map[string]string, len(formula.Tokens))
	condiciones := make([]string, 0)
	for _, token := range formula.Tokens {
		if token == nombreInternoElemento(cfg) {
			return fmt.Errorf("referencia circular: el Campo Calculado no puede utilizar su propio nombre interno %s", token)
		}
		candidatos := porNombre[token]
		if len(candidatos) == 0 {
			return fmt.Errorf("el token %s no coincide con el nombre interno de otro elemento de este cotizador", token)
		}
		// Un duplicado inactivo (elemento eliminado) no vuelve ambigua la
		// fórmula; solo lo hacen dos elementos activos con el mismo nombre.
		activos := make([]candidato, 0, len(candidatos))
		for _, c := range candidatos {
			if c.activo {
				activos = append(activos, c)
			}
		}
		if len(activos) > 0 {
			candidatos = activos
		}
		if len(candidatos) > 1 {
			ids := make([]string, 0, len(candidatos))
			for _, c := range candidatos {
				ids = append(ids, c.id)
			}
			return fmt.Errorf("el token %s es ambiguo: hay %d elementos con el mismo nombre interno en el cotizador (%s)", token, len(candidatos), strings.Join(ids, ", "))
		}
		c := candidatos[0]
		if !c.activo {
			return fmt.Errorf("el campo del token %s está inactivo", token)
		}
		uso := formula.Usos[token]
		switch c.tipo {
		case "CAMPO":
			tipoCampo := strings.ToUpper(strings.TrimSpace(fmt.Sprint(c.config["tipo_campo"])))
			if uso.Numerico && tipoCampo != "NUMERO" && tipoCampo != "MONEDA" && tipoCampo != "PORCENTAJE" {
				return fmt.Errorf("el token %s debe ser un Campo numérico para usarse en aritmética; los campos Sí/No solo se admiten como condición de SI", token)
			}
		case "CAMPO_CATALOGO":
			if uso.Numerico && c.tipoCatalogo == "SIN_VALOR" {
				return fmt.Errorf("el catálogo del token %s es descriptivo (SIN_VALOR) y no tiene valores de cálculo", token)
			}
		case "CAMPO_CALCULADO", "LISTA_PRECIOS", "TABLA":
		default:
			return fmt.Errorf("el tipo %s del token %s no se admite como operando", c.tipo, token)
		}
		operandos = append(operandos, c.id)
		mapa[token] = c.id
		if uso.Condicion {
			condiciones = append(condiciones, token)
		}
	}
	circular, err := h.tieneReferenciaCircular(ctx, elementoID, operandos, map[string]bool{})
	if err != nil {
		return err
	}
	if circular {
		return fmt.Errorf("los operandos generan una referencia circular: el Campo Calculado depende de sí mismo")
	}
	cfg["formula_texto"] = strings.TrimSpace(texto)
	cfg["tokens"] = formula.Tokens
	cfg["tokens_condicion"] = condiciones
	cfg["tokens_operandos"] = mapa
	cfg["operandos"] = operandos
	delete(cfg, "operacion")
	return nil
}

func cicloCamposCalculadosCompilados(tabs []tabCompilado) string {
	dependencias := map[string][]string{}
	for _, tab := range tabs {
		for _, el := range tab.Elementos {
			if el.Tipo != "CAMPO_CALCULADO" {
				continue
			}
			if ids, ok := el.Configuracion["operandos"].([]string); ok {
				dependencias[el.ElementoID] = ids
			} else {
				dependencias[el.ElementoID] = operandosDesdeConfiguracion(el.Configuracion)
			}
		}
	}
	estados := map[string]uint8{}
	var visitar func(string) string
	visitar = func(id string) string {
		if estados[id] == 1 {
			return id
		}
		if estados[id] == 2 {
			return ""
		}
		estados[id] = 1
		for _, operando := range dependencias[id] {
			if ciclo := visitar(operando); ciclo != "" {
				return ciclo
			}
		}
		estados[id] = 2
		return ""
	}
	for id := range dependencias {
		if ciclo := visitar(id); ciclo != "" {
			return ciclo
		}
	}
	return ""
}
