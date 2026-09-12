package middleware

// RequiereApiKey protege los endpoints que llama un sistema externo
// (Bitrix24 u otro) directamente, sin que haya un usuario de Cotiza
// detrás — por eso valida contra integraciones_api en vez de contra
// sesiones. Va en su propio grupo de rutas, separado del que usa
// RequiereSesion (ver cmd/server/main.go).

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// IntegracionIDKey guarda el integracion_id de la clave validada, para
// que el handler que crea la solicitud sepa de dónde vino sin volver
// a leer el header.
const IntegracionIDKey contextKey = "cotiza_integracion_id"

// mensajeApiKeyInvalida es intencionalmente el mismo para cualquier
// motivo de rechazo (header ausente, clave inexistente, integración
// inactiva) — mismo criterio que mensajeSesionInvalida: no dar pistas
// de cuál fue el problema.
const mensajeApiKeyInvalida = "Clave de API inválida o inactiva."

// RequiereApiKey lee "X-Api-Key" y la compara con bcrypt contra el
// api_key_hash de cada integración activa. bcrypt no permite buscar
// por igualdad directa (el hash incluye una sal distinta cada vez),
// así que hay que recorrer las integraciones activas una por una y
// comparar cada una — a diferencia de un login con correo, acá no hay
// un identificador previo que acote la búsqueda a una sola fila.
func RequiereApiKey(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clave := r.Header.Get("X-Api-Key")
			if clave == "" {
				responderApiKeyInvalida(w)
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			rows, err := db.Query(ctx, `SELECT integracion_id::text, api_key_hash FROM integraciones_api WHERE estado = 'Activo'`)
			if err != nil {
				responderApiKeyInvalida(w)
				return
			}

			var integracionID string
			encontrada := false
			for rows.Next() {
				var id, hash string
				if err := rows.Scan(&id, &hash); err != nil {
					continue
				}
				if bcrypt.CompareHashAndPassword([]byte(hash), []byte(clave)) == nil {
					integracionID = id
					encontrada = true
					break
				}
			}
			rows.Close()

			if !encontrada {
				responderApiKeyInvalida(w)
				return
			}

			// El sello de último uso no es crítico: si falla, la clave
			// sigue siendo válida para esta petición.
			_, _ = db.Exec(ctx, `UPDATE integraciones_api SET ultima_uso = now() WHERE integracion_id::text = $1`, integracionID)

			ctxConIntegracion := context.WithValue(r.Context(), IntegracionIDKey, integracionID)
			next.ServeHTTP(w, r.WithContext(ctxConIntegracion))
		})
	}
}

func responderApiKeyInvalida(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": mensajeApiKeyInvalida})
}
