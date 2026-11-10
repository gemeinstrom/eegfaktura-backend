-- Member mail texts a community customises (konzept-mail-texte-editierbar.md).
-- Additive: two new tables, existing tables unchanged.
CREATE TABLE IF NOT EXISTS base.mail_template
(
    tenant                   TEXT        NOT NULL,
    mail_key                 TEXT        NOT NULL, /* consent-request | zp-active | zp-inactive */
    subject                  TEXT        NOT NULL,
    body_html                TEXT        NOT NULL, /* sanitised HTML, placeholders as span/div data-ph */
    version                  INTEGER     NOT NULL DEFAULT 1,
    default_version          INTEGER     NOT NULL, /* catalog default the version is based on */
    notified_default_version INTEGER     NOT NULL DEFAULT 0,
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by               TEXT        NOT NULL,
    CONSTRAINT mail_template_pk PRIMARY KEY (tenant, mail_key)
);

CREATE TABLE IF NOT EXISTS base.mail_template_file
(
    id         UUID        NOT NULL PRIMARY KEY,
    tenant     TEXT        NOT NULL,
    mail_key   TEXT        NOT NULL,
    kind       TEXT        NOT NULL CHECK (kind IN ('image', 'attachment')),
    filename   TEXT        NOT NULL,
    mime_type  TEXT        NOT NULL,
    size       BIGINT      NOT NULL,
    content    BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_mail_template_file_tenant_key ON base.mail_template_file (tenant, mail_key);
