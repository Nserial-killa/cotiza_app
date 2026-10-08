package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type correoOfertaPrueba struct{ mensajes []string }

func (c *correoOfertaPrueba) Enviar(_ context.Context, _, _, cuerpo string) error {
	c.mensajes = append(c.mensajes, cuerpo)
	return nil
}

func llamarPublicoPrueba(t *testing.T, handler *EnlacesPublicosHandler, token, ruta string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var contenido []byte
	if body != nil {
		var err error
		contenido, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	peticion := httptest.NewRequest(http.MethodPost, "/api/publico/cotizacion/"+token+ruta, strings.NewReader(string(contenido)))
	peticion.Header.Set("Content-Type", "application/json")
	router := chiRouterPublicoPrueba(handler)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, peticion)
	return rec
}

func chiRouterPublicoPrueba(h *EnlacesPublicosHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/publico/cotizacion/{token}/respuesta", h.Responder)
	r.Post("/api/publico/cotizacion/{token}/codigo", h.EnviarCodigoAceptacion)
	r.Post("/api/publico/cotizacion/{token}/aceptar", h.Aceptar)
	return r
}

func TestRespuestaCliente_ComentarioApareceEnActividad(t *testing.T) {
	pool := setupTestDB(t)
	f := crearFixtureEnlace(t, pool)
	rec := postEnlace(t, f.Handler, f.CotizacionID, map[string]any{"version": 1, "correo_destinatario": "cliente@example.com"})
	if rec.Code != 200 {
		t.Fatalf("generar enlace: %d %s", rec.Code, rec.Body.String())
	}
	var link struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &link)
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/respuesta", map[string]any{"tipo": "COMENTARIO", "nombre": "Ana Cliente", "comentario": "Ajustar el plazo de entrega"})
	if rec.Code != 200 {
		t.Fatalf("comentario: %d %s", rec.Code, rec.Body.String())
	}
	var accion, comentario string
	if err := pool.QueryRow(context.Background(), `SELECT accion,comentario FROM cotizacion_historial WHERE cotizacion_id=$1 AND accion='COMENTARIO_CLIENTE' ORDER BY fecha DESC LIMIT 1`, f.CotizacionID).Scan(&accion, &comentario); err != nil {
		t.Fatal(err)
	}
	if accion != "COMENTARIO_CLIENTE" || !strings.Contains(comentario, "Ajustar el plazo") {
		t.Fatalf("actividad incorrecta: %s %s", accion, comentario)
	}
}

