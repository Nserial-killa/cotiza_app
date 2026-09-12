# Imagen mínima para correr scripts/seed_demo.sh: bash + curl + jq
# para hablar con el API real, y el cliente de psql SOLO para
# lecturas de idempotencia (nunca para insertar datos de negocio —
# eso lo hace siempre el script llamando al API, ver seed_demo.sh).
FROM alpine:3.19

RUN apk add --no-cache bash curl jq postgresql-client

WORKDIR /scripts
ENTRYPOINT ["/bin/bash", "/scripts/seed_demo.sh"]
