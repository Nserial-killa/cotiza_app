package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SalidasCotizadorHandler struct{ DB *pgxpool.Pool }

type salidaCotizador struct {
	CalculadoraID   string `json:"calculadora_id"`
	ClaveSalida     string `json:"clave_salida"`
	TipoFuente      string `json:"tipo_fuente"`
	FuenteID        string `json:"fuente_id"`
	PropiedadFuente string `json:"propiedad_fuente"`
	Requerido       bool   `json:"requerido"`
	Activo          bool   `json:"activo"`
}

// El tipo se deriva del vocabulario corporativo, nunca del cliente.
var tiposSalidas = map[string]string{
	"TOTAL_PRECIO": "MONEDA", "TOTAL_COSTO": "MONEDA", "TOTAL_GANANCIA": "MONEDA",
	"MARGEN_TOTAL": "PORCENTAJE", "SUBTOTAL": "MONEDA", "DESCUENTO": "MONEDA", "IMPUESTOS": "MONEDA",
	"MONEDA": "TEXTO", "TIPO_CLIENTE": "TEXTO", "TIPO_PROPUESTA": "TEXTO",
}

// Mismo orden del CHECK de clave_salida en 0027_salidas_cotizador.sql. El
// Diseñador (pantalla "Salidas") necesita ver el estado de las 10 de una
// sola vez, no solo las que ya tienen fila en mapa_salidas_cotizador.
var clavesSalidasOrden = []string{
	"TOTAL_PRECIO", "TOTAL_COSTO", "TOTAL_GANANCIA", "MARGEN_TOTAL", "SUBTOTAL",
	"DESCUENTO", "IMPUESTOS", "MONEDA", "TIPO_CLIENTE", "TIPO_PROPUESTA",
}

// completarMapaSalidas devuelve las 10 claves siempre, en orden fijo: la fila
// real si existe, o un registro vacío (tipo_fuente=="") que el frontend lee
// como "sin mapear todavía" — nunca se persiste tal cual.
func completarMapaSalidas(calculadoraID string, mapa []salidaCotizador) []salidaCotizador {
	porClave := map[string]salidaCotizador{}
	for _, s := range mapa {
		porClave[s.ClaveSalida] = s
	}
	completo := make([]salidaCotizador, 0, len(clavesSalidasOrden))
	for _, clave := range clavesSalidasOrden {
		if s, existe := porClave[clave]; existe {
			completo = append(completo, s)
			continue
		}
		completo = append(completo, salidaCotizador{CalculadoraID: calculadoraID, ClaveSalida: clave})
	}
	return completo
}

