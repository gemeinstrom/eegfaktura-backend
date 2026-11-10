-- Nahebereich und Tech-/FuelCode (konzept-buergerenergie-eigenversorgung-p2p.md, Abschnitt I; platform#229).
-- ec_zone_level (1-7, leer = unbekannt) einmalig aus area befuellt (GEA 1, LOCAL 3, REGIONAL 4, BEG 7; P2P/EVA bleiben
-- leer bis zur ersten Zaehlpunktliste). tech_code/fuel_code bleiben leer bis zur naechsten Zaehlpunktliste.
-- base.billing_masterdata listet ihre Spalten einzeln und bleibt unveraendert.
ALTER TABLE base.eeg
    ADD COLUMN IF NOT EXISTS ec_zone_level smallint CONSTRAINT eeg_ec_zone_level_check CHECK (ec_zone_level BETWEEN 1 AND 7);

UPDATE base.eeg
SET ec_zone_level = CASE area WHEN 'GEA' THEN 1 WHEN 'LOCAL' THEN 3 WHEN 'REGIONAL' THEN 4 WHEN 'BEG' THEN 7 END
WHERE ec_zone_level IS NULL
  AND area IN ('GEA', 'LOCAL', 'REGIONAL', 'BEG');

ALTER TABLE base.meteringpoint
    ADD COLUMN IF NOT EXISTS tech_code text,
    ADD COLUMN IF NOT EXISTS fuel_code text;
