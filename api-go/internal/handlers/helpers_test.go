package handlers

// Helpers compartidos por los _test.go de este paquete. Todas las
// pruebas de acá son de INTEGRACIÓN, no unitarias puras: pegan
// contra el Postgres real (el mismo de "docker compose up -d
// postgres"), porque los handlers usan *pgxpool.Pool directo, sin
// una interfaz intermedia que se pueda simular. Es la forma más
// honesta de probar SQL + bcrypt + forma de la respuesta juntos, al
// costo de necesitar la base levantada para correr "go test".
//
// Cada fixture se crea con un id único por test (para poder correr
// pruebas en paralelo sin chocar) y se borra solo al terminar con
// t.Cleanup — no dependen de datos del seed (0002_seed_demo_data.sql),
// así que no se rompen si el seed cambia.

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupTestDB conecta a DATABASE_URL. Si no está seteada, salta el
// test en vez de fallar — así "go test ./..." no rompe en una
// máquina sin Postgres corriendo (por ejemplo, en un CI que todavía
// no existe). Antes de correr los tests:
//
//	docker compose up -d postgres
//	export DATABASE_URL="postgres://cotiza_admin:changeme@localhost:5432/cotiza?sslmode=disable"
//	cd api-go && go test ./...
func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL no está seteada — ver el comentario de setupTestDB en helpers_test.go")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("no se pudo conectar a Postgres: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Postgres no respondió al ping: %v (¿está corriendo 'docker compose up -d postgres'?)", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// sufijoUnico da un identificador corto y distinto en cada llamada,
// para que los ids de prueba no choquen entre tests.
func sufijoUnico() string {
	return time.Now().Format("150405.000000000")
}

// crearUsuarioPrueba inserta un usuario descartable con un PIN
// conocido (hasheado con la misma extensión pgcrypto que usa
// 0002_seed_demo_data.sql) y lo borra al terminar el test.
func crearUsuarioPrueba(t *testing.T, pool *pgxpool.Pool, correo, pin, rol, estado string) string {
	t.Helper()
	id := "test-usr-" + sufijoUnico()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO usuarios (usuario_id, nombre, correo, pin_hash, rol, estado, puede_ver_gestor)
		VALUES ($1, 'Usuario de prueba', $2, crypt($3, gen_salt('bf')), $4, $5, true)`,
		id, correo, pin, rol, estado,
	)
	if err != nil {
		t.Fatalf("no se pudo crear el usuario de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM usuarios WHERE usuario_id = $1`, id)
	})
	return id
}

// crearCatalogoPrueba inserta un catálogo descartable, opcionalmente
// hijo de otro (catalogoPadreID == "" para uno sin padre).
func crearCatalogoPrueba(t *testing.T, pool *pgxpool.Pool, nombre, catalogoPadreID string) string {
	t.Helper()
	id := "test-cat-" + sufijoUnico()
	var padre any
	if catalogoPadreID != "" {
		padre = catalogoPadreID
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO catalogos (catalogo_id, nombre_catalogo, catalogo_padre_id, activo)
		VALUES ($1, $2, $3, true)`,
		id, nombre, padre,
	)
	if err != nil {
		t.Fatalf("no se pudo crear el catálogo de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM catalogos WHERE catalogo_id = $1`, id)
	})
	return id
}

// crearValorCatalogoPrueba inserta un valor descartable dentro de un
// catálogo, opcionalmente hijo de otro valor (valorPadreID == "" si
// no aplica).
func crearValorCatalogoPrueba(t *testing.T, pool *pgxpool.Pool, catalogoID, texto, valorPadreID string) string {
	t.Helper()
	id := "test-val-" + sufijoUnico()
	var padre any
	if valorPadreID != "" {
		padre = valorPadreID
	}
	_, err := pool.Exec(context.Background(), `
		INSERT INTO catalogo_valores (valor_id, catalogo_id, texto_visible, valor_sistema, valor_padre_id, activo)
		VALUES ($1, $2, $3, $3, $4, true)`,
		id, catalogoID, texto, padre,
	)
	if err != nil {
		t.Fatalf("no se pudo crear el valor de catálogo de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM catalogo_valores WHERE valor_id = $1`, id)
	})
	return id
}

// crearRelacionPrueba inserta una relación catálogo-padre/valor →
// catálogo-hijo/valor descartable.
func crearRelacionPrueba(t *testing.T, pool *pgxpool.Pool, catalogoPadreID, valorPadreID, catalogoHijoID, valorHijoID string) string {
	t.Helper()
	var relacionID string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO catalogo_relaciones (catalogo_padre_id, valor_padre_id, catalogo_hijo_id, valor_hijo_id, activo)
		VALUES ($1, $2, $3, $4, true)
		RETURNING relacion_id::text`,
		catalogoPadreID, valorPadreID, catalogoHijoID, valorHijoID,
	).Scan(&relacionID)
	if err != nil {
		t.Fatalf("no se pudo crear la relación de prueba: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM catalogo_relaciones WHERE relacion_id::text = $1`, relacionID)
	})
	return relacionID
}

// assertJSON decodifica el body de la respuesta grabada con
// encoding/json, fallando el test con un mensaje claro (incluyendo
// el body crudo) si no es JSON válido — para no depurar a ciegas
// cuando algo devuelve HTML de error en vez de la respuesta esperada.
func assertJSON(t *testing.T, bodyBytes []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(bodyBytes, out); err != nil {
		t.Fatalf("la respuesta no es el JSON esperado: %v\nbody crudo: %s", err, string(bodyBytes))
	}
}
