#!/bin/sh
set -u

: "${LOAD_EMAIL:?Defina LOAD_EMAIL}"
: "${LOAD_PIN:?Defina LOAD_PIN}"
: "${LOAD_API_KEY:?Defina LOAD_API_KEY}"

directorio_script=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
directorio_resultados=${RESULTS_DIR:-"$directorio_script/results"}
red_k6=${K6_NETWORK:-cotiza_app_cotiza_net}
base_url_k6=${BASE_URL_K6:-http://cotiza_api:8080}
duracion_k6=${DURATION:-30s}
niveles_k6=${LOAD_LEVELS:-"10 50 100"}
scripts_k6="login-cotizaciones.js dashboard.js reportes.js solicitudes-externas.js"
estado_final=0

mkdir -p "$directorio_resultados"
for script_k6 in $scripts_k6; do
  nombre_k6=${script_k6%.js}
  for vus_k6 in $niveles_k6; do
    echo "Ejecutando $nombre_k6 con $vus_k6 VUs durante $duracion_k6"
    if ! docker run --rm --network "$red_k6" \
      -e BASE_URL="$base_url_k6" \
      -e LOAD_EMAIL -e LOAD_PIN -e LOAD_API_KEY \
      -e VUS="$vus_k6" -e DURATION="$duracion_k6" \
      -v "$directorio_script:/scripts:ro" \
      -v "$directorio_resultados:/results" \
      grafana/k6:latest run \
      --summary-export="/results/${nombre_k6}-${vus_k6}vus.json" \
      "/scripts/$script_k6"
    then
      # k6 devuelve estado distinto de cero cuando falla un threshold. Se
      # conservan el resumen y las demás corridas para comparar niveles.
      estado_final=1
    fi
  done
done

exit "$estado_final"
