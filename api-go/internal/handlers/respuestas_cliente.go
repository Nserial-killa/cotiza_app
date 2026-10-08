package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type respuestaClienteRequest struct {
	Tipo       string `json:"tipo"`
	Nombre     string `json:"nombre"`
	Comentario string `json:"comentario"`
}

type codigoAceptacionRequest struct {
	Nombre string `json:"nombre"`
	Cargo  string `json:"cargo"`
}

type aceptarOfertaRequest struct {
	Codigo         string `json:"codigo"`
	NombreFirma    string `json:"nombre_firma"`
	Consentimiento bool   `json:"consentimiento"`
}

func ipVisitante(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return ip
	}
	return r.RemoteAddr
}

func tokenPublico(r *http.Request) string { return strings.TrimSpace(chi.URLParam(r, "token")) }

func hashCodigo(token, codigo string) string {
	suma := sha256.Sum256([]byte(token + ":" + codigo))
	return hex.EncodeToString(suma[:])
}

func codigoAleatorio() (string, error) {
	var bytes [4]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	valor := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	return fmt.Sprintf("%06d", valor%1000000), nil
}

func enlaceDisponible(ctx context.Context, db *pgxpool.Pool, token string) (cotizacionID string, version int, correo string, err error) {
	err = db.QueryRow(ctx, `SELECT cotizacion_id, version, COALESCE(correo_destinatario,'')
		FROM cotizacion_enlaces_publicos
		WHERE token=$1 AND (fecha_expiracion IS NULL OR fecha_expiracion>now())`, token).Scan(&cotizacionID, &version, &correo)
	return
}

func responderEnlaceNoDisponible(w http.ResponseWriter) {
	escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": mensajeEnlaceNoDisponible})
}

func estadoPermiteRespuesta(estado string) bool {
	switch estado {
	case "Borrador", "Revisión Comercial", "Enviada al Cliente", "Vista por el Cliente", "Cambios solicitados":
		return true
	default:
		return false
	}
}

