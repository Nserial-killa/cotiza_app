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
	salidasCotizador := &handlers.SalidasCotizadorHandler{DB: pool}
	seccionesAdicionales := &handlers.SeccionesAdicionalesHandler{DB: pool}
	compilador := &handlers.CompiladorHandler{DB: pool}
	usuarios := &handlers.UsuariosHandler{DB: pool}
	reglas := &handlers.ReglasHandler{DB: pool}
	reglasCotizador := &handlers.ReglasCotizadorHandler{DB: pool}
	cotizaciones := &handlers.CotizacionesHandler{DB: pool}
	clientes := &handlers.ClientesHandler{DB: pool}
	dashboard := &handlers.DashboardHandler{DB: pool}
	reportes := &handlers.ReportesHandler{DB: pool}
	calculadoras := &handlers.CalculadorasHandler{DB: pool}
	runtimeCotizador := &handlers.CotizadorRuntimeHandler{DB: pool}
	listaPreciosItems := &handlers.ListaPreciosItemsHandler{DB: pool}
	tablaColumnas := &handlers.TablaColumnasHandler{DB: pool}
	var correoOfertas handlers.RemitenteOferta
	if cfg.SMTPHost != "" && cfg.SMTPFrom != "" {
		correoOfertas = handlers.SMTPOferta{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Usuario: cfg.SMTPUser,
			Clave: cfg.SMTPPassword, Desde: cfg.SMTPFrom,
		}
	}
	// Pases de render del PDF: un solo registro compartido entre quien los
	// emite (ofertaPDF) y quien los consume (enlacesPublicos.VerCotizacion).
	pasesRender := handlers.NuevosPasesRender()
	enlacesPublicos := &handlers.EnlacesPublicosHandler{DB: pool, Correo: correoOfertas, BasePublica: cfg.PublicBaseURL, PasesRender: pasesRender}
	// Gotenberg solo se arma si hay URL; sin ella el endpoint responde 503 claro.
	var convertidorPDF handlers.ConvertidorPDF
	if cfg.GotenbergURL != "" {
		convertidorPDF = handlers.GotenbergPDF{BaseURL: cfg.GotenbergURL}
	}
	ofertaPDF := &handlers.OfertaPDFHandler{DB: pool, Convertidor: convertidorPDF, BaseInterna: cfg.PDFInternalBaseURL, Pases: pasesRender}
	vistaPreviaOferta := &handlers.VistaPreviaOfertaHandler{DB: pool}
	plantillas := &handlers.PlantillasHandler{DB: pool}
	plantillaEstructura := &handlers.PlantillaEstructuraHandler{DB: pool}
	plantillaVinculaciones := &handlers.PlantillaVinculacionesHandler{DB: pool}
	plantillaCondiciones := &handlers.PlantillaCondicionesHandler{DB: pool}
	plantillaTablaColumnas := &handlers.PlantillaTablaColumnasHandler{DB: pool}
	plantillaEstilo := &handlers.PlantillaEstiloHandler{DB: pool}
	plantillaBloqueCampos := &handlers.PlantillaBloqueCamposHandler{DB: pool}
	integraciones := &handlers.IntegracionesHandler{DB: pool}
	solicitudes := &handlers.SolicitudesHandler{DB: pool, Cotizaciones: cotizaciones}
	solicitudesExternas := &handlers.SolicitudesExternasHandler{DB: pool}

	router.Route("/api", func(r chi.Router) {
		// Públicas — sin sesión. Todo lo demás bajo /api exige un
		// token válido (ver el r.Group de acá abajo).
		r.Get("/health", health.Check)
		r.Post("/auth/login", auth.Login)
		r.Get("/publico/cotizacion/{token}", enlacesPublicos.VerCotizacion)
		r.Post("/publico/cotizacion/{token}/respuesta", enlacesPublicos.Responder)
		r.Post("/publico/cotizacion/{token}/codigo", enlacesPublicos.EnviarCodigoAceptacion)
		r.Post("/publico/cotizacion/{token}/aceptar", enlacesPublicos.Aceptar)

		// API externo (Bitrix24 u otro): clave propia por header
		// X-Api-Key, nunca una sesión de usuario — grupo aparte del de
		// abajo, con su propio middleware.
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequiereApiKey(pool))
			r.Post("/externo/solicitudes", solicitudesExternas.Crear)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.RequiereSesion(pool))

			r.Route("/auth", func(r chi.Router) {
				r.Delete("/logout", auth.Logout)
			})

			// --- Carril A (Configuración): catálogos, diseñador, reglas,
			//     compilador, plantillas.
			//
			// Las lecturas del Diseñador son administrativas. El motor de
			// ejecución usa el compilado fijado a la cotización y no necesita
			// exponer estas rutas a roles operativos.
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/catalogos/designer", catalogos.ListarDesigner)
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/cotizador/tabs", cotizadorTabs.ListarTabs)
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/cotizador/salidas", salidasCotizador.Listar)
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/cotizador/elementos", cotizadorTabs.ListarElementos)
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/cotizador/secciones-reutilizables", seccionesAdicionales.ListarReutilizables)
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/reglas", reglas.Listar)
			r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/cotizador/reglas", reglasCotizador.Listar)

			// Escritura del Diseñador: exige roles.puede_parametrizar
			// además de la sesión. Toda ruta POST/PATCH/DELETE de estos
			// módulos va DENTRO de este grupo —
			// permisos_rutas_test.go falla si alguna queda afuera.
			r.Group(func(r chi.Router) {
				r.Use(handlers.RequierePuedeParametrizar(pool))

				r.Post("/calculadoras", calculadoras.Crear)
				r.Post("/catalogos", catalogos.GuardarCatalogo)
				r.Delete("/catalogos/{id}", catalogos.EliminarCatalogo)
				r.Post("/catalogos/valores", catalogos.GuardarValor)
				r.Delete("/catalogos/valores/{id}", catalogos.EliminarValor)
				r.Post("/catalogos/relaciones", catalogos.GuardarRelaciones)
				r.Delete("/catalogos/relaciones/{id}", catalogos.EliminarRelacion)
				r.Post("/cotizador/salidas", salidasCotizador.Guardar)
				r.Delete("/cotizador/salidas/{clave_salida}", salidasCotizador.Eliminar)
				r.Post("/cotizador/tabs", cotizadorTabs.GuardarTab)
				r.Delete("/cotizador/tabs/{id}", cotizadorTabs.EliminarTab)
				r.Post("/cotizador/elementos", cotizadorTabs.GuardarElemento)
				r.Delete("/cotizador/elementos/{id}", cotizadorTabs.EliminarElemento)
				r.Post("/cotizador/elementos/{elemento_id}/secciones", seccionesAdicionales.Asociar)
				r.Delete("/cotizador/elementos/{elemento_id}/secciones/{tab_id}", seccionesAdicionales.Desasociar)
				r.Post("/cotizador/elementos/{elemento_id}/items", listaPreciosItems.Crear)
				r.Patch("/cotizador/items/{item_id}", listaPreciosItems.Editar)
				r.Delete("/cotizador/items/{item_id}", listaPreciosItems.Eliminar)
				r.Post("/cotizador/elementos/{elemento_id}/columnas", tablaColumnas.Crear)
				r.Patch("/cotizador/columnas/{columna_id}", tablaColumnas.Editar)
				r.Delete("/cotizador/columnas/{columna_id}", tablaColumnas.Eliminar)
				r.Post("/cotizador/validar", compilador.Validar)
				r.Post("/cotizador/compilar", compilador.Compilar)
				r.Post("/reglas", reglas.Guardar)
				r.Delete("/reglas/{id}", reglas.Eliminar)
				r.Post("/cotizador/reglas", reglasCotizador.Guardar)
				r.Delete("/cotizador/reglas/{id}", reglasCotizador.Eliminar)
			})

			// --- Carril A (Configuración): plantillas. Un solo r.Route: si
			// los GET quedaran afuera y las escrituras en otro r.Route
			// ("/plantillas") dentro del grupo de abajo, chi le daría el
			// prefijo entero al subrouter y los GET responderían 405.
			r.Route("/plantillas", func(r chi.Router) {
				r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/", plantillas.Listar)
				r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/opciones", plantillas.Opciones)
				r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/{id}", plantillas.Detalle)
				r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/{id}/validacion", plantillas.Validacion)
				r.With(handlers.RequierePuedeVerAdministracion(pool)).Get("/{id}/fuentes", plantillaVinculaciones.Fuentes)
				r.Group(func(r chi.Router) {
					r.Use(handlers.RequierePuedeParametrizar(pool))
					r.Post("/", plantillas.Crear)
					r.Patch("/{id}", plantillas.Editar)
					r.Post("/{id}/publicar", plantillas.Publicar)
					r.Post("/{id}/nueva-version", plantillas.NuevaVersion)
					r.Delete("/{id}", plantillas.Eliminar)
					r.Post("/{id}/secciones", plantillaEstructura.CrearSeccion)
					r.Post("/{id}/estructura-sugerida", plantillaEstructura.AplicarEstructuraSugerida)
					r.Patch("/secciones/{seccion_id}", plantillaEstructura.EditarSeccion)
					r.Delete("/secciones/{seccion_id}", plantillaEstructura.EliminarSeccion)
					r.Post("/secciones/{seccion_id}/orden", plantillaEstructura.OrdenarSecciones)
					r.Post("/secciones/{seccion_id}/bloques", plantillaEstructura.CrearBloque)
					r.Patch("/bloques/{bloque_id}", plantillaEstructura.EditarBloque)
					r.Delete("/bloques/{bloque_id}", plantillaEstructura.EliminarBloque)
					r.Post("/bloques/{bloque_id}/orden", plantillaEstructura.OrdenarBloques)
					r.Post("/bloques/{bloque_id}/vinculacion", plantillaVinculaciones.Guardar)
					r.Delete("/bloques/{bloque_id}/vinculacion", plantillaVinculaciones.Eliminar)
					r.Post("/bloques/{bloque_id}/condicion", plantillaCondiciones.Guardar)
					r.Delete("/bloques/{bloque_id}/condicion", plantillaCondiciones.Eliminar)
					r.Post("/bloques/{bloque_id}/columnas", plantillaTablaColumnas.Agregar)
					r.Patch("/columnas/{columna_id}", plantillaTablaColumnas.Editar)
					r.Delete("/columnas/{columna_id}", plantillaTablaColumnas.Eliminar)
					r.Post("/bloques/{bloque_id}/columnas/orden", plantillaTablaColumnas.Ordenar)
					r.Post("/bloques/{bloque_id}/campos", plantillaBloqueCampos.Agregar)
					r.Patch("/campos/{campo_id}", plantillaBloqueCampos.Editar)
					r.Delete("/campos/{campo_id}", plantillaBloqueCampos.Eliminar)
					r.Post("/bloques/{bloque_id}/campos/orden", plantillaBloqueCampos.Ordenar)
					r.Patch("/{id}/estilo", plantillaEstilo.Actualizar)
				})
			})

			// Motor de ejecución: permisos por bandera (editar borrador) y
			// alcance propio se validan dentro de cada handler.
			r.Get("/cotizador/runtime/{cotizacion_id}", runtimeCotizador.Obtener)
			r.Post("/cotizador/runtime/{cotizacion_id}/valores", runtimeCotizador.GuardarValores)
			r.Post("/cotizador/runtime/{cotizacion_id}/opciones", runtimeCotizador.AdministrarOpciones)

			// --- Carril B (Operación): cotizaciones, dashboard, reportes.
			r.With(handlers.RequierePuedeVerDashboard(pool)).Get("/dashboard", dashboard.Obtener)
			r.Route("/dashboard", func(r chi.Router) {
				r.Use(handlers.RequierePuedeVerDashboard(pool))
				r.Get("/resumen", dashboard.Resumen)
				r.Get("/tendencia", dashboard.Tendencia)
				r.Get("/estados", dashboard.Estados)
				r.Get("/segmentacion-clientes", dashboard.SegmentacionClientes)
				r.Get("/cotizadores", dashboard.Cotizadores)
			})
			r.Get("/reportes/cotizaciones", reportes.Listar)
			r.Get("/reportes/cotizaciones/exportar", reportes.Exportar)
			r.Get("/roles", usuarios.ListarRoles)
			r.Get("/calculadoras", calculadoras.Listar)
			r.Route("/clientes", func(r chi.Router) {
				r.Get("/", cotizaciones.ListarClientes)
				r.Get("/gestion", clientes.Listar)
				r.Post("/", clientes.Crear)
				r.Patch("/{id}", clientes.Editar)
			})
			r.Route("/usuarios", func(r chi.Router) {
				r.Get("/", usuarios.Listar)
				r.Get("/activos", usuarios.ListarActivos)
				r.Post("/", usuarios.Crear)
				r.Patch("/{id}", usuarios.Editar)
			})
			r.Route("/cotizaciones", func(r chi.Router) {
				r.Get("/", cotizaciones.Listar)
				r.Post("/", cotizaciones.Crear)
				r.Get("/{id}", cotizaciones.Detalle)
				r.Post("/{id}/version", cotizaciones.CrearVersion)
				r.Post("/{id}/cambiar-cotizador", cotizaciones.CambiarCotizador)
				r.Post("/{id}/estado", cotizaciones.CambiarEstado)
				r.Post("/{id}/enlace", enlacesPublicos.GenerarEnlace)
				r.Get("/{id}/enlace/pdf", ofertaPDF.Descargar) // PDF vía Gotenberg; permisos de ver la cotización
				r.Get("/{id}/vista-previa-oferta", vistaPreviaOferta.Ver)
			})
			r.Route("/integraciones", func(r chi.Router) {
				r.Get("/", integraciones.Listar)
				r.Post("/", integraciones.Crear)
				r.Patch("/{id}", integraciones.Editar)
			})
			r.Route("/solicitudes", func(r chi.Router) {
				r.Get("/", solicitudes.Listar)
				r.Post("/", solicitudes.Crear)
				r.Get("/{id}", solicitudes.Detalle)
				r.Patch("/{id}", solicitudes.CambiarEstado)
				r.Post("/{id}/convertir", solicitudes.Convertir)
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
