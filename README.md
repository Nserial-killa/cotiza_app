# Cotiza — Migración a Go + Python + PostgreSQL (Docker)

Reescritura del sistema Cotiza de Exceltec (hoy Google Apps Script +
Google Sheets) a un stack propio: API en Go, PostgreSQL como base de
datos, y Python para migración de datos y reportes. Todo entregado en
Docker.

## Estructura

```
cotiza/
├── docker-compose.yml
├── .env.example
├── api-go/              # API REST en Go (lógica de negocio)
│   ├── cmd/server/       # punto de entrada (main.go)
│   ├── internal/         # config, db, handlers, models, middleware
│   └── migrations/       # esquema SQL, numerado (0001, 0002, ...)
├── migration-python/     # script de migración Sheets -> Postgres
├── frontend/             # HTML/CSS/JS existente, adaptado
└── docs/                 # metodología Scrum, registros de horas
```

## Frontend: cómo se generó `frontend/index.html`

`frontend/index.html` **no se edita a mano** — se genera con:

```bash
python3 frontend/_build/assemble.py
```

Este script toma `frontend/legacy-gas/Cotiza_App.html` y todos sus
includes, resuelve la sintaxis de Google Apps Script (`<?!= include() ?>`),
inserta un CSS base propio (`frontend/_build/cotiza_base.css`) y un
shim de `google.script.run` (`frontend/_build/shim.html`) que simula
el backend para poder navegar el shell sin el API real todavía.

Si se edita cualquier archivo dentro de `frontend/legacy-gas/`, o el
CSS/shim de `_build/`, hay que volver a correr `assemble.py` para que
los cambios se reflejen en `index.html`. Ver `frontend/NOTAS_MIGRACION.md`
para el detalle de qué falta reemplazar en el Sprint 1 (los 25
`google.script.run` simulados → `fetch()` reales al API en Go).

## Cómo levantar el proyecto

> ¿Primera vez, sin Docker ni Go instalados? Ver
> `docs/ENTORNO_LOCAL_FEDORA.md` — instalación paso a paso en Fedora
> y cómo depurar el API en VSCode con breakpoints. Para macOS, ver
> `docs/Guia_Tecnica_Comandos_Cotiza.docx`.

1. Copiar `.env.example` a `.env` y ajustar valores si hace falta.
2. Construir y levantar los servicios base (Postgres + API):

   ```bash
   docker compose up --build
   ```

   La primera vez que se crea el volumen de Postgres, se ejecutan
   automáticamente los archivos de `api-go/migrations/` en orden.

3. Verificar que todo esté sano:

   ```bash
   curl http://localhost:8080/api/health
   # {"status":"ok","database":"ok"}
   ```

4. (Opcional) Migrar los datos actuales de los Excel/Sheets:

   ```bash
   # colocar BD_Cotizador_Exceltec.xlsx y BD_Cotizador_Parametros.xlsx
   # dentro de migration-python/data/ (no se versionan, ver .gitignore)
   docker compose --profile tools run --rm migration
   ```

5. (Opcional) pgAdmin para inspeccionar la base visualmente:

   ```bash
   docker compose --profile tools up pgadmin
   # http://localhost:5050
   ```

## Nota sobre `go.sum`

Este esqueleto se generó sin acceso al proxy de módulos de Go, así
que falta el archivo `go.sum`. Antes del primer build, correr una
vez en la máquina local (con internet):

```bash
cd api-go
go mod tidy
```

Esto genera `go.sum` con los hashes de `pgx`, `chi` y `cors`. Después
de eso, `docker compose up --build` funciona normal.

## Convenciones de migraciones SQL

Cada sprint que agrega tablas nuevas crea un archivo nuevo en
`api-go/migrations/`, numerado secuencialmente (`0002_...sql`,
`0003_...sql`). No se edita `0001` una vez que alguien ya corrió la
base con esos datos — se agregan `ALTER TABLE` o tablas nuevas en el
siguiente número.

## Documentos del proyecto

Ver `/docs`: metodología Scrum del equipo y registros semanales de
horas (Jimmy / Daniel), periodo 20-ago-2026 a 30-sep-2026.