// Responder registra comentarios, solicitudes de cambio y rechazos en la
// versión exacta del enlace. El visitante nunca puede reabrir una aceptada.
func (h *EnlacesPublicosHandler) Responder(w http.ResponseWriter, r *http.Request) {
	token := tokenPublico(r)
	if token == "" {
		responderEnlaceNoDisponible(w)
		return
	}
	var req respuestaClienteRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Tipo = strings.ToUpper(strings.TrimSpace(req.Tipo))
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.Comentario = strings.TrimSpace(req.Comentario)
	if req.Tipo != "COMENTARIO" && req.Tipo != "CAMBIOS" && req.Tipo != "RECHAZO" {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "La acción solicitada no es válida."})
		return
	}
	if req.Nombre == "" || len([]rune(req.Nombre)) > 150 || req.Comentario == "" || len([]rune(req.Comentario)) > 3000 {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "Indique su nombre y un comentario de hasta 3000 caracteres."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	cotizacionID, version, correo, err := enlaceDisponible(ctx, h.DB, token)
	if errors.Is(err, pgx.ErrNoRows) {
		responderEnlaceNoDisponible(w)
		return
	}
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible consultar la oferta."})
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la respuesta."})
		return
	}
	defer tx.Rollback(ctx)
	var versionActual int
	if err := tx.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id=$1 FOR UPDATE`, cotizacionID).Scan(&versionActual); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la respuesta."})
		return
	}
	var estadoAnterior string
	if err := tx.QueryRow(ctx, `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2 FOR UPDATE`, cotizacionID, version).Scan(&estadoAnterior); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la respuesta."})
		return
	}
	if !estadoPermiteRespuesta(estadoAnterior) {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "Esta versión ya está cerrada y no admite nuevas respuestas."})
		return
	}
	var nuevoEstado *string
	accion := "COMENTARIO_CLIENTE"
	if req.Tipo == "CAMBIOS" {
		nuevoEstado = strPtr("Cambios solicitados")
		accion = "CAMBIOS_SOLICITADOS_CLIENTE"
	}
	if req.Tipo == "RECHAZO" {
		nuevoEstado = strPtr("Perdida")
		accion = "RECHAZADA_POR_CLIENTE"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cotizacion_respuestas_cliente(token,tipo,nombre,correo,comentario,ip_publica)
		VALUES($1,$2,$3,NULLIF($4,''),$5,$6)`, token, req.Tipo, req.Nombre, correo, req.Comentario, ipVisitante(r)); err != nil {
		log.Printf("respuesta cliente: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la respuesta."})
		return
	}
	if nuevoEstado != nil {
		if _, err := tx.Exec(ctx, `UPDATE cotizacion_versiones SET estado=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, *nuevoEstado); err != nil {
			escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible actualizar la oferta."})
			return
		}
		if version == versionActual {
			if _, err := tx.Exec(ctx, `UPDATE cotizaciones SET estado=$2 WHERE cotizacion_id=$1`, cotizacionID, *nuevoEstado); err != nil {
				escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible actualizar la oferta."})
				return
			}
		}
	}
	comentarioHistorial := req.Nombre + " (cliente): " + req.Comentario
	if err := insertarHistorial(ctx, tx, cotizacionID, &version, accion, &estadoAnterior, nuevoEstado, comentarioHistorial, ""); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la actividad."})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible guardar la respuesta."})
		return
	}
	escribirJSON(w, 200, map[string]any{"ok": true, "estado": func() string {
		if nuevoEstado != nil {
			return *nuevoEstado
		}
		return estadoAnterior
	}()})
}

// EnviarCodigoAceptacion comprueba el buzón definido por Exceltec al
// generar el enlace. El código nunca se devuelve al navegador ni se guarda
// en claro; caduca a los diez minutos y limita los reintentos.
func (h *EnlacesPublicosHandler) EnviarCodigoAceptacion(w http.ResponseWriter, r *http.Request) {
	token := tokenPublico(r)
	if token == "" {
		responderEnlaceNoDisponible(w)
		return
	}
	var req codigoAceptacionRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Nombre = strings.TrimSpace(req.Nombre)
	req.Cargo = strings.TrimSpace(req.Cargo)
	if req.Nombre == "" || len([]rune(req.Nombre)) > 150 || len([]rune(req.Cargo)) > 150 {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "Indique el nombre del firmante; nombre y cargo deben tener menos de 150 caracteres."})
		return
	}
	if h.Correo == nil {
		escribirJSON(w, 503, map[string]any{"ok": false, "error": ErrCorreoNoConfigurado.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	cotizacionID, version, correo, err := enlaceDisponible(ctx, h.DB, token)
	if errors.Is(err, pgx.ErrNoRows) {
		responderEnlaceNoDisponible(w)
		return
	}
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible consultar la oferta."})
		return
	}
	if correo == "" {
		escribirJSON(w, 422, map[string]any{"ok": false, "error": "Exceltec debe asociar el correo del destinatario a este enlace antes de aceptar la oferta."})
		return
	}
	var estado string
	if err := h.DB.QueryRow(ctx, `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version).Scan(&estado); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible consultar el estado."})
		return
	}
	if !estadoPermiteRespuesta(estado) {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "Esta versión ya está cerrada."})
		return
	}
	codigo, err := codigoAleatorio()
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible generar el código."})
		return
	}
	var tokenVerificado string
	err = h.DB.QueryRow(ctx, `INSERT INTO cotizacion_verificaciones_aceptacion(token,codigo_hash,nombre,cargo,correo,vence_en)
		VALUES($1,$2,$3,NULLIF($4,''),$5,now()+interval '10 minutes')
		ON CONFLICT(token) DO UPDATE SET codigo_hash=EXCLUDED.codigo_hash,nombre=EXCLUDED.nombre,
		cargo=EXCLUDED.cargo,correo=EXCLUDED.correo,vence_en=EXCLUDED.vence_en,enviado_en=now(),intentos=0
		WHERE cotizacion_verificaciones_aceptacion.enviado_en < now()-interval '1 minute'
		RETURNING token`, token, hashCodigo(token, codigo), req.Nombre, req.Cargo, correo).Scan(&tokenVerificado)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, 429, map[string]any{"ok": false, "error": "Espere un minuto antes de solicitar otro código."})
		return
	}
	if err != nil {
		log.Printf("codigo aceptación: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible generar el código."})
		return
	}
	asunto := "Código para aceptar la oferta de Exceltec"
	base := strings.TrimRight(h.BasePublica, "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	enlace := base + "/publico.html?token=" + url.QueryEscape(token)
	cuerpo := fmt.Sprintf("Hola %s,\n\nRevise la oferta y complete los datos de aceptación en: %s\n\nSu código para confirmar la aceptación es: %s\nCaduca en 10 minutos. Si no solicitó este código, ignore el mensaje.\n", req.Nombre, enlace, codigo)
	if err := h.Correo.Enviar(ctx, correo, asunto, cuerpo); err != nil {
		_, _ = h.DB.Exec(ctx, `DELETE FROM cotizacion_verificaciones_aceptacion WHERE token=$1 AND codigo_hash=$2`, token, hashCodigo(token, codigo))
		log.Printf("correo de verificación: %v", err)
		if errors.Is(err, ErrCorreoNoConfigurado) {
			escribirJSON(w, 503, map[string]any{"ok": false, "error": "Exceltec aún no configuró el remitente de correo real. No se envió ningún código."})
			return
		}
		escribirJSON(w, 503, map[string]any{"ok": false, "error": "No se pudo enviar el código de verificación. Contacte a Exceltec."})
		return
	}
	escribirJSON(w, 200, map[string]any{"ok": true, "correo_destino": ocultarCorreo(correo), "vence_en_minutos": 10,
		"modo_prueba": correoEnModoPrueba(h.Correo)})
}

