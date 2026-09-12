package handlers

// Auditoría de QA — dimensión 8: Transactions & Rollback.
//
// Cada prueba de acá provoca un fallo en una operación multi-tabla y
// confirma que NO quedaron datos a medias. Hay dos clases de prueba, y
// la distinción importa para leer el resultado:
//
//   - ROLLBACK REAL: el fallo ocurre DESPUÉS de que la transacción ya
//     escribió filas, así que la prueba ejercita de verdad el
//     `defer tx.Rollback(ctx)`. Son las que empiezan con un comentario
//     "rollback real".
//   - VALIDACIÓN PREVIA: el handler valida todo ANTES del BeginTx, así
//     que nunca hay nada que revertir. La prueba sigue siendo útil
//     (fija el invariante "un pedido rechazado no escribe nada"), pero
//     NO prueba el rollback. Van marcadas como tal para no dar una
//     sensación falsa de cobertura.
//
// Las costuras de fallo usadas son todas alcanzables desde el handler
// público, sin tocar código de producción ni romper el esquema a
// mitad: valores padre que no pertenecen al catálogo, una plantilla
// base inexistente, y un actor de sesión que no existe en `usuarios`
// (viola la FK de `cotizacion_usuarios.usuario_id` /
// `clientes.usuario_creador_id`).

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// txContar corre un COUNT(*) y devuelve el número, fallando el test si
// la consulta revienta. Prefijo tx- para no chocar con helpers de otros
// archivos de prueba del paquete.
func txContar(t *testing.T, pool *pgxpool.Pool, consulta string, args ...any) int {
	t.Helper()
	var total int
	if err := pool.QueryRow(context.Background(), consulta, args...).Scan(&total); err != nil {
		t.Fatalf("no se pudo contar filas (%s): %v", consulta, err)
	}
	return total
}

// txActorInexistente devuelve un id de usuario con forma válida que NO
// existe en `usuarios`. Usarlo como actor de sesión hace fallar la
// escritura que tiene FK contra usuarios, que es la costura que
// necesitan las pruebas de rollback real de cotizaciones/solicitudes.
func txActorInexistente() string {
	return "test-usr-inexistente-" + sufijoUnico()
}

// ---------------------------------------------------------------
// 1. catalogos.GuardarRelaciones — rollback real
// ---------------------------------------------------------------

