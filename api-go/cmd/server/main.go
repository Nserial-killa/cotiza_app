// Cotiza API — punto de entrada.
//
// Estructura pensada para crecer por módulo (Carril A / Carril B del
// plan de trabajo) sin pisarse: cada quien agrega sus rutas en
// internal/handlers y las registra acá, dentro de su propio grupo
// de rutas ("/api/catalogos", "/api/cotizaciones", etc).
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"cotiza/api/internal/config"
	"cotiza/api/internal/db"
	"cotiza/api/internal/handlers"
	"cotiza/api/internal/middleware"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuración inválida: %v", err)
	}

	pool, err := db.NewPool(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("no se pudo conectar a la base de datos: %v", err)
	}
	defer pool.Close()

	router := chi.NewRouter()
	router.Use(chimiddleware.Logger)
	router.Use(chimiddleware.Recoverer)
	router.Use(chimiddleware.Timeout(30 * time.Second))
	router.Use(cors.Handler(cors.Options{
		// En desarrollo se permite todo origen; ajustar en producción
		// al dominio real del frontend.
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	health := &handlers.HealthHandler{DB: pool}
	auth := &handlers.AuthHandler{DB: pool}
	catalogos := &handlers.CatalogosHandler{DB: pool}
	cotizadorTabs := &handlers.CotizadorTabsHandler{DB: pool}
	compilador := &handlers.CompiladorHandler{DB: pool}
	usuarios := &handlers.UsuariosHandler{DB: pool}
	reglas := &handlers.ReglasHandler{DB: pool}
	cotizaciones := &handlers.CotizacionesHandler{DB: pool}

	router.Route("/api", func(r chi.Router) {
		// Públicas — sin sesión. Todo lo demás bajo /api exige un
		// token válido (ver el r.Group de acá abajo).
		r.Get("/health", health.Check)
		r.Post("/auth/login", auth.Login)

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequiereSesion(pool))

			r.Route("/auth", func(r chi.Router) {
				r.Delete("/logout", auth.Logout)
			})

			// --- Carril A (Configuración): catálogos, diseñador, reglas,
			//     compilador, plantillas.
			r.Get("/catalogos/designer", catalogos.ListarDesigner)
			r.Post("/catalogos", catalogos.GuardarCatalogo)
			r.Delete("/catalogos/{id}", catalogos.EliminarCatalogo)
			r.Post("/catalogos/valores", catalogos.GuardarValor)
			r.Delete("/catalogos/valores/{id}", catalogos.EliminarValor)
			r.Post("/catalogos/relaciones", catalogos.GuardarRelaciones)
			r.Delete("/catalogos/relaciones/{id}", catalogos.EliminarRelacion)
			r.Get("/cotizador/tabs", cotizadorTabs.ListarTabs)
			r.Post("/cotizador/tabs", cotizadorTabs.GuardarTab)
			r.Delete("/cotizador/tabs/{id}", cotizadorTabs.EliminarTab)
			r.Get("/cotizador/elementos", cotizadorTabs.ListarElementos)
			r.Post("/cotizador/elementos", cotizadorTabs.GuardarElemento)
			r.Delete("/cotizador/elementos/{id}", cotizadorTabs.EliminarElemento)
			r.Post("/cotizador/validar", compilador.Validar)
			r.Post("/cotizador/compilar", compilador.Compilar)
			r.Route("/reglas", func(r chi.Router) {
				r.Get("/", reglas.Listar)
				r.Post("/", reglas.Guardar)
				r.Delete("/{id}", reglas.Eliminar)
			})

			// --- Carril B (Operación): cotizaciones, dashboard, reportes.
			r.Get("/roles", usuarios.ListarRoles)
			r.Route("/usuarios", func(r chi.Router) {
				r.Get("/", usuarios.Listar)
				r.Post("/", usuarios.Crear)
				r.Patch("/{id}", usuarios.Editar)
			})
			r.Route("/cotizaciones", func(r chi.Router) {
				r.Get("/", cotizaciones.Listar)
				r.Get("/{id}", cotizaciones.Detalle)
				r.Post("/{id}/version", cotizaciones.CrearVersion)
				r.Post("/{id}/estado", cotizaciones.CambiarEstado)
			})
		})
	})

	// El propio Go sirve el frontend estático (HTML/CSS/JS existente).
	// Evita tener un contenedor nginx aparte para un proyecto de este
	// tamaño; se puede separar más adelante si hace falta.
	staticDir := http.Dir(cfg.StaticDir)
	fileServer := http.FileServer(staticDir)
	router.Handle("/*", fileServer)

	addr := ":" + cfg.Port
	log.Printf("Cotiza API escuchando en %s (env=%s)", addr, cfg.Env)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
