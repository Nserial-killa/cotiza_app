package handlers

// Permisos por rol a nivel de endpoint. Hasta esta ronda solo
// puede_ver_price filtraba algo (y como filtro de campos, no como 403);
// las otras cinco banderas de roles existían desde el Sprint 0 sin
// bloquear nada. Este archivo concentra la resolución de TODAS las
// banderas del rol de la sesión en una sola consulta, más los atajos
// exigirX que ya escriben el 403 y devuelven false para cortar el
// handler ahí.
//
// Es un permiso ADICIONAL sobre rutas que ya exigen sesión
// (middleware.RequiereSesion). Si aun así el usuario_id no está en el
// contexto (el handler quedó montado fuera del grupo protegido) se
// responde 401, igual que ya lo hacían Crear/Convertir.
//
// Significado de cada bandera (confirmado, no reinterpretar):
//   - puede_crear: crear una cotización nueva (POST /api/cotizaciones y
//     convertir una Solicitud, que también la crea).
//   - puede_editar_borrador: guardar valores de una cotización (y
//     administrar sus Opciones de Propuesta, que es editarla).
//   - puede_crear_version: crear una versión nueva de una cotización.
//   - puede_aprobar: cambiar el estado específicamente a Aceptada o
//     Ganada. No bloquea ninguna otra transición.
//   - puede_parametrizar: escribir en el Diseñador completo (catálogos,
//     cotizador, reglas, salidas, compilar y plantillas). Se aplica como
//     middleware en main.go (RequierePuedeParametrizar) porque son
//     decenas de rutas; cmd/server/permisos_rutas_test.go audita que
//     ninguna de escritura quede fuera.
//   - alcance_propio: ver el bloque de "Alcance propio" más abajo.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cotiza/api/internal/middleware"
)

// permisosSesion es la foto de las banderas del rol del usuario de la
// sesión HOY en la base (nunca lo que diga el cliente ni lo que se
// guardó en localStorage al hacer login).
type permisosSesion struct {
	UsuarioID           string
	Rol                 string
	PuedeCrear          bool
	PuedeEditarBorrador bool
	PuedeCrearVersion   bool
	PuedeVerPrice       bool
	PuedeAprobar        bool
	PuedeParametrizar   bool
	AlcancePropio       bool
}

// permisoRol describe una bandera: su columna (viaja en el 403 como
// "permiso" para que el frontend y las pruebas la identifiquen), la
// acción en palabras para el mensaje, y cómo leerla de permisosSesion.
type permisoRol struct {
	columna string
	accion  string
	tiene   func(permisosSesion) bool
}

var (
	permisoCrear = permisoRol{"puede_crear", "crear cotizaciones",
		func(p permisosSesion) bool { return p.PuedeCrear }}
	permisoEditarBorrador = permisoRol{"puede_editar_borrador", "editar los valores de una cotización",
		func(p permisosSesion) bool { return p.PuedeEditarBorrador }}
	permisoCrearVersion = permisoRol{"puede_crear_version", "crear versiones nuevas de una cotización",
		func(p permisosSesion) bool { return p.PuedeCrearVersion }}
	permisoAprobar = permisoRol{"puede_aprobar", "marcar una cotización como Aceptada o Ganada",
		func(p permisosSesion) bool { return p.PuedeAprobar }}
	permisoParametrizar = permisoRol{"puede_parametrizar", "modificar el Diseñador (catálogos, cotizador, reglas, salidas y plantillas)",
		func(p permisosSesion) bool { return p.PuedeParametrizar }}
)

// estadosQueExigenAprobar son los únicos destinos de CambiarEstado que
// piden puede_aprobar.
var estadosQueExigenAprobar = map[string]bool{"Aceptada": true, "Ganada": true}

var errSesionSinUsuario = errors.New("la sesión no tiene un usuario_id asociado")

