"""
Migración inicial: BD_Cotizador_Exceltec.xlsx / BD_Cotizador_Parametros.xlsx
                    -> PostgreSQL (esquema 0001_init_schema.sql)

Alcance actual: Organizaciones, Roles (ya sembrados por SQL, se omiten),
Usuarios, Calculadoras, Clientes, Cliente_Contactos, Catalogos,
Catalogo_Valores, Catalogo_Relaciones.

Este script cubre las tablas del Sprint 0/1. A medida que se agreguen
migraciones nuevas (Diseñador, Cotizaciones, Plantillas), se agregan
funciones nuevas acá siguiendo el mismo patrón.

IMPORTANTE — seguridad:
La hoja "Usuarios" trae "pin_acceso" en texto plano. Este script NUNCA
inserta ese valor tal cual: lo hashea con bcrypt antes de guardar en
"pin_hash". Si se necesita el PIN original para pruebas, pedirlo a
Jimmy/Daniel fuera de este repositorio.

Uso:
    python migrate_sheets_to_postgres.py \
        --exceltec ./data/BD_Cotizador_Exceltec.xlsx \
        --parametros ./data/BD_Cotizador_Parametros.xlsx
"""

from __future__ import annotations

import argparse
import os
import sys
from typing import Any

import bcrypt
import pandas as pd
import psycopg
from dotenv import load_dotenv

load_dotenv()


def get_connection() -> psycopg.Connection:
    database_url = os.environ.get("DATABASE_URL")
    if not database_url:
        sys.exit("ERROR: falta la variable de entorno DATABASE_URL")
    return psycopg.connect(database_url)


def read_sheet(path: str, sheet_name: str) -> pd.DataFrame:
    df = pd.read_excel(path, sheet_name=sheet_name)
    # Normaliza NaN -> None para que psycopg inserte NULL correctamente.
    return df.where(pd.notnull(df), None)


def hash_pin(pin_value: Any) -> str:
    """Convierte un PIN en texto plano/numérico a un hash bcrypt."""
    pin_str = str(int(pin_value)) if isinstance(pin_value, float) else str(pin_value)
    return bcrypt.hashpw(pin_str.encode("utf-8"), bcrypt.gensalt()).decode("utf-8")


def migrar_organizaciones(conn: psycopg.Connection, path: str) -> None:
    df = read_sheet(path, "Organizaciones")
    with conn.cursor() as cur:
        for _, row in df.iterrows():
            cur.execute(
                """
                INSERT INTO organizaciones
                    (organizacion_id, nombre, razon_social, identificacion_fiscal,
                     correo, telefono, sitio_web, logo_url, zona_horaria,
                     moneda_base, estado)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (organizacion_id) DO NOTHING
                """,
                (
                    row.get("organizacion_id"), row.get("nombre"),
                    row.get("razon_social"), row.get("identificacion_fiscal"),
                    row.get("correo"), row.get("telefono"), row.get("sitio_web"),
                    row.get("logo_url"), row.get("zona_horaria"),
                    row.get("moneda_base"), row.get("estado") or "Activo",
                ),
            )
    conn.commit()
    print(f"  organizaciones: {len(df)} filas procesadas")


def migrar_usuarios(conn: psycopg.Connection, path: str) -> None:
    df = read_sheet(path, "Usuarios")
    with conn.cursor() as cur:
        for _, row in df.iterrows():
            pin_hash = hash_pin(row.get("pin_acceso") or "0000")
            cur.execute(
                """
                INSERT INTO usuarios
                    (usuario_id, nombre, correo, pin_hash, rol, estado,
                     puede_ver_gestor, ultimo_acceso, fecha_creacion)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (usuario_id) DO NOTHING
                """,
                (
                    row.get("usuario_id"), row.get("nombre"), row.get("correo"),
                    pin_hash, row.get("rol"), row.get("estado") or "Activo",
                    bool(row.get("puede_ver_gestor")), row.get("ultimo_acceso"),
                    row.get("fecha_creacion"),
                ),
            )
    conn.commit()
    print(f"  usuarios: {len(df)} filas procesadas (PINs hasheados con bcrypt)")


