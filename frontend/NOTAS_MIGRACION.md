# Migración del frontend (GAS → estático servido por Go)

## ✅ Ya hecho (fin de Sprint 0)

`frontend/index.html` **ya es el ensamblado real** de `Cotiza_App.html` +
todos sus includes (generado con `assemble.py`, no a mano). Esto incluye:

- Los 5 `<style>` y 6 `<script>` includes que faltaban en la entrega
  original quedaron documentados como comentarios HTML (`<!-- FALTA: X -->`)
  en vez de romper la página.
- Un CSS propio (`cotiza_base.css` durante el ensamblado) reconstruyendo
  el diseño real a partir de las clases confirmadas en el HTML/JS
  original — no es pixel-perfect todavía, pero es fiel a la estructura.
- Un shim de `google.script.run` (25 funciones RPC reales detectadas)
  que simula el backend: login entra con cualquier correo/PIN y
  navega el shell completo, pero **todos los datos son de ejemplo**.
- Login por defecto al abrir `/` (ya era el comportamiento nativo del
  HTML original — `loginView` sin `hidden`, `appView` con `hidden` —
  no hizo falta forzar nada).

Probado con clicks reales simulados: login → dashboard → navegación
entre las 9 secciones del sidebar, incluida la vista más compleja
(Diseñador de Catálogos). Sin errores de JavaScript.

## ✅ Sprint 1 — `login()` migrada (1 de 25)

`webValidarUsuario` ya **no** pasa por el shim: `login()` hace
`POST /api/auth/login` contra el API en Go (`api-go/internal/handlers/auth.go`,
valida el PIN con bcrypt contra `usuarios.pin_hash`). La entrada
correspondiente se borró de `RPC_METHODS` en `_build/shim.html`, así
que si algún código volviera a llamar `google.script.run.webValidarUsuario`
fallaría de una vez en vez de recibir datos falsos.

El cambio se hizo en `legacy-gas/cotiza_scripts.html` (la fuente) y
`index.html` se regeneró con `assemble.py` — editar `index.html` a mano
no sirve, el siguiente ensamblado lo sobrescribe.

Quedan 24 funciones simuladas.

## 🔜 Lo que sigue

1. **Reemplazar el shim por el API real en Go.** Cada una de las 24
   funciones que quedan en el shim se cambia por un `fetch('/api/...')`
   real. Ver el ejemplo completo (antes/después de `login()`) más abajo.
2. **Pulir el CSS** contra las capturas de referencia reales (colores
   exactos, el sparkline de las tarjetas del dashboard se sale un poco
   del contenedor — ajuste menor de `overflow`).
3. **Conseguir o reconstruir los 6 scripts faltantes** si hacen falta
   (Reglas, Mantenimiento de Reglas, Compilador, Cotizador runtime,
   Solicitudes, Plantillas) — varias vistas ya muestran mensajes
   sensatos de "sin datos" sin ellos, pero su lógica de negocio real
   no está.

## 1. Quitar la sintaxis de Apps Script

`Cotiza_App.html` usa `<?!= include('Cotiza_Sidebar'); ?>` para
ensamblar el SPA. Eso es exclusivo de GAS HTMLService y no existe en
un servidor Go. Ya resuelto por `assemble.py` — no hace falta tocarlo
a mano de nuevo salvo que se agregue un archivo nuevo al shell.

## 2. Reemplazar las llamadas a `google.script.run`

Ejemplo real y completo, tomado de `cotiza_scripts.html` (función
`login()`, la primera que hay que migrar en el Sprint 1):

**ANTES (Google Apps Script):**

```js
function login(){
  const correo = byId("loginCorreo").value.trim();
  const pin = byId("loginPin").value.trim();

  if(!correo || !pin){
    setLoginMessage("Debe indicar correo y PIN.", "error");
    return;
  }

  setLoading("loginView", true);

  google.script.run
    .withSuccessHandler(function(res){
      if(!res || !res.ok){
        setLoginMessage(res.error || "No fue posible validar el usuario.", "error");
        setLoading("loginView", false);
        return;
      }
      localStorage.setItem("cotiza_usuario", JSON.stringify(res.usuario));
      iniciarApp(res.usuario);
    })
    .withFailureHandler(function(error){
      setLoading("loginView", false);
      setLoginMessage(error.message || "Error ejecutando Apps Script.", "error");
    })
    .webValidarUsuario(correo, pin);
}
```