func resolverPermisosSesion(ctx context.Context, db consultorFila, r *http.Request) (permisosSesion, error) {
	usuarioID, _ := r.Context().Value(middleware.UsuarioIDKey).(string)
	if usuarioID == "" {
		return permisosSesion{}, errSesionSinUsuario
	}
	// LEFT JOIN + COALESCE: un usuario con un rol que no existe en la
	// tabla queda con todo en false (falla cerrado), no con un error.
	p := permisosSesion{UsuarioID: usuarioID}
	err := db.QueryRow(ctx, `
		SELECT u.rol,
		       COALESCE(rl.puede_crear, false), COALESCE(rl.puede_editar_borrador, false),
		       COALESCE(rl.puede_crear_version, false), COALESCE(rl.puede_ver_price, false),
		       COALESCE(rl.puede_aprobar, false), COALESCE(rl.puede_parametrizar, false),
		       COALESCE(rl.alcance_propio, false)
		  FROM usuarios u
		  LEFT JOIN roles rl ON rl.rol = u.rol
		 WHERE u.usuario_id = $1`, usuarioID,
	).Scan(&p.Rol, &p.PuedeCrear, &p.PuedeEditarBorrador, &p.PuedeCrearVersion,
		&p.PuedeVerPrice, &p.PuedeAprobar, &p.PuedeParametrizar, &p.AlcancePropio)
	return p, err
}

// cargarPermisos resuelve las banderas o escribe el 500 y devuelve false.
func cargarPermisos(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) (permisosSesion, bool) {
	p, err := resolverPermisosSesion(ctx, db, r)
	if errors.Is(err, errSesionSinUsuario) {
		escribirJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "No fue posible identificar al usuario de la sesión."})
		return p, false
	}
	if err != nil {
		log.Printf("permisos: error resolviendo el rol de la sesión: %v", err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return p, false
	}
	return p, true
}

func responderSinPermiso(w http.ResponseWriter, p permisosSesion, permiso permisoRol) {
	escribirJSON(w, http.StatusForbidden, map[string]any{
		"ok":      false,
		"error":   fmt.Sprintf("Su rol (%s) no tiene permiso para %s.", p.Rol, permiso.accion),
		"permiso": permiso.columna,
	})
}

// exigirPermiso resuelve las banderas y exige una. Devuelve los permisos
// para que el handler pueda seguir usándolos (por ejemplo, para el
// alcance) sin una segunda consulta.
func exigirPermiso(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, permiso permisoRol) (permisosSesion, bool) {
	p, ok := cargarPermisos(ctx, w, r, db)
	if !ok {
		return p, false
	}
	if !permiso.tiene(p) {
		responderSinPermiso(w, p, permiso)
		return p, false
	}
	return p, true
}

func exigirPuedeCrear(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) (permisosSesion, bool) {
	return exigirPermiso(ctx, w, r, db, permisoCrear)
}

func exigirPuedeEditarBorrador(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) (permisosSesion, bool) {
	return exigirPermiso(ctx, w, r, db, permisoEditarBorrador)
}

func exigirPuedeCrearVersion(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) (permisosSesion, bool) {
	return exigirPermiso(ctx, w, r, db, permisoCrearVersion)
}

func exigirPuedeAprobar(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool) (permisosSesion, bool) {
	return exigirPermiso(ctx, w, r, db, permisoAprobar)
}

