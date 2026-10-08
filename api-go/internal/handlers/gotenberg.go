package handlers

// Cliente mínimo de Gotenberg (https://gotenberg.dev), probado contra la
// imagen gotenberg/gotenberg:8.37.0. Ruta y campos confirmados en la
// documentación oficial "Convert URL to PDF":
//
//	POST /forms/chromium/convert/url   (multipart/form-data)
//	url                 -> la página a convertir
//	waitForExpression   -> JS que debe dar true antes de imprimir
//	emulatedMediaType   -> "print" aplica las reglas @media print
//	printBackground     -> incluye colores/fondos
//	preferCssPageSize   -> respeta el @page{size:...} que pone publico.html
//	skipNetworkIdleEvent=false -> espera a que la red quede quieta (logos)
//
// No se usa waitForSelector: según la documentación espera a que el
// elemento APAREZCA en el DOM, y #propuesta existe desde el principio
// (oculto con el atributo hidden). Lo que hay que esperar es que se
// vuelva visible, y eso solo lo expresa waitForExpression.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// ConvertidorPDF es lo que el handler del PDF necesita: dada una URL
// (siempre armada por el servidor), devolver los bytes del PDF. Es una
// interfaz para que las pruebas usen un doble sin levantar Gotenberg.
type ConvertidorPDF interface {
	ConvertirURL(ctx context.Context, url string) ([]byte, error)
}

// expresionPropuestaVisible es la condición de espera: el contenedor
// #propuesta existe y ya no tiene el atributo hidden (render() lo quita
// cuando terminó de dibujar la oferta), y además document.fonts terminó
// de cargar las fuentes en uso (la de firma, fonts/DancingScript.woff2):
// sin esto el PDF podría salir con la fuente de reemplazo.
const expresionPropuestaVisible = `(function(){var p=document.getElementById("propuesta");return !!p&&!p.hidden&&document.fonts.status==="loaded";})()`

// maxBytesPDF limita lo que se acepta de Gotenberg: una oferta normal pesa
// cientos de KB; 25 MB es holgado y evita llenar la memoria por un error.
const maxBytesPDF = 25 << 20

// GotenbergPDF habla con un Gotenberg real por HTTP.
type GotenbergPDF struct {
	BaseURL string       // ej. http://gotenberg:3000 (red interna de Docker)
	Cliente *http.Client // nil = http.DefaultClient; el plazo lo da el ctx
}

// ConvertirURL le pide a Gotenberg que imprima url a PDF.
func (g GotenbergPDF) ConvertirURL(ctx context.Context, url string) ([]byte, error) {
	if strings.TrimSpace(g.BaseURL) == "" { // sin Gotenberg configurado no hay PDF
		return nil, errors.New("gotenberg: GOTENBERG_URL no está configurada")
	}
	var cuerpo bytes.Buffer                    // aquí se arma el multipart
	formulario := multipart.NewWriter(&cuerpo) // escritor de campos multipart
	campos := [][2]string{                     // pares campo/valor, en orden fijo
		{"url", url}, // la página interna a convertir
		{"waitForExpression", expresionPropuestaVisible}, // espera a que #propuesta sea visible
		{"emulatedMediaType", "print"},                   // aplica @media print (oculta barra y diálogos)
		{"printBackground", "true"},                      // conserva los colores de la plantilla
		{"preferCssPageSize", "true"},                    // usa Carta/A4 definido por la plantilla
		{"skipNetworkIdleEvent", "false"},                // espera a que terminen de cargar imágenes/logos
	}
	for _, campo := range campos {
		if err := formulario.WriteField(campo[0], campo[1]); err != nil { // escribe cada campo
			return nil, fmt.Errorf("gotenberg: armando el formulario: %w", err)
		}
	}
	if err := formulario.Close(); err != nil { // cierra el multipart (escribe el boundary final)
		return nil, fmt.Errorf("gotenberg: cerrando el formulario: %w", err)
	}
	destino := strings.TrimRight(g.BaseURL, "/") + "/forms/chromium/convert/url"   // ruta oficial de "Convert URL"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destino, &cuerpo) // petición atada al ctx (plazo)
	if err != nil {
		return nil, fmt.Errorf("gotenberg: armando la petición: %w", err)
	}
	req.Header.Set("Content-Type", formulario.FormDataContentType()) // multipart con su boundary
	cliente := g.Cliente                                             // cliente HTTP a usar
	if cliente == nil {
		cliente = http.DefaultClient // por defecto, el estándar (el ctx corta la espera)
	}
	resp, err := cliente.Do(req) // envía y espera la respuesta
	if err != nil {
		return nil, fmt.Errorf("gotenberg: sin respuesta: %w", err)
	}
	defer resp.Body.Close()               // libera la conexión al terminar
	if resp.StatusCode != http.StatusOK { // Gotenberg responde 200 solo si generó el PDF
		detalle, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) // primeras líneas del error, para el log
		return nil, fmt.Errorf("gotenberg: respondió %d: %s", resp.StatusCode, strings.TrimSpace(string(detalle)))
	}
	pdf, err := io.ReadAll(io.LimitReader(resp.Body, maxBytesPDF+1)) // lee con tope (+1 para detectar exceso)
	if err != nil {
		return nil, fmt.Errorf("gotenberg: leyendo el PDF: %w", err)
	}
	if len(pdf) > maxBytesPDF { // más grande que el tope: se descarta
		return nil, errors.New("gotenberg: el PDF supera el tamaño máximo permitido")
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) { // todo PDF empieza con esa firma
		return nil, errors.New("gotenberg: la respuesta no es un PDF")
	}
	return pdf, nil // bytes listos para descargar
}
