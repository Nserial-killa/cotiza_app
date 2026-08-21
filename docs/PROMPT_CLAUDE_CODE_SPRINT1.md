CONTEXTO — Proyecto Cotiza, cierre de Sprint 0 / arranque Sprint 1

El frontend real de Cotiza ya está ensamblado y funcionando en
`frontend/index.html`. Se generó con `frontend/_build/assemble.py`,
que toma `frontend/legacy-gas/Cotiza_App.html` y todos sus includes,
resuelve la sintaxis de Google Apps Script, y agrega:

- Un CSS propio en `frontend/_build/cotiza_base.css` (las 5 hojas de
  estilo originales de Cotiza no llegaron en la entrega — este CSS
  reconstruye el diseño usando las clases reales del HTML original).
- Un shim de `google.script.run` en `frontend/_build/shim.html` que
  simula el backend (25 funciones RPC detectadas) para poder navegar
  todo el shell sin el API real. `webValidarUsuario` responde con un
  usuario de ejemplo; las demás 24 responden con una forma vacía
  "ok:true" genérica.

Ya probé que esto funciona de punta a punta: abrir `/` muestra login
por defecto, entrar con cualquier correo/PIN navega al dashboard, y
la navegación entre las 9 secciones del sidebar funciona sin errores
de JavaScript (incluida la vista más compleja, el Diseñador de
Catálogos, que muestra mensajes sensatos de "sin datos" en vez de
crashear).

TAREAS PARA ESTE SPRINT (Sprint 1):

1. Leer `frontend/NOTAS_MIGRACION.md` completo antes de tocar nada —
   ahí está el ejemplo exacto (antes/después) de cómo migrar la
   función `login()` de `google.script.run` a `fetch()`.

2. Implementar el primer endpoint real en Go: `POST /api/auth/login`
   en `api-go/internal/handlers/` (crear un archivo nuevo, ej. `auth.go`).
   - Recibe `{correo, pin}` en JSON.
   - Busca el usuario en la tabla `usuarios` (ver
     `api-go/migrations/0001_init_schema.sql`) por `correo`.
   - Compara el PIN contra `pin_hash` usando bcrypt (el hash ya se
     genera así en `migration-python/migrate_sheets_to_postgres.py`,
     usar la misma librería del lado Go: `golang.org/x/crypto/bcrypt`).
   - Responde `{ok:true, usuario:{...}}` en el mismo formato que el
     shim ya simula (revisar `frontend/_build/shim.html` para el
     shape exacto: usuario_id, nombre, correo, rol, puede_ver_gestor).
   - Si no existe el usuario o el PIN no coincide, responder
     `{ok:false, error:"..."}` con status 401 — NUNCA revelar si
     falló por correo inexistente o PIN incorrecto (mismo mensaje
     genérico para ambos casos, por seguridad).
   - Registrar la ruta en `api-go/cmd/server/main.go`, dentro del
     grupo `/api` ya existente.

3. En `frontend/index.html`, reemplazar ÚNICAMENTE la función
   `login()` (dentro del bloque `Cotiza_Scripts`) por la versión con
   `fetch()` que ya está documentada en `NOTAS_MIGRACION.md`. No tocar
   el resto del archivo a mano — si hace falta otro cambio de fondo,
   editar `frontend/legacy-gas/cotiza_scripts.html` o los archivos de
   `_build/` y volver a correr `python3 frontend/_build/assemble.py`.

4. Quitar la entrada `webValidarUsuario` de la lista `RPC_METHODS` en
   `frontend/_build/shim.html` una vez que el fetch real funcione
   (las otras 24 quedan simuladas hasta que se implementen sus
   endpoints correspondientes en sprints siguientes).

5. Probar de punta a punta:
   - `docker compose up --build`
   - Migrar al menos un usuario real: colocar los Excel en
     `migration-python/data/` y correr
     `docker compose --profile tools run --rm migration`
   - Abrir `http://localhost:8080`, loguearse con el correo/PIN real
     de ese usuario migrado, confirmar que entra al dashboard.
   - Probar también con credenciales incorrectas: debe rechazar con
     el mensaje genérico, sin distinguir "usuario no existe" de
     "PIN incorrecto".

NO HACER en esta tarea (queda para sprints siguientes):
- No implementar las otras 24 funciones RPC todavía.
- No tocar el CSS de forma extensa — solo si algo se ve realmente
  roto navegando. El pulido visual fino es tarea aparte.
- No reconstruir los 6 scripts faltantes (Reglas, Compilador,
  Cotizador runtime, Solicitudes, Plantillas, Mantenimiento de
  Reglas) — eso es alcance de sprints posteriores según
  Metodologia_Scrum_Cotiza.docx.

Al terminar, correr `go vet ./...` y `gofmt -l .` en `api-go/` antes
de dar la tarea por terminada.