// RequierePuedeParametrizar es el middleware que main.go aplica al grupo
// de rutas de ESCRITURA del Diseñador. Los GET de esos mismos módulos
// quedan fuera del grupo: cualquier sesión puede seguir leyéndolos.
func RequierePuedeParametrizar(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if _, ok := exigirPermiso(ctx, w, r, db, permisoParametrizar); !ok {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ------------------------------------------------------------
// Alcance propio (roles.alcance_propio, 0033).
//
// Qué es "propia" — investigado contra el esquema real, no asumido:
//
//   - cotizaciones NO tiene columna de dueño/creador. La única relación
//     usuario↔cotización es cotizacion_usuarios(usuario_id, funcion),
//     con funcion ∈ {Vendedor, Analista, Líder de producto} (0007).
//   - No existe un campo "Analista/Consultor" dentro de la
//     configuración del cotizador que apunte a un usuario: los valores
//     del cotizador (cotizacion_valores) son texto libre por elemento,
//     no referencias a usuarios, así que no sirven como criterio de
//     acceso. La relación real es cotizacion_usuarios.
//   - El rol Consultor "apoya alcances y esfuerzos" (0001): es la
//     función Analista, la misma que Solicitudes ya asigna con
//     solicitudes.analista_id.
//
// Por eso "propia" NO es el mismo criterio para los dos roles:
//   - Vendedor:  figura en cotizacion_usuarios con funcion = 'Vendedor'.
//   - Consultor: figura en cotizacion_usuarios con funcion = 'Analista'.
//
// Para que ese vínculo exista de verdad: crearCotizacionEnTx registra a
// quien crea con funcionResponsablePorRol (un Consultor queda como
// Analista, no como Vendedor), y SolicitudesHandler.Convertir copia a
// cotizacion_usuarios el vendedor/analista/líder asignados en la
// Solicitud.
//
// Un rol que en el futuro se marque con alcance_propio sin estar en
// funcionesAlcancePorRol ve las cotizaciones donde figure en cualquier
// función — es la lectura más amplia de "responsable" que el esquema
// permite, y la más fácil de ajustar agregando el rol al mapa.
//
// El alcance aplica al listado (filtro) y a TODO endpoint que opere
// sobre una cotización puntual (Gestor, motor de ejecución, versión,
// estado, enlace, vista previa) con 403 — ocultarla del listado sin
// bloquear el acceso directo por ID no protege nada. No aplica a
// Dashboard, Reportes ni Clientes.
// ------------------------------------------------------------

var funcionesAlcancePorRol = map[string][]string{
	"Vendedor":  {"Vendedor"},
	"Consultor": {"Analista"},
}

var funcionesCotizacionTodas = []string{"Vendedor", "Analista", "Líder de producto"}

func (p permisosSesion) funcionesAlcance() []string {
	if funciones, ok := funcionesAlcancePorRol[p.Rol]; ok {
		return funciones
	}
	return funcionesCotizacionTodas
}

// funcionResponsablePorRol es la función con la que queda registrado en
// cotizacion_usuarios quien crea una cotización.
func funcionResponsablePorRol(rol string) string {
	if rol == "Consultor" {
		return "Analista"
	}
	return "Vendedor"
}

func describirFunciones(funciones []string) string {
	return strings.Join(funciones, " o ")
}

// exigirAlcanceCotizacion responde 403 si el rol tiene alcance_propio y
// el usuario no figura como responsable de la cotización. Una
// cotización inexistente da el mismo 403 que una ajena: para un rol
// restringido no se distingue "no existe" de "no es suya".
func exigirAlcanceCotizacion(ctx context.Context, w http.ResponseWriter, db *pgxpool.Pool, p permisosSesion, cotizacionID string) bool {
	if !p.AlcancePropio {
		return true
	}
	funciones := p.funcionesAlcance()
	var propia bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM cotizacion_usuarios
		               WHERE cotizacion_id = $1 AND usuario_id = $2 AND funcion = ANY($3))`,
		cotizacionID, p.UsuarioID, funciones,
	).Scan(&propia)
	if err != nil {
		log.Printf("permisos: error validando alcance de %s sobre %s: %v", p.UsuarioID, cotizacionID, err)
		escribirJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "No fue posible validar los permisos."})
		return false
	}
	if !propia {
		escribirJSON(w, http.StatusForbidden, map[string]any{
			"ok": false,
			"error": fmt.Sprintf("No tiene acceso a esta cotización. Su rol (%s) solo puede abrir las cotizaciones donde figura como %s.",
				p.Rol, describirFunciones(funciones)),
			"permiso": "alcance_propio",
		})
		return false
	}
	return true
}

// exigirAccesoCotizacion es el atajo para los endpoints que solo leen
// una cotización puntual: resuelve permisos y aplica el alcance.
func exigirAccesoCotizacion(ctx context.Context, w http.ResponseWriter, r *http.Request, db *pgxpool.Pool, cotizacionID string) (permisosSesion, bool) {
	p, ok := cargarPermisos(ctx, w, r, db)
	if !ok {
		return p, false
	}
	return p, exigirAlcanceCotizacion(ctx, w, db, p, cotizacionID)
}
