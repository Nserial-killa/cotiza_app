// Package db maneja el pool de conexiones a PostgreSQL.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool crea un pool de conexiones y verifica conectividad con un
// Ping antes de devolverlo, para fallar rápido si la base no responde
// (por ejemplo si el contenedor de Postgres aún no terminó de arrancar).
func NewPool(databaseURL string) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: no se pudo crear el pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: no se pudo conectar a PostgreSQL: %w", err)
	}

	return pool, nil
}
