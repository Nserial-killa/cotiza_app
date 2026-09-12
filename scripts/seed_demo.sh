#!/usr/bin/env bash
# ============================================================
# Cotiza — datos de demostración reales.
#
# Este script NUNCA inserta datos de negocio por SQL directo: cada
# entidad (catálogo, cotizador, tabs/elementos, clientes, cotizaciones,
# plantilla, integración, solicitud) se crea llamando al API real con
# curl, en el mismo orden en que lo haría una persona desde el
# navegador — login, diseñar el catálogo y el cotizador, validar y
# compilar, dar de alta clientes y cotizaciones, llenar el motor de
# ejecución, cambiar de estado, armar y publicar una plantilla, y
# generar una integración + una solicitud de ejemplo por el endpoint
# externo. La ÚNICA lectura directa a Postgres (vía psql) es de
# solo lectura, para decidir si el cotizador ya se compiló en una
# corrida anterior — nunca para escribir.
#
# Idempotente: se puede correr tantas veces como se quiera.
#   - Catálogo, valores, cotizador, tabs y elementos: upsert por ID fijo
#     (los mismos endpoints que usa el Diseñador real).
#   - Clientes, cotizaciones y plantilla: se busca por nombre exacto
#     antes de crear; si ya existen, se reutilizan y no se repiten los
#     pasos que ya se hicieron (llenar valores, cambiar estado).
#   - Integración de API: la clave en texto plano solo se puede leer
#     una vez, así que se guarda en scripts/demo_api_key.txt; si ese
#     archivo ya no sirve (por ejemplo, se recreó el volumen de
#     Postgres con "docker compose down -v" pero el archivo local
#     sobrevivió), se revoca la integración vieja y se crea una nueva.
# ============================================================

set -eu

API="${API_BASE_URL:-http://api:8080}"
DATABASE_URL="${DATABASE_URL:-}"
PUBLIC_BASE_URL="${PUBLIC_BASE_URL:-http://localhost:8080}"

ADMIN_CORREO="demo.admin@exceltecgroup.com"
ADMIN_PIN="1234"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
API_KEY_FILE="$SCRIPT_DIR/demo_api_key.txt"
RESUMEN_FILE="$SCRIPT_DIR/demo_resumen.txt"

CATALOGO_ID="CTZ-CAT-DEMO-TIPOSERV"

CALC_ID="CTZ-CALC-DEMO-CONSULTORIA-TI"
CALC_NOMBRE="Consultoría de Implementación TI"
TAB1_ID="CTZ-TAB-DEMO-GENERAL"
TAB2_ID="CTZ-TAB-DEMO-ALCANCE"
EL_HORAS_ID="CTZ-ELE-DEMO-HORAS"

CLIENTE1_NOMBRE="Distribuidora Comercial del Pacífico S.A."
CLIENTE1_RAZON="Distribuidora Comercial del Pacífico Sociedad Anónima"
CLIENTE2_NOMBRE="Grupo Constructor Altamira S.A."
CLIENTE2_RAZON="Grupo Constructor Altamira Sociedad Anónima"
CLIENTE3_NOMBRE="Financiera Coopeservicios R.L."
CLIENTE3_RAZON="Financiera Coopeservicios Responsabilidad Limitada"

PLANTILLA_NOMBRE="Propuesta Estándar de Consultoría TI"

INTEGRACION_NOMBRE="Integración Demo Bitrix24"
IDEMPOTENCY_KEY="seed-demo-solicitud-consultora-regional"

TOKEN=""
PLANTILLA_ID=""
SOLICITUD_ID=""
COTIZACION1_ID=""
COTIZACION2_ID=""
COTIZACION3_ID=""
PUBLIC_LINK_URL=""

trap 'echo "[seed] Error inesperado en la línea $LINENO — revisar el log de arriba." >&2' ERR

