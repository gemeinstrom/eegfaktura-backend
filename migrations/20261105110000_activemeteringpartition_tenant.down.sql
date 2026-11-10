-- The definition of 20250603150318_create_views (window over all communities).
CREATE OR REPLACE VIEW base.activeMeteringPartition AS
SELECT partp.metering_point_id,
       partp.version,
       partp.participant_id,
       partp.tenant,
       partp."partFact",
       partp."createdAt",
       partp."createdBy",
       partp.rowid
FROM (SELECT metering_partition_factor.metering_point_id,
             metering_partition_factor.version,
             metering_partition_factor.participant_id,
             metering_partition_factor.tenant,
             metering_partition_factor."partFact",
             metering_partition_factor."createdAt",
             metering_partition_factor."createdBy",
             row_number()
             OVER (PARTITION BY metering_partition_factor.metering_point_id, metering_partition_factor.participant_id ORDER BY metering_partition_factor.version DESC) AS rowid
      FROM base.metering_partition_factor) partp
WHERE partp.rowid = 1;
