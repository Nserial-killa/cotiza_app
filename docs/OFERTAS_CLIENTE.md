# Ofertas compartidas con clientes

## Flujo

En **Cotizaciones → Gestionar**, abra **Link del cliente**, indique el correo autorizado y genere el enlace de la versión seleccionada. El enlace abre la propuesta resuelta con su plantilla. El cliente puede comentar, solicitar cambios, rechazar o aceptar la oferta. Las respuestas aparecen en **Actividad**; los cambios y el rechazo actualizan el estado de esa versión.

Para aceptar, el firmante escribe su nombre y cargo, solicita un código enviado al correo autorizado, introduce ese código y confirma expresamente la firma electrónica. El código caduca en diez minutos y admite cinco intentos. La aceptación registra versión, nombre, correo, fecha, IP, agente de usuario y huella SHA-256 del documento; se muestra al pie de la propuesta. Al confirmar, se envía al correo autorizado una constancia con el enlace al documento firmado. Una versión cerrada no admite otra respuesta.

**Imprimir / Guardar PDF** usa el diálogo de impresión del navegador: elija “Guardar como PDF”. El enlace de correo lleva al documento firmado; no se adjunta un PDF al mensaje. Esta es una firma electrónica con verificación por correo y registro de evidencia, no una firma digital con certificado o un servicio de firma certificado.

## Configuración y pruebas locales

`docker compose up --build` inicia PostgreSQL, la API y Mailpit. Abra `http://localhost:8025` para inspeccionar los correos de prueba. En producción, configure `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM` y `PUBLIC_BASE_URL` con el dominio público HTTPS; no use Mailpit para correo real. La migración `0035_respuesta_cliente.sql` debe aplicarse a las bases existentes antes de desplegar la API.

**Importante:** Mailpit acepta el mensaje SMTP pero no lo reenvía a Gmail, Microsoft 365 ni otros buzones. El aviso de la propuesta identifica este modo. Para entrega real, cree un `.env` local (no versionado) con los datos del proveedor autorizado: `SMTP_HOST`, `SMTP_PORT=587` (STARTTLS), `SMTP_USER`, `SMTP_PASSWORD`, `SMTP_FROM` y `PUBLIC_BASE_URL=https://...`. El remitente debe estar autorizado por ese proveedor. Reinicie con `docker compose up -d --build api` y pruebe con un correo controlado antes de compartir enlaces. Nunca pegue la contraseña en el repositorio ni en un chat.

Para usar una cuenta Gmail como remitente, configure `SMTP_HOST=smtp.gmail.com`, `SMTP_PORT=587`, `SMTP_USER` y `SMTP_FROM` con la misma dirección autorizada. Active la verificación en dos pasos en Google y cree una **contraseña de aplicación** para `SMTP_PASSWORD`; la contraseña normal de Gmail no corresponde. El archivo `.env` local debe quedar fuera de Git y con permisos restrictivos. Sin esa credencial, Cotiza rechaza el pedido de código y no indica falsamente que lo envió. Cuando la cuenta esté configurada, reinicie la API y solicite un código desde un enlace cuyo correo destinatario sea un buzón que pueda revisar (Gmail u otro proveedor); confirme la recepción en ese buzón, no en Mailpit.

Prueba manual: genere un enlace con correo, ábralo en una ventana privada, envíe un comentario y compruebe **Actividad**. Solicite el código, léalo en Mailpit, acepte y verifique el estado, la firma al pie y el mensaje de confirmación. Guarde el documento como PDF desde la barra superior.