# A stderr, nunca a stdout: varias funciones (ensure_cliente,
# ensure_cotizacion, etc.) devuelven su resultado por stdout vía
# "$(...)" — si log() escribiera ahí, ese texto se colaría en el
# valor devuelto (fue exactamente el bug que rompió la primera
# corrida real de este script).
log() { echo "[seed] $*" >&2; }

urlencode() { jq -rn --arg s "$1" '$s|@uri'; }

# --- espera a que la API esté saludable -------------------------------
# docker-compose ya usa "depends_on: api: condition: service_healthy"
# con el mismo /api/health; este loop es la segunda línea de defensa
# por si el servicio queda "healthy" un instante antes de aceptar
# conexiones de verdad.
esperar_api() {
  log "Esperando a que $API/api/health responda..."
  local intento
  for intento in $(seq 1 60); do
    if curl -fsS "$API/api/health" >/dev/null 2>&1; then
      log "API saludable."
      return 0
    fi
    sleep 2
  done
  echo "[seed] ERROR: $API/api/health nunca respondió saludable." >&2
  exit 1
}

# --- helper HTTP autenticado con la sesión (Authorization: Bearer) ----
api() {
  local metodo="$1" ruta="$2" body="${3:-}"
  local -a args=(-sS -X "$metodo" "$API$ruta" -H "Content-Type: application/json")
  if [ -n "$TOKEN" ]; then
    args+=(-H "Authorization: Bearer $TOKEN")
  fi
  if [ -n "$body" ]; then
    args+=(-d "$body")
  fi
  curl "${args[@]}"
}

# requerir_ok aborta el script (estamos en el proceso principal, no en
# una subshell de "$(...)") si la respuesta no trae ok:true.
requerir_ok() {
  local resp="$1" contexto="$2"
  if [ "$(echo "$resp" | jq -r '.ok // false')" != "true" ]; then
    echo "[seed] ERROR ($contexto): $resp" >&2
    exit 1
  fi
}

login() {
  log "Iniciando sesión como $ADMIN_CORREO..."
  local body resp
  body="$(jq -n --arg correo "$ADMIN_CORREO" --arg pin "$ADMIN_PIN" '{correo:$correo, pin:$pin}')"
  resp="$(curl -sS -X POST "$API/api/auth/login" -H "Content-Type: application/json" -d "$body")"
  requerir_ok "$resp" "iniciando sesión con $ADMIN_CORREO"
  TOKEN="$(echo "$resp" | jq -r '.token')"
  log "Sesión iniciada."
}

# ============================================================
# Catálogo "Tipo de Servicio"
# ============================================================
crear_valor_catalogo() {
  local valor_id="$1" clave="$2" texto="$3" orden="$4"
  local body resp
  body="$(jq -n --arg vid "$valor_id" --arg cid "$CATALOGO_ID" --arg clave "$clave" \
    --arg texto "$texto" --argjson orden "$orden" \
    '{valor_id:$vid, catalogo_id:$cid, clave:$clave, texto_visible:$texto,
      valor_sistema:$clave, descripcion:"", orden:$orden, activo:true}')"
  resp="$(api POST /api/catalogos/valores "$body")"
  requerir_ok "$resp" "guardando el valor de catálogo $valor_id"
}

ensure_catalogo() {
  log "Creando/actualizando el catálogo 'Tipo de Servicio'..."
  local body resp
  body="$(jq -n --arg id "$CATALOGO_ID" \
    '{catalogo_id:$id, nombre_catalogo:"Tipo de Servicio",
      alcance:"COTIZADOR", descripcion:"Clasificación del tipo de servicio contratado.",
      activo:true, catalogo_padre_id:"", orden:0}')"
  resp="$(api POST /api/catalogos "$body")"
  requerir_ok "$resp" "guardando el catálogo $CATALOGO_ID"

  crear_valor_catalogo "CTZ-VAL-DEMO-IMPLEMENTACION" "IMPLEMENTACION" "Implementación" 0
  crear_valor_catalogo "CTZ-VAL-DEMO-SOPORTE" "SOPORTE" "Soporte" 1
  crear_valor_catalogo "CTZ-VAL-DEMO-CONSULTORIA" "CONSULTORIA" "Consultoría" 2
  crear_valor_catalogo "CTZ-VAL-DEMO-CAPACITACION" "CAPACITACION" "Capacitación" 3
}

