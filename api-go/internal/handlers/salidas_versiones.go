package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func copiarVersionRuntime(ctx context.Context, tx pgx.Tx, id string, origen, destino int) error {
	rows, err := tx.Query(ctx, `SELECT opcion_id FROM cotizacion_opciones WHERE cotizacion_id=$1 AND numero_version=$2 ORDER BY opcion_id`, id, origen)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, op)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	reemplazos := map[string]string{}
	for _, op := range ids {
		var nueva string
		if err := tx.QueryRow(ctx, `INSERT INTO cotizacion_opciones(cotizacion_id,numero_version,elemento_padre_id,nombre,es_recomendada,orden,total_precio,total_costo,total_ganancia,margen_total,moneda,tipo_cambio,subtotal,descuento,impuestos)
			SELECT cotizacion_id,$2,elemento_padre_id,nombre,es_recomendada,orden,total_precio,total_costo,total_ganancia,margen_total,moneda,tipo_cambio,subtotal,descuento,impuestos FROM cotizacion_opciones WHERE opcion_id=$1 RETURNING opcion_id`, op, destino).Scan(&nueva); err != nil {
			return err
		}
		reemplazos[op] = nueva
		if _, err := tx.Exec(ctx, `INSERT INTO cotizacion_valores(cotizacion_id,version,elemento_id,opcion_id,valor) SELECT cotizacion_id,$2,elemento_id,$3,valor FROM cotizacion_valores WHERE opcion_id=$1`, op, destino, nueva); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cotizacion_valores(cotizacion_id,version,elemento_id,valor) SELECT cotizacion_id,$3,elemento_id,valor FROM cotizacion_valores WHERE cotizacion_id=$1 AND version=$2 AND opcion_id IS NULL`, id, origen, destino); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cotizacion_salidas(cotizacion_id,numero_version,clave_salida,fuente_id,tipo_dato,valor_numero,valor_texto,valor_booleano,valor_fecha,valor_visible,moneda)
		SELECT cotizacion_id,$3,clave_salida,fuente_id,tipo_dato,valor_numero,valor_texto,valor_booleano,valor_fecha,valor_visible,moneda FROM cotizacion_salidas WHERE cotizacion_id=$1 AND numero_version=$2`, id, origen, destino); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cotizacion_items(cotizacion_id,numero_version,fuente_id,opcion_id,categoria,descripcion,cantidad,precio_unitario,total_precio,total_costo,moneda,detalle_json,orden)
		SELECT cotizacion_id,$3,fuente_id,opcion_id,categoria,descripcion,cantidad,precio_unitario,total_precio,total_costo,moneda,detalle_json,orden FROM cotizacion_items WHERE cotizacion_id=$1 AND numero_version=$2`, id, origen, destino); err != nil {
		return err
	}
	for viejo, nuevo := range reemplazos {
		if _, err := tx.Exec(ctx, `UPDATE cotizacion_items SET opcion_id=$3 WHERE cotizacion_id=$1 AND numero_version=$2 AND opcion_id=$4`, id, destino, nuevo, viejo); err != nil {
			return err
		}
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT snapshot_json FROM cotizacion_versiones WHERE cotizacion_id=$1 AND numero_version=$2`, id, origen).Scan(&raw); err != nil {
		return err
	}
	if len(raw) > 0 {
		var datos map[string]any
		if err := json.Unmarshal(raw, &datos); err != nil {
			return err
		}
		datos = remapearOpcionesSnapshot(datos, reemplazos).(map[string]any)
		datos["numero_version"] = destino
		nuevo, err := json.Marshal(datos)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE cotizacion_versiones SET snapshot_json=$3 WHERE cotizacion_id=$1 AND numero_version=$2`, id, destino, nuevo); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE cotizacion_versiones d SET tipo_cambio=o.tipo_cambio,subtotal=o.subtotal,descuento=o.descuento,impuestos=o.impuestos FROM cotizacion_versiones o WHERE d.cotizacion_id=$1 AND d.numero_version=$3 AND o.cotizacion_id=$1 AND o.numero_version=$2`, id, origen, destino)
	return err
}

func remapearOpcionesSnapshot(v any, ids map[string]string) any {
	switch valor := v.(type) {
	case map[string]any:
		nuevo := map[string]any{}
		for k, d := range valor {
			if id, ok := ids[k]; ok {
				k = id
			}
			nuevo[k] = remapearOpcionesSnapshot(d, ids)
		}
		return nuevo
	case []any:
		for i, d := range valor {
			valor[i] = remapearOpcionesSnapshot(d, ids)
		}
		return valor
	case string:
		if id, ok := ids[valor]; ok {
			return id
		}
	}
	return v
}

// tabsPublicasSnapshot conserva la forma pública histórica sin exponer el
// bloque privado fuentes_externas, sus costos ni las salidas internas.
func tabsPublicasSnapshot(s snapshotCotizacion) []*enlacePublicoTab {
	resultado := []*enlacePublicoTab{}
	metas := indexarElementosRuntime(s.Estructura)
	els := indexarElementosCompletoRuntime(s.Estructura)
	tabs, _ := s.Estructura["tabs"].([]any)
	for _, raw := range tabs {
		tab, _ := raw.(map[string]any)
		orden, _ := numeroDesdeValor(tab["orden"])
		t := &enlacePublicoTab{TabID: fmt.Sprint(tab["tab_id"]), Nombre: fmt.Sprint(tab["nombre"]), Orden: int(orden), Elementos: []enlacePublicoElemento{}}
		var recorrer func([]any)
		recorrer = func(elementos []any) {
			for _, raw := range elementos {
				el, _ := raw.(map[string]any)
				id := fmt.Sprint(el["elemento_id"])
				tipo := fmt.Sprint(el["tipo"])
				cfg, _ := el["configuracion"].(map[string]any)
				if !boolDesdeConfiguracion(cfg, "visible_oferta", true) {
					continue
				}
				orden, _ := numeroDesdeValor(el["orden"])
				etiqueta, _ := el["etiqueta"].(string)
				valor := valorFuenteSalida(id, els, metas, s.Valores, s.OpcionesEfectivas)
				if tipo == "LEYENDA" || tipo == "TEXTO_INFORMATIVO" || tipo == "TITULO" {
					valor = etiqueta
				}
				if tipo == "CAMPO_CATALOGO" {
					for _, dato := range s.FuentesExternas[id] {
						if dato["valor_sistema"] == valor {
							valor = dato["texto_visible"]
							break
						}
					}
				}
				t.Elementos = append(t.Elementos, enlacePublicoElemento{ElementoID: id, Tipo: tipo, Etiqueta: etiqueta, Orden: int(orden), Valor: valor})
				if hijos, ok := el["hijos"].([]any); ok {
					recorrer(hijos)
				}
			}
		}
		elementos, _ := tab["elementos"].([]any)
		recorrer(elementos)
		resultado = append(resultado, t)
	}
	return resultado
}
