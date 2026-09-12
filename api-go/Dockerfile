# --- Etapa 1: build ---
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Copiar solo los archivos de módulos primero: si no cambian,
# Docker reutiliza esta capa de caché en builds siguientes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/cotiza-api ./cmd/server

# --- Etapa 2: imagen final, liviana ---
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /out/cotiza-api ./cotiza-api

EXPOSE 8080
ENTRYPOINT ["./cotiza-api"]