# ============================================================
# Cotizador "Consultoría de Implementación TI"
# ============================================================
crear_tab() {
  local tab_id="$1" nombre="$2" orden="$3"
  local body resp
  body="$(jq -n --arg tid "$tab_id" --arg cid "$CALC_ID" --arg nombre "$nombre" --argjson orden "$orden" \
    '{tab_id:$tid, calculadora_id:$cid, nombre:$nombre, descripcion:"",
      alcance:"PROPIO", orden:$orden, activo:true}')"
  resp="$(api POST /api/cotizador/tabs "$body")"
  requerir_ok "$resp" "guardando la sección $tab_id"
}

crear_elemento() {
  local elemento_id="$1" tab_id="$2" tipo="$3" etiqueta="$4" catalogo_id="$5"
  local columnas="$6" orden="$7" requerido="$8"
  local body resp
  body="$(jq -n --arg eid "$elemento_id" --arg tid "$tab_id" --arg tipo "$tipo" \
    --arg etiqueta "$etiqueta" --arg catid "$catalogo_id" \
    --argjson columnas "$columnas" --argjson orden "$orden" --argjson requerido "$requerido" \
    '{elemento_id:$eid, tab_id:$tid, tipo:$tipo, etiqueta:$etiqueta, catalogo_id:$catid,
      columnas_ancho:$columnas, orden:$orden, requerido:$requerido, configuracion:{}, activo:true}')"
  resp="$(api POST /api/cotizador/elementos "$body")"
  requerir_ok "$resp" "guardando el elemento $elemento_id"
}

calculadora_estado() {
  local id="$1"
  psql "$DATABASE_URL" -tAc "SELECT estado FROM calculadoras WHERE calculadora_id = '$id'" 2>/dev/null | tr -d '[:space:]'
}

compilar_cotizador_si_hace_falta() {
  local estado_actual
  estado_actual="$(calculadora_estado "$CALC_ID")"
  if [ "$estado_actual" = "Publicado" ]; then
    log "El cotizador ya está Publicado (corrida anterior) — no se vuelve a compilar."
    return 0
  fi

  log "Validando el cotizador..."
  local body resp
  body="$(jq -n --arg id "$CALC_ID" '{calculadora_id:$id}')"
  resp="$(api POST /api/cotizador/validar "$body")"
  requerir_ok "$resp" "validando el cotizador $CALC_ID"
  if [ "$(echo "$resp" | jq -r '.valido')" != "true" ]; then
    echo "[seed] ERROR: el cotizador de demostración no pasó la validación: $resp" >&2
    exit 1
  fi

  log "Compilando y publicando el cotizador..."
  resp="$(api POST /api/cotizador/compilar "$body")"
  requerir_ok "$resp" "compilando el cotizador $CALC_ID"
  if [ "$(echo "$resp" | jq -r '.compilado')" != "true" ]; then
    echo "[seed] ERROR: no se pudo compilar el cotizador de demostración: $resp" >&2
    exit 1
  fi
  log "Cotizador publicado (versión $(echo "$resp" | jq -r '.version_configuracion'))."
}

