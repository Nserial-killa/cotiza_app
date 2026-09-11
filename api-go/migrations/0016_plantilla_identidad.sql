-- Identidad visual y contenido de encabezado/pie para propuestas.
ALTER TABLE plantilla_estilos
    ADD COLUMN color_primario TEXT
        CHECK (color_primario IS NULL OR color_primario ~ '^#[0-9A-Fa-f]{6}$'),
    ADD COLUMN color_secundario TEXT
        CHECK (color_secundario IS NULL OR color_secundario ~ '^#[0-9A-Fa-f]{6}$'),
    ADD COLUMN color_acento TEXT
        CHECK (color_acento IS NULL OR color_acento ~ '^#[0-9A-Fa-f]{6}$'),
    ADD COLUMN color_texto TEXT
        CHECK (color_texto IS NULL OR color_texto ~ '^#[0-9A-Fa-f]{6}$'),
    ADD COLUMN color_fondo TEXT
        CHECK (color_fondo IS NULL OR color_fondo ~ '^#[0-9A-Fa-f]{6}$'),
    ADD COLUMN fuente_titulos TEXT,
    ADD COLUMN fuente_texto TEXT,
    ADD COLUMN logo_url TEXT,
    ADD COLUMN logo_tamano TEXT NOT NULL DEFAULT 'MEDIANO'
        CHECK (logo_tamano IN ('PEQUENO', 'MEDIANO', 'GRANDE')),
    ADD COLUMN mostrar_logo BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN mostrar_organizacion BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN nombre_organizacion_visible TEXT,
    ADD COLUMN texto_encabezado TEXT,
    ADD COLUMN texto_pie TEXT,
    ADD COLUMN numerar_paginas BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN marca_confidencial BOOLEAN NOT NULL DEFAULT FALSE;
