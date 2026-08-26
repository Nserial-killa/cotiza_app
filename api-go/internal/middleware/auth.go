// Package middleware trae la verificación de sesión compartida por
// todos los endpoints protegidos. Sprint 3: el alcance es únicamente
// "¿hay una sesión válida sí o no?" — ni refresh tokens ni permisos
// distintos por rol todavía (eso es un paso aparte, para después).
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	// UsuarioIDKey guarda el usuario_id de la sesión validada.
	UsuarioIDKey contextKey = "cotiza_usuario_id"
	// TokenKey guarda el token crudo, para que handlers como
	// AuthHandler.Logout no tengan que volver a parsear el header.
	TokenKey contextKey = "cotiza_token"
)

// mensajeSesionInvalida es intencionalmente el mismo para cualquier
// motivo de rechazo (sin header, formato inválido, token inexistente,
// token vencido) — no hay razón para distinguirlos del lado del
// cliente, y mezclarlos evita filtrar detalles de por qué falló.
const mensajeSesionInvalida = "Sesión inválida o expirada. Inicie sesión de nuevo."

// RequiereSesion exige "Authorization: Bearer <token>" contra la
// tabla sesiones. Si el token es válido y no venció, actualiza
// fecha_ultimo_uso y deja usuario_id/token disponibles en el contexto
// de la petición (UsuarioIDKey/TokenKey) antes de seguir a la
// siguiente ruta.
func RequiereSesion(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := tokenDesdeHeader(r)
			if token == "" {
				responderNoAutorizado(w)
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			var usuarioID string
			var fechaExpiracion time.Time
			err := db.QueryRow(ctx,
				`SELECT usuario_id, fecha_expiracion FROM sesiones WHERE token = $1`, token,
			).Scan(&usuarioID, &fechaExpiracion)

			if err != nil {
				// Token inexistente (pgx.ErrNoRows) u otro error de
				// consulta: en ambos casos, mismo 401 genérico.
				responderNoAutorizado(w)
				return
			}
			if time.Now().After(fechaExpiracion) {
				responderNoAutorizado(w)
				return
			}

			// El sello de último uso no es crítico: si falla, la
			// sesión sigue siendo válida para esta petición.
			_, _ = db.Exec(ctx, `UPDATE sesiones SET fecha_ultimo_uso = now() WHERE token = $1`, token)

			ctxConSesion := context.WithValue(r.Context(), UsuarioIDKey, usuarioID)
			ctxConSesion = context.WithValue(ctxConSesion, TokenKey, token)
			next.ServeHTTP(w, r.WithContext(ctxConSesion))
		})
	}
}

func tokenDesdeHeader(r *http.Request) string {
	const prefijo = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefijo) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefijo))
}

func responderNoAutorizado(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": mensajeSesionInvalida})
}