ensure_cotizador() {
  log "Creando/actualizando el cotizador '$CALC_NOMBRE'..."
  local body resp
  body="$(jq -n --arg id "$CALC_ID" --arg nombre "$CALC_NOMBRE" \
    '{calculadora_id:$id, nombre_calculadora:$nombre, linea_negocio:"Servicios Profesionales",
      servicio_base:"Consultoría e Implementación",
      descripcion:"Cotizador para propuestas de consultoría e implementación de sistemas TI para clientes corporativos."}')"
  resp="$(api POST /api/calculadoras "$body")"
  requerir_ok "$resp" "guardando el cotizador $CALC_ID"

  crear_tab "$TAB1_ID" "Información General" 0
  crear_tab "$TAB2_ID" "Alcance y Condiciones" 1

  # 5 elementos en 2 secciones, cubriendo los 4 tipos simples.
  crear_elemento "CTZ-ELE-DEMO-TIPOSERV" "$TAB1_ID" "CAMPO_CATALOGO" "Tipo de Servicio" "$CATALOGO_ID" 2 0 true
  crear_elemento "$EL_HORAS_ID" "$TAB1_ID" "CAMPO" "Horas Estimadas de Consultoría" "" 2 1 true
  crear_elemento "CTZ-ELE-DEMO-LEYENDA" "$TAB1_ID" "LEYENDA" "Complete la información general del proyecto antes de continuar." "" 4 2 false
  crear_elemento "CTZ-ELE-DEMO-ALCANCE" "$TAB2_ID" "CAMPO" "Alcance del Proyecto" "" 4 0 false
  crear_elemento "CTZ-ELE-DEMO-INFO" "$TAB2_ID" "TEXTO_INFORMATIVO" "Los precios no incluyen impuestos de ley." "" 4 1 false

  compilar_cotizador_si_hace_falta
}

# ============================================================
# Clientes
# ============================================================
buscar_cliente_por_nombre() {
  local nombre="$1" resp
  resp="$(api GET "/api/clientes/gestion?busqueda=$(urlencode "$nombre")")"
  requerir_ok "$resp" "buscando el cliente $nombre"
  echo "$resp" | jq -r --arg nombre "$nombre" '.clientes[] | select(.nombre_comercial == $nombre) | .cliente_id' | head -n1
}

ensure_cliente() {
  local nombre="$1" razon="$2"
  local id
  id="$(buscar_cliente_por_nombre "$nombre")"
  if [ -n "$id" ]; then
    log "Cliente '$nombre' ya existe ($id)."
    printf '%s' "$id"
    return 0
  fi
  log "Creando cliente '$nombre'..."
  local body resp
  body="$(jq -n --arg n "$nombre" --arg r "$razon" '{nombre_comercial:$n, razon_social:$r}')"
  resp="$(api POST /api/clientes "$body")"
  requerir_ok "$resp" "creando el cliente $nombre"
  echo "$resp" | jq -r '.cliente_id'
}

# ============================================================
# Cotizaciones
# ============================================================
buscar_cotizacion() {
  local cliente_nombre="$1" resp
  resp="$(api GET "/api/cotizaciones?calculadora_id=$(urlencode "$CALC_ID")&busqueda=$(urlencode "$cliente_nombre")")"
  requerir_ok "$resp" "buscando cotizaciones de $cliente_nombre"
  echo "$resp" | jq -c --arg nombre "$cliente_nombre" \
    '[.cotizaciones[] | select(.empresa == $nombre or .cliente == $nombre)] | first // empty'
}

# Imprime "cotizacion_id|estado|ya_existia" (ya_existia: 0 recién
# creada, 1 reutilizada de una corrida anterior).
ensure_cotizacion() {
  local cliente_id="$1" cliente_nombre="$2" tipo_propuesta="$3"
  local existente
  existente="$(buscar_cotizacion "$cliente_nombre")"
  if [ -n "$existente" ]; then
    local cid estado
    cid="$(echo "$existente" | jq -r '.cotizacion_id')"
    estado="$(echo "$existente" | jq -r '.estado')"
    log "Cotización de '$cliente_nombre' ya existe ($cid, $estado)."
    printf '%s|%s|1' "$cid" "$estado"
    return 0
  fi
  log "Creando cotización para '$cliente_nombre'..."
  local body resp cid
  body="$(jq -n --arg cid "$cliente_id" --arg calc "$CALC_ID" --arg tipo "$tipo_propuesta" \
    '{cliente_id:$cid, calculadora_id:$calc, tipo_propuesta:$tipo}')"
  resp="$(api POST /api/cotizaciones "$body")"
  requerir_ok "$resp" "creando la cotización de $cliente_nombre"
  cid="$(echo "$resp" | jq -r '.cotizacion_id')"
  printf '%s|Borrador|0' "$cid"
}

