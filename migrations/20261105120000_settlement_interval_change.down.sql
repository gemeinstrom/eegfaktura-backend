-- Revert Abrechnungsintervall-Nachweis: die Tabelle samt Nachweisen entfaellt; ein aelteres backend nutzt sie nicht.
DROP TABLE IF EXISTS base.settlement_interval_change;
