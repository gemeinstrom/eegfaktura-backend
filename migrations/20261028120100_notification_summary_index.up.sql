-- The Messages list finds the stored summaries of a community at any age (energyrequest.Collapser, decision
-- 28.10.2026). One row per closed energy request; no data change. CONCURRENTLY: no lock on the table while it is
-- built; the file must hold this single statement (no transaction around it).
CREATE INDEX CONCURRENTLY IF NOT EXISTS notification_summary_idx ON base.notification (tenant, id) WHERE (notification->>'type') = 'ZUSAMMENFASSUNG_PT';