llenar_valores() {
  local cid="$1" tiposvc="$2" horas="$3" alcance="$4"
  local body resp
  body="$(jq -n --arg tiposvc "$tiposvc" --argjson horas "$horas" --arg alcance "$alcance" \
    '{version:1, valores:{"CTZ-ELE-DEMO-TIPOSERV":$tiposvc, "CTZ-ELE-DEMO-HORAS":$horas, "CTZ-ELE-DEMO-ALCANCE":$alcance}}')"
  resp="$(api POST "/api/cotizador/runtime/$cid/valores" "$body")"
  requerir_ok "$resp" "guardando los valores del cotizador para $cid"
}

cambiar_estado_cotizacion() {
  local cid="$1" estado="$2" comentario="$3"
  local body resp
  body="$(jq -n --arg estado "$estado" --arg comentario "$comentario" \
    '{version:1, estado:$estado, comentario:$comentario}')"
  resp="$(api POST "/api/cotizaciones/$cid/estado" "$body")"
  requerir_ok "$resp" "cambiando el estado de $cid a $estado"
}

generar_enlace_publico() {
  local cid="$1" resp url
  resp="$(api POST "/api/cotizaciones/$cid/enlace" '{"version":1}')"
  requerir_ok "$resp" "generando el enlace público de $cid"
  url="$(echo "$resp" | jq -r '.url')"
  printf '%s%s' "$PUBLIC_BASE_URL" "$url"
}

ensure_cotizaciones() {
  local cliente1_id cliente2_id cliente3_id
  cliente1_id="$(ensure_cliente "$CLIENTE1_NOMBRE" "$CLIENTE1_RAZON")"
  cliente2_id="$(ensure_cliente "$CLIENTE2_NOMBRE" "$CLIENTE2_RAZON")"
  cliente3_id="$(ensure_cliente "$CLIENTE3_NOMBRE" "$CLIENTE3_RAZON")"

  local r1 r2 r3
  r1="$(ensure_cotizacion "$cliente1_id" "$CLIENTE1_NOMBRE" "Comercial")"
  r2="$(ensure_cotizacion "$cliente2_id" "$CLIENTE2_NOMBRE" "Comercial")"
  r3="$(ensure_cotizacion "$cliente3_id" "$CLIENTE3_NOMBRE" "Comercial")"

  local estado2 existia2 estado3 existia3
  COTIZACION1_ID="$(echo "$r1" | cut -d'|' -f1)"
  COTIZACION2_ID="$(echo "$r2" | cut -d'|' -f1)"
  estado2="$(echo "$r2" | cut -d'|' -f2)"
  existia2="$(echo "$r2" | cut -d'|' -f3)"
  COTIZACION3_ID="$(echo "$r3" | cut -d'|' -f1)"
  estado3="$(echo "$r3" | cut -d'|' -f2)"
  existia3="$(echo "$r3" | cut -d'|' -f3)"

  # Cliente 1 se queda en Borrador tal cual nace — nada más que hacer.

  if [ "$existia2" = "0" ]; then
    log "Llenando el cotizador y enviando la cotización de '$CLIENTE2_NOMBRE' al cliente..."
    llenar_valores "$COTIZACION2_ID" "CONSULTORIA" 220 \
      "Implementación de ERP corporativo con migración de datos y capacitación de usuarios clave."
    cambiar_estado_cotizacion "$COTIZACION2_ID" "Enviada al Cliente" "Cotización enviada al cliente para su revisión."
  fi
  # Idempotente de por sí: reutiliza el enlace ya emitido si lo hubiera.
  PUBLIC_LINK_URL="$(generar_enlace_publico "$COTIZACION2_ID")"

  if [ "$existia3" = "0" ]; then
    log "Llenando el cotizador y marcando como Aceptada la cotización de '$CLIENTE3_NOMBRE'..."
    llenar_valores "$COTIZACION3_ID" "IMPLEMENTACION" 340 \
      "Implementación completa de plataforma de gestión documental y flujos de aprobación."
    cambiar_estado_cotizacion "$COTIZACION3_ID" "Aceptada" "Cliente aceptó la propuesta."
  fi
}

