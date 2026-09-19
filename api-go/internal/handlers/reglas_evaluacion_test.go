package handlers

import "testing"

func reglaEvalActiva(r reglaCotizadorEval) reglaCotizadorEval {
	r.Activo = true
	return r
}

func TestUnitEvaluarCondicionRegla_IgualYDistinto(t *testing.T) {
	valores := map[string]any{"USA_TELEFONIA": "No"}
	regla := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "USA_TELEFONIA", Operador: "IGUAL_A", ValorComparacion: "No"})
	if !evaluarCondicionRegla(valores, regla) {
		t.Fatal("USA_TELEFONIA=No debería cumplir IGUAL_A 'No'")
	}
	regla.ValorComparacion = "no" // case-insensitive
	if !evaluarCondicionRegla(valores, regla) {
		t.Fatal("la comparación de IGUAL_A debería ser insensible a mayúsculas")
	}
	regla.ValorComparacion = "Sí"
	if evaluarCondicionRegla(valores, regla) {
		t.Fatal("USA_TELEFONIA=No no debería cumplir IGUAL_A 'Sí'")
	}

	distinto := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "USA_TELEFONIA", Operador: "DISTINTO_DE", ValorComparacion: "Sí"})
	if !evaluarCondicionRegla(valores, distinto) {
		t.Fatal("USA_TELEFONIA=No debería cumplir DISTINTO_DE 'Sí'")
	}
}

func TestUnitEvaluarCondicionRegla_Numericos(t *testing.T) {
	valores := map[string]any{"CANTIDAD_AGENTES": "0"}
	menor := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "CANTIDAD_AGENTES", Operador: "MENOR_QUE", ValorComparacion: "1"})
	if !evaluarCondicionRegla(valores, menor) {
		t.Fatal("CANTIDAD_AGENTES=0 debería cumplir MENOR_QUE 1 (R07)")
	}

	valores["CANTIDAD_AGENTES"] = "5"
	if evaluarCondicionRegla(valores, menor) {
		t.Fatal("CANTIDAD_AGENTES=5 no debería cumplir MENOR_QUE 1")
	}

	mayorIgual := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "CANTIDAD_AGENTES", Operador: "MAYOR_O_IGUAL_QUE", ValorComparacion: "5"})
	if !evaluarCondicionRegla(valores, mayorIgual) {
		t.Fatal("CANTIDAD_AGENTES=5 debería cumplir MAYOR_O_IGUAL_QUE 5")
	}

	// valor_comparacion no numérico contra un operador numérico: no cumple,
	// no revienta.
	invalido := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "CANTIDAD_AGENTES", Operador: "MENOR_QUE", ValorComparacion: "no-es-numero"})
	if evaluarCondicionRegla(valores, invalido) {
		t.Fatal("un valor_comparacion no numérico nunca debería cumplir un operador numérico")
	}
}

func TestUnitEvaluarCondicionRegla_VacioYNoVacio(t *testing.T) {
	estaVacio := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "CAMPO_X", Operador: "ESTA_VACIO"})
	noEstaVacio := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "CAMPO_X", Operador: "NO_ESTA_VACIO"})

	// el campo nunca se guardó (no existe en el mapa): cuenta como vacío.
	if !evaluarCondicionRegla(map[string]any{}, estaVacio) {
		t.Fatal("un campo sin valor guardado debería cumplir ESTA_VACIO")
	}
	if evaluarCondicionRegla(map[string]any{}, noEstaVacio) {
		t.Fatal("un campo sin valor guardado no debería cumplir NO_ESTA_VACIO")
	}

	// string vacío también cuenta como vacío.
	if !evaluarCondicionRegla(map[string]any{"CAMPO_X": ""}, estaVacio) {
		t.Fatal("un string vacío debería cumplir ESTA_VACIO")
	}

	// con valor presente.
	if evaluarCondicionRegla(map[string]any{"CAMPO_X": "algo"}, estaVacio) {
		t.Fatal("un campo con valor no debería cumplir ESTA_VACIO")
	}
	if !evaluarCondicionRegla(map[string]any{"CAMPO_X": "algo"}, noEstaVacio) {
		t.Fatal("un campo con valor debería cumplir NO_ESTA_VACIO")
	}

	// CONVERSACIONES_EXTRA=0 (R05 del documento) NO es "vacío" — 0 es una
	// respuesta válida, distinta de "no respondió".
	if evaluarCondicionRegla(map[string]any{"CONVERSACIONES_EXTRA": "0"}, reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "CONVERSACIONES_EXTRA", Operador: "ESTA_VACIO"})) {
		t.Fatal("CONVERSACIONES_EXTRA=0 no debería contar como vacío")
	}
}

