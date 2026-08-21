// Package config centraliza la lectura de variables de entorno.
// Mantener esto en un solo lugar evita "os.Getenv" repartido por
// todo el código y facilita agregar validaciones a futuro.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	Env         string
	Port        string
	DatabaseURL string
	StaticDir   string
}

// Load lee la configuración desde variables de entorno, aplicando
// valores por defecto razonables para desarrollo local.
func Load() (*Config, error) {
	cfg := &Config{
		Env:         getEnv("ENV", "development"),
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", ""),
		StaticDir:   getEnv("STATIC_DIR", "/app/static"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: la variable DATABASE_URL es obligatoria")
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