# ============================================================
# Plantilla de propuesta
# ============================================================
buscar_plantilla_por_nombre() {
  local resp
  resp="$(api GET "/api/plantillas?busqueda=$(urlencode "$PLANTILLA_NOMBRE")")"
  requerir_ok "$resp" "buscando la plantilla $PLANTILLA_NOMBRE"
  echo "$resp" | jq -r --arg nombre "$PLANTILLA_NOMBRE" '.plantillas[] | select(.nombre == $nombre) | .plantilla_id' | head -n1
}

crear_seccion_plantilla() {
  local plantilla_id="$1" nombre="$2" diseno="$3"
  local body resp
  body="$(jq -n --arg nombre "$nombre" --arg diseno "$diseno" \
    '{nombre:$nombre, titulo:$nombre, visibilidad:"SIEMPRE", diseno_bloques:$diseno}')"
  resp="$(api POST "/api/plantillas/$plantilla_id/secciones" "$body")"
  requerir_ok "$resp" "creando la sección '$nombre' de la plantilla"
  echo "$resp" | jq -r '.seccion_id'
}

crear_bloque_plantilla() {
  local seccion_id="$1" tipo="$2" nombre_interno="$3" titulo="$4" contenido="$5" columna="$6"
  local body resp
  body="$(jq -n --arg tipo "$tipo" --arg ni "$nombre_interno" --arg titulo "$titulo" \
    --arg contenido "$contenido" --argjson columna "$columna" \
    '{tipo_bloque:$tipo, nombre_interno:$ni, titulo:$titulo, contenido:$contenido, columna:$columna}')"
  resp="$(api POST "/api/plantillas/secciones/$seccion_id/bloques" "$body")"
  requerir_ok "$resp" "creando el bloque '$nombre_interno'"
  echo "$resp" | jq -r '.bloque_id'
}

vincular_bloque_plantilla() {
  local bloque_id="$1" fuente_tipo="$2" fuente_id="$3"
  local body resp
  body="$(jq -n --arg calc "$CALC_ID" --arg tipo "$fuente_tipo" --arg fid "$fuente_id" \
    '{calculadora_id:$calc, fuente_tipo:$tipo, fuente_id:$fid}')"
  resp="$(api POST "/api/plantillas/bloques/$bloque_id/vinculacion" "$body")"
  requerir_ok "$resp" "vinculando el bloque $bloque_id a $fuente_tipo/$fuente_id"
}