func TestUnitEvaluarCondicionRegla_OperadorSobreCampoSinValor(t *testing.T) {
	// Un operador que no sea ESTA_VACIO/NO_ESTA_VACIO nunca dispara sobre
	// un campo que todavía no tiene valor guardado — ni siquiera
	// DISTINTO_DE, para no arriesgar un falso positivo antes de que el
	// usuario responda.
	regla := reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "USA_TELEFONIA", Operador: "DISTINTO_DE", ValorComparacion: "Sí"})
	if evaluarCondicionRegla(map[string]any{}, regla) {
		t.Fatal("DISTINTO_DE sobre un campo sin valor debería ser false, no un falso positivo")
	}
}

func TestUnitEvaluarEstadoCamposRegla_OcultarGanaSobreMostrar(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "USA_TELEFONIA", Operador: "IGUAL_A", ValorComparacion: "No", Accion: "OCULTAR", CamposObjetivo: []string{"MINUTOS_MES"}}),
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "OTRO_CAMPO", Operador: "IGUAL_A", ValorComparacion: "X", Accion: "MOSTRAR", CamposObjetivo: []string{"MINUTOS_MES"}}),
	}
	valores := map[string]any{"USA_TELEFONIA": "No", "OTRO_CAMPO": "X"}
	estados := evaluarEstadoCamposRegla(valores, reglas)
	estado, ok := estados["MINUTOS_MES"]
	if !ok {
		t.Fatal("MINUTOS_MES debería aparecer en el resultado")
	}
	if estado.Visible {
		t.Fatal("OCULTAR debería ganarle a MOSTRAR sobre el mismo campo (más restrictivo gana)")
	}
}

func TestUnitEvaluarEstadoCamposRegla_DeshabilitarGanaSobreHabilitar(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "A", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "DESHABILITAR", CamposObjetivo: []string{"CAMPO_X"}}),
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "B", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "HABILITAR", CamposObjetivo: []string{"CAMPO_X"}}),
	}
	valores := map[string]any{"A": "1", "B": "1"}
	estado := evaluarEstadoCamposRegla(valores, reglas)["CAMPO_X"]
	if estado.Habilitado {
		t.Fatal("DESHABILITAR debería ganarle a HABILITAR sobre el mismo campo")
	}
}

func TestUnitEvaluarEstadoCamposRegla_PonerEnCeroYRequerido(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "USA_TELEFONIA", Operador: "IGUAL_A", ValorComparacion: "No", Accion: "PONER_EN_CERO", CamposObjetivo: []string{"COSTO_VOZ", "PRECIO_VOZ", "CONSUMO_EXTRA_VOZ"}}),
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "USA_TELEFONIA", Operador: "IGUAL_A", ValorComparacion: "Sí", Accion: "CAMPO_REQUERIDO", CamposObjetivo: []string{"MINUTOS_MES"}}),
	}
	valores := map[string]any{"USA_TELEFONIA": "No"}
	estados := evaluarEstadoCamposRegla(valores, reglas)
	for _, campo := range []string{"COSTO_VOZ", "PRECIO_VOZ", "CONSUMO_EXTRA_VOZ"} {
		if !estados[campo].ForzarCero {
			t.Fatalf("%s debería quedar con forzar_cero=true (R01)", campo)
		}
	}
	if _, ok := estados["MINUTOS_MES"]; ok {
		t.Fatal("la regla CAMPO_REQUERIDO condicionada a USA_TELEFONIA=Sí no debería dispararse con USA_TELEFONIA=No")
	}
}

func TestUnitEvaluarEstadoCamposRegla_ExigirMinimoTomaElMasAlto(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "A", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "EXIGIR_MINIMO", ValorAccion: "10", CamposObjetivo: []string{"CAMPO_X"}}),
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "B", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "EXIGIR_MINIMO", ValorAccion: "25", CamposObjetivo: []string{"CAMPO_X"}}),
	}
	valores := map[string]any{"A": "1", "B": "1"}
	estado := evaluarEstadoCamposRegla(valores, reglas)["CAMPO_X"]
	if estado.Minimo == nil || *estado.Minimo != 25 {
		t.Fatalf("esperaba el mínimo más restrictivo (25), obtuvo %v", estado.Minimo)
	}
}

