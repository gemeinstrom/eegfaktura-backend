-- The tenant filter of the callers (WHERE tenant = ...) reaches below the window function only when tenant is a
-- partitioning column; before, every scan of the view numbered all rows of all communities, and as the inner side
-- of a nested loop once per metering point (support ticket 6611200: MasterData_MeteringPoint over the 5 s deadline).
-- Same result: a participant belongs to one community. Same columns in the same order (CREATE OR REPLACE); no data change.
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
             OVER (PARTITION BY metering_partition_factor.tenant, metering_partition_factor.metering_point_id, metering_partition_factor.participant_id ORDER BY metering_partition_factor.version DESC) AS rowid
      FROM base.metering_partition_factor) partp
WHERE partp.rowid = 1;