def migrar_calculadoras(conn: psycopg.Connection, path: str) -> None:
    df = read_sheet(path, "Calculadoras")
    with conn.cursor() as cur:
        for _, row in df.iterrows():
            cur.execute(
                """
                INSERT INTO calculadoras
                    (calculadora_id, nombre_calculadora, linea_negocio, servicio_base,
                     version_actual, estado, url_html, descripcion, fecha_creacion)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (calculadora_id) DO NOTHING
                """,
                (
                    row.get("calculadora_id"), row.get("nombre_calculadora"),
                    row.get("linea_negocio"), row.get("servicio_base"),
                    row.get("version_actual"), row.get("estado") or "Activo",
                    row.get("url_html"), row.get("descripcion"),
                    row.get("fecha_creacion"),
                ),
            )
    conn.commit()
    print(f"  calculadoras: {len(df)} filas procesadas")


def migrar_clientes(conn: psycopg.Connection, path: str) -> None:
    df = read_sheet(path, "Clientes")
    with conn.cursor() as cur:
        for _, row in df.iterrows():
            cur.execute(
                """
                INSERT INTO clientes
                    (cliente_id, organizacion_id, origen, crm_conexion_id, crm_tipo,
                     crm_id, tipo_persona, nombre_comercial, razon_social,
                     identificacion, industria, pais, provincia, ciudad, direccion,
                     sitio_web, estado, usuario_creador_id, fecha_creacion)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s,
                        %s, %s, %s, %s)
                ON CONFLICT (cliente_id) DO NOTHING
                """,
                (
                    row.get("cliente_id"), row.get("organizacion_id"), row.get("origen"),
                    row.get("crm_conexion_id"), row.get("crm_tipo"), row.get("crm_id"),
                    row.get("tipo_persona"), row.get("nombre_comercial"),
                    row.get("razon_social"), row.get("identificacion"),
                    row.get("industria"), row.get("pais"), row.get("provincia"),
                    row.get("ciudad"), row.get("direccion"), row.get("sitio_web"),
                    row.get("estado") or "Activo", row.get("usuario_creador_id"),
                    row.get("fecha_creacion"),
                ),
            )
    conn.commit()
    print(f"  clientes: {len(df)} filas procesadas")


def migrar_catalogos(conn: psycopg.Connection, path: str) -> None:
    df = read_sheet(path, "Catalogos")
    with conn.cursor() as cur:
        for _, row in df.iterrows():
            cur.execute(
                """
                INSERT INTO catalogos
                    (catalogo_id, nombre_catalogo, descripcion, alcance,
                     catalogo_padre_id, orden, activo)
                VALUES (%s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (catalogo_id) DO NOTHING
                """,
                (
                    row.get("catalogo_id"), row.get("nombre_catalogo"),
                    row.get("descripcion"), row.get("alcance"),
                    row.get("catalogo_padre_id"), row.get("orden") or 0,
                    bool(row.get("activo", True)),
                ),
            )
    conn.commit()
    print(f"  catalogos: {len(df)} filas procesadas")


def migrar_catalogo_valores(conn: psycopg.Connection, path: str) -> None:
    df = read_sheet(path, "Catalogo_Valores")
    with conn.cursor() as cur:
        for _, row in df.iterrows():
            cur.execute(
                """
                INSERT INTO catalogo_valores
                    (valor_id, catalogo_id, clave, texto_visible, valor_sistema,
                     descripcion, valor_padre_id, orden, activo)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (valor_id) DO NOTHING
                """,
                (
                    row.get("valor_id"), row.get("catalogo_id"), row.get("clave"),
                    row.get("texto_visible"), row.get("valor_sistema"),
                    row.get("descripcion"), row.get("valor_padre_id"),
                    row.get("orden") or 0, bool(row.get("activo", True)),
                ),
            )
    conn.commit()
    print(f"  catalogo_valores: {len(df)} filas procesadas")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--exceltec", required=True, help="Ruta a BD_Cotizador_Exceltec.xlsx")
    parser.add_argument("--parametros", required=True, help="Ruta a BD_Cotizador_Parametros.xlsx")
    args = parser.parse_args()

    conn = get_connection()
    try:
        print("Migrando desde BD_Cotizador_Exceltec.xlsx...")
        migrar_organizaciones(conn, args.exceltec)
        migrar_usuarios(conn, args.exceltec)
        migrar_calculadoras(conn, args.exceltec)
        migrar_clientes(conn, args.exceltec)

        print("Migrando desde BD_Cotizador_Parametros.xlsx...")
        migrar_catalogos(conn, args.parametros)
        migrar_catalogo_valores(conn, args.parametros)

        print("Migración Sprint 0/1 completada.")
    finally:
        conn.close()


if __name__ == "__main__":
    main()