func TestUnitEvaluarEstadoCamposRegla_CampoSinReglasNoAparece(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "A", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "OCULTAR", CamposObjetivo: []string{"CAMPO_X"}}),
	}
	estados := evaluarEstadoCamposRegla(map[string]any{"A": "0"}, reglas)
	if len(estados) != 0 {
		t.Fatalf("condición no cumplida: esperaba mapa vacío, obtuvo %+v", estados)
	}
}

func TestUnitEvaluarEstadoCamposRegla_ReglaInactivaNoAplica(t *testing.T) {
	reglas := []reglaCotizadorEval{
		{CampoCondicionID: "A", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "OCULTAR", CamposObjetivo: []string{"CAMPO_X"}, Activo: false},
	}
	estados := evaluarEstadoCamposRegla(map[string]any{"A": "1"}, reglas)
	if len(estados) != 0 {
		t.Fatalf("una regla inactiva no debería aplicar, obtuvo %+v", estados)
	}
}

func TestUnitEvaluarValidacionReglas_BloquearGuardado(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{ReglaID: "R07", CampoCondicionID: "CANTIDAD_AGENTES", Operador: "MENOR_QUE", ValorComparacion: "1", Accion: "BLOQUEAR_GUARDADO"}),
	}
	errores := evaluarValidacionReglas(map[string]any{"CANTIDAD_AGENTES": "0"}, reglas)
	if len(errores) != 1 || errores[0].ReglaID != "R07" {
		t.Fatalf("esperaba 1 error de R07, obtuvo %+v", errores)
	}
	if errores[0].Mensaje == "" {
		t.Fatal("esperaba un mensaje genérico cuando la regla no trae uno propio")
	}

	sinErrores := evaluarValidacionReglas(map[string]any{"CANTIDAD_AGENTES": "3"}, reglas)
	if len(sinErrores) != 0 {
		t.Fatalf("CANTIDAD_AGENTES=3 no debería bloquear, obtuvo %+v", sinErrores)
	}
}

func TestUnitEvaluarValidacionReglas_MensajePropioSePreserva(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{ReglaID: "R07", CampoCondicionID: "CANTIDAD_AGENTES", Operador: "MENOR_QUE", ValorComparacion: "1", Accion: "BLOQUEAR_GUARDADO", Mensaje: "Debe haber al menos 1 agente."}),
	}
	errores := evaluarValidacionReglas(map[string]any{"CANTIDAD_AGENTES": "0"}, reglas)
	if len(errores) != 1 || errores[0].Mensaje != "Debe haber al menos 1 agente." {
		t.Fatalf("esperaba el mensaje configurado, obtuvo %+v", errores)
	}
}

func TestUnitEvaluarValidacionReglas_CampoRequeridoSoloSiEstaVacio(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{ReglaID: "R-REQ", CampoCondicionID: "USA_TELEFONIA", Operador: "IGUAL_A", ValorComparacion: "Sí", Accion: "CAMPO_REQUERIDO", CamposObjetivo: []string{"MINUTOS_MES"}}),
	}
	errores := evaluarValidacionReglas(map[string]any{"USA_TELEFONIA": "Sí"}, reglas)
	if len(errores) != 1 {
		t.Fatalf("MINUTOS_MES vacío con USA_TELEFONIA=Sí debería exigir el campo, obtuvo %+v", errores)
	}

	sinErrores := evaluarValidacionReglas(map[string]any{"USA_TELEFONIA": "Sí", "MINUTOS_MES": "100"}, reglas)
	if len(sinErrores) != 0 {
		t.Fatalf("MINUTOS_MES con valor no debería exigir nada, obtuvo %+v", sinErrores)
	}
}

func TestUnitEvaluarValidacionReglas_NoContaminaEstadoDeVisibilidad(t *testing.T) {
	reglas := []reglaCotizadorEval{
		reglaEvalActiva(reglaCotizadorEval{CampoCondicionID: "A", Operador: "IGUAL_A", ValorComparacion: "1", Accion: "BLOQUEAR_GUARDADO"}),
	}
	// BLOQUEAR_GUARDADO no tiene campos_objetivo ni afecta
	// evaluarEstadoCamposRegla — ambas funciones deben ignorar
	// mutuamente las acciones que no les corresponden.
	estados := evaluarEstadoCamposRegla(map[string]any{"A": "1"}, reglas)
	if len(estados) != 0 {
		t.Fatalf("BLOQUEAR_GUARDADO no debería producir estado de campo, obtuvo %+v", estados)
	}
}