func leerMapaSalidas(ctx context.Context, q consultadorRuntime, calculadoraID string) ([]salidaCotizador, error) {
	rows, err := q.Query(ctx, `SELECT calculadora_id,clave_salida,tipo_fuente,COALESCE(fuente_id,''),COALESCE(propiedad_fuente,''),requerido,activo FROM mapa_salidas_cotizador WHERE calculadora_id=$1 ORDER BY clave_salida`, calculadoraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultado := []salidaCotizador{}
	for rows.Next() {
		var s salidaCotizador
		if err := rows.Scan(&s.CalculadoraID, &s.ClaveSalida, &s.TipoFuente, &s.FuenteID, &s.PropiedadFuente, &s.Requerido, &s.Activo); err != nil {
			return nil, err
		}
		resultado = append(resultado, s)
	}
	return resultado, rows.Err()
}

func (h *SalidasCotizadorHandler) Listar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var existe bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM calculadoras WHERE calculadora_id=$1)`, id).Scan(&existe); err != nil {
		h.error(w, err)
		return
	}
	if !existe {
		escribirJSON(w, 404, map[string]any{"ok": false, "error": "El cotizador indicado no existe."})
		return
	}
	mapa, err := leerMapaSalidas(ctx, h.DB, id)
	if err != nil {
		h.error(w, err)
		return
	}
	escribirJSON(w, 200, map[string]any{"ok": true, "salidas": completarMapaSalidas(id, mapa), "tipos_salidas": tiposSalidas})
}

func (h *SalidasCotizadorHandler) Guardar(w http.ResponseWriter, r *http.Request) {
	req := salidaCotizador{Activo: true}
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.CalculadoraID = strings.TrimSpace(req.CalculadoraID)
	req.ClaveSalida = strings.ToUpper(strings.TrimSpace(req.ClaveSalida))
	req.TipoFuente = strings.ToUpper(strings.TrimSpace(req.TipoFuente))
	req.FuenteID = strings.TrimSpace(req.FuenteID)
	req.PropiedadFuente = strings.ToLower(strings.TrimSpace(req.PropiedadFuente))
	if tiposSalidas[req.ClaveSalida] == "" || !map[string]bool{"CAMPO": true, "CALCULADO": true, "TOTAL_TABLA": true, "LISTA_PRECIO": true, "ESCENARIO": true, "SALIDA": true}[req.TipoFuente] {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "Indique qué salida configura y de qué tipo de fuente toma su valor."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		h.error(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// Serializar cambios del mapa, incluida la detección de ciclos entre dos sesiones.
	var id string
	err = tx.QueryRow(ctx, `SELECT calculadora_id FROM calculadoras WHERE calculadora_id=$1 FOR UPDATE`, req.CalculadoraID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, 404, map[string]any{"ok": false, "error": "El cotizador indicado no existe."})
		return
	}
	if err != nil {
		h.error(w, err)
		return
	}
	mapa, err := leerMapaSalidas(ctx, tx, id)
	if err != nil {
		h.error(w, err)
		return
	}
	nuevo := []salidaCotizador{req}
	for _, s := range mapa {
		if s.ClaveSalida != req.ClaveSalida {
			nuevo = append(nuevo, s)
		}
	}
	elementos, metas, err := fuentesSalidasDiseno(ctx, tx, id)
	if err != nil {
		h.error(w, err)
		return
	}
	// Se permite dejar una fuente vacía como configuración pendiente. Nunca
	// se publica así una salida activa; una referencia no vacía sí se valida.
	if err := validarMapaSalidas(nuevo, elementos, metas, true); err != nil {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_, err = tx.Exec(ctx, `INSERT INTO mapa_salidas_cotizador(calculadora_id,clave_salida,tipo_fuente,fuente_id,propiedad_fuente,requerido,activo)
		VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7) ON CONFLICT(calculadora_id,clave_salida) DO UPDATE SET
		tipo_fuente=EXCLUDED.tipo_fuente,fuente_id=EXCLUDED.fuente_id,propiedad_fuente=EXCLUDED.propiedad_fuente,requerido=EXCLUDED.requerido,activo=EXCLUDED.activo`,
		id, req.ClaveSalida, req.TipoFuente, req.FuenteID, req.PropiedadFuente, req.Requerido, req.Activo)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		h.error(w, err)
		return
	}
	escribirJSON(w, 200, map[string]any{"ok": true, "salida": req})
}

func (h *SalidasCotizadorHandler) Eliminar(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("calculadora_id"))
	clave := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "clave_salida")))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		h.error(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var existe string
	err = tx.QueryRow(ctx, `SELECT calculadora_id FROM calculadoras WHERE calculadora_id=$1 FOR UPDATE`, id).Scan(&existe)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, 404, map[string]any{"ok": false, "error": "El cotizador no existe."})
		return
	}
	if err != nil {
		h.error(w, err)
		return
	}
	var usada bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mapa_salidas_cotizador WHERE calculadora_id=$1 AND tipo_fuente='SALIDA' AND fuente_id=$2 AND activo)`, id, clave).Scan(&usada)
	if err != nil {
		h.error(w, err)
		return
	}
	if usada {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "Otra salida toma su valor de esta. Cambie o quite esa asociación primero."})
		return
	}
	res, err := tx.Exec(ctx, `DELETE FROM mapa_salidas_cotizador WHERE calculadora_id=$1 AND clave_salida=$2`, id, clave)
	if err != nil {
		h.error(w, err)
		return
	}
	if res.RowsAffected() == 0 {
		escribirJSON(w, 404, map[string]any{"ok": false, "error": "La salida indicada no existe."})
		return
	}
	if err = tx.Commit(ctx); err != nil {
		h.error(w, err)
		return
	}
	escribirJSON(w, 200, map[string]any{"ok": true})
}

