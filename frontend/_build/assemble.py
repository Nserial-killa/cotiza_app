#!/usr/bin/env python3
"""
Ensambla el frontend real de Cotiza (Google Apps Script) en un
index.html estático, sin la sintaxis <?!= include(...) ?> de GAS.

Uso (desde cualquier lado, las rutas son relativas a este archivo):
    python3 frontend/_build/assemble.py

Lee de:  frontend/legacy-gas/
Escribe: frontend/index.html

Volver a correr esto cada vez que se edite algo en legacy-gas/ o en
_build/cotiza_base.css / _build/shim.html.
"""
import re
from pathlib import Path

BUILD_DIR = Path(__file__).resolve().parent          # frontend/_build/
FRONTEND = BUILD_DIR.parent                            # frontend/
LEGACY = FRONTEND / "legacy-gas"
OUT = FRONTEND / "index.html"

# Mapeo nombre-de-include-GAS -> archivo real que sí tenemos.
FILE_MAP = {
    "Cotiza_Login": "cotiza_login.html",
    "Cotiza_Sidebar": "cotiza_sidebar.html",
    "Cotiza_Dashboard": "cotiza_dashboard.html",
    "Cotiza_Cotizaciones": "cotiza_cotizaciones.html",
    "Cotiza_Solicitudes": "cotiza_solicitudes.html",
    "Cotiza_Clientes": "cotiza_clientes.html",
    "Cotizador_App": "cotizador_app.html",
    "Cotiza_Usuarios": "cotiza_usuarios.html",
    "Cotiza_Reportes": "cotiza_reportes.html",
    "Plantillas_App": "plantillas_app.html",
    "Cotiza_Configuracion": "cotiza_configuracion.html",
    "Cotiza_Modals": "cotiza_modals.html",
    "Cotiza_Scripts": "cotiza_scripts.html",
}

# Includes que NO llegaron en la entrega original — se documentan
# como comentario en vez de inventar contenido.
MISSING = [
    "Cotiza_Styles", "Cotiza_Style_Catalogos", "Cotiza_Style_Cotizador",
    "Cotiza_Style_Solicitudes", "Cotiza_Style_Plantillas",
    "Cotiza_Script_Solicitudes", "Cotiza_Script_Plantillas",
    "Cotiza_Script_Reglas", "Cotiza_Script_Mantenimiento_Reglas",
    "Cotiza_Script_Compilador_Cotizador", "Cotiza_Script_Cotizador",
]

INCLUDE_RE = re.compile(r"""<\?!=\s*include\(\s*['"]([^'"]+)['"]\s*\)\s*;?\s*\?>""")


def read(name):
    return (LEGACY / name).read_text(encoding="utf-8", errors="ignore")


def resolve_include(match):
    key = match.group(1)
    if key in FILE_MAP:
        content = read(FILE_MAP[key])
        # cotiza_scripts.html tiene a su vez un include propio adentro
        # (Cotiza_Script_Cotizador) — resolverlo recursivamente.
        content = INCLUDE_RE.sub(resolve_include, content)
        return f"\n<!-- === {key} ({FILE_MAP[key]}) === -->\n{content}\n"
    if key in MISSING:
        return f"\n<!-- FALTA: {key}.html (no incluido en la entrega original de Jimmy) -->\n"
    return f"\n<!-- AVISO: include desconocido '{key}', no se resolvió -->\n"


def main():
    template = read("Cotiza_App.html")

    # 1) Resolver todos los <?!= include(...) ?> (incluye recursión para Cotiza_Scripts)
    assembled = INCLUDE_RE.sub(resolve_include, template)

    # 2) Reemplazar la variable COTIZA_WEBAPP_URL (no aplica sin Apps Script)
    assembled = assembled.replace(
        "const COTIZA_WEBAPP_URL = <?!= COTIZA_WEBAPP_URL ?>;",
        'const COTIZA_WEBAPP_URL = "";'
    )

    # 3) Insertar el CSS base propio dentro del <style>...</style> que quedó vacío
    #    (ahí vivían los 5 includes de estilo, todos faltantes)
    css = (BUILD_DIR / "cotiza_base.css").read_text(encoding="utf-8")
    assembled = re.sub(
        r"<style>.*?</style>",
        "<style>\n" + css + "\n</style>",
        assembled,
        count=1,
        flags=re.DOTALL,
    )

    # 4) Insertar el shim de google.script.run ANTES de "Cotiza_Scripts".
    shim = (BUILD_DIR / "shim.html").read_text(encoding="utf-8")
    marker = "<!-- === Cotiza_Scripts (cotiza_scripts.html) === -->"
    assembled = assembled.replace(marker, shim + "\n" + marker, 1)

    # 5) Extender CotizaApp con el módulo del Diseñador (catálogos/tabs),
    #    que existe como archivo pero no estaba referenciado por include().
    catalogos_js = read("cotiza_script_catalogos.html")
    assembled = assembled.replace(
        "</body>",
        f"\n<!-- === cotiza_script_catalogos.html (extiende CotizaApp, sin include() propio) === -->\n{catalogos_js}\n</body>",
        1,
    )

    OUT.write_text(assembled, encoding="utf-8")
    print(f"OK: {OUT} generado ({len(assembled):,} caracteres)")

    # Reporte de includes no resueltos que puedan haber quedado
    remaining = INCLUDE_RE.findall(assembled)
    if remaining:
        print("ADVERTENCIA — quedaron includes sin resolver:", remaining)
    else:
        print("Todos los <?!= include(...) ?> fueron resueltos o documentados.")


if __name__ == "__main__":
    main()
