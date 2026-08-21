package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// AuthHandler agrupa los endpoints de autenticación (Carril B).
type AuthHandler struct {
	DB *pgxpool.Pool
}

type loginRequest struct {
	Correo string `json:"correo"`
	Pin    string `json:"pin"`
}

// usuarioSesion es el shape que el frontend espera en res.usuario
// (el mismo que simulaba el shim de google.script.run: usuario_id,
// nombre, correo, rol, puede_ver_gestor).
type usuarioSesion struct {
	UsuarioID      string `json:"usuario_id"`
	Nombre         string `json:"nombre"`
	Correo         string `json:"correo"`
	Rol            string `json:"rol"`
	PuedeVerGestor bool   `json:"puede_ver_gestor"`
	PuedeVerPrice  bool   `json:"puede_ver_price"`
}

type loginResponse struct {
	OK      bool           `json:"ok"`
	Usuario *usuarioSesion `json:"usuario,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// mensajeCredencialesInvalidas es intencionalmente genérico: no debe
// permitir distinguir "el correo no existe" de "el PIN es incorrecto"
// ni de "el usuario está inactivo". Ver CLAUDE.md (sección PINs).
const mensajeCredencialesInvalidas = "Correo o PIN incorrectos."

// hashSenuelo es un hash bcrypt válido (de un valor que nadie usa)
// contra el que se compara cuando el correo no existe, para que el
// tiempo de respuesta no delate si el usuario está o no en la base.
// El costo (12) debe coincidir con el que genera bcrypt.gensalt() en
// migration-python/migrate_sheets_to_postgres.py; si allá cambia,
// cambiarlo acá también o la comparación señuelo tarda distinto.
const hashSenuelo = "$2b$12$Mxs5IpzyI28UYFvC8mJhQunQRe1jWEMxQLS/qzNPs.WRD8C7srvgO"

// Login valida correo + PIN contra usuarios.pin_hash (bcrypt) y
// devuelve los datos de sesión que el frontend guarda en
// localStorage. Nunca registra ni devuelve el PIN.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		escribirJSON(w, http.StatusBadRequest, loginResponse{
			OK:    false,
			Error: "El cuerpo de la solicitud no es JSON válido.",
		})
		return
	}

	correo := strings.ToLower(strings.TrimSpace(req.Correo))
	pin := strings.TrimSpace(req.Pin)

	if correo == "" || pin == "" {
		escribirJSON(w, http.StatusBadRequest, loginResponse{
			OK:    false,
			Error: "Debe indicar correo y PIN.",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// El estado se filtra en Go, no en el WHERE, para que un usuario
	// inactivo siga el mismo camino (y el mismo costo en tiempo) que
	// un PIN incorrecto.
	const consulta = `
		SELECT u.usuario_id, u.nombre, u.correo, u.pin_hash, u.rol,
		       u.estado, u.puede_ver_gestor,
		       COALESCE(rl.puede_ver_price, false)
		  FROM usuarios u
		  LEFT JOIN roles rl ON rl.rol = u.rol
		 WHERE lower(u.correo) = $1`

	var (
		usuario       usuarioSesion
		pinHash       string
		estado        string
		puedeVerPrice bool
	)

	err := h.DB.QueryRow(ctx, consulta, correo).Scan(
		&usuario.UsuarioID, &usuario.Nombre, &usuario.Correo, &pinHash,
		&usuario.Rol, &estado, &usuario.PuedeVerGestor, &puedeVerPrice,
	)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Comparación señuelo: gasta el mismo tiempo que un bcrypt
		// real para no revelar por temporización que el correo no existe.
		_ = bcrypt.CompareHashAndPassword([]byte(hashSenuelo), []byte(pin))
		escribirJSON(w, http.StatusUnauthorized, loginResponse{
			OK:    false,
			Error: mensajeCredencialesInvalidas,
		})
		return
	case err != nil:
		log.Printf("auth: error consultando usuario: %v", err)
		escribirJSON(w, http.StatusInternalServerError, loginResponse{
			OK:    false,
			Error: "Error interno validando el usuario.",
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(pinHash), []byte(pin)); err != nil {
		escribirJSON(w, http.StatusUnauthorized, loginResponse{
			OK:    false,
			Error: mensajeCredencialesInvalidas,
		})
		return
	}

	if !strings.EqualFold(estado, "Activo") {
		// Mismo mensaje genérico: no se informa que la cuenta existe
		// pero está inactiva.
		escribirJSON(w, http.StatusUnauthorized, loginResponse{
			OK:    false,
			Error: mensajeCredencialesInvalidas,
		})
		return
	}

	usuario.PuedeVerPrice = puedeVerPrice

	// El acceso ya es válido; si el sello de último acceso falla, se
	// registra en el log pero no se le niega la entrada al usuario.
	if _, err := h.DB.Exec(ctx,
		`UPDATE usuarios SET ultimo_acceso = now() WHERE usuario_id = $1`,
		usuario.UsuarioID,
	); err != nil {
		log.Printf("auth: no se pudo actualizar ultimo_acceso de %s: %v", usuario.UsuarioID, err)
	}

	escribirJSON(w, http.StatusOK, loginResponse{OK: true, Usuario: &usuario})
}

func escribirJSON(w http.ResponseWriter, status int, cuerpo any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(cuerpo); err != nil {
		log.Printf("handlers: no se pudo escribir la respuesta JSON: %v", err)
	}
}