func (h *SalidasCotizadorHandler) error(w http.ResponseWriter, err error) {
	log.Printf("salidas cotizador: %v", err)
	escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible procesar el mapa de salidas."})
}

func fuentesSalidasDiseno(ctx context.Context, q consultadorRuntime, id string) (map[string]map[string]any, map[string]elementoRuntime, error) {
	rows, err := q.Query(ctx, `SELECT e.elemento_id,e.tipo,e.configuracion,COALESCE(p.tipo,''),COALESCE(e.componente_padre_id,''),COALESCE(e.etiqueta,'')
		FROM elementos_tab_cotizador e JOIN tabs_cotizador t ON t.tab_id=e.tab_id
		LEFT JOIN elementos_tab_cotizador p ON p.elemento_id=e.componente_padre_id AND p.activo
		WHERE e.activo AND t.activo AND (t.calculadora_id=$1 OR EXISTS(SELECT 1 FROM tabs_cotizador_asociaciones a WHERE a.tab_id=t.tab_id AND a.calculadora_id=$1))`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	els := map[string]map[string]any{}
	metas := map[string]elementoRuntime{}
	for rows.Next() {
		var id, tipo, padreTipo, padre, etiqueta string
		var cfg map[string]any
		if err := rows.Scan(&id, &tipo, &cfg, &padreTipo, &padre, &etiqueta); err != nil {
			return nil, nil, err
		}
		// La etiqueta solo sirve para los mensajes de validarMapaSalidas.
		els[id] = map[string]any{"tipo": tipo, "configuracion": cfg, "etiqueta": etiqueta}
		meta := elementoRuntime{Tipo: tipo}
		if padreTipo == "OPCIONES_PROPUESTA" {
			meta.PadreOpcionesID = padre
		}
		metas[id] = meta
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// Mismo criterio que indexarElementosRuntime (Ronda F2): con un
	// OPCIONES_PROPUESTA de alcance COTIZACION, todo campo/cálculo fuera de
	// un componente LOCAL es fuente ESCENARIO de ese componente global.
	if global := padreOpcionesCotizacionIndexado(els); global != "" {
		for id, meta := range metas {
			if meta.PadreOpcionesID == "" && tiposConValorPorOpcionCotizacion[meta.Tipo] {
				meta.PadreOpcionesID = global
				metas[id] = meta
			}
		}
	}
	return els, metas, nil
}

// nombreTipoElemento: nombre del tipo de componente tal como lo muestra el Diseñador.
func nombreTipoElemento(tipo string) string {
	nombres := map[string]string{"CAMPO": "Campo", "CAMPO_CATALOGO": "Campo de catálogo", "CAMPO_CALCULADO": "Campo calculado", "TABLA": "Tabla", "LISTA_PRECIOS": "Lista de precios"}
	if n := nombres[tipo]; n != "" {
		return n
	}
	return "componente"
}

// descripcionTipoSalida: qué clase de valor espera la salida, sin el código
// técnico (MONEDA/TEXTO/PORCENTAJE).
func descripcionTipoSalida(clave string) string {
	if tiposSalidas[clave] == "TEXTO" {
		return "un campo de texto o de catálogo"
	}
	return "un valor numérico"
}

func validarMapaSalidas(mapa []salidaCotizador, els map[string]map[string]any, metas map[string]elementoRuntime, permitirPendientes bool) error {
	porClave := map[string]salidaCotizador{}
	for _, s := range mapa {
		if s.Activo {
			porClave[s.ClaveSalida] = s
		}
	}
	estados := map[string]uint8{}
	var validar func(string) error
	validar = func(clave string) error {
		if estados[clave] == 1 {
			return fmt.Errorf("Hay una referencia circular entre salidas: %s termina dependiendo de su propio valor; elija otra fuente", nombreSalida(clave, false))
		}
		if estados[clave] == 2 {
			return nil
		}
		estados[clave] = 1
		s, existe := porClave[clave]
		if !existe {
			return fmt.Errorf("%s no está configurada o está inactiva; configure su fuente en Salidas", nombreSalida(clave, false))
		}
		if s.FuenteID == "" {
			if permitirPendientes {
				estados[clave] = 2
				return nil
			}
			return fmt.Errorf("%s no tiene fuente; seleccione de dónde toma su valor en Salidas antes de publicar", nombreSalida(clave, false))
		}
		if s.TipoFuente == "SALIDA" {
			if tiposSalidas[clave] != tiposSalidas[s.FuenteID] || s.PropiedadFuente != "" {
				return fmt.Errorf("%s solo puede tomar su valor de otra salida del mismo tipo (número con número, texto con texto)", nombreSalida(clave, false))
			}
			if err := validar(s.FuenteID); err != nil {
				return err
			}
		} else {
			el := els[s.FuenteID]
			if el == nil {
				return fmt.Errorf("La fuente elegida para %s ya no existe, está inactiva o no pertenece a este cotizador; seleccione otra fuente en Salidas", nombreSalida(clave, false))
			}
			tipo, _ := el["tipo"].(string)
			cfg, _ := el["configuracion"].(map[string]any)
			padre := metas[s.FuenteID].PadreOpcionesID
			if s.TipoFuente == "ESCENARIO" && padre == "" {
				return fmt.Errorf("%s: la fuente «Escenario» tiene que ser un campo dentro de Opciones de Propuesta, y «%s» no lo es", nombreSalida(clave, false), etiquetaElemento(els, s.FuenteID))
			}
			if s.TipoFuente != "ESCENARIO" && padre != "" {
				return fmt.Errorf("%s: «%s» está dentro de Opciones de Propuesta; elija la fuente «Escenario» para usar solo la opción recomendada", nombreSalida(clave, false), etiquetaElemento(els, s.FuenteID))
			}
			if s.TipoFuente != "ESCENARIO" && !((s.TipoFuente == "CAMPO" && (tipo == "CAMPO" || tipo == "CAMPO_CATALOGO")) || (s.TipoFuente == "CALCULADO" && tipo == "CAMPO_CALCULADO") || (s.TipoFuente == "TOTAL_TABLA" && tipo == "TABLA") || (s.TipoFuente == "LISTA_PRECIO" && tipo == "LISTA_PRECIOS")) {
				return fmt.Errorf("%s: el tipo de fuente elegido no corresponde a «%s» (%s)", nombreSalida(clave, false), etiquetaElemento(els, s.FuenteID), nombreTipoElemento(tipo))
			}
			numerico := tipo == "CAMPO_CALCULADO" || tipo == "TABLA" || tipo == "LISTA_PRECIOS" || (tipo == "CAMPO" && map[string]bool{"NUMERO": true, "MONEDA": true, "PORCENTAJE": true}[strings.ToUpper(fmt.Sprint(cfg["tipo_campo"]))])
			texto := (tipo == "CAMPO" || tipo == "CAMPO_CATALOGO") && !numerico && !map[string]bool{"NUMERO": true, "MONEDA": true, "PORCENTAJE": true, "FECHA": true, "CHECK": true}[strings.ToUpper(fmt.Sprint(cfg["tipo_campo"]))]
			if (tiposSalidas[clave] == "TEXTO" && !texto) || (tiposSalidas[clave] != "TEXTO" && !numerico) {
				return fmt.Errorf("%s necesita %s y «%s» no lo es. Seleccione un campo del tipo correcto o un cálculo numérico", nombreSalida(clave, false), descripcionTipoSalida(clave), etiquetaElemento(els, s.FuenteID))
			}
			permitida := s.PropiedadFuente == "" || s.PropiedadFuente == "valor"
			if tipo == "LISTA_PRECIOS" {
				permitida = permitida || s.PropiedadFuente == "total_precio" || s.PropiedadFuente == "total_costo"
			}
			if tipo == "TABLA" {
				permitida = permitida || s.PropiedadFuente == "total_precio"
			}
			if !permitida {
				return fmt.Errorf("%s: el dato elegido de «%s» no está disponible para un %s", nombreSalida(clave, false), etiquetaElemento(els, s.FuenteID), nombreTipoElemento(tipo))
			}
		}
		estados[clave] = 2
		return nil
	}
	for _, s := range mapa {
		if s.Activo {
			if err := validar(s.ClaveSalida); err != nil {
				return err
			}
		}
	}
	return nil
}

func mapaSalidasEstructura(estructura map[string]any) ([]salidaCotizador, error) {
	mapa := []salidaCotizador{}
	raw, err := json.Marshal(estructura["salidas"])
	if err != nil {
		return nil, err
	}
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &mapa); err != nil {
			return nil, err
		}
	}

	// Un compilado anterior al paso Salidas puede traer funcion_campo pero
	// no el arreglo normalizado. Derivarlo al leer mantiene esas versiones
	// históricas ejecutables y hace que el próximo guardado materialice
	// cotizacion_salidas, sin consultar el diseño vivo ni cambiar el JSON.
	type elementoSalidaCompilada struct {
		ElementoID   string                    `json:"elemento_id"`
		Tipo         string                    `json:"tipo"`
		FuncionCampo string                    `json:"funcion_campo"`
		Hijos        []elementoSalidaCompilada `json:"hijos"`
	}
	type tabSalidaCompilada struct {
		Elementos []elementoSalidaCompilada `json:"elementos"`
	}
	var tabs []tabSalidaCompilada
	rawTabs, err := json.Marshal(estructura["tabs"])
	if err != nil {
		return nil, err
	}
	if string(rawTabs) != "null" {
		if err := json.Unmarshal(rawTabs, &tabs); err != nil {
			return nil, err
		}
	}
	configuradas := map[string]bool{}
	for _, salida := range mapa {
		configuradas[salida.ClaveSalida] = true
	}
	calculadoraID := strings.TrimSpace(fmt.Sprint(estructura["calculadora_id"]))
	var recorrer func(elementoSalidaCompilada, bool)
	recorrer = func(elemento elementoSalidaCompilada, dentroOpciones bool) {
		clave := salidaPorFuncionCampo[strings.ToUpper(strings.TrimSpace(elemento.FuncionCampo))]
		if clave != "" && !configuradas[clave] {
			tipoFuente := "CAMPO"
			if dentroOpciones {
				tipoFuente = "ESCENARIO"
			} else if elemento.Tipo == "CAMPO_CALCULADO" {
				tipoFuente = "CALCULADO"
			} else if elemento.Tipo == "LISTA_PRECIOS" {
				tipoFuente = "LISTA_PRECIO"
			}
			mapa = append(mapa, salidaCotizador{CalculadoraID: calculadoraID, ClaveSalida: clave,
				TipoFuente: tipoFuente, FuenteID: elemento.ElementoID,
				Requerido: clave == "TOTAL_PRECIO", Activo: true})
			configuradas[clave] = true
		}
		hijosEnOpciones := dentroOpciones || elemento.Tipo == "OPCIONES_PROPUESTA"
		for _, hijo := range elemento.Hijos {
			recorrer(hijo, hijosEnOpciones)
		}
	}
	for _, tab := range tabs {
		for _, elemento := range tab.Elementos {
			recorrer(elemento, false)
		}
	}
	return mapa, nil
}
