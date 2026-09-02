package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"cotiza/api/internal/middleware"
)

// UsuariosHandler expone alta, edición y listado de usuarios (pantalla
// "Usuarios y Permisos"). Crear y Editar exigen sesión (ver
// internal/middleware/auth.go) y, desde el Sprint 4, permisos por rol:
// solo un Administrador puede crear usuarios o editar a otra persona;
// cualquier otro rol únicamente puede editar su propio nombre, correo
// y PIN — nunca su propio rol ni estado. No hay todavía permisos más
// finos que ese (ej. "Gerente Comercial puede ver X pero no Y").
type UsuariosHandler struct {
	DB *pgxpool.Pool
}

// patronCorreoValido es deliberadamente simple (texto a ambos lados de
// una "@", y al menos un "." después) — no hace falta una validación
// exhaustiva de RFC 5322 para esta pantalla.
var patronCorreoValido = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// costoPin debe coincidir con el que usa migration-python/migrate_sheets_to_postgres.py
// (bcrypt.gensalt() por defecto = 12), para que todos los pin_hash de la
// tabla se comporten igual sin importar quién los generó.
const costoPin = 12

// usuarioAdmin es el shape que devuelve esta pantalla — nunca incluye
// pin_hash ni el PIN en texto plano.
type usuarioAdmin struct {
	UsuarioID    string     `json:"usuario_id"`
	Nombre       string     `json:"nombre"`
	Correo       string     `json:"correo"`
	Rol          string     `json:"rol"`
	Estado       string     `json:"estado"`
	UltimoAcceso *time.Time `json:"ultimo_acceso"`
}

type rolDisponible struct {
	Rol         string  `json:"rol"`
	Descripcion *string `json:"descripcion,omitempty"`
}

type crearUsuarioRequest struct {
	Nombre string `json:"nombre"`
	Correo string `json:"correo"`
	Pin    string `json:"pin"`
	Rol    string `json:"rol"`
	Estado string `json:"estado"`
}

type editarUsuarioRequest struct {
	Nombre string `json:"nombre"`
	Correo string `json:"correo"`
	Rol    string `json:"rol"`
	Estado string `json:"estado"`
	Pin    string `json:"pin"` // vacío = no resetear el PIN
}