ensure_plantilla() {
  local existente
  existente="$(buscar_plantilla_por_nombre)"
  if [ -n "$existente" ]; then
    log "La plantilla '$PLANTILLA_NOMBRE' ya existe ($existente) — no se vuelve a crear."
    PLANTILLA_ID="$existente"
    return 0
  fi

  log "Creando la plantilla '$PLANTILLA_NOMBRE'..."
  local body resp
  body="$(jq -n --arg nombre "$PLANTILLA_NOMBRE" --arg calc "$CALC_ID" \
    '{nombre:$nombre,
      descripcion:"Plantilla base para propuestas de servicios de consultoría e implementación TI.",
      crear_desde:"", calculadora_ids:[$calc], tipos_propuesta:["Comercial"], organizacion_id:""}')"
  resp="$(api POST /api/plantillas "$body")"
  requerir_ok "$resp" "creando la plantilla $PLANTILLA_NOMBRE"
  PLANTILLA_ID="$(echo "$resp" | jq -r '.plantilla_id')"

  local seccion_presentacion seccion_alcance bloque_horas bloque_precio
  seccion_presentacion="$(crear_seccion_plantilla "$PLANTILLA_ID" "Presentación" "UNA")"
  seccion_alcance="$(crear_seccion_plantilla "$PLANTILLA_ID" "Alcance y Precio" "DOS_50_50")"

  crear_bloque_plantilla "$seccion_presentacion" "TEXTO" "intro" "Presentación" \
    "Estimados, presentamos nuestra propuesta de consultoría e implementación de sistemas TI, preparada especialmente para su organización." 0 >/dev/null

  bloque_horas="$(crear_bloque_plantilla "$seccion_alcance" "CAMPO_VINCULADO" "horas_consultoria" "Horas de Consultoría Estimadas" "" 0)"
  bloque_precio="$(crear_bloque_plantilla "$seccion_alcance" "CAMPO_VINCULADO" "precio_total" "Inversión Total" "" 1)"

  # Un bloque vinculado a un campo real del cotizador, y otro a un dato
  # de la cotización (precio) — no a texto libre inventado a mano.
  vincular_bloque_plantilla "$bloque_horas" "CAMPO" "$EL_HORAS_ID"
  vincular_bloque_plantilla "$bloque_precio" "COTIZACION_BASE" "total_precio"

  log "Publicando la plantilla..."
  resp="$(api POST "/api/plantillas/$PLANTILLA_ID/publicar" "")"
  requerir_ok "$resp" "publicando la plantilla $PLANTILLA_ID"
}

# ============================================================
# Integración de API + solicitud externa de ejemplo
# ============================================================
crear_integracion() {
  local body resp api_key
  body="$(jq -n --arg nombre "$INTEGRACION_NOMBRE" '{nombre:$nombre}')"
  resp="$(api POST /api/integraciones "$body")"
  requerir_ok "$resp" "creando la integración $INTEGRACION_NOMBRE"
  api_key="$(echo "$resp" | jq -r '.api_key')"
  printf '%s' "$api_key" >"$API_KEY_FILE"
  # 644 y no algo más restrictivo: el contenedor escribe como root, así
  # que un modo como 600 dejaría el archivo ilegible para quien esté
  # usando el repo desde el host (dueño distinto). No es una clave de
  # producción — vive en un volumen bind-mount local para el demo.
  chmod 644 "$API_KEY_FILE"
  printf '%s' "$api_key"
}

revocar_integracion_activa_si_existe() {
  local resp id
  resp="$(api GET /api/integraciones)"
  requerir_ok "$resp" "listando integraciones existentes"
  id="$(echo "$resp" | jq -r --arg n "$INTEGRACION_NOMBRE" \
    '.integraciones[] | select(.nombre == $n and .estado == "Activo") | .integracion_id' | head -n1)"
  if [ -n "$id" ]; then
    log "Revocando la integración anterior ($id): la clave guardada localmente ya no coincide."
    local body
    body='{"estado":"Inactivo"}'
    resp="$(api PATCH "/api/integraciones/$id" "$body")"
    requerir_ok "$resp" "revocando la integración $id"
  fi
}

cuerpo_solicitud_externa() {
  jq -n \
    --arg cn "Consultora Regional de Energía S.A." \
    --arg ctn "Marta Jiménez" \
    --arg cc "marta.jimenez@ejemplo-cliente.com" \
    --arg ct "+506 2222-3333" \
    --arg calc "$CALC_ID" \
    --arg desc "Solicitud de cotización para servicios de consultoría e implementación de sistemas TI, recibida desde el CRM (demo)." \
    '{cliente_nombre:$cn, contacto_nombre:$ctn, contacto_correo:$cc, contacto_telefono:$ct,
      calculadora_id:$calc, descripcion:$desc}'
}

