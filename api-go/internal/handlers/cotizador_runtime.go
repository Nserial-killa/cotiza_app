package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

// CotizadorRuntimeHandler sirve y persiste la ejecución de una estructura
// previamente compilada. No interpreta fórmulas ni tipos complejos.
type CotizadorRuntimeHandler struct {
	DB *pgxpool.Pool
}

type guardarValoresRuntimeRequest struct {
	Version int                        `json:"version"`
	Valores map[string]json.RawMessage `json:"valores"`
}

type elementoRuntime struct {
	Tipo       string
	CatalogoID string
}

type contextoRuntime struct {
	CotizacionID string
	Version      int
	CompiladoID  string
	Estructura   map[string]any
	Elementos    map[string]elementoRuntime
}

type errorRuntime struct {
	status  int
	mensaje string
}

func (e *errorRuntime) Error() string { return e.mensaje }

// Obtener devuelve la estructura fijada para la cotización y los valores de
// su versión. La primera apertura fija el compilado activo si aún no existía.
func (h *CotizadorRuntimeHandler) Obtener(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "cotizacion_id"))
	version, err := versionOpcional(r.URL.Query().Get("version"))
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	runtime, err := h.cargarContexto(ctx, cotizacionID, version, true)
	if err != nil {
		h.responderError(w, "obteniendo runtime", cotizacionID, err)
		return
	}
	if err := h.incluirOpcionesCatalogo(ctx, runtime.Estructura); err != nil {
		log.Printf("cotizador runtime: error cargando opciones de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar las opciones de catálogo."})
		return
	}
	valores, err := h.leerValores(ctx, cotizacionID, runtime.Version)
	if err != nil {
		log.Printf("cotizador runtime: error leyendo valores de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible cargar los valores de la cotización."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "estructura": runtime.Estructura, "valores": valores, "version": runtime.Version})
}

