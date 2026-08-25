# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Contexto

Cotiza es la reescritura del cotizador de Exceltec, que hoy corre en Google Apps
Script + Google Sheets, hacia un stack propio: API en Go, PostgreSQL, y Python
solo para migrar los datos de los Excel/Sheets originales. Todo se entrega en
Docker. El proyecto se desarrolla por sprints; el código y el esquema SQL están
deliberadamente incompletos y se amplían sprint a sprint (ver "Alcance por
sprint" abajo).

El código, comentarios, nombres de tablas/columnas y documentación están en
español. Mantener ese idioma en todo lo nuevo.

## Comandos

Todo desde la raíz del repo salvo que se indique lo contrario.

```bash
# Stack completo (Postgres + API en Docker) — así se entrega el sprint
docker compose up --build
curl http://localhost:8080/api/health      # {"status":"ok","database":"ok"}

# Desarrollo día a día: solo Postgres en Docker, el API nativo
docker compose up -d postgres
cd api-go
export DATABASE_URL="postgres://cotiza_admin:changeme@localhost:5432/cotiza?sslmode=disable"
export PORT=8080 STATIC_DIR=../frontend
go run ./cmd/server
# (o F5 en VSCode: el perfil de .vscode/launch.json ya trae estas variables)

# Verificación antes de cerrar una tarea de Go
cd api-go && go build ./... && go vet ./... && gofmt -l .

# Regenerar el frontend estático (obligatorio tras tocar legacy-gas/ o _build/)
python3 frontend/_build/assemble.py

# Migrar los Excel a Postgres (requiere los .xlsx en migration-python/data/)
docker compose --profile tools run --rm migration

# Inspeccionar la base (el -i es necesario para pasarle SQL por stdin)
docker exec -i cotiza_postgres psql -U cotiza_admin -d cotiza -c "SELECT correo, rol FROM usuarios;"

# pgAdmin en http://localhost:5050 (admin@cotiza.local / admin)
docker compose --profile tools up pgadmin

# Re-aplicar migraciones SQL: solo corren al crear el volumen, hay que borrarlo
docker compose down -v && docker compose up --build
```

No hay suite de tests todavía. `api-go/requests.http` (extensión REST Client de
VSCode) sirve como smoke test manual de los endpoints, con los casos de éxito
y de error de `/api/auth/login` ya escritos.

**Credenciales para probar sin los Excel.** `0002_seed_demo_data.sql` siembra
usuarios de demo (`demo.admin@exceltecgroup.com` / PIN `1234`, entre otros —
ver el encabezado del archivo). Los `.xlsx` reales no se versionan, así que en
una máquina limpia el seed es la única forma de loguearse; si `migration-python/data/`
está vacío, el contenedor `migration` falla con `FileNotFoundError` — es lo
esperado, no un bug.

## Arquitectura

**Un solo binario sirve API y frontend.** `api-go/cmd/server/main.go` monta chi
con las rutas bajo `/api` y un `http.FileServer` en `/*` apuntando a
`cfg.StaticDir` (`/app/static` en Docker, montado desde `./frontend`). No hay
nginx; el frontend y el API comparten origen, por eso los `fetch()` del frontend
usan rutas relativas (`/api/...`).

**Registro de rutas por carril.** `main.go` tiene el grupo `/api` con dos
secciones comentadas: Carril A (Configuración: catálogos, diseñador, reglas,
compilador, plantillas) y Carril B (Operación: auth, cotizaciones, dashboard,
reportes). Cada módulo nuevo se agrega como su propio `r.Route(...)`/`r.Mount(...)`
en el carril que le toca, para que dos personas trabajando en paralelo no
colisionen en el mismo bloque.

**Config centralizada.** Todo `os.Getenv` vive en `internal/config`. No leer
variables de entorno directamente desde handlers.

**Frontend: `index.html` es generado, no fuente.** El original es un SPA de
Apps Script (`<?!= include('Cotiza_Sidebar') ?>`). `frontend/_build/assemble.py`
resuelve esos includes desde `frontend/legacy-gas/`, inyecta
`_build/cotiza_base.css` (las 5 hojas de estilo originales nunca llegaron) y
`_build/shim.html`, y escribe `frontend/index.html` (~680 KB). El archivo
generado **sí se versiona**, así que cualquier cambio de una línea en la fuente
produce un diff enorme en `index.html` — es normal. Editar `legacy-gas/` o
`_build/` y volver a correr el script; nunca editar `index.html` a mano.

**El shim es el backend temporal, y se va vaciando.** `_build/shim.html` simula
`google.script.run` para las funciones RPC que todavía no tienen endpoint (24
de las 25 originales). Migrar una función es: escribir el endpoint en Go,
cambiar esa llamada por un `fetch()` en `legacy-gas/`, borrar su entrada de
`RPC_METHODS` y regenerar. Borrarla del shim es parte del trabajo, no un
detalle: así una llamada olvidada revienta de una vez en vez de recibir datos
falsos. `frontend/NOTAS_MIGRACION.md` lleva la cuenta y tiene el antes/después
completo de la única migrada (`login()` → `POST /api/auth/login`). No agregar
lógica de negocio nueva al shim.

**`auth.go` es la plantilla para los endpoints nuevos.** Convenciones que
conviene repetir: handler como struct con `DB *pgxpool.Pool` construido en
`main.go`; respuesta con envoltorio `{"ok":bool, ...}` / `{"ok":false,"error":"..."}`
porque es la forma que el frontend heredado ya sabe leer (`if(!res || !res.ok)`);
el helper compartido `escribirJSON` (definido en `auth.go`, disponible para todo
el paquete `handlers`).

**Migración de datos.** `migration-python/migrate_sheets_to_postgres.py` lee
`BD_Cotizador_Exceltec.xlsx` (organizaciones, usuarios, calculadoras, clientes) y
`BD_Cotizador_Parametros.xlsx` (catálogos, valores). Cada tabla es una función
`migrar_*` con `INSERT ... ON CONFLICT DO NOTHING`, así que el script es
idempotente. Los `.xlsx`/`.xlsm` no se versionan; la versión `.xlsm` es un
esquema viejo, no usarla.

## Convenciones que importan

**Migraciones SQL.** `api-go/migrations/*.sql` se monta en
`/docker-entrypoint-initdb.d`, o sea que **solo se ejecuta al crear el volumen de
Postgres**. Nunca editar una migración ya aplicada: agregar `0003_...sql`,
`0004_...sql` con `ALTER TABLE`/tablas nuevas. Ojo con `0002_seed_demo_data.sql`:
editarlo no cambia nada en una base que ya existe, hay que recrear el volumen.

**Esquema.** IDs de negocio son `TEXT` (vienen de las hojas: `CTZ-CAT-001`,
etc.); las tablas puente usan `UUID DEFAULT gen_random_uuid()`. Toda tabla con
`fecha_actualizacion` lleva un trigger `trg_<tabla>_fecha` que llama a
`set_fecha_actualizacion()`. Los catálogos son padre-hijo por dos vías: a nivel
de valor (`catalogo_valores.valor_padre_id`) y a nivel de catálogo completo
(`catalogo_relaciones`).

**PINs.** La hoja original guarda `pin_acceso` en texto plano. La base guarda
solo `usuarios.pin_hash`. Hoy hay tres piezas involucradas y conviene tenerlas
claras porque no usan el mismo costo de bcrypt:

- Python (`hash_pin`) genera `$2b$` con `bcrypt.gensalt()` → costo **12**.
- `0002_seed_demo_data.sql` genera con `crypt(pin, gen_salt('bf'))` de pgcrypto
  → costo **6**. Go verifica ambos sin problema.
- Go **solo verifica**, nunca hashea (todavía no hay alta de usuarios por API).

`auth.go` compara contra un hash señuelo de costo 12 cuando el correo no existe,
para que el tiempo de respuesta no delate si el usuario está en la base. Si
cambia el costo del lado de Python, hay que regenerar ese señuelo o la defensa
deja de servir.

**Respuestas de login.** Correo inexistente, PIN incorrecto y usuario inactivo
devuelven el **mismo** 401 con el mismo texto (`mensajeCredencialesInvalidas`).
Por eso `estado` se filtra en Go y no en el `WHERE`: los tres casos recorren el
mismo camino y cuestan lo mismo. No agregar mensajes que distingan un caso de
otro, ni loguear el PIN. Igual criterio para credenciales de CRM:
`crm_conexiones.credencial_referencia` guarda una referencia a un secreto
externo, nunca la credencial.

**Versión de Go.** `go.mod` declara `go 1.22` porque el Dockerfile compila con
`golang:1.22-alpine`, y por eso `golang.org/x/crypto` está fijo en v0.31.0 (la
última compatible). `go get` sube la directiva `go` a la del toolchain local sin
avisar y rompe el build de Docker — después de cualquier `go get`/`go mod tidy`,
verificar con `go mod edit -go=1.22` y `docker compose build api`. Para usar
dependencias más nuevas hay que subir primero la imagen del builder.

**Quirk conocido del frontend.** El botón de login dispara dos veces: tiene
`onclick="CotizaApp.login()"` en `cotiza_login.html` **y**
`bind("btnLogin","click",login)` en `cotiza_scripts.html`. Con el shim daba
igual; contra el API real son dos POST por click. Vale revisar si el mismo par
duplicado aparece en otros botones al migrar las funciones que faltan.

## Alcance por sprint

Mantener esquema, endpoints y frontend alineados al sprint en curso; no diseñar
tablas ni endpoints que nadie usa todavía.

- **Sprint 0 (hecho):** esqueleto Docker, `/api/health`, esquema 0001
  (organizaciones, roles, usuarios, calculadoras, CRM/clientes, catálogos),
  frontend ensamblado navegable con el shim.
- **Sprint 1 (hecho):**
  - `POST /api/auth/login` real (`internal/handlers/auth.go`) + `login()`
    migrada a `fetch()` + `webValidarUsuario` fuera del shim.
  - Catálogos, lectura y escritura (`internal/handlers/catalogos.go`):
    `GET /api/catalogos/designer` (3 modos) + `POST /api/catalogos`,
    `POST /api/catalogos/valores`, `POST /api/catalogos/relaciones`
    (esta última transaccional). Frontend conectado con `fetch()` en
    `cotiza_scripts.html`.
  - Usuarios y Roles, construido de cero (`internal/handlers/usuarios.go`):
    `GET /api/usuarios`, `GET /api/roles`, `POST /api/usuarios`,
    `PATCH /api/usuarios/{id}`. PIN hasheado con bcrypt costo 12 (igual
    que `migration-python`). Pantalla nueva en `cotiza_usuarios.html` +
    modal en `cotiza_modals.html` (no existía nada antes).
  - Pendiente explícito, NO resuelto en Sprint 1: no hay sesión/token
    persistente ni verificación de autorización en el servidor — todos
    los endpoints son accesibles sin login real todavía. Hay que
    planearlo antes de exponer esto fuera de desarrollo local.
- **Después:** diseñador/reglas/plantillas/cotizaciones (esquema 0003+),
  sesión/autenticación por token, y las funciones RPC restantes. Los 6
  scripts JS que nunca llegaron (Reglas, Mantenimiento de Reglas,
  Compilador, Cotizador runtime, Solicitudes, Plantillas) siguen
  documentados como `<!-- FALTA: ... -->` en el ensamblado.

`docs/ENTORNO_LOCAL_FEDORA.md` cubre instalación en Fedora, el flujo de debug con
breakpoints y los problemas comunes (puertos ocupados, migraciones que no se
aplican, permisos de Docker).
