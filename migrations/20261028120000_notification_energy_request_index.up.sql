-- The Messages list finds the RUN rows of a community at any age (energyrequest.Collapser, decision 28.10.2026).
-- Only the hidden rows of energy requests (one per request); no data change. CONCURRENTLY: no lock on the table
-- while it is built; the file must hold this single statement (no transaction around it).
CREATE INDEX CONCURRENTLY IF NOT EXISTS notification_energy_request_idx ON base.notification (tenant, id) WHERE process = 'ENERGY_REQUEST';