// TestTransaccionGuardarRelaciones_ValorPadreAjenoRevierteValorYRelaciones
// es la costura más limpia del repo: el handler hace upsert del valor y
// borra las relaciones viejas ANTES de validar, dentro del loop, que
// cada valor padre pertenezca al catálogo indicado. Con dos padres —el
// primero válido, el segundo ajeno— el fallo cae en la segunda
// iteración, cuando ya se escribió el upsert, el DELETE y un INSERT.
func TestTransaccionGuardarRelaciones_ValorPadreAjenoRevierteValorYRelaciones(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CatalogosHandler{DB: pool}

	catalogoPadre := crearCatalogoPrueba(t, pool, "Padre relaciones tx", "")
	catalogoHijo := crearCatalogoPrueba(t, pool, "Hijo relaciones tx", "")
	catalogoAjeno := crearCatalogoPrueba(t, pool, "Ajeno relaciones tx", "")

	valorPadreViejo := crearValorCatalogoPrueba(t, pool, catalogoPadre, "Padre viejo", "")
	valorPadreNuevo := crearValorCatalogoPrueba(t, pool, catalogoPadre, "Padre nuevo", "")
	valorAjeno := crearValorCatalogoPrueba(t, pool, catalogoAjeno, "Padre de otro catálogo", "")
	valorHijo := crearValorCatalogoPrueba(t, pool, catalogoHijo, "Texto original", "")

	// Relación preexistente del valor hijo: es la que el handler borra
	// al principio de la transacción y la que debe reaparecer al
	// revertir.
	crearRelacionPrueba(t, pool, catalogoPadre, valorPadreViejo, catalogoHijo, valorHijo)

	rec := postCatalogos(t, handler.GuardarRelaciones, "/api/catalogos/relaciones", map[string]any{
		"valor_id":           valorHijo,
		"catalogo_id":        catalogoHijo,
		"clave":              "CLAVE-TX",
		"texto_visible":      "TEXTO PISADO POR LA TX",
		"activo":             true,
		"_catalogo_padre_id": catalogoPadre,
		"_valor_padre_ids":   []string{valorPadreNuevo, valorAjeno},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("valor padre ajeno: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	// (a) el upsert del valor tiene que haberse revertido
	var textoVisible string
	if err := pool.QueryRow(context.Background(), `SELECT texto_visible FROM catalogo_valores WHERE valor_id=$1`, valorHijo).Scan(&textoVisible); err != nil {
		t.Fatalf("no se pudo releer el valor hijo: %v", err)
	}
	if textoVisible != "Texto original" {
		t.Errorf("el upsert del valor quedó escrito pese al 400 (texto_visible=%q): la transacción no revirtió el UPDATE de catalogo_valores", textoVisible)
	}

	// (b) la relación vieja tiene que seguir existiendo
	viejas := txContar(t, pool, `SELECT COUNT(*)::int FROM catalogo_relaciones WHERE valor_hijo_id=$1 AND valor_padre_id=$2`, valorHijo, valorPadreViejo)
	if viejas != 1 {
		t.Errorf("la relación preexistente quedó borrada (%d filas): el DELETE de la transacción no se revirtió", viejas)
	}

	// (c) la relación nueva del primer padre (que sí se insertó antes
	// del fallo) no puede haber quedado
	nuevas := txContar(t, pool, `SELECT COUNT(*)::int FROM catalogo_relaciones WHERE valor_hijo_id=$1 AND valor_padre_id=$2`, valorHijo, valorPadreNuevo)
	if nuevas != 0 {
		t.Errorf("quedaron %d relación(es) nuevas escritas pese al 400: la transacción no revirtió el INSERT", nuevas)
	}

	// (d) el total de relaciones del hijo vuelve a ser exactamente el original
	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM catalogo_relaciones WHERE valor_hijo_id=$1`, valorHijo); total != 1 {
		t.Errorf("el valor hijo quedó con %d relaciones y debía volver a tener 1: la transacción dejó datos a medias", total)
	}
}

// ---------------------------------------------------------------
// 2. plantillas.Crear — rollback real
// ---------------------------------------------------------------

// TestTransaccionCrearPlantilla_BaseInexistenteNoDejaPlantillaNiAsociaciones
// cubre la otra costura post-escritura: Crear inserta la plantilla y
// sus dos tablas de asociaciones y sólo DESPUÉS intenta copiar el
// contenido de `crear_desde`. Con una base inexistente el handler
// responde 404 con tres INSERT ya hechos.
func TestTransaccionCrearPlantilla_BaseInexistenteNoDejaPlantillaNiAsociaciones(t *testing.T) {
	entorno := nuevoEntornoPlantillas(t)
	nombre := "Plantilla tx rollback " + sufijoUnico()

	rec := llamarPlantilla(t, http.MethodPost, "/api/plantillas", "/api/plantillas", entorno.plantillas.Crear, map[string]any{
		"nombre":          nombre,
		"descripcion":     "No debería quedar en la base",
		"calculadora_ids": []string{entorno.calculadora},
		"tipos_propuesta": []string{"COMERCIAL"},
		"organizacion_id": entorno.organizacion,
		"crear_desde":     "00000000-0000-0000-0000-000000000000",
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("crear con base inexistente: esperaba 404, dio %d: %s", rec.Code, rec.Body.String())
	}

	if total := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantillas WHERE nombre=$1`, nombre); total != 0 {
		t.Errorf("quedó %d fila(s) en plantillas pese al 404: la transacción no revirtió el INSERT principal", total)
	}
	// El cotizador y la organización del entorno son únicos por prueba,
	// así que contar asociaciones que los referencien detecta cualquier
	// fila que hubiera quedado confirmada (sea colgada de esta plantilla
	// o de otra creada por el mismo pedido). No se cuentan huérfanas a
	// nivel de tabla: la FK con ON DELETE CASCADE las hace imposibles,
	// y el conteo global dependería de datos de otras pruebas.
	asociaciones := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantilla_calculadoras WHERE calculadora_id=$1`, entorno.calculadora)
	if asociaciones != 0 {
		t.Errorf("quedaron %d fila(s) en plantilla_calculadoras pese al 404: la transacción no revirtió las asociaciones", asociaciones)
	}
	tipos := txContar(t, entorno.pool, `
		SELECT COUNT(*)::int FROM plantilla_tipos_propuesta pt
		  JOIN plantillas p ON p.plantilla_id = pt.plantilla_id
		 WHERE p.organizacion_id=$1`, entorno.organizacion)
	if tipos != 0 {
		t.Errorf("quedaron %d fila(s) en plantilla_tipos_propuesta pese al 404: la transacción no revirtió las asociaciones", tipos)
	}
}

// ---------------------------------------------------------------
// 3. cotizaciones.Crear — rollback real
// ---------------------------------------------------------------

// TestTransaccionCrearCotizacion_ActorInexistenteRevierteCotizacionYVersion
// usa la FK de `cotizacion_usuarios.usuario_id`: con un cliente que ya
// existe, la transacción alcanza a insertar `cotizaciones` y
// `cotizacion_versiones` y revienta recién al asignar el vendedor. Es
// el rollback multi-tabla más importante del sistema (el alta de una
// cotización toca 5 tablas).
func TestTransaccionCrearCotizacion_ActorInexistenteRevierteCotizacionYVersion(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)

	actor := txActorInexistente()
	rec, res := postCrearCotizacion(t, handler, actor, map[string]any{
		"cliente_id":     clienteID,
		"calculadora_id": calculadoraID,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("actor inexistente: esperaba 500 (violación de FK), dio %d: %s", rec.Code, rec.Body.String())
	}
	if res["ok"] != false {
		t.Errorf("la respuesta de error debería traer ok=false: %v", res)
	}

	// Alcanza con verificar la fila padre acotada a ESTE cotizador:
	// cotizacion_versiones, cotizacion_usuarios y cotizacion_historial
	// tienen FK a cotizaciones con ON DELETE CASCADE, así que ninguna
	// puede sobrevivir sin su cotización. Contar filas "huérfanas" a
	// nivel de tabla completa sería una afirmación vacía (el esquema no
	// las permite) y además haría que esta prueba dependiera de datos
	// que dejaran otras.
	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM cotizaciones WHERE calculadora_id=$1`, calculadoraID); total != 0 {
		t.Errorf("quedaron %d cotización(es) escritas pese al 500: la transacción no revirtió el INSERT en cotizaciones (y con ella arrastraría versión, vendedor e historial)", total)
	}
	// El vendedor que disparó la violación tampoco puede haber quedado
	// asociado a ninguna cotización.
	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM cotizacion_usuarios WHERE usuario_id=$1`, actor); total != 0 {
		t.Errorf("quedaron %d fila(s) en cotizacion_usuarios para el actor inexistente", total)
	}
}

// TestTransaccionCrearCotizacion_ActorInexistenteNoDejaClienteHuerfano
// cubre la variante con cliente nuevo: ahí la primera escritura de la
// transacción es el INSERT en `clientes`, que ya viola la FK
// `clientes.usuario_creador_id`. El fallo es más temprano, pero el
// invariante que importa es el mismo: no puede quedar un cliente
// fantasma creado por un alta que falló.
func TestTransaccionCrearCotizacion_ActorInexistenteNoDejaClienteHuerfano(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	calculadoraID, _ := crearBaseAltaCotizacion(t, pool, false)
	nombreCliente := "Cliente tx huérfano " + sufijoUnico()

	rec, _ := postCrearCotizacion(t, handler, txActorInexistente(), map[string]any{
		"cliente_nombre_nuevo": nombreCliente,
		"calculadora_id":       calculadoraID,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("actor inexistente con cliente nuevo: esperaba 500, dio %d: %s", rec.Code, rec.Body.String())
	}

	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM clientes WHERE nombre_comercial=$1`, nombreCliente); total != 0 {
		t.Errorf("quedó %d cliente(s) huérfano(s) de un alta que falló: la transacción no revirtió el INSERT en clientes", total)
	}
	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM cotizaciones WHERE calculadora_id=$1`, calculadoraID); total != 0 {
		t.Errorf("quedaron %d cotización(es) de un alta que falló", total)
	}
}

// ---------------------------------------------------------------
// 4. solicitudes.Convertir — rollback real
// ---------------------------------------------------------------

// TestTransaccionConvertirSolicitud_ActorInexistenteNoMarcaConvertidaNiCreaCotizacion
// es la transacción más ancha del sistema: envuelve todo
// crearCotizacionEnTx MÁS el UPDATE que marca la solicitud como
// Convertida. Con un actor inexistente el alta falla en
// `cotizacion_usuarios` y la solicitud tiene que quedar intacta: si
// quedara marcada Convertida sin cotización detrás, la solicitud sería
// irrecuperable (CambiarEstado rechaza tocar una ya convertida).
func TestTransaccionConvertirSolicitud_ActorInexistenteNoMarcaConvertidaNiCreaCotizacion(t *testing.T) {
	pool := setupTestDB(t)
	cotizaciones := &CotizacionesHandler{DB: pool}
	handler := &SolicitudesHandler{DB: pool, Cotizaciones: cotizaciones}
	calculadoraID, clienteID := crearBaseAltaCotizacion(t, pool, true)
	solicitudID := crearSolicitudPrueba(t, pool, "Cliente solicitud tx", clienteID, calculadoraID, "Nueva")

	rec, _ := postConvertirSolicitud(t, handler, txActorInexistente(), solicitudID, map[string]any{})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("convertir con actor inexistente: esperaba 500, dio %d: %s", rec.Code, rec.Body.String())
	}

	var estado string
	var cotizacionGenerada *string
	if err := pool.QueryRow(context.Background(),
		`SELECT estado, cotizacion_id_generada FROM solicitudes WHERE solicitud_id::text=$1`, solicitudID,
	).Scan(&estado, &cotizacionGenerada); err != nil {
		t.Fatalf("no se pudo releer la solicitud: %v", err)
	}
	if estado != "Nueva" {
		t.Errorf("la solicitud quedó en estado %q pese al 500: la transacción no revirtió el UPDATE y la solicitud queda irrecuperable", estado)
	}
	if cotizacionGenerada != nil {
		t.Errorf("la solicitud quedó apuntando a la cotización %q, que no se creó: la transacción dejó datos a medias", *cotizacionGenerada)
	}
	// Igual que en el alta directa: la fila padre acotada a este
	// cotizador es suficiente, porque versión, vendedor e historial
	// cuelgan de ella con ON DELETE CASCADE.
	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM cotizaciones WHERE calculadora_id=$1`, calculadoraID); total != 0 {
		t.Errorf("quedaron %d cotización(es) de una conversión que falló: la transacción no revirtió el alta", total)
	}
}

