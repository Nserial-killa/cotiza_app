#!/usr/bin/env bash
# Ejecuta el mismo cliente HTTP que usan las pruebas Go de integración.
# Requiere Go 1.22+. El API y el usuario Administrador deben existir.
# No lee ni escribe SQL. El PIN viaja por stdin, nunca como argumento.
set -euo pipefail
ISA_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ISA_SCRIPT_DIR/../api-go"
printf '%s\n' "${ISA_ADMIN_PIN:-1234}" | go run ./cmd/seed-isa-custom \
  -api "${API_BASE_URL:-http://localhost:8080}" \
  -correo "${ISA_ADMIN_CORREO:-demo.admin@exceltecgroup.com}" "$@"
