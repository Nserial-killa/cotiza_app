package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func crearCalculadoraTabsPrueba(t *testing.T) (*CotizadorTabsHandler, string) {
	t.Helper()
	pool := setupTestDB(t)
	id := "TEST-CALC-" + sufijoUnico()
	if _, err := pool.Exec(context.Background(), `INSERT INTO calculadoras (calculadora_id, nombre_calculadora) VALUES ($1, 'Cotizador de prueba')`, id); err != nil {
		t.Fatalf("no se pudo crear la calculadora: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM cotizadores_compilados WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM tabs_cotizador WHERE calculadora_id=$1`, id)
		pool.Exec(context.Background(), `DELETE FROM calculadoras WHERE calculadora_id=$1`, id)
	})
	return &CotizadorTabsHandler{DB: pool}, id
}

func TestCotizadorTabs_CrearYListar(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Datos generales",
		"alcance": "PROPIO", "orden": 2, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/tabs?calculadora_id="+url.QueryEscape(calculadoraID), nil)
	rec = httptest.NewRecorder()
	handler.ListarTabs(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar tabs: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		OK   bool           `json:"ok"`
		Tabs []tabCotizador `json:"tabs"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || len(res.Tabs) != 1 || res.Tabs[0].TabID != tabID || res.Tabs[0].Nombre != "Datos generales" {
		t.Fatalf("tab guardado no apareció correctamente: %+v", res)
	}
}

func TestCotizadorElementos_CreaLosCuatroTiposSimples(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-EL-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Elementos", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tab: %d: %s", rec.Code, rec.Body.String())
	}
	catalogoID := crearCatalogoPrueba(t, handler.DB, "Catálogo para elemento", "")
	tipos := []string{"CAMPO", "CAMPO_CATALOGO", "LEYENDA", "TEXTO_INFORMATIVO"}
	for i, tipo := range tipos {
		body := map[string]any{
			"elemento_id": "TEST-EL-" + tipo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": tipo, "etiqueta": "Elemento " + tipo, "columnas_ancho": 1,
			"orden": i + 1, "requerido": tipo == "CAMPO", "configuracion": map[string]any{"prueba": true}, "activo": true,
		}
		if tipo == "CAMPO_CATALOGO" {
			body["catalogo_id"] = catalogoID
		}
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("crear %s: esperaba 200, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	rec = httptest.NewRecorder()
	handler.ListarElementos(rec, req)
	var res struct {
		OK        bool                   `json:"ok"`
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || !res.OK || len(res.Elementos) != len(tipos) {
		t.Fatalf("esperaba %d elementos persistidos, obtuvo %d: %s", len(tipos), len(res.Elementos), rec.Body.String())
	}
}

func TestCotizadorElementos_ValidaCatalogoSegunTipo(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-VALID-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Validaciones", "activo": true,
	})
	catalogoID := crearCatalogoPrueba(t, handler.DB, "Catálogo validación", "")

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-SIN-CAT-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO_CATALOGO", "etiqueta": "Sin catálogo", "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("CAMPO_CATALOGO sin catalogo_id: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	for _, tipo := range []string{"CAMPO", "LEYENDA", "TEXTO_INFORMATIVO"} {
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": "TEST-EL-CAT-INVALIDO-" + tipo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": tipo, "etiqueta": "Catálogo inválido", "catalogo_id": catalogoID, "activo": true,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s con catalogo_id: esperaba 400, dio %d: %s", tipo, rec.Code, rec.Body.String())
		}
	}
}

// TestCotizadorElementos_ContenedorConHijos cubre la Ronda 1 de tipos
// nuevos (migración 0017): crea un Contenedor de 2 columnas y dos Campos
// con componente_padre_id apuntando a él, dentro del mismo tab.
func TestCotizadorElementos_ContenedorConHijos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CONT-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Contenedores", "activo": true,
	})
	contenedorID := "TEST-EL-CONT-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": contenedorID, "tab_id": tabID, "tipo": "CONTENEDOR", "etiqueta": "Datos del cliente",
		"orden": 1, "configuracion": map[string]any{"columnas": 2, "estilo": "card"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear contenedor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	for i, sufijo := range []string{"A", "B"} {
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": "TEST-EL-HIJO-" + sufijo + "-" + sufijoUnico(), "tab_id": tabID,
			"tipo": "CAMPO", "etiqueta": "Campo " + sufijo, "componente_padre_id": contenedorID,
			"orden": i + 2, "activo": true,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("crear hijo %s: esperaba 200, dio %d: %s", sufijo, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	rec = httptest.NewRecorder()
	handler.ListarElementos(rec, req)
	var res struct {
		OK        bool                   `json:"ok"`
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	if !res.OK || len(res.Elementos) != 3 {
		t.Fatalf("esperaba 3 elementos (contenedor + 2 hijos): %+v", res)
	}
	hijos := 0
	for _, el := range res.Elementos {
		if el.ComponentePadreID != nil && *el.ComponentePadreID == contenedorID {
			hijos++
		}
	}
	if hijos != 2 {
		t.Fatalf("esperaba 2 hijos con componente_padre_id=%s, obtuvo %d: %+v", contenedorID, hijos, res.Elementos)
	}
}

func TestCotizadorElementos_ContenedorValidaColumnasYSinPadre(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CONT-VAL-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Validación contenedor", "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CONT-BAD-COLS-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CONTENEDOR", "etiqueta": "Malo", "configuracion": map[string]any{"columnas": 5}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("columnas=5: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	otroContenedorID := "TEST-EL-CONT-OTRO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": otroContenedorID, "tab_id": tabID, "tipo": "CONTENEDOR", "etiqueta": "Otro",
		"configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CONT-CON-PADRE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CONTENEDOR", "etiqueta": "No debería poder tener padre", "componente_padre_id": otroContenedorID,
		"configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("contenedor con padre: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizadorElementos_OpcionesPropuestaValidaConfiguracionYAdmiteHijos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-OPCIONES-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Opciones", "activo": true,
	})
	campoPrincipalID := "TEST-EL-OPC-PRINCIPAL-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoPrincipalID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Precio", "activo": true,
	})
	padreID := "TEST-EL-OPCIONES-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": padreID, "tab_id": tabID, "tipo": "OPCIONES_PROPUESTA", "etiqueta": "Planes",
		"configuracion": map[string]any{
			"cantidad_inicial": 2, "nombres_sugeridos": "Starter, Premium", "vista_editar": "PESTANAS",
			"vista_resumen": "CAJAS", "vista_oferta": "TABLA_COMPARATIVA", "campo_principal_id": campoPrincipalID,
			"permitir_duplicar": true, "permitir_eliminar": true, "permitir_renombrar": true,
		}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear Opciones de Propuesta: %d: %s", rec.Code, rec.Body.String())
	}
	hijoID := "TEST-EL-OPC-HIJO-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": hijoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Licencias",
		"componente_padre_id": padreID, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear hijo de Opciones de Propuesta: %d: %s", rec.Code, rec.Body.String())
	}

	casosInvalidos := []map[string]any{
		{"cantidad_inicial": 0, "vista_editar": "PESTANAS", "vista_resumen": "CAJAS", "vista_oferta": "CAJAS"},
		{"cantidad_inicial": 1, "vista_editar": "CARRUSEL", "vista_resumen": "CAJAS", "vista_oferta": "CAJAS"},
	}
	for _, configuracion := range casosInvalidos {
		rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
			"elemento_id": "TEST-EL-OPC-INVALIDO-" + sufijoUnico(), "tab_id": tabID,
			"tipo": "OPCIONES_PROPUESTA", "etiqueta": "Inválido", "configuracion": configuracion, "activo": true,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("configuración inválida debía responder 400: %+v -> %d %s", configuracion, rec.Code, rec.Body.String())
		}
	}
}

func TestCotizadorElementos_PadreDebeSerContenedorActivoDelMismoTab(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-PADRE-" + sufijoUnico()
	otroTabID := "TEST-TAB-PADRE-OTRO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Padre", "activo": true,
	})
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": calculadoraID, "nombre": "Otro tab", "activo": true,
	})

	campoID := "TEST-EL-NO-CONTENEDOR-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "No es contenedor", "activo": true,
	})
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-HIJO-BAD-TIPO-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Hijo", "componente_padre_id": campoID, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("padre no es CONTENEDOR: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	contenedorOtroTabID := "TEST-EL-CONT-OTRO-TAB-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": contenedorOtroTabID, "tab_id": otroTabID, "tipo": "CONTENEDOR", "etiqueta": "Contenedor de otro tab",
		"configuracion": map[string]any{"columnas": 2}, "activo": true,
	})
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-HIJO-OTRO-TAB-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Hijo cruzado", "componente_padre_id": contenedorOtroTabID, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("padre de otro tab: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCotizadorElementos_CajaValorValidaCampoFuente(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CAJA-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Caja de valor", "activo": true,
	})
	campoID := "TEST-EL-FUENTE-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Total", "activo": true,
	})

	cajaID := "TEST-EL-CAJA-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": cajaID, "tab_id": tabID, "tipo": "CAJA_VALOR", "etiqueta": "Total mostrado",
		"campo_fuente_id": campoID, "configuracion": map[string]any{"prefijo": "US$ ", "valor_por_defecto": "0.00"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear caja de valor: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAJA-SIN-FUENTE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAJA_VALOR", "etiqueta": "Sin campo fuente", "configuracion": map[string]any{"valor_por_defecto": "0.00"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("caja de valor sin campo fuente debe ser válida: %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAJA-FUENTE-INEXISTENTE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAJA_VALOR", "etiqueta": "Fuente inexistente", "campo_fuente_id": "NO-EXISTE-" + sufijoUnico(), "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("campo_fuente_id inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CAMPO-CON-FUENTE-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "No debería aceptar fuente", "campo_fuente_id": campoID, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("campo_fuente_id en tipo CAMPO: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_CampoCalculadoValidaOperandos cubre la Ronda 2 del
// Diseñador (migración 0018): un Campo Calculado con dos operandos CAMPO
// numéricos válidos se guarda; sin operandos suficientes, con un operando
// inexistente, de otro cotizador, o no numérico (CAMPO texto), se rechaza.
// Ronda F2: un operando de OTRA SECCIÓN del mismo cotizador sí se acepta —
// las referencias de datos tienen alcance de cotizador.
func TestCotizadorElementos_CampoCalculadoValidaOperandos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CALC-" + sufijoUnico()
	otroTabID := "TEST-TAB-CALC-OTRO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Cálculo", "activo": true,
	})
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": calculadoraID, "nombre": "Otro tab", "activo": true,
	})

	campoNumericoID := "TEST-EL-NUM-A-" + sufijoUnico()
	campoNumericoBID := "TEST-EL-NUM-B-" + sufijoUnico()
	campoTextoID := "TEST-EL-TXT-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoNumericoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Costo",
		"configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoNumericoBID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Margen",
		"configuracion": map[string]any{"tipo_campo": "PORCENTAJE"}, "activo": true,
	})
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoTextoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Nombre",
		"configuracion": map[string]any{"tipo_campo": "TEXTO"}, "activo": true,
	})
	campoOtroTabID := "TEST-EL-OTRO-TAB-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoOtroTabID, "tab_id": otroTabID, "tipo": "CAMPO", "etiqueta": "De otro tab",
		"configuracion": map[string]any{"tipo_campo": "NUMERO"}, "activo": true,
	})

	otroHandler, otraCalculadoraID := crearCalculadoraTabsPrueba(t)
	tabOtraCalcID := "TEST-TAB-CALC-OTRA-CALC-" + sufijoUnico()
	postCatalogos(t, otroHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabOtraCalcID, "calculadora_id": otraCalculadoraID, "nombre": "Otro cotizador", "activo": true,
	})
	campoOtraCalcID := "TEST-EL-OTRA-CALC-" + sufijoUnico()
	postCatalogos(t, otroHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoOtraCalcID, "tab_id": tabOtraCalcID, "tipo": "CAMPO", "etiqueta": "De otro cotizador",
		"configuracion": map[string]any{"tipo_campo": "NUMERO"}, "activo": true,
	})

	casos := []struct {
		nombre    string
		operandos []string
		operacion string
		esperaOK  bool
	}{
		{"dos operandos numéricos válidos", []string{campoNumericoID, campoNumericoBID}, "SUMA", true},
		{"un solo operando en SUMA (mínimo 2)", []string{campoNumericoID}, "SUMA", false},
		{"un solo operando en PROMEDIO sí alcanza", []string{campoNumericoID}, "PROMEDIO", true},
		{"operando inexistente", []string{campoNumericoID, "NO-EXISTE-" + sufijoUnico()}, "SUMA", false},
		{"operando de otra sección del mismo cotizador (Ronda F2)", []string{campoNumericoID, campoOtroTabID}, "SUMA", true},
		{"operando de otro cotizador", []string{campoNumericoID, campoOtraCalcID}, "SUMA", false},
		{"operando de tipo texto", []string{campoNumericoID, campoTextoID}, "SUMA", false},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
				"elemento_id": "TEST-EL-CALC-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO_CALCULADO",
				"etiqueta": "Calculado", "configuracion": map[string]any{
					"operacion": c.operacion, "tipo_resultado": "MONEDA", "decimales": 2, "operandos": c.operandos,
				}, "activo": true,
			})
			if c.esperaOK && rec.Code != http.StatusOK {
				t.Fatalf("esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
			}
			if !c.esperaOK && rec.Code != http.StatusBadRequest {
				t.Fatalf("esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCotizadorElementos_CampoCalculadoAnidadoYCircular cubre dependencia de
// 2 niveles (B depende de A, C depende de B) y el rechazo de un ciclo (A
// pasa a depender de C, que depende de B, que depende de A).
func TestCotizadorElementos_CampoCalculadoAnidadoYCircular(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CIRC-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Circular", "activo": true,
	})
	campoBaseID := "TEST-EL-BASE-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoBaseID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Base",
		"configuracion": map[string]any{"tipo_campo": "NUMERO"}, "activo": true,
	})
	calcAID := "TEST-EL-CALC-A-" + sufijoUnico()
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcAID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "A",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{campoBaseID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear A: %s", rec.Body.String())
	}
	calcBID := "TEST-EL-CALC-B-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcBID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "B (depende de A)",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{calcAID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear B: %s", rec.Body.String())
	}
	calcCID := "TEST-EL-CALC-C-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcCID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "C (depende de B)",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{calcBID}}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear C (anidado 2 niveles): %s", rec.Body.String())
	}

	// Ahora A pasa a depender de C: A -> C -> B -> A, ciclo.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": calcAID, "tab_id": tabID, "tipo": "CAMPO_CALCULADO", "etiqueta": "A",
		"configuracion": map[string]any{"operacion": "PROMEDIO", "tipo_resultado": "NUMERO", "decimales": 2, "operandos": []string{calcCID}}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("referencia circular: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_FuncionCampoUnicaPorCotizadorNoGlobal cubre que la
// unicidad de funcion_campo (distinta de NORMAL) es por calculadora_id, no
// global: el mismo rol se rechaza dos veces en el mismo cotizador pero se
// permite en cotizadores distintos.
func TestCotizadorElementos_FuncionCampoUnicaPorCotizadorNoGlobal(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	_, otraCalculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-FUNC-" + sufijoUnico()
	otroTabID := "TEST-TAB-FUNC-OTRA-CALC-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Función", "activo": true,
	})
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": otroTabID, "calculadora_id": otraCalculadoraID, "nombre": "Función otra calc", "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-FUNC-1-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Total 1",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("primer TOTAL_PRECIO_OFERTA: %s", rec.Body.String())
	}
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-FUNC-2-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Total 2",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("segundo TOTAL_PRECIO_OFERTA en el mismo cotizador: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-FUNC-OTRA-CALC-" + sufijoUnico(), "tab_id": otroTabID, "tipo": "CAMPO", "etiqueta": "Total otra calc",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mismo rol en otro cotizador debería permitirse: %d: %s", rec.Code, rec.Body.String())
	}
	// NORMAL sí puede repetirse libremente.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-NORMAL-1-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Normal 1", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("normal 1: %s", rec.Body.String())
	}
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-NORMAL-2-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Normal 2", "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("NORMAL repetido debería permitirse: %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_FuncionCampoSoloEnTiposConValorPropio cubre que
// funcion_campo (distinta de NORMAL) se rechaza en TITULO/CONTENEDOR/etc.
func TestCotizadorElementos_FuncionCampoSoloEnTiposConValorPropio(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-FUNC-TIPO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Función por tipo", "activo": true,
	})
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-TITULO-FUNC-" + sufijoUnico(), "tab_id": tabID, "tipo": "TITULO", "etiqueta": "Título",
		"funcion_campo": "TOTAL_PRECIO_OFERTA", "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("TITULO con funcion_campo: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_ListaPreciosValidaConfiguracion cubre la Ronda 3
// del Diseñador (migración 0019): tipo_lista_precios, valor_que_alimenta e
// item_seleccionado_por_defecto.
func TestCotizadorElementos_ListaPreciosValidaConfiguracion(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-LP-VAL-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Precios", "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-LP-BAD-TIPO-" + sufijoUnico(), "tab_id": tabID, "tipo": "LISTA_PRECIOS",
		"etiqueta": "Servicios", "configuracion": map[string]any{"tipo_lista_precios": "TRIPLE"}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tipo_lista_precios inválido: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-LP-BAD-VALOR-" + sufijoUnico(), "tab_id": tabID, "tipo": "LISTA_PRECIOS",
		"etiqueta": "Servicios", "configuracion": map[string]any{"tipo_lista_precios": "UNICA", "valor_que_alimenta": "COSTO_TOTAL"}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("valor_que_alimenta distinto de PRECIO_UNITARIO: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	elementoID := "TEST-EL-LP-OK-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "LISTA_PRECIOS", "etiqueta": "Servicios",
		"configuracion": map[string]any{"tipo_lista_precios": "MULTIPLE", "mostrar_cantidad": false}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear lista de precios válida: %d: %s", rec.Code, rec.Body.String())
	}

	// item_seleccionado_por_defecto con un código que no existe todavía: rechaza.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "LISTA_PRECIOS", "etiqueta": "Servicios",
		"configuracion": map[string]any{"tipo_lista_precios": "MULTIPLE", "item_seleccionado_por_defecto": "NO-EXISTE"}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("item_seleccionado_por_defecto inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	itemsHandler := &ListaPreciosItemsHandler{DB: handler.DB}
	_, resItem := crearItemListaPrecios(t, itemsHandler, elementoID, map[string]any{"codigo": "ITEM-A", "nombre": "Item A", "precio": 100})
	if resItem["ok"] != true {
		t.Fatalf("crear ítem base: %+v", resItem)
	}

	// ahora "ITEM-A" sí existe: se acepta como item_seleccionado_por_defecto.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "LISTA_PRECIOS", "etiqueta": "Servicios",
		"configuracion": map[string]any{"tipo_lista_precios": "MULTIPLE", "item_seleccionado_por_defecto": "item-a"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("item_seleccionado_por_defecto existente: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	// PRIMERO_ACTIVO siempre se acepta, incluso sin ítems.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-LP-DEFECTO-" + sufijoUnico(), "tab_id": tabID, "tipo": "LISTA_PRECIOS",
		"etiqueta": "Servicios", "configuracion": map[string]any{"tipo_lista_precios": "UNICA", "item_seleccionado_por_defecto": "PRIMERO_ACTIVO"}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PRIMERO_ACTIVO: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_CampoCalculadoAceptaListaPreciosComoOperando cubre
// la extensión de la Ronda 3 a la validación de operandos de Campo
// Calculado (Ronda 2): una Lista de Precios ahora es un operando válido.
func TestCotizadorElementos_CampoCalculadoAceptaListaPreciosComoOperando(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-LP-OPERANDO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Precios", "activo": true,
	})
	listaID := crearElementoListaPreciosPrueba(t, handler, tabID)
	campoID := "TEST-EL-LP-CAMPO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Descuento",
		"configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-LP-CALC-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO_CALCULADO",
		"etiqueta": "Total", "configuracion": map[string]any{
			"operacion": "SUMA", "tipo_resultado": "MONEDA", "decimales": 2, "operandos": []string{listaID, campoID},
		}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Campo Calculado con Lista de Precios como operando: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_ListarOcultaCostoMargenSinPuedeVerPrice cubre el
// fix de seguridad: GET /api/cotizador/elementos nunca tuvo restricción de
// rol, pero costo_interno/margen_porcentaje de los ítems de Lista de
// Precios (Ronda 3) son precio interno igual que en cualquier otra
// pantalla — deben quedar AUSENTES del JSON (no en 0 ni null) para una
// sesión sin puede_ver_price, y presentes para una que sí lo tiene.
func TestCotizadorElementos_ListarOcultaCostoMargenSinPuedeVerPrice(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-LP-PERM-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Precios", "activo": true,
	})
	elementoID := crearElementoListaPreciosPrueba(t, handler, tabID)
	itemsHandler := &ListaPreciosItemsHandler{DB: handler.DB}
	_, resItem := crearItemListaPrecios(t, itemsHandler, elementoID, map[string]any{
		"codigo": "A", "nombre": "Ítem A", "precio": 100, "costo_interno": 60, "margen_porcentaje": 40,
	})
	if resItem["ok"] != true {
		t.Fatalf("crear ítem base: %+v", resItem)
	}

	vendedor := crearUsuarioPrueba(t, handler.DB, "vendedor.lp."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	admin := crearAdminActorPrueba(t, handler.DB)

	itemsDeRespuesta := func(actorID string) map[string]json.RawMessage {
		req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
		req = conActor(req, actorID)
		rec := httptest.NewRecorder()
		handler.ListarElementos(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("listar elementos: %d: %s", rec.Code, rec.Body.String())
		}
		var res struct {
			Elementos []struct {
				ElementoID string                       `json:"elemento_id"`
				Items      []map[string]json.RawMessage `json:"items"`
			} `json:"elementos"`
		}
		assertJSON(t, rec.Body.Bytes(), &res)
		for _, el := range res.Elementos {
			if el.ElementoID == elementoID {
				if len(el.Items) != 1 {
					t.Fatalf("esperaba 1 ítem para %s, obtuvo %d", elementoID, len(el.Items))
				}
				return el.Items[0]
			}
		}
		t.Fatalf("no se encontró el elemento %s en la respuesta", elementoID)
		return nil
	}

	itemVendedor := itemsDeRespuesta(vendedor)
	if _, presente := itemVendedor["costo_interno"]; presente {
		t.Error("costo_interno no debería venir para un Vendedor sin puede_ver_price")
	}
	if _, presente := itemVendedor["margen_porcentaje"]; presente {
		t.Error("margen_porcentaje no debería venir para un Vendedor sin puede_ver_price")
	}
	if _, presente := itemVendedor["precio"]; !presente {
		t.Error("precio debería seguir presente — no es el dato restringido")
	}

	itemAdmin := itemsDeRespuesta(admin)
	if _, presente := itemAdmin["costo_interno"]; !presente {
		t.Error("costo_interno debería venir para un Administrador (puede_ver_price=true)")
	}
	if _, presente := itemAdmin["margen_porcentaje"]; !presente {
		t.Error("margen_porcentaje debería venir para un Administrador (puede_ver_price=true)")
	}
}

// TestCotizadorElementos_TablaValidaConfiguracion cubre la Ronda 4 del
// Diseñador (migración 0020): tipo_tabla, etiqueta_total, unidad y los
// booleanos de comportamiento.
func TestCotizadorElementos_TablaValidaConfiguracion(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-TABLA-VAL-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Tabla", "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-TABLA-BAD-TIPO-" + sufijoUnico(), "tab_id": tabID, "tipo": "TABLA",
		"etiqueta": "Perfiles", "configuracion": map[string]any{"tipo_tabla": "JERARQUICA"}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tipo_tabla inválido: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	elementoID := "TEST-EL-TABLA-OK-" + sufijoUnico()
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "TABLA", "etiqueta": "Perfiles",
		"configuracion": map[string]any{"etiqueta_total": "TOTAL HORAS", "unidad": "horas", "permitir_agregar_filas": false}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("crear tabla válida: %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/cotizador/elementos?tab_id="+url.QueryEscape(tabID), nil)
	rec = httptest.NewRecorder()
	handler.ListarElementos(rec, req)
	var res struct {
		Elementos []elementoTabCotizador `json:"elementos"`
	}
	assertJSON(t, rec.Body.Bytes(), &res)
	for _, el := range res.Elementos {
		if el.ElementoID != elementoID {
			continue
		}
		if el.Configuracion["tipo_tabla"] != "SIMPLE" {
			t.Errorf("esperaba tipo_tabla=SIMPLE por defecto, obtuvo %v", el.Configuracion["tipo_tabla"])
		}
		if el.Configuracion["etiqueta_total"] != "TOTAL HORAS" {
			t.Errorf("esperaba etiqueta_total=TOTAL HORAS, obtuvo %v", el.Configuracion["etiqueta_total"])
		}
		if el.Configuracion["permitir_agregar_filas"] != false {
			t.Errorf("esperaba permitir_agregar_filas=false, obtuvo %v", el.Configuracion["permitir_agregar_filas"])
		}
		if el.Configuracion["permitir_eliminar_filas"] != true {
			t.Errorf("esperaba permitir_eliminar_filas=true (default), obtuvo %v", el.Configuracion["permitir_eliminar_filas"])
		}
	}
}

// TestCotizadorElementos_CampoCalculadoAceptaTablaComoOperando cubre la
// extensión "si el tiempo alcanza" de la Ronda 4 a la validación de
// operandos de Campo Calculado (Ronda 2).
func TestCotizadorElementos_CampoCalculadoAceptaTablaComoOperando(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-TABLA-OPERANDO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Tabla", "activo": true,
	})
	tablaID := crearElementoTablaPrueba(t, handler, tabID)
	campoID := "TEST-EL-TABLA-CAMPO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Descuento",
		"configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})

	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-TABLA-CALC-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO_CALCULADO",
		"etiqueta": "Total", "configuracion": map[string]any{
			"operacion": "SUMA", "tipo_resultado": "MONEDA", "decimales": 2, "operandos": []string{tablaID, campoID},
		}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Campo Calculado con Tabla como operando: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCotizadorElementos_CampoCalculadoAceptaCampoCatalogoSegunTipoCalculo
// cubre la tarea 2 de la migración 0023: un Campo Catálogo solo es operando
// válido si su catálogo tiene tipo_calculo != SIN_VALOR.
func TestCotizadorElementos_CampoCalculadoAceptaCampoCatalogoSegunTipoCalculo(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-CATCALC-OPERANDO-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Catálogo calculable", "activo": true,
	})

	catalogoPorcentajeID := crearCatalogoPrueba(t, handler.DB, "Catálogo margen operando", "")
	if _, err := handler.DB.Exec(context.Background(), `UPDATE catalogos SET tipo_calculo='PORCENTAJE' WHERE catalogo_id=$1`, catalogoPorcentajeID); err != nil {
		t.Fatalf("no se pudo fijar tipo_calculo: %v", err)
	}
	catalogoSinValorID := crearCatalogoPrueba(t, handler.DB, "Catálogo descriptivo operando", "")

	campoNumericoID := "TEST-EL-CATCALC-NUM-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoNumericoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Base",
		"configuracion": map[string]any{"tipo_campo": "MONEDA"}, "activo": true,
	})
	campoCatalogoPorcentajeID := "TEST-EL-CATCALC-PCT-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoCatalogoPorcentajeID, "tab_id": tabID, "tipo": "CAMPO_CATALOGO",
		"etiqueta": "Margen", "catalogo_id": catalogoPorcentajeID, "activo": true,
	})
	campoCatalogoSinValorID := "TEST-EL-CATCALC-SV-" + sufijoUnico()
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": campoCatalogoSinValorID, "tab_id": tabID, "tipo": "CAMPO_CATALOGO",
		"etiqueta": "Tipo de agente", "catalogo_id": catalogoSinValorID, "activo": true,
	})

	// Con tipo_calculo != SIN_VALOR, el Campo Catálogo es un operando válido.
	rec := postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CATCALC-CALC-OK-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO_CALCULADO",
		"etiqueta": "Total con margen", "configuracion": map[string]any{
			"operacion": "MULTIPLICACION", "tipo_resultado": "MONEDA", "decimales": 2,
			"operandos": []string{campoNumericoID, campoCatalogoPorcentajeID},
		}, "activo": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("Campo Calculado con Campo Catálogo PORCENTAJE como operando: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}

	// Con tipo_calculo == SIN_VALOR, se rechaza con un mensaje que lo explique.
	rec = postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-EL-CATCALC-CALC-MAL-" + sufijoUnico(), "tab_id": tabID, "tipo": "CAMPO_CALCULADO",
		"etiqueta": "Total inválido", "configuracion": map[string]any{
			"operacion": "MULTIPLICACION", "tipo_resultado": "MONEDA", "decimales": 2,
			"operandos": []string{campoNumericoID, campoCatalogoSinValorID},
		}, "activo": true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Campo Calculado con Campo Catálogo SIN_VALOR como operando: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("solo descriptivo")) {
		t.Errorf("el mensaje de error %q no explica que el catálogo es solo descriptivo", rec.Body.String())
	}
}

func TestCotizadorTabs_EliminarInactivaTabYElementos(t *testing.T) {
	handler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-TAB-DELETE-" + sufijoUnico()
	elementoID := "TEST-EL-DELETE-" + sufijoUnico()
	postCatalogos(t, handler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Eliminar", "activo": true,
	})
	postCatalogos(t, handler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": elementoID, "tab_id": tabID, "tipo": "CAMPO", "etiqueta": "Campo", "activo": true,
	})
	rec := deleteConRuta(t, "/api/cotizador/tabs/{id}", "/api/cotizador/tabs/"+tabID, handler.EliminarTab)
	if rec.Code != http.StatusOK {
		t.Fatalf("eliminar tab: esperaba 200, dio %d: %s", rec.Code, rec.Body.String())
	}
	var tabActivo, elementoActivo bool
	if err := handler.DB.QueryRow(context.Background(), `SELECT activo FROM tabs_cotizador WHERE tab_id=$1`, tabID).Scan(&tabActivo); err != nil {
		t.Fatal(err)
	}
	if err := handler.DB.QueryRow(context.Background(), `SELECT activo FROM elementos_tab_cotizador WHERE elemento_id=$1`, elementoID).Scan(&elementoActivo); err != nil {
		t.Fatal(err)
	}
	if tabActivo || elementoActivo {
		t.Fatalf("tab y elemento debían quedar inactivos: tab=%v elemento=%v", tabActivo, elementoActivo)
	}
}
