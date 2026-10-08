// Package config centraliza la lectura de variables de entorno.
// Mantener esto en un solo lugar evita "os.Getenv" repartido por
// todo el código y facilita agregar validaciones a futuro.
package config

import (
	"fmt"
	"os"
)

type Config struct {
	Env           string
	Port          string
	DatabaseURL   string
	StaticDir     string
	SMTPHost      string
	SMTPPort      string
	SMTPUser      string
	SMTPPassword  string
	SMTPFrom      string
	PublicBaseURL string
	// GotenbergURL es el servicio que convierte la oferta a PDF (red interna
	// de Docker, ej. http://gotenberg:3000). Vacío = PDF no disponible.
	GotenbergURL string
	// PDFInternalBaseURL es cómo Gotenberg llega a ESTE API desde adentro de
	// la red de Docker (ej. http://api:8080). No es la URL pública.
	PDFInternalBaseURL string
}

// Load lee la configuración desde variables de entorno, aplicando
// valores por defecto razonables para desarrollo local.
func Load() (*Config, error) {
	cfg := &Config{
		Env:           getEnv("ENV", "development"),
		Port:          getEnv("PORT", "8080"),
		DatabaseURL:   getEnv("DATABASE_URL", ""),
		StaticDir:     getEnv("STATIC_DIR", "/app/static"),
		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getEnv("SMTP_PORT", "587"),
		SMTPUser:      getEnv("SMTP_USER", ""),
		SMTPPassword:  getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:      getEnv("SMTP_FROM", ""),
		PublicBaseURL: getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
		// Sin default: si no se configuran, el PDF responde 503 en vez de
		// apuntar a un host que quizás no exista.
		GotenbergURL:       getEnv("GOTENBERG_URL", ""),
		PDFInternalBaseURL: getEnv("PDF_INTERNAL_BASE_URL", ""),
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