// GuardarValores valida cada elemento contra el JSON compilado fijado y hace
// upsert atómico de los valores de la versión indicada.
func (h *CotizadorRuntimeHandler) GuardarValores(w http.ResponseWriter, r *http.Request) {
	cotizacionID := strings.TrimSpace(chi.URLParam(r, "cotizacion_id"))
	var req guardarValoresRuntimeRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.Version <= 0 || req.Valores == nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar version y valores."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	runtime, err := h.cargarContexto(ctx, cotizacionID, req.Version, true)
	if err != nil {
		h.responderError(w, "guardando runtime", cotizacionID, err)
		return
	}
	for elementoID, valor := range req.Valores {
		elementoID = strings.TrimSpace(elementoID)
		elemento, existe := runtime.Elementos[elementoID]
		if !existe {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El elemento %s no pertenece a la estructura compilada de esta cotización.", elementoID)})
			return
		}
		if !json.Valid(valor) {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor del elemento %s no es JSON válido.", elementoID)})
			return
		}
		if elemento.Tipo == "CAMPO_CATALOGO" {
			var valorSistema string
			if err := json.Unmarshal(valor, &valorSistema); err != nil || strings.TrimSpace(valorSistema) == "" {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor de %s debe ser una opción activa del catálogo %s.", elementoID, elemento.CatalogoID)})
				return
			}
			var permitido bool
			if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalogo_valores WHERE catalogo_id=$1 AND valor_sistema=$2 AND activo=true)`, elemento.CatalogoID, valorSistema).Scan(&permitido); err != nil {
				log.Printf("cotizador runtime: error validando catálogo %s: %v", elemento.CatalogoID, err)
				escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el valor de catálogo."})
				return
			}
			if !permitido {
				escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": fmt.Sprintf("El valor %s no es una opción activa del catálogo %s para el elemento %s.", valorSistema, elemento.CatalogoID, elementoID)})
				return
			}
		}
	}

	tx, err := h.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible iniciar el guardado."})
		return
	}
	defer tx.Rollback(ctx)
	for elementoID, valor := range req.Valores {
		_, err = tx.Exec(ctx, `
			INSERT INTO cotizacion_valores (cotizacion_id, version, elemento_id, valor)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (cotizacion_id, version, elemento_id) DO UPDATE SET valor=EXCLUDED.valor`,
			cotizacionID, req.Version, strings.TrimSpace(elementoID), string(valor))
		if err != nil {
			break
		}
	}
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	comentario := fmt.Sprintf("Se actualizaron %d valor(es) del cotizador.", len(req.Valores))
	if err == nil {
		err = insertarHistorial(ctx, tx, cotizacionID, &req.Version, "valores_actualizados", nil, nil, comentario, usuarioID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		log.Printf("cotizador runtime: error guardando valores de %s: %v", cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los valores de la cotización."})
		return
	}
	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "cotizacion_id": cotizacionID, "version": req.Version, "valores_guardados": len(req.Valores)})
}

func (h *CotizadorRuntimeHandler) cargarContexto(ctx context.Context, cotizacionID string, versionSolicitada int, fijar bool) (contextoRuntime, error) {
	resultado := contextoRuntime{CotizacionID: cotizacionID, Elementos: make(map[string]elementoRuntime)}
	if cotizacionID == "" {
		return resultado, &errorRuntime{status: http.StatusBadRequest, mensaje: "Debe indicar cotizacion_id."}
	}
	var calculadoraID string
	var versionActual int
	var compiladoID *string
	err := h.DB.QueryRow(ctx, `SELECT calculadora_id, version_actual, compilado_id_usado::text FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID).Scan(&calculadoraID, &versionActual, &compiladoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return resultado, &errorRuntime{status: http.StatusNotFound, mensaje: "La cotización indicada no existe."}
	}
	if err != nil {
		return resultado, err
	}
	resultado.Version = versionSolicitada
	if resultado.Version == 0 {
		resultado.Version = versionActual
	}
	var versionExiste bool
	if err := h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2)`, cotizacionID, resultado.Version).Scan(&versionExiste); err != nil {
		return resultado, err
	}
	if !versionExiste {
		return resultado, &errorRuntime{status: http.StatusNotFound, mensaje: "La versión indicada de la cotización no existe."}
	}
	if compiladoID == nil || strings.TrimSpace(*compiladoID) == "" {
		var activo string
		err := h.DB.QueryRow(ctx, `SELECT compilado_id::text FROM cotizadores_compilados WHERE calculadora_id=$1 AND estado='ACTIVA'`, calculadoraID).Scan(&activo)
		if errors.Is(err, pgx.ErrNoRows) {
			return resultado, &errorRuntime{status: http.StatusConflict, mensaje: "El cotizador no tiene una versión compilada activa."}
		}
		if err != nil {
			return resultado, err
		}
		compiladoID = &activo
		if fijar {
			if _, err := h.DB.Exec(ctx, `UPDATE cotizaciones SET compilado_id_usado=$2::uuid WHERE cotizacion_id=$1 AND compilado_id_usado IS NULL`, cotizacionID, activo); err != nil {
				return resultado, err
			}
		}
	}
	resultado.CompiladoID = *compiladoID
	var estructuraJSON []byte
	err = h.DB.QueryRow(ctx, `SELECT configuracion FROM cotizadores_compilados WHERE compilado_id=$1::uuid AND calculadora_id=$2`, resultado.CompiladoID, calculadoraID).Scan(&estructuraJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return resultado, &errorRuntime{status: http.StatusConflict, mensaje: "La versión compilada fijada ya no está disponible para este cotizador."}
	}
	if err != nil {
		return resultado, err
	}
	if err := json.Unmarshal(estructuraJSON, &resultado.Estructura); err != nil {
		return resultado, fmt.Errorf("estructura compilada inválida: %w", err)
	}
	resultado.Elementos = indexarElementosRuntime(resultado.Estructura)
	return resultado, nil
}

func indexarElementosRuntime(estructura map[string]any) map[string]elementoRuntime {
	resultado := make(map[string]elementoRuntime)
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		for _, elementoRaw := range elementos {
			elemento, _ := elementoRaw.(map[string]any)
			id := strings.TrimSpace(fmt.Sprint(elemento["elemento_id"]))
			if id == "" {
				continue
			}
			resultado[id] = elementoRuntime{Tipo: strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))), CatalogoID: strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"]))}
		}
	}
	return resultado
}

func (h *CotizadorRuntimeHandler) incluirOpcionesCatalogo(ctx context.Context, estructura map[string]any) error {
	tabs, _ := estructura["tabs"].([]any)
	for _, tabRaw := range tabs {
		tab, _ := tabRaw.(map[string]any)
		elementos, _ := tab["elementos"].([]any)
		for _, elementoRaw := range elementos {
			elemento, _ := elementoRaw.(map[string]any)
			if strings.ToUpper(strings.TrimSpace(fmt.Sprint(elemento["tipo"]))) != "CAMPO_CATALOGO" {
				continue
			}
			catalogoID := strings.TrimSpace(fmt.Sprint(elemento["catalogo_id"]))
			rows, err := h.DB.Query(ctx, `
				SELECT valor_id, COALESCE(clave, ''), texto_visible, valor_sistema, COALESCE(orden, 0)
				FROM catalogo_valores WHERE catalogo_id=$1 AND activo=true
				ORDER BY COALESCE(orden, 0), texto_visible`, catalogoID)
			if err != nil {
				return err
			}
			opciones := make([]map[string]any, 0)
			for rows.Next() {
				var valorID, clave, textoVisible, valorSistema string
				var orden int
				if err := rows.Scan(&valorID, &clave, &textoVisible, &valorSistema, &orden); err != nil {
					rows.Close()
					return err
				}
				opciones = append(opciones, map[string]any{"valor_id": valorID, "clave": clave, "texto_visible": textoVisible, "valor_sistema": valorSistema, "orden": orden})
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			elemento["opciones"] = opciones
		}
	}
	return nil
}

func (h *CotizadorRuntimeHandler) leerValores(ctx context.Context, cotizacionID string, version int) (map[string]any, error) {
	rows, err := h.DB.Query(ctx, `SELECT elemento_id, valor FROM cotizacion_valores WHERE cotizacion_id=$1 AND version=$2`, cotizacionID, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	valores := make(map[string]any)
	for rows.Next() {
		var elementoID string
		var raw []byte
		if err := rows.Scan(&elementoID, &raw); err != nil {
			return nil, err
		}
		var valor any
		if err := json.Unmarshal(raw, &valor); err != nil {
			return nil, err
		}
		valores[elementoID] = valor
	}
	return valores, rows.Err()
}

func versionOpcional(texto string) (int, error) {
	texto = strings.TrimSpace(texto)
	if texto == "" {
		return 0, nil
	}
	version, err := strconv.Atoi(texto)
	if err != nil || version <= 0 {
		return 0, errors.New("version debe ser un número entero positivo")
	}
	return version, nil
}

func (h *CotizadorRuntimeHandler) responderError(w http.ResponseWriter, operacion, cotizacionID string, err error) {
	var conocido *errorRuntime
	if errors.As(err, &conocido) {
		escribirJSON(w, conocido.status, map[string]any{"ok": false, "error": conocido.mensaje})
		return
	}
	log.Printf("cotizador runtime: error %s de %s: %v", operacion, cotizacionID, err)
	escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible procesar el runtime del cotizador."})
}
