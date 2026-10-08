package handlers

// Pases de render para el PDF de la oferta (tarea 2).
//
// Problema: el PDF se genera haciendo que Gotenberg (Chromium) abra
// publico.html?token=..., y esa página llama a GET
// /api/publico/cotizacion/{token}, que suma una visita y puede pasar la
// versión de "Enviada al Cliente" a "Vista por el Cliente" con su
// historial. Descargar un PDF desde el Gestor no es una visita del
// cliente, así que esa llamada tiene que poder hacerse SIN efectos.
//
// Solución: un "pase de render" de un solo uso y vida corta. El endpoint
// protegido del PDF (oferta_pdf.go) emite uno atado al token del enlace,
// lo pega a la URL interna que le manda a Gotenberg, y VerCotizacion lo
// consume: si el pase es válido, sirve el documento sin sellar visita ni
// cambiar estado. Si el pase no es válido, responde el mismo 404 que un
// token inventado (falla cerrado: nunca cae a "visita normal").
//
// Vive en memoria del proceso: hay un solo contenedor "api" y el pase dura
// segundos. Si algún día se escala a varias réplicas, esto tiene que pasar
// a una tabla (la petición de Gotenberg podría caer en otra réplica).

import (
	"crypto/subtle"
	"sync"
	"time"
)

// vidaPaseRender es cuánto vive un pase sin usarse: alcanza para que
// Chromium arranque y pida el JSON, y es corto para que un pase filtrado
// (por ejemplo, en un log) ya no sirva cuando alguien lo lea.
const vidaPaseRender = 60 * time.Second

// paseRender es lo que se recuerda de cada pase emitido.
type paseRender struct {
	tokenEnlace string    // token del enlace público al que queda atado el pase
	vence       time.Time // instante a partir del cual el pase ya no sirve
}

// PasesRender guarda los pases vigentes. Se crea uno solo en main.go y se
// comparte entre el handler del PDF (que emite) y el del enlace (que consume).
type PasesRender struct {
	mu    sync.Mutex            // protege el mapa: los handlers corren en goroutines distintas
	pases map[string]paseRender // pase -> datos; se borra al consumirse o al vencer
	ahora func() time.Time      // reloj inyectable; nil = time.Now (las pruebas lo reemplazan)
}

// NuevosPasesRender arma el registro vacío listo para usar.
func NuevosPasesRender() *PasesRender {
	return &PasesRender{pases: make(map[string]paseRender)} // mapa vacío, reloj real
}

// reloj devuelve la hora actual usando el reloj inyectado si lo hay.
func (p *PasesRender) reloj() time.Time {
	if p.ahora != nil { // en pruebas se puede fijar la hora
		return p.ahora()
	}
	return time.Now() // en producción, la hora real
}

// Emitir crea un pase nuevo para tokenEnlace y lo devuelve.
func (p *PasesRender) Emitir(tokenEnlace string) (string, error) {
	pase, err := generarToken() // mismo generador que el token de sesión: crypto/rand, 32 bytes en hex
	if err != nil {
		return "", err // sin aleatoriedad no se emite nada
	}
	p.mu.Lock()         // toma el candado antes de tocar el mapa
	defer p.mu.Unlock() // y lo suelta al salir
	ahora := p.reloj()  // una sola lectura del reloj para todo el bloque
	for clave, existente := range p.pases {
		if ahora.After(existente.vence) { // limpieza perezosa: borra los vencidos
			delete(p.pases, clave)
		}
	}
	p.pases[pase] = paseRender{tokenEnlace: tokenEnlace, vence: ahora.Add(vidaPaseRender)} // registra el pase nuevo
	return pase, nil                                                                       // el llamador lo pega a la URL interna
}

// Consumir devuelve true solo si pase existe, no venció y pertenece a
// tokenEnlace. En cualquier caso lo borra: un pase se usa una sola vez,
// y uno presentado con el token equivocado también queda inutilizado.
func (p *PasesRender) Consumir(pase, tokenEnlace string) bool {
	if p == nil || pase == "" { // sin registro o sin pase: no hay nada que validar
		return false
	}
	p.mu.Lock()                    // candado: consultar y borrar tiene que ser atómico
	defer p.mu.Unlock()            // se suelta al salir
	datos, existe := p.pases[pase] // busca el pase
	delete(p.pases, pase)          // un solo uso: se borra pase lo que pase
	if !existe {                   // nunca se emitió o ya se usó
		return false
	}
	if p.reloj().After(datos.vence) { // venció antes de usarse
		return false
	}
	// Comparación en tiempo constante del token atado contra el pedido.
	return subtle.ConstantTimeCompare([]byte(datos.tokenEnlace), []byte(tokenEnlace)) == 1
}