func ocultarCorreo(correo string) string {
	partes := strings.SplitN(correo, "@", 2)
	if len(partes) != 2 {
		return "***"
	}
	return string([]rune(partes[0])[0]) + "***@" + partes[1]
}

func (h *EnlacesPublicosHandler) Aceptar(w http.ResponseWriter, r *http.Request) {
	token := tokenPublico(r)
	if token == "" {
		responderEnlaceNoDisponible(w)
		return
	}
	var req aceptarOfertaRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Codigo = strings.TrimSpace(req.Codigo)
	req.NombreFirma = strings.TrimSpace(req.NombreFirma)
	if len(req.Codigo) != 6 || req.NombreFirma == "" || !req.Consentimiento {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "Ingrese el código de seis dígitos, escriba su nombre y confirme su aceptación electrónica."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cotizacionID, version, correo, err := enlaceDisponible(ctx, h.DB, token)
	if errors.Is(err, pgx.ErrNoRows) {
		responderEnlaceNoDisponible(w)
		return
	}
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible consultar la oferta."})
		return
	}
	tx, err := h.DB.Begin(ctx)
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible aceptar la oferta."})
		return
	}
	defer tx.Rollback(ctx)
	var versionActual int
	if err := tx.QueryRow(ctx, `SELECT version_actual FROM cotizaciones WHERE cotizacion_id=$1 FOR UPDATE`, cotizacionID).Scan(&versionActual); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible aceptar la oferta."})
		return
	}
	var estadoAnterior string
	if err := tx.QueryRow(ctx, `SELECT estado FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2 FOR UPDATE`, cotizacionID, version).Scan(&estadoAnterior); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible aceptar la oferta."})
		return
	}
	if !estadoPermiteRespuesta(estadoAnterior) {
		escribirJSON(w, 409, map[string]any{"ok": false, "error": "Esta versión ya está cerrada."})
		return
	}
	var hashGuardado, nombre, correoVerificado string
	var cargo *string
	var vence time.Time
	var intentos int
	err = tx.QueryRow(ctx, `SELECT codigo_hash,nombre,cargo,correo,vence_en,intentos FROM cotizacion_verificaciones_aceptacion WHERE token=$1 FOR UPDATE`, token).Scan(&hashGuardado, &nombre, &cargo, &correoVerificado, &vence, &intentos)
	if errors.Is(err, pgx.ErrNoRows) {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "Solicite primero un código de verificación."})
		return
	}
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible verificar el código."})
		return
	}
	if time.Now().After(vence) || intentos >= 5 {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "El código caducó o agotó sus intentos. Solicite otro."})
		return
	}
	if subtle.ConstantTimeCompare([]byte(hashGuardado), []byte(hashCodigo(token, req.Codigo))) != 1 {
		_, _ = tx.Exec(ctx, `UPDATE cotizacion_verificaciones_aceptacion SET intentos=intentos+1 WHERE token=$1`, token)
		_ = tx.Commit(ctx)
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "Código incorrecto. Revise el correo e intente de nuevo."})
		return
	}
	if !strings.EqualFold(nombre, req.NombreFirma) || !strings.EqualFold(correo, correoVerificado) {
		escribirJSON(w, 400, map[string]any{"ok": false, "error": "El nombre de la firma debe coincidir con el del código solicitado."})
		return
	}
	doc, err := construirDocumentoOferta(ctx, h.DB, cotizacionID, version)
	if err != nil {
		log.Printf("aceptación: documento: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible fijar la evidencia del documento."})
		return
	}
	doc.Estado = ""
	bytesDocumento, err := json.Marshal(doc)
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible fijar la evidencia del documento."})
		return
	}
	suma := sha256.Sum256(bytesDocumento)
	constancia, err := generarToken()
	if err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible generar la constancia."})
		return
	}
	constancia = "EX-" + strings.ToUpper(constancia[:16])
	if _, err := tx.Exec(ctx, `INSERT INTO cotizacion_aceptaciones_cliente
		(token,cotizacion_id,numero_version,nombre_firmante,cargo_firmante,correo_firmante,codigo_constancia,documento_sha256,ip_publica,agente_usuario)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, token, cotizacionID, version, nombre, cargo, correo, constancia, hex.EncodeToString(suma[:]), ipVisitante(r), r.UserAgent()); err != nil {
		log.Printf("aceptación: evidencia: %v", err)
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la aceptación."})
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE cotizacion_versiones SET estado='Aceptada',fecha_aceptacion=now(),aceptada_por=$3,origen_aceptacion='ENLACE_CLIENTE' WHERE cotizacion_id=$1 AND numero_version=$2`, cotizacionID, version, nombre); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la aceptación."})
		return
	}
	if version == versionActual {
		if _, err := tx.Exec(ctx, `UPDATE cotizaciones SET estado='Aceptada',version_aceptada=$2 WHERE cotizacion_id=$1`, cotizacionID, version); err != nil {
			escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la aceptación."})
			return
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE cotizaciones SET version_aceptada=$2 WHERE cotizacion_id=$1`, cotizacionID, version); err != nil {
			escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la aceptación."})
			return
		}
	}
	if err := insertarHistorial(ctx, tx, cotizacionID, &version, "ACEPTADA_POR_CLIENTE", &estadoAnterior, strPtr("Aceptada"), fmt.Sprintf("%s (%s), constancia %s", nombre, correo, constancia), ""); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible registrar la actividad."})
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM cotizacion_verificaciones_aceptacion WHERE token=$1`, token); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible cerrar la verificación."})
		return
	}
	if err := tx.Commit(ctx); err != nil {
		escribirJSON(w, 500, map[string]any{"ok": false, "error": "No fue posible confirmar la aceptación."})
		return
	}
	correoEnviado := false
	if h.Correo != nil {
		base := strings.TrimRight(h.BasePublica, "/")
		if base == "" {
			base = "http://localhost:8080"
		}
		enlace := base + "/publico.html?token=" + url.QueryEscape(token)
		asunto := "Oferta de Exceltec aceptada · constancia " + constancia
		cuerpo := fmt.Sprintf("Hola %s,\n\nSu aceptación electrónica de la oferta quedó registrada.\nConstancia: %s\nDocumento aceptado: %s\n\nAbra el documento para revisar la firma al pie e imprimirlo o guardarlo como PDF.\n", nombre, constancia, enlace)
		if err := h.Correo.Enviar(ctx, correo, asunto, cuerpo); err != nil {
			log.Printf("correo confirmación oferta: %v", err)
		} else {
			correoEnviado = true
			_, _ = h.DB.Exec(ctx, `UPDATE cotizacion_aceptaciones_cliente SET correo_confirmacion_enviado=now() WHERE token=$1`, token)
		}
	}
	escribirJSON(w, 200, map[string]any{"ok": true, "estado": "Aceptada", "codigo_constancia": constancia, "correo_confirmacion_enviado": correoEnviado})
}

