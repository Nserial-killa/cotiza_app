package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

type resultadoHTTPConcurrente struct {
	Codigo int
	Cuerpo map[string]any
	Texto  string
	Err    error
}

// ejecutarEnParalelo usa una barrera común para que todas las goroutines
// empiecen la operación al mismo tiempo. Cada una escribe en una posición
// exclusiva del slice, por lo que el propio test también queda libre de races.
func ejecutarEnParalelo(cantidad int, operacion func(int) resultadoHTTPConcurrente) []resultadoHTTPConcurrente {
	resultados := make([]resultadoHTTPConcurrente, cantidad)
	inicio := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(cantidad)
	for i := 0; i < cantidad; i++ {
		go func(indice int) {
			defer wg.Done()
			<-inicio
			resultados[indice] = operacion(indice)
		}(i)
	}
	close(inicio)
	wg.Wait()
	return resultados
}

func ejecutarHTTPConcurrente(handler http.Handler, req *http.Request) resultadoHTTPConcurrente {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resultado := resultadoHTTPConcurrente{Codigo: rec.Code, Texto: rec.Body.String()}
	resultado.Err = json.Unmarshal(rec.Body.Bytes(), &resultado.Cuerpo)
	return resultado
}

func TestConcurrencia_DosPublicacionesDejanUnaSolaVersionActiva(t *testing.T) {
	tabsHandler, calculadoraID := crearCalculadoraTabsPrueba(t)
	tabID := "TEST-COMP-CONC-TAB-" + sufijoUnico()
	postCatalogos(t, tabsHandler.GuardarTab, "/api/cotizador/tabs", map[string]any{
		"tab_id": tabID, "calculadora_id": calculadoraID, "nombre": "Publicación concurrente", "activo": true,
	})
	postCatalogos(t, tabsHandler.GuardarElemento, "/api/cotizador/elementos", map[string]any{
		"elemento_id": "TEST-COMP-CONC-EL-" + sufijoUnico(), "tab_id": tabID,
		"tipo": "CAMPO", "etiqueta": "Nombre", "orden": 1, "activo": true,
	})

	handler := &CompiladorHandler{DB: tabsHandler.DB}
	body, err := json.Marshal(map[string]any{"calculadora_id": calculadoraID})
	if err != nil {
		t.Fatal(err)
	}
	resultados := ejecutarEnParalelo(2, func(_ int) resultadoHTTPConcurrente {
		req := httptest.NewRequest(http.MethodPost, "/api/cotizador/compilar", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return ejecutarHTTPConcurrente(http.HandlerFunc(handler.Compilar), req)
	})
	for i, resultado := range resultados {
		if resultado.Err != nil {
			t.Fatalf("respuesta %d no fue JSON: %v: %s", i, resultado.Err, resultado.Texto)
		}
		if resultado.Codigo != http.StatusOK || resultado.Cuerpo["ok"] != true || resultado.Cuerpo["compilado"] != true {
			t.Fatalf("publicación %d inesperada: status=%d body=%s", i, resultado.Codigo, resultado.Texto)
		}
	}

	var total, activas, anteriores, versiones int
	err = tabsHandler.DB.QueryRow(context.Background(), `
		SELECT COUNT(*)::int,
		       COUNT(*) FILTER (WHERE estado='ACTIVA')::int,
		       COUNT(*) FILTER (WHERE estado='ANTERIOR')::int,
		       COUNT(DISTINCT version)::int
		  FROM cotizadores_compilados WHERE calculadora_id=$1`, calculadoraID,
	).Scan(&total, &activas, &anteriores, &versiones)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || activas != 1 || anteriores != 1 || versiones != 2 {
		t.Fatalf("versionado concurrente inconsistente: total=%d activas=%d anteriores=%d versiones=%d", total, activas, anteriores, versiones)
	}
}

func TestConcurrencia_DosConversionesDeLaMismaSolicitudCreanUnaCotizacion(t *testing.T) {
	pool := setupTestDB(t)
	cotizacionesHandler := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizacionesHandler}
	actor := crearAdminActorPrueba(t, pool)
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente conversión concurrente", clienteID, calculadoraID, "Nueva")

	router := chi.NewRouter()
	router.Post("/api/solicitudes/{id}/convertir", handler.Convertir)
	resultados := ejecutarEnParalelo(2, func(_ int) resultadoHTTPConcurrente {
		req := httptest.NewRequest(http.MethodPost, "/api/solicitudes/"+solicitudID+"/convertir", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Content-Type", "application/json")
		return ejecutarHTTPConcurrente(router, conActor(req, actor))
	})

	exitos, conflictos := 0, 0
	var cotizacionID string
	for i, resultado := range resultados {
		if resultado.Err != nil {
			t.Fatalf("respuesta %d no fue JSON: %v: %s", i, resultado.Err, resultado.Texto)
		}
		switch resultado.Codigo {
		case http.StatusOK:
			exitos++
			cotizacionID, _ = resultado.Cuerpo["cotizacion_id"].(string)
		case http.StatusConflict:
			conflictos++
			mensaje, _ := resultado.Cuerpo["error"].(string)
			if !strings.Contains(mensaje, "ya fue convertida") {
				t.Fatalf("el conflicto no explicó la conversión previa: %s", resultado.Texto)
			}
		default:
			t.Fatalf("conversión %d inesperada: status=%d body=%s", i, resultado.Codigo, resultado.Texto)
		}
	}
	if exitos != 1 || conflictos != 1 || cotizacionID == "" {
		t.Fatalf("esperaba un éxito y un conflicto: éxitos=%d conflictos=%d resultados=%+v", exitos, conflictos, resultados)
	}

	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID)
		pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, cotizacionID)
	})
	var estado, cotizacionGenerada string
	if err := pool.QueryRow(context.Background(), `SELECT estado,cotizacion_id_generada FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID).Scan(&estado, &cotizacionGenerada); err != nil {
		t.Fatal(err)
	}
	var creadas int
	comentario := "Cotización creada a partir de la solicitud " + solicitudID + "."
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*)::int FROM cotizacion_historial WHERE comentario=$1`, comentario).Scan(&creadas); err != nil {
		t.Fatal(err)
	}
	if estado != "Convertida" || cotizacionGenerada != cotizacionID || creadas != 1 {
		t.Fatalf("conversión final inconsistente: estado=%s solicitud.cotizacion=%s respuesta.cotizacion=%s creadas=%d", estado, cotizacionGenerada, cotizacionID, creadas)
	}
}

