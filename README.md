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

## Datos de demostración

`docker compose up --build` levanta, además de `postgres` y `api`, un
servicio `seed` que corre una sola vez: espera a que el API esté
saludable y llama a `scripts/seed_demo.sh`, que arma un caso de uso
real llamando al API REST igual que lo haría una persona desde el
navegador (login, catálogo, cotizador, validar/compilar, clientes,
cotizaciones, plantilla, integración + solicitud) — nunca insertando
JSON compilado a mano. Es idempotente: correr `docker compose up` otra
vez sobre el mismo volumen no duplica nada.

Al terminar deja, ya explorables en la pantalla:

- El cotizador **Consultoría de Implementación TI**, publicado, con su
  catálogo "Tipo de Servicio".
- 3 cotizaciones de ejemplo: una en Borrador, una Enviada al Cliente
  (con su link público) y una Aceptada.
- La plantilla de propuesta **Propuesta Estándar de Consultoría TI**,
  publicada.
- Una integración de API con una solicitud de ejemplo en estado Nueva.

El resumen completo (usuario para entrar, la URL del link público,
dónde quedó la clave de API) se imprime al final de
`docker compose logs seed` y también queda guardado en
`scripts/demo_resumen.txt` (no se versiona, cambia en cada corrida).
La clave de API en sí queda en `scripts/demo_api_key.txt`, que tampoco
se versiona — solo se puede leer del API una vez, al crearla.

## PDF de la oferta (Gotenberg)

El botón **Descargar PDF** del Gestor llama a
`GET /api/cotizaciones/{id}/enlace/pdf`. El API le pide al servicio
`gotenberg` (imagen fija `gotenberg/gotenberg:8.37.0`) que abra la página
pública de esa versión y la imprima, así que el PDF es idéntico a lo que ve
el cliente, firma incluida. Hace falta haber generado antes el enlace de esa
versión; si no, responde 404.

Variables nuevas del servicio `api` (ver `.env.example`):

| Variable | Default en Docker | Para qué sirve |
|---|---|---|
| `GOTENBERG_URL` | `http://gotenberg:3000` | Dónde está Gotenberg. Vacía = PDF desactivado. |
| `PDF_INTERNAL_BASE_URL` | `http://api:8080` | Cómo llega Gotenberg a **este** API desde la red interna de Docker. No es la URL pública. |

Seguridad, por si alguien toca el compose:

- `gotenberg` **no publica puertos** al host: solo lo alcanza el `api`
  por la red `cotiza_net`.
- `CHROMIUM_DENY_PRIVATE_IPS=true` + `CHROMIUM_ALLOW_LIST=^http://api:8080(/|$)`
  (en `docker-compose.yml` va escrito `$$` porque Compose interpola `$`):
  Chromium solo puede abrir el API. Por eso, hoy, un logo con URL
  `https://` externa **no** aparece en el PDF (la página muestra el nombre
  de la organización en su lugar). Para habilitarlo se agrega a la lista
  el host exacto de los logos, nunca un `^https://` genérico.
- La URL que se convierte la arma el servidor (nunca viene del navegador)
  y lleva un pase de un solo uso: renderizar el PDF no suma visitas ni pasa
  la cotización a "Vista por el Cliente".

La firma usa la fuente local `frontend/fonts/DancingScript.woff2`
(licencia SIL OFL 1.1 en `frontend/fonts/OFL-DancingScript.txt`), servida
por el mismo API para que navegador y PDF se vean igual.

**Desarrollo nativo (`go run ./cmd/server`): el PDF responde 503.** Sin las
dos variables de arriba el endpoint contesta
`{"ok":false,"error":"El servicio de PDF no está configurado en este servidor."}`
(503). Aunque las configures, un Gotenberg en Docker no puede llegar a
`localhost:8080` del host. Para probar el PDF, usar el stack completo
(`docker compose up --build`).

### Limpiar contenedores de prueba

Para probar el PDF sin tocar el volumen real se pueden levantar
contenedores aparte (Postgres desechable, API y Gotenberg en su propia red).
Si quedaron corriendo, se quitan así (los que no existan dan un aviso
inofensivo):

```bash
docker rm -f cotiza_api_pdfprueba cotiza_gotenberg_pdfprueba cotiza_gotenberg_libre cotiza_pg_pruebas
docker network rm cotiza_pdf_net
docker image rm cotiza-api-pdfprueba cotiza-seed-pdfprueba   # opcional: imágenes locales de prueba
```

Ninguno de esos comandos toca `cotiza_postgres` ni el volumen
`cotiza_pgdata`. Lo que sí borra los datos reales es
`docker compose down -v`.

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