func consultarAceptacionPublica(ctx context.Context, db *pgxpool.Pool, token string) (any, error) {
	var nombre, correo, codigo, hash string
	var cargo, ip *string
	var fecha time.Time
	err := db.QueryRow(ctx, `SELECT nombre_firmante,cargo_firmante,correo_firmante,codigo_constancia,documento_sha256,ip_publica,fecha
		FROM cotizacion_aceptaciones_cliente WHERE token=$1`, token).Scan(&nombre, &cargo, &correo, &codigo, &hash, &ip, &fecha)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"nombre_firmante": nombre, "cargo_firmante": valorTexto(cargo), "correo_firmante": correo,
		"codigo_constancia": codigo, "documento_sha256": hash, "ip_publica": valorTexto(ip), "fecha": fecha}, nil
}

func (h *CotizacionesHandler) consultarEnlaces(ctx context.Context, cotizacionID string) ([]map[string]any, error) {
	rows, err := h.DB.Query(ctx, `SELECT e.token,e.version,e.correo_destinatario,e.fecha_expiracion,e.visitas,
		a.nombre_firmante,a.codigo_constancia,a.fecha
		FROM cotizacion_enlaces_publicos e LEFT JOIN cotizacion_aceptaciones_cliente a ON a.token=e.token
		WHERE e.cotizacion_id=$1 ORDER BY e.version DESC`, cotizacionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resultados := make([]map[string]any, 0)
	for rows.Next() {
		var token string
		var version, visitas int
		var correo, nombre, codigo *string
		var expira, aceptada *time.Time
		if err := rows.Scan(&token, &version, &correo, &expira, &visitas, &nombre, &codigo, &aceptada); err != nil {
			return nil, err
		}
		estado := "Activo"
		if expira != nil && time.Now().After(*expira) {
			estado = "Vencido"
		}
		resultados = append(resultados, map[string]any{"version": version, "url_publica": "/publico.html?token=" + url.QueryEscape(token),
			"estado": estado, "correo_destinatario": valorTexto(correo), "visitas": visitas, "nombre_firmante": valorTexto(nombre),
			"codigo_constancia": valorTexto(codigo), "fecha_aceptacion": aceptada})
	}
	return resultados, rows.Err()
}