func TestRespuestaCliente_CambiosYRechazoCierranLaVersion(t *testing.T) {
	pool := setupTestDB(t)
	f := crearFixtureEnlace(t, pool)
	rec := postEnlace(t, f.Handler, f.CotizacionID, map[string]any{"version": 1, "correo_destinatario": "cliente@example.com"})
	if rec.Code != http.StatusOK {
		t.Fatalf("generar enlace: %d %s", rec.Code, rec.Body.String())
	}
	var link struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &link)
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/respuesta", map[string]any{"tipo": "CAMBIOS", "nombre": "Ana Cliente", "comentario": "Necesitamos otro plazo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("solicitar cambios: %d %s", rec.Code, rec.Body.String())
	}
	var estado string
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, f.CotizacionID).Scan(&estado); err != nil {
		t.Fatal(err)
	}
	if estado != "Cambios solicitados" {
		t.Fatalf("estado tras cambios = %q", estado)
	}
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/respuesta", map[string]any{"tipo": "RECHAZO", "nombre": "Ana Cliente", "comentario": "No aprobamos el presupuesto"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rechazar: %d %s", rec.Code, rec.Body.String())
	}
	if err := pool.QueryRow(context.Background(), `SELECT estado FROM cotizaciones WHERE cotizacion_id=$1`, f.CotizacionID).Scan(&estado); err != nil {
		t.Fatal(err)
	}
	if estado != "Perdida" {
		t.Fatalf("estado tras rechazo = %q", estado)
	}
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/respuesta", map[string]any{"tipo": "COMENTARIO", "nombre": "Ana Cliente", "comentario": "Uno más"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("una versión cerrada no debería admitir respuestas: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRespuestaCliente_AceptacionVerificadaYFirmaPersistente(t *testing.T) {
	pool := setupTestDB(t)
	f := crearFixtureEnlace(t, pool)
	correo := &correoOfertaPrueba{}
	f.Handler.Correo = correo
	f.Handler.BasePublica = "https://cotiza.example"
	rec := postEnlace(t, f.Handler, f.CotizacionID, map[string]any{"version": 1, "correo_destinatario": "cliente@example.com"})
	if rec.Code != 200 {
		t.Fatalf("generar enlace: %d %s", rec.Code, rec.Body.String())
	}
	var link struct {
		Token string `json:"token"`
	}
	assertJSON(t, rec.Body.Bytes(), &link)
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/codigo", map[string]any{"nombre": "Ana Cliente", "cargo": "Gerente"})
	if rec.Code != 200 {
		t.Fatalf("código: %d %s", rec.Code, rec.Body.String())
	}
	if len(correo.mensajes) != 1 {
		t.Fatalf("se esperó un correo de verificación, llegaron %d", len(correo.mensajes))
	}
	segundoCodigo := llamarPublicoPrueba(t, f.Handler, link.Token, "/codigo", map[string]any{"nombre": "Ana Cliente", "cargo": "Gerente"})
	if segundoCodigo.Code != http.StatusTooManyRequests || len(correo.mensajes) != 1 {
		t.Fatalf("debe limitar el reenvío del código: %d, mensajes=%d", segundoCodigo.Code, len(correo.mensajes))
	}
	codigo := regexp.MustCompile(`\b[0-9]{6}\b`).FindString(correo.mensajes[0])
	if codigo == "" {
		t.Fatal("el correo no contenía código")
	}
	incorrecto := "000000"
	if codigo == incorrecto {
		incorrecto = "999999"
	}
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/aceptar", map[string]any{"codigo": incorrecto, "nombre_firma": "Ana Cliente", "consentimiento": true})
	if rec.Code != 400 {
		t.Fatalf("código incorrecto: %d %s", rec.Code, rec.Body.String())
	}
	rec = llamarPublicoPrueba(t, f.Handler, link.Token, "/aceptar", map[string]any{"codigo": codigo, "nombre_firma": "Ana Cliente", "consentimiento": true})
	if rec.Code != 200 {
		t.Fatalf("aceptar: %d %s", rec.Code, rec.Body.String())
	}
	var aceptada struct {
		Codigo        string `json:"codigo_constancia"`
		CorreoEnviado bool   `json:"correo_confirmacion_enviado"`
	}
	assertJSON(t, rec.Body.Bytes(), &aceptada)
	if aceptada.Codigo == "" || !aceptada.CorreoEnviado || len(correo.mensajes) != 2 {
		t.Fatalf("faltó constancia o confirmación: %+v mensajes=%d", aceptada, len(correo.mensajes))
	}
	var estado string
	var versionAceptada int
	if err := pool.QueryRow(context.Background(), `SELECT estado,version_aceptada FROM cotizaciones WHERE cotizacion_id=$1`, f.CotizacionID).Scan(&estado, &versionAceptada); err != nil {
		t.Fatal(err)
	}
	if estado != "Aceptada" || versionAceptada != 1 {
		t.Fatalf("estado=%s versión aceptada=%d", estado, versionAceptada)
	}
	publico := getPublico(t, f.Handler, link.Token)
	if publico.Code != 200 {
		t.Fatalf("documento firmado: %d %s", publico.Code, publico.Body.String())
	}
	var documento struct {
		Aceptacion *struct {
			Nombre string `json:"nombre_firmante"`
			Codigo string `json:"codigo_constancia"`
			Hash   string `json:"documento_sha256"`
		} `json:"aceptacion"`
	}
	assertJSON(t, publico.Body.Bytes(), &documento)
	if documento.Aceptacion == nil || documento.Aceptacion.Nombre != "Ana Cliente" || documento.Aceptacion.Codigo != aceptada.Codigo || len(documento.Aceptacion.Hash) != 64 {
		t.Fatalf("falta firma o evidencia: %+v", documento.Aceptacion)
	}
	duplicado := llamarPublicoPrueba(t, f.Handler, link.Token, "/aceptar", map[string]any{"codigo": codigo, "nombre_firma": "Ana Cliente", "consentimiento": true})
	if duplicado.Code != http.StatusConflict {
		t.Fatalf("aceptación duplicada: %d %s", duplicado.Code, duplicado.Body.String())
	}
}
