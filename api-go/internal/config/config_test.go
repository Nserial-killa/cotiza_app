package config

// Auditoría de QA — dimensión 1 (Unit). internal/config no tenía
// pruebas y es el único lugar del proyecto que lee variables de
// entorno (convención de CLAUDE.md: ningún handler llama a os.Getenv
// directo). Un default equivocado acá rompe el arranque en Docker sin
// que ninguna otra prueba lo note, porque el resto de la suite nunca
// pasa por Load().
//
// Son pruebas puras: usan t.Setenv, que restaura el entorno al
// terminar cada test y por eso no se pueden correr en paralelo con
// t.Parallel() (el propio testing lo prohíbe).

import (
	"strings"
	"testing"
)

// limpiarEntorno deja las cuatro variables sin definir para partir de
// un estado conocido en cada caso.
func limpiarEntorno(t *testing.T) {
	t.Helper()
	for _, clave := range []string{"ENV", "PORT", "DATABASE_URL", "STATIC_DIR"} {
		t.Setenv(clave, "")
	}
}

func TestUnitConfigLoad_AplicaLosDefaultsDeDesarrollo(t *testing.T) {
	limpiarEntorno(t)
	t.Setenv("DATABASE_URL", "postgres://x/y")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("con DATABASE_URL definida no debería fallar: %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("ENV por defecto debería ser development y es %q", cfg.Env)
	}
	if cfg.Port != "8080" {
		t.Errorf("PORT por defecto debería ser 8080 y es %q: el docker-compose publica ese puerto", cfg.Port)
	}
	if cfg.StaticDir != "/app/static" {
		t.Errorf("STATIC_DIR por defecto debería ser /app/static y es %q: es la ruta donde el compose monta ./frontend", cfg.StaticDir)
	}
}

func TestUnitConfigLoad_SinDatabaseURLFallaConMensajeClaro(t *testing.T) {
	limpiarEntorno(t)

	cfg, err := Load()
	if err == nil {
		t.Fatalf("sin DATABASE_URL Load debería fallar; devolvió %+v", cfg)
	}
	if cfg != nil {
		t.Errorf("cuando hay error, la config debería venir nil y vino %+v", cfg)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("el mensaje de error debería nombrar la variable que falta y dice %q", err.Error())
	}
}

func TestUnitConfigLoad_LasVariablesDefinidasGananAlDefault(t *testing.T) {
	limpiarEntorno(t)
	t.Setenv("ENV", "production")
	t.Setenv("PORT", "9090")
	t.Setenv("DATABASE_URL", "postgres://usuario:clave@host:5432/cotiza")
	t.Setenv("STATIC_DIR", "../frontend")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if cfg.Env != "production" || cfg.Port != "9090" || cfg.StaticDir != "../frontend" {
		t.Errorf("las variables definidas deberían pisar los defaults y quedó %+v", cfg)
	}
	if cfg.DatabaseURL != "postgres://usuario:clave@host:5432/cotiza" {
		t.Errorf("DATABASE_URL no se leyó tal cual: %q", cfg.DatabaseURL)
	}
}

// TestUnitConfigGetEnv_UnaVariableVaciaCuentaComoAusente fija una
// decisión que no es obvia leyendo la firma: getEnv trata "" como
// ausente y cae al default. Importa porque el perfil de VSCode y el
// compose exportan variables vacías en algunos casos, y el
// comportamiento deseado es usar el default, no una cadena vacía.
func TestUnitConfigGetEnv_UnaVariableVaciaCuentaComoAusente(t *testing.T) {
	limpiarEntorno(t)
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "postgres://x/y")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("no debería fallar: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("PORT=\"\" debería caer al default 8080 y quedó %q", cfg.Port)
	}
}
