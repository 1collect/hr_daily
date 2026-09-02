ALTER TABLE report_unit_responsibles
  ADD COLUMN report_date date;

-- Assignments are now selected independently for every report date. Global
-- assignments from the previous model must not be carried into every day.
DELETE FROM report_unit_responsibles;

ALTER TABLE report_unit_responsibles
  ALTER COLUMN report_date SET NOT NULL;

ALTER TABLE report_unit_responsibles
  DROP CONSTRAINT report_unit_responsibles_pkey;

ALTER TABLE report_unit_responsibles
  ADD PRIMARY KEY (report_date, report_type, unit_id, user_id);

DROP INDEX report_unit_responsibles_user_idx;

CREATE INDEX report_unit_responsibles_user_idx
  ON report_unit_responsibles(user_id, report_date, report_type, unit_id);
