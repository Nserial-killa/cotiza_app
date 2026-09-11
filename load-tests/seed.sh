#!/bin/sh
set -eu

: "${LOAD_EMAIL:?Defina LOAD_EMAIL para el usuario de carga}"
: "${LOAD_PIN:?Defina LOAD_PIN para el usuario de carga}"
: "${LOAD_API_KEY:?Defina LOAD_API_KEY para la integración de carga}"

directorio_script=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
raiz_repositorio=$(CDPATH= cd -- "$directorio_script/.." && pwd)
usuario_bd_carga=${DB_USER_CARGA:-cotiza_admin}
base_bd_carga=${DB_NAME_CARGA:-cotiza}

cd "$raiz_repositorio"
docker compose exec -T postgres psql \
  -v ON_ERROR_STOP=1 \
  -v load_email="$LOAD_EMAIL" \
  -v load_pin="$LOAD_PIN" \
  -v load_api_key="$LOAD_API_KEY" \
  -U "$usuario_bd_carga" -d "$base_bd_carga" -f - < "$directorio_script/seed.sql"
