# Grid operator of metering points: check and correction (platform#107)

Since platform#107 the backend derives the grid operator of a metering point from the metering
point number (first 8 characters) and translates retired numbers with `grid-operator-alias` from
the backend config (e.g. Energienetze Steiermark `AT008200` → `AT008000`). These scripts bring the
existing data in line and are run by the operator.

The backend also changes existing rows when they are written: a full update (PUT) sets the
derived value, and any other partial update fills it in when the stored value is empty. This
includes system updates such as `activesince`/`inactivesince` from EDA answers. Run the scripts
soon after the release, so `excluded` rows are not changed by such a write first. Numbers that do
not start with `AT` + 6 digits are skipped, like in the backend.

1. **Check** (`check.sql`, read only): lists every metering point whose stored grid operator differs
   from the derived one.
   - `weicht_von_eeg_ab = true`: the derived grid operator differs from the one in the EEG
     settings. Clarify with the EEG first (wrong EEG setting, wrong metering point number, or a
     missing alias entry).
   - `soll_bekannt = false`: the grid operator is missing in `base.gridoperators`, only the name
     stays empty.
2. **Correct** (`fix.sql`): put metering points that must not change yet into `excluded`, run the
   script, look at the row count, then `COMMIT` (or `ROLLBACK`).
3. **Check again**: only the excluded metering points may remain.

Keep the alias list at the top of both scripts in sync with `grid-operator-alias` in the backend
config. When the list grows, run the scripts again.
