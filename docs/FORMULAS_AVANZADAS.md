# Campo Calculado: fórmula avanzada

En el modal del Diseñador, seleccione **Tipo de fórmula → Avanzada**.
Use «Agregar campo», los operadores y «Agregar valor», o escriba directamente.
Los errores del servidor aparecen junto al editor; una fórmula inválida no se guarda.
El modo Simple conserva su comportamiento anterior. No se requiere migración.

## Sintaxis permitida

```text
SI(USA_TELEFONIA; MINUTOS_MES * COSTO_MINUTO_VOZ; 0)
COSTO_CHAT / (1 - MARGEN_CHAT)
SI(USA_TELEFONIA; PRECIO_VOZ; SI(USA_CHAT; PRECIO_CHAT; 0))
```

- Operadores: `+`, `-`, `*`, `/` y paréntesis, con precedencia aritmética normal.
- Números decimales con punto, sin notación exponencial. Para negativos: `(0 - 5)`.
- La única función es `SI(token; entonces; sino)`. Se admiten ramas anidadas.
- `Sí`, `SI`, `true` y `1` significan verdadero (ignorando mayúsculas y espacios extremos); cualquier otro valor significa falso. No hay comparaciones ni scripting.
- Solo se evalúa la rama elegida: los campos aún vacíos de la otra rama no bloquean el cálculo.
- Máximo: 4096 caracteres y 64 niveles de profundidad.

## Referencias y valores

Cada token es el **nombre interno**, exacto y sensible a mayúsculas, de otro elemento activo de la misma sección. Use letras ASCII, números y guion bajo; comience por letra o guion bajo. `SI` es reservado.

Se persiste en `configuracion.nombre_interno`, admitiendo el legado `nombre_elemento`. No se usan etiquetas ni IDs como tokens. Los nombres ambiguos, las referencias inexistentes y los ciclos (también entre Simple y Avanzada) se rechazan.

En aritmética se admiten Campo numérico/moneda/porcentaje, Campo Catálogo con valor de cálculo, Campo Calculado, Lista de Precios y Tabla. Se reutiliza el resolutor del modo Simple: la selección de catálogo se convierte automáticamente en `valor_calculo` (por ejemplo, `M30 → 0.30`), la lista aporta su total y la tabla su total numérico. No escriba `.VALOR_CALCULO`.

Un Campo textual o un catálogo descriptivo puede usarse **solo como condición** de `SI`, leyendo su selección original Sí/No. Los campos dentro de Opciones de Propuesta usan los valores de cada opción por separado.

División entre cero, valores numéricos faltantes o desbordamientos dejan `valor_resuelto: null`, igual que el error controlado del modo Simple. No se inventa un cero.

## API y publicación

Al guardar `CAMPO_CALCULADO`, envíe en `configuracion`:

```json
{"nombre_interno":"PRECIO_CHAT","tipo_formula":"AVANZADA","formula_texto":"COSTO_CHAT / (1 - MARGEN_CHAT)","tipo_resultado":"MONEDA","decimales":2}
```

El servidor deriva `tokens`, `tokens_condicion`, `operandos` (IDs) y `tokens_operandos` (nombre → ID); no confía en listas enviadas por el cliente. La publicación revalida referencias y congela estos datos y los valores de catálogo. Renombrar un campo después no altera las versiones ya publicadas.

## Verificación

```bash
python3 frontend/_build/assemble.py
cd api-go
go test ./internal/handlers -run TestFormulaAvanzada -v
go test ./... -race -v
go build ./...
go vet ./...
gofmt -l .
```

Configure `DATABASE_URL` hacia PostgreSQL para las pruebas de integración; sin él se omiten. Las pruebas del parser son independientes de la base. Para explorar entradas malformadas: `go test ./internal/handlers -run '^$' -fuzz FuzzFormulaAvanzada_NoPanic -fuzztime 20s`.