**DESPUÉS (fetch contra el API en Go):**

```js
async function login(){
  const correo = byId("loginCorreo").value.trim();
  const pin = byId("loginPin").value.trim();

  if(!correo || !pin){
    setLoginMessage("Debe indicar correo y PIN.", "error");
    return;
  }

  setLoading("loginView", true);

  try {
    const resp = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ correo, pin }),
    });
    const res = await resp.json();

    if(!resp.ok || !res.ok){
      setLoginMessage(res.error || "No fue posible validar el usuario.", "error");
      setLoading("loginView", false);
      return;
    }
    localStorage.setItem("cotiza_usuario", JSON.stringify(res.usuario));
    iniciarApp(res.usuario);
  } catch(error) {
    setLoading("loginView", false);
    setLoginMessage(error.message || "Error de conexión con el servidor.", "error");
  }
}
```

(El bloque de arriba es el esquema del cambio; la versión que quedó
en `legacy-gas/cotiza_scripts.html` es un poco más larga porque
conserva dos cosas del original que este resumen omite: el
`setLoginMessage("Validando acceso...")` y la validación de
`usuario.puede_ver_gestor` antes de entrar al gestor.)

Nótese lo que **no cambia**: los ids del HTML (`loginCorreo`,
`loginPin`, `loginView`), las funciones de apoyo (`byId`,
`setLoginMessage`, `setLoading`, `iniciarApp`) y la lógica de negocio.
Solo cambia el mecanismo de transporte: `google.script.run.withX()`
se vuelve un `fetch()` normal. Ese patrón se repite, uno por uno,
para las 25 funciones listadas en el shim de `frontend/index.html`.

Lista de funciones detectadas (referencia, se va tachando a medida
que se migre cada una): ~~webValidarUsuario~~ (migrada en el Sprint 1
→ `POST /api/auth/login`), webListarUsuariosActivos,
webListarCalculadorasPorUsuario, webObtenerDashboardPorUsuario,
webBuscarCotizacionesV2, webObtenerGestionCotizacionV2,
webCrearNuevaVersion, webEjecutarAccionCotizacion,
webActualizarEstadoCotizacion, webGenerarLinkCliente,
webCompilarCotizador, webValidarCotizadorParaCompilar,
webListarCatalogosDesigner, webCotizadorListarTabs,
webCotizadorGuardarTab, webListarModuloParametros,
webGuardarModuloParametro, webCambiarEstadoModuloParametro,
webEliminarModuloParametro, webListarServiciosProductosParametros,
webGuardarServicioProductoParametro,
webCambiarEstadoServicioProductoParametro,
webListarFormasPagoParametros, webGuardarFormaPagoParametro,
webCambiarEstadoFormaPagoParametro.

## 3. Archivos que faltan

`Cotiza_App.html` original también incluye estos archivos que no
llegaron en la primera entrega — pedirlos si existen, si no, se
reconstruyen desde los requerimientos:

- Cotiza_Styles, Cotiza_Style_Catalogos, Cotiza_Style_Cotizador,
  Cotiza_Style_Solicitudes, Cotiza_Style_Plantillas (CSS)
- Cotiza_Script_Solicitudes, Cotiza_Script_Plantillas,
  Cotiza_Script_Reglas, Cotiza_Script_Mantenimiento_Reglas,
  Cotiza_Script_Compilador_Cotizador, Cotiza_Script_Cotizador (JS —
  este último referenciado dentro del propio cotiza_scripts.html)

Sin las hojas de estilo, el HTML se ve con el CSS de reemplazo que ya
está en `frontend/index.html`, respetando las mismas clases usadas en
el HTML (`.dash-kpi-card`, `.btn-primary`, `.sidebar`, etc.) — así, si
el CSS original aparece después, encaja sin tocar el HTML de nuevo.


