package middleware

// Auditoría de QA — dimensión 1 (Unit) del paquete middleware.
//
// Los otros _test.go de este paquete son de integración: necesitan
// Postgres para crear sesiones y claves de API. Este archivo cubre lo
// único que es lógica pura acá —el parseo del header Authorization— y
// por eso corre sin base (env -u DATABASE_URL go test ./internal/middleware/ -run TestUnit).

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUnitTokenDesdeHeader_SoloAceptaElEsquemaBearer(t *testing.T) {
	casos := []struct {
		nombre   string
		header   string
		ponerlo  bool
		esperado string
	}{
		{"Bearer con token", "Bearer abc123", true, "abc123"},
		{"sin header", "", false, ""},
		{"header vacío", "", true, ""},
		{"token sin esquema", "abc123", true, ""},
		{"esquema equivocado", "Basic abc123", true, ""},
		{"solo el esquema", "Bearer ", true, ""},
		{"espacios extra alrededor del token", "Bearer   abc123  ", true, "abc123"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/cotizaciones", nil)
			if caso.ponerlo {
				req.Header.Set("Authorization", caso.header)
			}
			if got := tokenDesdeHeader(req); got != caso.esperado {
				t.Errorf("tokenDesdeHeader(%q) = %q, se esperaba %q", caso.header, got, caso.esperado)
			}
		})
	}
}

// TestUnitTokenDesdeHeader_EsSensibleAMayusculas fija a propósito el
// comportamiento actual: el prefijo se compara literalmente contra
// "Bearer ", así que "bearer abc" (minúscula) se descarta y la
// petición termina en 401.
//
// El RFC 7235 define el esquema como case-insensitive, así que esto es
// más estricto que el estándar. Se fija por prueba para que, si algún
// día un cliente legítimo manda el esquema en minúscula y se decide
// aflojar la comparación, el cambio sea una decisión visible (esta
// prueba falla y se actualiza) y no un descubrimiento en producción.
func TestUnitTokenDesdeHeader_EsSensibleAMayusculas(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/cotizaciones", nil)
	req.Header.Set("Authorization", "bearer abc123")

	if got := tokenDesdeHeader(req); got != "" {
		t.Errorf("tokenDesdeHeader con \"bearer\" en minúscula devolvió %q; "+
			"hoy la comparación es sensible a mayúsculas y el resultado esperado es cadena vacía. "+
			"Si se cambió a propósito para aceptar el esquema case-insensitive (RFC 7235), actualizar esta prueba", got)
	}
}
