# Entorno local en Fedora + VSCode

Guía para dejar la máquina lista y correr Cotiza en modo desarrollo.
Pensada para Fedora (dnf), partiendo de que no hay Docker ni Go
instalados todavía.

## 1. Instalar Docker Engine + Compose

Fedora no trae Docker en sus repos por defecto (por licencia), hay
que agregar el repositorio oficial de Docker:

```bash
sudo dnf -y install dnf-plugins-core
sudo dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo
sudo dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

sudo systemctl enable --now docker

# Para no tener que usar sudo en cada comando docker:
sudo usermod -aG docker $USER
```

Después del `usermod`, cerrar sesión y volver a entrar (o correr
`newgrp docker` en la terminal actual) para que el cambio de grupo
tome efecto. Verificar:

```bash
docker --version
docker compose version
docker run hello-world
```

## 2. Instalar Go

```bash
sudo dnf install -y golang
go version
```

El `go.mod` del proyecto pide Go 1.22. Si `dnf` instala una versión
más vieja, usar el tarball oficial en su lugar:

```bash
cd /tmp
curl -LO https://go.dev/dl/go1.22.5.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.5.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
source ~/.bashrc
go version
```

## 3. Abrir el proyecto en VSCode

```bash
unzip cotiza-proyecto-base.zip
cd cotiza
code .
```

Al abrir, VSCode va a sugerir instalar las extensiones recomendadas
(`.vscode/extensions.json`): Go, Docker, REST Client, PostgreSQL y
dotenv. Aceptar esa notificación. La primera vez que se abre un
archivo `.go`, la extensión de Go va a pedir instalar herramientas
adicionales (`gopls`, `dlv`, etc.) — aceptar también, son necesarias
para autocompletado y para depurar.

## 4. Variables de entorno

```bash
cp .env.example .env
```

## 5. Generar `go.sum` (una sola vez)

```bash
cd api-go
go mod tidy
cd ..
```

Esto descarga y fija las versiones de `pgx`, `chi` y `cors`.

## 6. Dos formas de correrlo

### Opción A — Todo en Docker (para probar tal cual se entrega)

```bash
docker compose up --build
```

Probar en el navegador o en `api-go/requests.http` (abrir el archivo
en VSCode con la extensión REST Client y darle "Send Request" al
`GET /api/health`):

```
http://localhost:8080/api/health
```

Debería responder `{"status":"ok","database":"ok"}`.

Para bajarlo: `docker compose down` (o `Ctrl+C` y luego `docker
compose down` si quedó algo corriendo en segundo plano).

### Opción B — Modo desarrollo rápido (recomendada para trabajar día a día)

Reconstruir la imagen de Go en cada cambio es lento. Para iterar
rápido y poder poner breakpoints:

1. Levantar **solo** Postgres en Docker:

   ```bash
   docker compose up -d postgres
   ```

2. Correr el API directamente en la máquina (no en Docker), apuntando
   a ese Postgres:

   ```bash
   cd api-go
   go run ./cmd/server
   ```

   Esto usa las variables de entorno ya configuradas en
   `.vscode/launch.json` si se corre desde el depurador (ver punto 3),
   o hay que exportarlas a mano si se corre por terminal:

   ```bash
   export DATABASE_URL="postgres://cotiza_admin:changeme@localhost:5432/cotiza?sslmode=disable"
   export PORT=8080
   go run ./cmd/server
   ```

3. **Depurar con breakpoints en VSCode:** abrir cualquier archivo
   `.go`, poner un breakpoint (click a la izquierda del número de
   línea), y presionar **F5**. Ya está la configuración lista
   (`.vscode/launch.json`, perfil "Cotiza API (debug local...)"), así
   que VSCode arranca el servidor en modo debug automáticamente,
   conectado al Postgres que levantaste en el paso 1.

Con este flujo, cada cambio en el código Go se prueba con `go run` o
F5 en segundos, sin reconstruir contenedores. Antes de subir/entregar
un sprint, sí conviene correr la Opción A completa una vez, para
confirmar que `docker compose up --build` funciona de punta a punta.

## 7. Problemas comunes

- **`permission denied` al correr `docker`:** falta el `newgrp
  docker` o cerrar sesión después del `usermod` del paso 1.
- **`port is already allocated` (5432 u 8080):** ya hay algo
  corriendo en ese puerto. `docker compose down` primero, o cambiar
  el puerto en `.env` (`POSTGRES_PORT`, `API_PORT`).
- **Migraciones no se aplicaron:** los `.sql` de `api-go/migrations/`
  solo corren la primera vez que se crea el volumen de Postgres. Si
  se edita `0001_init_schema.sql` después de haber corrido la base
  una vez, hay que borrar el volumen para que se vuelva a aplicar:

  ```bash
  docker compose down -v   # ¡borra los datos! solo en desarrollo
  docker compose up --build
  ```