// Listar devuelve los usuarios para la tabla de la pantalla. Nunca
// selecciona pin_hash.
func (h *UsuariosHandler) Listar(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT usuario_id, nombre, correo, rol, estado, ultimo_acceso
		  FROM usuarios
		 ORDER BY nombre`)
	if err != nil {
		log.Printf("usuarios: error listando: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los usuarios."})
		return
	}
	defer rows.Close()

	usuarios := make([]usuarioAdmin, 0)
	for rows.Next() {
		var item usuarioAdmin
		if err := rows.Scan(&item.UsuarioID, &item.Nombre, &item.Correo, &item.Rol, &item.Estado, &item.UltimoAcceso); err != nil {
			log.Printf("usuarios: error leyendo fila: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los usuarios."})
			return
		}
		usuarios = append(usuarios, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("usuarios: error leyendo filas: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los usuarios."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "usuarios": usuarios})
}

// ListarRoles alimenta el <select> de rol del formulario de alta/edición.
// Los roles se siembran en 0001_init_schema.sql; esta pantalla no crea
// roles nuevos.
func (h *UsuariosHandler) ListarRoles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rows, err := h.DB.Query(ctx, `
		SELECT rol, descripcion
		  FROM roles
		 WHERE estado = 'Activo'
		 ORDER BY rol`)
	if err != nil {
		log.Printf("usuarios: error listando roles: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los roles."})
		return
	}
	defer rows.Close()

	roles := make([]rolDisponible, 0)
	for rows.Next() {
		var item rolDisponible
		if err := rows.Scan(&item.Rol, &item.Descripcion); err != nil {
			log.Printf("usuarios: error leyendo rol: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los roles."})
			return
		}
		roles = append(roles, item)
	}
	if err := rows.Err(); err != nil {
		log.Printf("usuarios: error leyendo roles: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible consultar los roles."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "roles": roles})
}

// Crear da de alta un usuario nuevo. Solo un Administrador puede
// hacerlo. El PIN llega en texto plano y se hashea acá mismo, antes de
// tocar la base — nunca se guarda ni se loguea el valor recibido.
func (h *UsuariosHandler) Crear(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	_, rolSesion, err := h.obtenerSesion(ctx, r)
	if err != nil {
		log.Printf("usuarios: error validando la sesión: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return
	}
	if rolSesion != "Administrador" {
		escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo un Administrador puede crear usuarios."})
		return
	}

	var req crearUsuarioRequest
	if err := decodificarJSON(r, &req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	nombre := strings.TrimSpace(req.Nombre)
	correo := strings.ToLower(strings.TrimSpace(req.Correo))
	pin := strings.TrimSpace(req.Pin)
	rol := strings.TrimSpace(req.Rol)
	estado := strings.TrimSpace(req.Estado)
	if estado == "" {
		estado = "Activo"
	}

	if nombre == "" || correo == "" || pin == "" || rol == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar nombre, correo, PIN y rol."})
		return
	}
	if !patronCorreoValido.MatchString(correo) {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El correo no tiene un formato válido."})
		return
	}

	existeRol, err := rolExiste(ctx, h.DB, rol)
	if err != nil {
		log.Printf("usuarios: error validando rol %s: %v", rol, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el rol."})
		return
	}
	if !existeRol {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El rol indicado no existe."})
		return
	}

	pinHash, err := bcrypt.GenerateFromPassword([]byte(pin), costoPin)
	if err != nil {
		log.Printf("usuarios: error hasheando PIN: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible procesar el PIN."})
		return
	}

	// Reintenta con un id nuevo solo si el generado choca con uno
	// existente (extremadamente improbable con el sufijo aleatorio).
	var usuarioID string
	for intento := 0; intento < 3; intento++ {
		usuarioID = generarUsuarioID(correo)
		_, err = h.DB.Exec(ctx, `
			INSERT INTO usuarios (usuario_id, nombre, correo, pin_hash, rol, estado, puede_ver_gestor)
			VALUES ($1, $2, $3, $4, $5, $6, true)`,
			usuarioID, nombre, correo, string(pinHash), rol, estado,
		)
		if err == nil {
			break
		}
		if pgErr, esUnica := comoViolacionUnica(err); esUnica {
			if strings.Contains(pgErr.ConstraintName, "correo") {
				escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Ya existe un usuario con ese correo."})
				return
			}
			// Choque en usuario_id: reintentar con otro sufijo aleatorio.
			continue
		}
		break
	}
	if err != nil {
		log.Printf("usuarios: error creando usuario: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible crear el usuario."})
		return
	}

	escribirJSON(w, http.StatusCreated, map[string]any{"ok": true, "usuario": usuarioAdmin{
		UsuarioID: usuarioID,
		Nombre:    nombre,
		Correo:    correo,
		Rol:       rol,
		Estado:    estado,
	}})
}

// Editar actualiza nombre/correo/rol/estado y, si se indica un PIN
// nuevo, lo hashea y lo reemplaza. Un Administrador puede editar a
// cualquier usuario y cambiar cualquier campo; cualquier otro rol solo
// puede editar SU PROPIO usuario (comparado contra el usuario_id de la
// sesión, nunca el {id} de la URL a ciegas) y solo nombre/correo/pin —
// si el cuerpo trae "rol" o "estado" sin ser Administrador, se
// rechaza en vez de aplicarlo o ignorarlo en silencio, para que un
// intento de escalar privilegios quede visible en la respuesta.
func (h *UsuariosHandler) Editar(w http.ResponseWriter, r *http.Request) {
	usuarioID := strings.TrimSpace(chi.URLParam(r, "id"))
	if usuarioID == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar el usuario a editar."})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	usuarioIDSesion, rolSesion, err := h.obtenerSesion(ctx, r)
	if err != nil {
		log.Printf("usuarios: error validando la sesión: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return
	}
	esAdmin := rolSesion == "Administrador"

	if !esAdmin && usuarioID != usuarioIDSesion {
		escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo puede editar su propio usuario."})
		return
	}

	cuerpo, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "No fue posible leer el cuerpo de la solicitud."})
		return
	}

	var req editarUsuarioRequest
	decoder := json.NewDecoder(bytes.NewReader(cuerpo))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cuerpo de la solicitud no es JSON válido."})
		return
	}

	if !esAdmin {
		var crudo map[string]json.RawMessage
		if err := json.Unmarshal(cuerpo, &crudo); err != nil {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El cuerpo de la solicitud no es JSON válido."})
			return
		}
		if _, traeRol := crudo["rol"]; traeRol {
			escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo un Administrador puede cambiar el rol."})
			return
		}
		if _, traeEstado := crudo["estado"]; traeEstado {
			escribirJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "Solo un Administrador puede cambiar el estado."})
			return
		}
	}

	nombre := strings.TrimSpace(req.Nombre)
	correo := strings.ToLower(strings.TrimSpace(req.Correo))
	pin := strings.TrimSpace(req.Pin)
	rol := strings.TrimSpace(req.Rol)
	estado := strings.TrimSpace(req.Estado)

	if nombre == "" || correo == "" {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar nombre y correo."})
		return
	}
	if !patronCorreoValido.MatchString(correo) {
		escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El correo no tiene un formato válido."})
		return
	}

	if esAdmin {
		if rol == "" || estado == "" {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Debe indicar rol y estado."})
			return
		}
		existeRol, err := rolExiste(ctx, h.DB, rol)
		if err != nil {
			log.Printf("usuarios: error validando rol %s: %v", rol, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar el rol."})
			return
		}
		if !existeRol {
			escribirJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "El rol indicado no existe."})
			return
		}
	} else {
		// No vinieron en el cuerpo (ya se rechazó si lo intentó) — se
		// mantienen los actuales para no pisarlos con la cadena vacía.
		if err := h.DB.QueryRow(ctx, `SELECT rol, estado FROM usuarios WHERE usuario_id = $1`, usuarioID).Scan(&rol, &estado); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Usuario no encontrado."})
				return
			}
			log.Printf("usuarios: error leyendo rol/estado actuales de %s: %v", usuarioID, err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los cambios."})
			return
		}
	}

	var tag pgconn.CommandTag
	if pin == "" {
		tag, err = h.DB.Exec(ctx, `
			UPDATE usuarios SET nombre = $1, correo = $2, rol = $3, estado = $4
			 WHERE usuario_id = $5`,
			nombre, correo, rol, estado, usuarioID,
		)
	} else {
		var pinHash []byte
		pinHash, err = bcrypt.GenerateFromPassword([]byte(pin), costoPin)
		if err != nil {
			log.Printf("usuarios: error hasheando PIN nuevo: %v", err)
			escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible procesar el PIN."})
			return
		}
		tag, err = h.DB.Exec(ctx, `
			UPDATE usuarios SET nombre = $1, correo = $2, rol = $3, estado = $4, pin_hash = $5
			 WHERE usuario_id = $6`,
			nombre, correo, rol, estado, string(pinHash), usuarioID,
		)
	}
	if err != nil {
		if _, esUnica := comoViolacionUnica(err); esUnica {
			escribirJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Ya existe un usuario con ese correo."})
			return
		}
		log.Printf("usuarios: error editando %s: %v", usuarioID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible guardar los cambios."})
		return
	}
	if tag.RowsAffected() == 0 {
		escribirJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "Usuario no encontrado."})
		return
	}

	var actualizado usuarioAdmin
	err = h.DB.QueryRow(ctx, `
		SELECT usuario_id, nombre, correo, rol, estado, ultimo_acceso
		  FROM usuarios WHERE usuario_id = $1`, usuarioID,
	).Scan(&actualizado.UsuarioID, &actualizado.Nombre, &actualizado.Correo, &actualizado.Rol, &actualizado.Estado, &actualizado.UltimoAcceso)
	if err != nil {
		log.Printf("usuarios: error releyendo %s tras editar: %v", usuarioID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "El usuario se actualizó, pero no se pudo confirmar el resultado."})
		return
	}

	escribirJSON(w, http.StatusOK, map[string]any{"ok": true, "usuario": actualizado})
}

func rolExiste(ctx context.Context, db *pgxpool.Pool, rol string) (bool, error) {
	var existe bool
	err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM roles WHERE rol = $1)`, rol).Scan(&existe)
	return existe, err
}