# Llama al endpoint externo (autenticado con X-Api-Key, no con sesión)
# e imprime "HTTP_STATUS\nCUERPO". Nunca aborta el script por sí sola:
# el llamador decide qué hacer con un 401 (clave vencida/inválida).
llamar_solicitud_externa() {
  local clave="$1" body="$2"
  curl -sS -w '\n%{http_code}' -X POST "$API/api/externo/solicitudes" \
    -H "Content-Type: application/json" \
    -H "X-Api-Key: $clave" \
    -H "Idempotency-Key: $IDEMPOTENCY_KEY" \
    -d "$body"
}

ensure_integracion_y_solicitud() {
  local api_key=""
  if [ -f "$API_KEY_FILE" ]; then
    api_key="$(cat "$API_KEY_FILE")"
  fi
  if [ -z "$api_key" ]; then
    log "No hay clave de API guardada — creando la integración '$INTEGRACION_NOMBRE'..."
    api_key="$(crear_integracion)"
  fi

  local body salida status cuerpo intento
  body="$(cuerpo_solicitud_externa)"

  for intento in 1 2; do
    salida="$(llamar_solicitud_externa "$api_key" "$body")"
    status="$(echo "$salida" | tail -n1)"
    cuerpo="$(echo "$salida" | sed '$d')"
    if [ "$status" = "201" ]; then
      SOLICITUD_ID="$(echo "$cuerpo" | jq -r '.solicitud_id')"
      log "Solicitud externa de ejemplo lista (id $SOLICITUD_ID)."
      return 0
    fi
    if [ "$status" = "401" ] && [ "$intento" = "1" ]; then
      log "La clave de API guardada ya no es válida (probable 'docker compose down -v' con el volumen recreado); generando una nueva."
      revocar_integracion_activa_si_existe
      api_key="$(crear_integracion)"
      continue
    fi
    echo "[seed] ERROR creando la solicitud externa de demostración -> HTTP $status: $cuerpo" >&2
    exit 1
  done
}

# ============================================================
# Resumen final
# ============================================================
imprimir_resumen() {
  {
    echo "===================================================================="
    echo " Cotiza — datos de demostración listos"
    echo "===================================================================="
    echo "Ingresar con:"
    echo "  correo: $ADMIN_CORREO"
    echo "  PIN:    $ADMIN_PIN"
    echo
    echo "Cotizador de ejemplo: $CALC_NOMBRE ($CALC_ID), publicado."
    echo
    echo "Cotizaciones de ejemplo (3):"
    echo "  - $CLIENTE1_NOMBRE -> $COTIZACION1_ID (Borrador)"
    echo "  - $CLIENTE2_NOMBRE -> $COTIZACION2_ID (Enviada al Cliente)"
    echo "  - $CLIENTE3_NOMBRE -> $COTIZACION3_ID (Aceptada)"
    echo
    echo "Link público de la cotización 'Enviada al Cliente':"
    echo "  $PUBLIC_LINK_URL"
    echo
    echo "Plantilla de propuesta: $PLANTILLA_NOMBRE ($PLANTILLA_ID), publicada."
    echo
    echo "Integración de API + solicitud de ejemplo:"
    echo "  clave de API guardada en: $API_KEY_FILE (no se imprime acá; solo se puede leer una vez)"
    echo "  solicitud externa de ejemplo: $SOLICITUD_ID (estado Nueva)"
    echo "===================================================================="
  } | tee "$RESUMEN_FILE"
}

main() {
  esperar_api
  login
  ensure_catalogo
  ensure_cotizador
  ensure_cotizaciones
  ensure_plantilla
  ensure_integracion_y_solicitud
  imprimir_resumen
}

main "$@"
