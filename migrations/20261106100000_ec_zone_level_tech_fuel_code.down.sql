-- Revert 20261106100000: die drei Spalten fallen weg, ihre Werte kommen mit der naechsten Zaehlpunktliste wieder
-- (ec_zone_level der EEG/BEG/GEA mit dem erneuten Befuellen der Up-Migration).
ALTER TABLE base.meteringpoint
    DROP COLUMN IF EXISTS fuel_code,
    DROP COLUMN IF EXISTS tech_code;

ALTER TABLE base.eeg
    DROP COLUMN IF EXISTS ec_zone_level;
