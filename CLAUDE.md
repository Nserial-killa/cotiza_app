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
# Primera vez (go.sum no está versionado y falta en el esqueleto)
cd api-go && go mod tidy

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
cd api-go && go vet ./... && gofmt -l .

# Regenerar el frontend estático (obligatorio tras tocar legacy-gas/ o _build/)
python3 frontend/_build/assemble.py

# Migrar los Excel a Postgres (requiere los .xlsx en migration-python/data/)
docker compose --profile tools run --rm migration

# pgAdmin en http://localhost:5050 (admin@cotiza.local / admin)
docker compose --profile tools up pgadmin

# Re-aplicar migraciones SQL: solo corren al crear el volumen, hay que borrarlo
docker compose down -v && docker compose up --build
```

No hay suite de tests todavía. `api-go/requests.http` (extensión REST Client de
VSCode) sirve como smoke test manual de los endpoints.

## Arquitectura

**Un solo binario sirve API y frontend.** `api-go/cmd/server/main.go` monta chi
con las rutas bajo `/api` y un `http.FileServer` en `/*` apuntando a
`cfg.StaticDir` (`/app/static` en Docker, montado desde `./frontend`). No hay
nginx; el frontend y el API comparten origen, por eso los `fetch()` del frontend
usan rutas relativas (`/api/...`).

**Registro de rutas por carril.** `main.go` tiene el grupo `/api` con dos
secciones comentadas: Carril A (Configuración: catálogos, diseñador, reglas,
compilador, plantillas) y Carril B (Operación: auth, cotizaciones, dashboard,
reportes). Cada módulo nuevo se agrega como su propio `r.Mount("/x", ...)` en el
carril que le toca, para que dos personas trabajando en paralelo no colisionen en
el mismo bloque.

**Config centralizada.** Todo `os.Getenv` vive en `internal/config`. No leer
variables de entorno directamente desde handlers.

**Frontend: `index.html` es generado, no fuente.** El original es un SPA de
Apps Script (`<?!= include('Cotiza_Sidebar') ?>`). `frontend/_build/assemble.py`
resuelve esos includes desde `frontend/legacy-gas/`, inyecta
`_build/cotiza_base.css` (las 5 hojas de estilo originales nunca llegaron) y
`_build/shim.html`, y escribe `frontend/index.html` (~680 KB). Editar
`legacy-gas/` o `_build/` y volver a correr el script; nunca editar
`index.html` a mano salvo el caso puntual que un sprint autorice explícitamente.

**El shim es el backend temporal.** `_build/shim.html` simula
`google.script.run` con las 25 funciones RPC detectadas en el frontend original.
La migración consiste en, por cada función, escribir el endpoint en Go, cambiar
esa llamada por un `fetch()`, y borrar la entrada correspondiente de
`RPC_METHODS` en el shim. `frontend/NOTAS_MIGRACION.md` tiene el ejemplo
completo antes/después (`login()` → `POST /api/auth/login`) y la lista de las 25
funciones — ese patrón se repite tal cual. No agregar lógica de negocio nueva al
shim.

**Migración de datos.** `migration-python/migrate_sheets_to_postgres.py` lee
`BD_Cotizador_Exceltec.xlsx` (organizaciones, usuarios, calculadoras, clientes) y
`BD_Cotizador_Parametros.xlsx` (catálogos, valores). Cada tabla es una función
`migrar_*` con `INSERT ... ON CONFLICT DO NOTHING`, así que el script es
idempotente. Los `.xlsx`/`.xlsm` no se versionan; la versión `.xlsm` es un
esquema viejo, no usarla.

## Convenciones que importan

**Migraciones SQL.** `api-go/migrations/*.sql` se monta en
`/docker-entrypoint-initdb.d`, o sea que **solo se ejecuta al crear el volumen de
Postgres**. Nunca editar `0001_init_schema.sql` una vez que alguien corrió la
base: agregar `0002_...sql`, `0003_...sql` con `ALTER TABLE`/tablas nuevas.

**Esquema.** IDs de negocio son `TEXT` (vienen de las hojas: `CTZ-CAT-001`,
etc.); las tablas puente usan `UUID DEFAULT gen_random_uuid()`. Toda tabla con
`fecha_actualizacion` lleva un trigger `trg_<tabla>_fecha` que llama a
`set_fecha_actualizacion()`. Los catálogos son padre-hijo por dos vías: a nivel
de valor (`catalogo_valores.valor_padre_id`) y a nivel de catálogo completo
(`catalogo_relaciones`).

**PINs.** La hoja original guarda `pin_acceso` en texto plano. La base guarda
solo `usuarios.pin_hash` (bcrypt: `bcrypt` en Python,
`golang.org/x/crypto/bcrypt` en Go). No replicar el esquema viejo ni loguear el
PIN. Los fallos de login responden 401 con un mensaje genérico único — no
distinguir "correo no existe" de "PIN incorrecto". Igual criterio para
credenciales de CRM: `crm_conexiones.credencial_referencia` guarda una
referencia a un secreto externo, nunca la credencial.

## Alcance por sprint

Mantener esquema, endpoints y frontend alineados al sprint en curso; no diseñar
tablas ni endpoints que nadie usa todavía.

- **Sprint 0 (hecho):** esqueleto Docker, `/api/health`, esquema 0001
  (organizaciones, roles, usuarios, calculadoras, CRM/clientes, catálogos),
  frontend ensamblado navegable con el shim.
- **Sprint 1 (en curso):** ver `docs/PROMPT_CLAUDE_CODE_SPRINT1.md` — la tarea
  concreta y su lista de "no hacer". Resumen: `POST /api/auth/login` real +
  migrar solo la función `login()` del frontend.
- **Después:** diseñador/reglas/plantillas/cotizaciones (esquema 0002+) y las 24
  funciones RPC restantes.

`docs/ENTORNO_LOCAL_FEDORA.md` cubre instalación en Fedora, el flujo de debug con
breakpoints y los problemas comunes (puertos ocupados, migraciones que no se
aplican, permisos de Docker).