// obtenerSesion resuelve el usuario_id que middleware.RequiereSesion
// dejó en el contexto de la petición al rol que tiene HOY en la base
// (nunca se confía en un rol que el cliente pudiera mandar en el
// cuerpo). Cualquier error acá — sesión sin usuario_id asociado, o el
// usuario_id ya no existe — se trata como "no se pudo validar los
// permisos", no como una sesión inválida (eso ya lo filtró el
// middleware antes de llegar acá).
func (h *UsuariosHandler) obtenerSesion(ctx context.Context, r *http.Request) (usuarioID, rol string, err error) {
	usuarioID, _ = r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		return "", "", errors.New("la sesión no tiene un usuario_id asociado")
	}
	err = h.DB.QueryRow(ctx, `SELECT rol FROM usuarios WHERE usuario_id = $1`, usuarioID).Scan(&rol)
	return usuarioID, rol, err
}

// comoViolacionUnica reconoce el código de Postgres para llaves/valores
// únicos duplicados (23505), sin importar si vino de pgx.Exec o de un
// error envuelto.
func comoViolacionUnica(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr, true
	}
	return nil, false
}

// generarUsuarioID crea un id legible a partir del correo (para que se
// pueda reconocer al usuario en la base a simple vista) más un sufijo
// aleatorio que evita choques entre altas simultáneas.
func generarUsuarioID(correo string) string {
	local := correo
	if pos := strings.IndexByte(correo, '@'); pos >= 0 {
		local = correo[:pos]
	}

	var base strings.Builder
	for _, c := range strings.ToLower(local) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			base.WriteRune(c)
		case c == '.' || c == '_' || c == '-':
			base.WriteByte('-')
		}
	}
	texto := strings.Trim(base.String(), "-")
	if texto == "" {
		texto = "usuario"
	}
	if len(texto) > 24 {
		texto = texto[:24]
	}

	sufijo := make([]byte, 3)
	_, _ = rand.Read(sufijo) // crypto/rand.Read no falla en la práctica; un id repetido reintenta en el caller.

	return "usr-" + texto + "-" + hex.EncodeToString(sufijo)
}