func TestConcurrencia_CodigosOfertaSonUnicosBajoCarga(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)
	body, err := json.Marshal(map[string]any{"cliente_id": clienteID, "calculadora_id": calculadoraID, "tipo_propuesta": "Carga concurrente"})
	if err != nil {
		t.Fatal(err)
	}

	const cantidad = 20
	resultados := ejecutarEnParalelo(cantidad, func(_ int) resultadoHTTPConcurrente {
		req := httptest.NewRequest(http.MethodPost, "/api/cotizaciones", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return ejecutarHTTPConcurrente(http.HandlerFunc(handler.Crear), conActor(req, actor))
	})

	ids := make([]string, 0, cantidad)
	codigos := make(map[string]bool, cantidad)
	for i, resultado := range resultados {
		if resultado.Err != nil {
			t.Fatalf("respuesta %d no fue JSON: %v: %s", i, resultado.Err, resultado.Texto)
		}
		if resultado.Codigo != http.StatusCreated || resultado.Cuerpo["ok"] != true {
			t.Fatalf("alta %d inesperada: status=%d body=%s", i, resultado.Codigo, resultado.Texto)
		}
		id, _ := resultado.Cuerpo["cotizacion_id"].(string)
		codigo, _ := resultado.Cuerpo["codigo_oferta"].(string)
		if id == "" || codigo == "" || codigos[codigo] {
			t.Fatalf("id/código vacío o repetido en alta %d: id=%q código=%q", i, id, codigo)
		}
		ids = append(ids, id)
		codigos[codigo] = true
	}
	t.Cleanup(func() {
		for _, id := range ids {
			pool.Exec(context.Background(), `DELETE FROM cotizaciones WHERE cotizacion_id=$1`, id)
		}
	})

	var filas, distintos int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*)::int,COUNT(DISTINCT codigo_oferta)::int FROM cotizaciones WHERE cotizacion_id=ANY($1)`, ids).Scan(&filas, &distintos); err != nil {
		t.Fatal(err)
	}
	if filas != cantidad || distintos != cantidad || len(codigos) != cantidad {
		t.Fatalf("códigos bajo carga no fueron únicos: filas=%d distintosBD=%d distintosRespuesta=%d", filas, distintos, len(codigos))
	}
}

func TestConcurrencia_DosSesionesEditanUnaPlantillaSinMezclarCambios(t *testing.T) {
	e := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, e, "Plantilla antes de concurrencia", nil)
	bodyA, err := json.Marshal(map[string]any{
		"nombre": "Plantilla concurrente A", "descripcion": "Paquete completo A",
		"calculadora_ids": []string{e.calculadora}, "tipos_propuesta": []string{"TIPO-A"},
		"organizacion_id": e.organizacion, "disponible_nuevas_propuestas": true, "permite_duplicar": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	bodyB, err := json.Marshal(map[string]any{
		"nombre": "Plantilla concurrente B", "descripcion": "Paquete completo B",
		"calculadora_ids": []string{e.calculadora}, "tipos_propuesta": []string{"TIPO-B"},
		"organizacion_id": e.organizacion, "disponible_nuevas_propuestas": false, "permite_duplicar": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cuerpos := [][]byte{bodyA, bodyB}
	router := chi.NewRouter()
	router.Patch("/api/plantillas/{id}", e.plantillas.Editar)
	resultados := ejecutarEnParalelo(2, func(indice int) resultadoHTTPConcurrente {
		req := httptest.NewRequest(http.MethodPatch, "/api/plantillas/"+plantillaID, bytes.NewReader(cuerpos[indice]))
		req.Header.Set("Content-Type", "application/json")
		return ejecutarHTTPConcurrente(router, req)
	})
	for i, resultado := range resultados {
		if resultado.Err != nil || resultado.Codigo != http.StatusOK {
			t.Fatalf("edición %d falló: err=%v status=%d body=%s", i, resultado.Err, resultado.Codigo, resultado.Texto)
		}
	}

	var nombre, descripcion, tipos string
	var disponible, duplicable bool
	err = e.pool.QueryRow(context.Background(), `
		SELECT p.nombre,p.descripcion,p.disponible_nuevas_propuestas,p.permite_duplicar,
		       COALESCE(string_agg(pt.tipo_propuesta,',' ORDER BY pt.tipo_propuesta),'')
		  FROM plantillas p
		  LEFT JOIN plantilla_tipos_propuesta pt ON pt.plantilla_id=p.plantilla_id
		 WHERE p.plantilla_id::text=$1
		 GROUP BY p.plantilla_id`, plantillaID,
	).Scan(&nombre, &descripcion, &disponible, &duplicable, &tipos)
	if err != nil {
		t.Fatal(err)
	}
	esA := nombre == "Plantilla concurrente A" && descripcion == "Paquete completo A" && disponible && !duplicable && tipos == "TIPO-A"
	esB := nombre == "Plantilla concurrente B" && descripcion == "Paquete completo B" && !disponible && duplicable && tipos == "TIPO-B"
	if !esA && !esB {
		t.Fatalf("la plantilla terminó mezclando paquetes concurrentes: nombre=%q descripción=%q disponible=%t duplicable=%t tipos=%q", nombre, descripcion, disponible, duplicable, tipos)
	}
}

func TestConcurrencia_DosSesionesEditanUnUsuarioSinMezclarCambios(t *testing.T) {
	pool := setupTestDB(t)
	handler := &UsuariosHandler{DB: pool}
	adminA := crearAdminActorPrueba(t, pool)
	adminB := crearAdminActorPrueba(t, pool)
	usuarioID := crearUsuarioPrueba(t, pool, "antes.concurrente."+sufijoUnico()+"@exceltecgroup.com", "1234", "Vendedor", "Activo")
	correoA := "usuario.concurrente.a." + sufijoUnico() + "@exceltecgroup.com"
	correoB := "usuario.concurrente.b." + sufijoUnico() + "@exceltecgroup.com"
	bodyA, err := json.Marshal(map[string]any{"nombre": "Usuario concurrente A", "correo": correoA, "rol": "Vendedor", "estado": "Activo"})
	if err != nil {
		t.Fatal(err)
	}
	bodyB, err := json.Marshal(map[string]any{"nombre": "Usuario concurrente B", "correo": correoB, "rol": "Gerente Comercial", "estado": "Inactivo"})
	if err != nil {
		t.Fatal(err)
	}
	cuerpos := [][]byte{bodyA, bodyB}
	actores := []string{adminA, adminB}
	router := chi.NewRouter()
	router.Patch("/api/usuarios/{id}", handler.Editar)
	resultados := ejecutarEnParalelo(2, func(indice int) resultadoHTTPConcurrente {
		req := httptest.NewRequest(http.MethodPatch, "/api/usuarios/"+usuarioID, bytes.NewReader(cuerpos[indice]))
		req.Header.Set("Content-Type", "application/json")
		return ejecutarHTTPConcurrente(router, conActor(req, actores[indice]))
	})
	for i, resultado := range resultados {
		if resultado.Err != nil || resultado.Codigo != http.StatusOK {
			t.Fatalf("edición %d falló: err=%v status=%d body=%s", i, resultado.Err, resultado.Codigo, resultado.Texto)
		}
	}

	var nombre, correo, rol, estado string
	if err := pool.QueryRow(context.Background(), `SELECT nombre,correo,rol,estado FROM usuarios WHERE usuario_id=$1`, usuarioID).Scan(&nombre, &correo, &rol, &estado); err != nil {
		t.Fatal(err)
	}
	esA := nombre == "Usuario concurrente A" && correo == correoA && rol == "Vendedor" && estado == "Activo"
	esB := nombre == "Usuario concurrente B" && correo == correoB && rol == "Gerente Comercial" && estado == "Inactivo"
	if !esA && !esB {
		t.Fatalf("el usuario terminó mezclando paquetes concurrentes: nombre=%q correo=%q rol=%q estado=%q", nombre, correo, rol, estado)
	}
}
