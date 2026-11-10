-- Abrechnungsintervall wechseln (konzept-abrechnungsintervall.md, platform#230): Nachweis jedes Wechsels (R6).
-- Nur Einfuegen durch die Anwendung, kein Aendern oder Loeschen. Bestand bekommt keinen Nachweis.
CREATE TABLE IF NOT EXISTS base.settlement_interval_change
(
    id                BIGSERIAL PRIMARY KEY,
    tenant            TEXT        NOT NULL,
    old_value         TEXT        NOT NULL,
    new_value         TEXT        NOT NULL,
    effective_from    DATE, /* NULL = gilt sofort (noch keine endgueltige Abrechnung) */
    last_final_period TEXT, /* letzter endgueltiger Zeitraum laut uebermitteltem billing-Stand */
    changed_by        TEXT        NOT NULL,
    changed_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_settlement_interval_change_tenant ON base.settlement_interval_change (tenant);