// ---------------------------------------------------------------
// 5. Validación previa al BeginTx (no ejercitan rollback)
// ---------------------------------------------------------------

// TestTransaccionEditarPlantilla_CotizadorInexistenteDejaAsociacionesIntactas
// VALIDACIÓN PREVIA: `Editar` corre validarReferenciasPlantilla antes
// de cualquier UPDATE/DELETE, así que no hay nada que revertir. La
// prueba fija el invariante de todos modos: un pedido rechazado no
// puede haber tocado las asociaciones que ya estaban.
func TestTransaccionEditarPlantilla_CotizadorInexistenteDejaAsociacionesIntactas(t *testing.T) {
	entorno := nuevoEntornoPlantillas(t)
	plantillaID := crearPlantillaPrueba(t, entorno, "Plantilla tx editar "+sufijoUnico(), nil)

	antes := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantilla_calculadoras WHERE plantilla_id::text=$1`, plantillaID)
	tiposAntes := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantilla_tipos_propuesta WHERE plantilla_id::text=$1`, plantillaID)

	rec := llamarPlantilla(t, http.MethodPatch, "/api/plantillas/{id}", "/api/plantillas/"+plantillaID, entorno.plantillas.Editar, map[string]any{
		"calculadora_ids": []string{entorno.calculadora, "TEST-CALC-QUE-NO-EXISTE"},
		"tipos_propuesta": []string{"TECNICA"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("editar con cotizador inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	if despues := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantilla_calculadoras WHERE plantilla_id::text=$1`, plantillaID); despues != antes {
		t.Errorf("las asociaciones de cotizadores pasaron de %d a %d en una edición rechazada: quedaron datos a medias", antes, despues)
	}
	if despues := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantilla_tipos_propuesta WHERE plantilla_id::text=$1`, plantillaID); despues != tiposAntes {
		t.Errorf("los tipos de propuesta pasaron de %d a %d en una edición rechazada: quedaron datos a medias", tiposAntes, despues)
	}
	// El cotizador válido del pedido tampoco puede haberse duplicado.
	if total := txContar(t, entorno.pool, `SELECT COUNT(*)::int FROM plantilla_calculadoras WHERE plantilla_id::text=$1 AND calculadora_id=$2`, plantillaID, entorno.calculadora); total != 1 {
		t.Errorf("el cotizador asociado quedó %d veces (esperaba 1) tras una edición rechazada", total)
	}
}

// TestTransaccionGuardarValoresRuntime_ValorInvalidoNoGuardaNiElValido
// VALIDACIÓN PREVIA: GuardarValores valida TODOS los elementos del
// pedido antes del BeginTx, así que un valor inválido corta sin haber
// escrito nada. El invariante que importa —guardado todo-o-nada, sin
// medias tintas entre el valor bueno y el malo— se verifica igual, y
// no depende del orden de iteración del map porque cualquier elemento
// inválido corta el pedido completo.
func TestTransaccionGuardarValoresRuntime_ValorInvalidoNoGuardaNiElValido(t *testing.T) {
	fixture := crearFixtureRuntime(t)

	rec := postValoresRuntime(t, fixture, map[string]any{"version": 1, "valores": map[string]any{
		fixture.CampoID:      "Valor válido que no debe persistir",
		fixture.CatalogoElID: "OPCION_QUE_NO_EXISTE",
	}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("valor de catálogo inválido: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}

	if total := txContar(t, fixture.Handler.DB, `SELECT COUNT(*)::int FROM cotizacion_valores WHERE cotizacion_id=$1`, fixture.CotizacionID); total != 0 {
		t.Errorf("quedaron %d valor(es) guardados pese al 400: el guardado no fue todo-o-nada", total)
	}
	if total := txContar(t, fixture.Handler.DB, `SELECT COUNT(*)::int FROM cotizacion_historial WHERE cotizacion_id=$1 AND accion='valores_actualizados'`, fixture.CotizacionID); total != 0 {
		t.Errorf("se registró %d fila(s) de historial de un guardado que falló", total)
	}
}

// TestTransaccionCrearCotizacion_CotizadorInexistenteNoCreaClienteNuevo
// VALIDACIÓN PREVIA: crearCotizacionEnTx valida el cotizador como
// primera operación de la transacción, antes de resolver o crear el
// cliente. Por eso un cotizador inválido responde 400 sin haber
// escrito nada — el cliente nuevo nunca llega a insertarse.
func TestTransaccionCrearCotizacion_CotizadorInexistenteNoCreaClienteNuevo(t *testing.T) {
	pool := setupTestDB(t)
	handler := &CotizacionesHandler{DB: pool}
	actor := crearAdminActorPrueba(t, pool)
	nombreCliente := "Cliente sin cotizador " + sufijoUnico()

	rec, _ := postCrearCotizacion(t, handler, actor, map[string]any{
		"cliente_nombre_nuevo": nombreCliente,
		"calculadora_id":       "TEST-CALC-QUE-NO-EXISTE",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cotizador inexistente: esperaba 400, dio %d: %s", rec.Code, rec.Body.String())
	}
	if total := txContar(t, pool, `SELECT COUNT(*)::int FROM clientes WHERE nombre_comercial=$1`, nombreCliente); total != 0 {
		t.Errorf("se creó %d cliente(s) para un alta rechazada: el cliente no debería nacer si el cotizador no es válido", total)
	}
}
