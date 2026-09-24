package isacustom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Cliente struct {
	BaseURL     string
	Prefijo     string
	HTTP        *http.Client
	token       string
	Incidencias []Incidencia
}

type Incidencia struct {
	Paso       string `json:"paso"`
	Metodo     string `json:"metodo"`
	Ruta       string `json:"ruta"`
	EstadoHTTP int    `json:"estado_http"`
	Detalle    string `json:"detalle"`
}

type Respuesta struct {
	Estado int
	Datos  map[string]any
}

func NuevoCliente(base, prefijo string) *Cliente {
	return &Cliente{BaseURL: strings.TrimRight(base, "/"), Prefijo: strings.ToUpper(prefijo), HTTP: &http.Client{Timeout: 30 * time.Second}}
}
func (c *Cliente) ID(codigo string) string { return c.Prefijo + "-" + codigo }

func (c *Cliente) Llamar(ctx context.Context, metodo, ruta string, cuerpo any) (Respuesta, error) {
	var buf bytes.Buffer
	if cuerpo != nil {
		if err := json.NewEncoder(&buf).Encode(cuerpo); err != nil {
			return Respuesta{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, metodo, c.BaseURL+ruta, &buf)
	if err != nil {
		return Respuesta{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return Respuesta{}, fmt.Errorf("%s %s: %w", metodo, ruta, err)
	}
	defer res.Body.Close()
	var datos map[string]any
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&datos); err != nil {
		return Respuesta{}, fmt.Errorf("%s %s: HTTP %d, JSON inválido: %w", metodo, ruta, res.StatusCode, err)
	}
	return Respuesta{res.StatusCode, datos}, nil
}

func (r Respuesta) OK() bool { return r.Estado >= 200 && r.Estado < 300 && r.Datos["ok"] == true }
func (r Respuesta) Error() string {
	if r.Datos["error"] != nil {
		return texto(r.Datos["error"])
	}
	if r.Datos["errores"] != nil {
		return texto(r.Datos["errores"])
	}
	return fmt.Sprintf("HTTP %d: %v", r.Estado, r.Datos)
}

func (c *Cliente) Login(ctx context.Context, correo, pin string) error {
	r, err := c.Llamar(ctx, "POST", "/api/auth/login", map[string]any{"correo": correo, "pin": pin})
	if err != nil {
		return err
	}
	if !r.OK() {
		return fmt.Errorf("login: %s", r.Error())
	}
	if mapa(r.Datos["usuario"])["rol"] != "Administrador" {
		return fmt.Errorf("el seed requiere una sesión de Administrador")
	}
	c.token = texto(r.Datos["token"])
	return nil
}
func (c *Cliente) Logout(ctx context.Context) {
	_, _ = c.Llamar(ctx, "DELETE", "/api/auth/logout", nil)
	c.token = ""
}

// Intentar conserva el rechazo real y sigue con piezas independientes. Nunca
// convierte un HTTP 400 en éxito ni sustituye una fórmula por un importe fijo.
func (c *Cliente) Intentar(ctx context.Context, paso, metodo, ruta string, cuerpo any) Respuesta {
	r, err := c.Llamar(ctx, metodo, ruta, cuerpo)
	if err != nil {
		c.Incidencias = append(c.Incidencias, Incidencia{paso, metodo, ruta, 0, err.Error()})
		return r
	}
	if !r.OK() {
		c.Incidencias = append(c.Incidencias, Incidencia{paso, metodo, ruta, r.Estado, r.Error()})
	}
	return r
}

func (c *Cliente) Advertir(paso, detalle string) {
	c.Incidencias = append(c.Incidencias, Incidencia{Paso: paso, Detalle: detalle})
}
