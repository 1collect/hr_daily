ALTER TABLE daily_office_shared
  ADD COLUMN planned_reserve integer NOT NULL DEFAULT 0 CHECK (planned_reserve >= 0);

ALTER TABLE main_office_daily_shared
  ADD COLUMN planned_reserve integer NOT NULL DEFAULT 0 CHECK (planned_reserve >= 0);
