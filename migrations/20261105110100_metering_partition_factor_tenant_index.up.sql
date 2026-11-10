-- The rows of one community for base.activeMeteringPartition (20261105110000, support ticket 6611200) without a scan
-- of the whole table; no data change. CONCURRENTLY: no lock on the table while it is built; the file must hold this
-- single statement (no transaction around it).
CREATE INDEX CONCURRENTLY IF NOT EXISTS metering_partition_factor_tenant_idx ON base.metering_partition_factor (tenant);
