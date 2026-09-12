#!/bin/sh
set -eu

directorio_script=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
raiz_repositorio=$(CDPATH= cd -- "$directorio_script/.." && pwd)
usuario_bd_carga=${DB_USER_CARGA:-cotiza_admin}
base_bd_carga=${DB_NAME_CARGA:-cotiza}

cd "$raiz_repositorio"
docker compose exec -T postgres psql \
  -v ON_ERROR_STOP=1 \
  -U "$usuario_bd_carga" -d "$base_bd_carga" -f - < "$directorio_script/cleanup.sql"
