ALTER TABLE report_unit_responsibles
  RENAME COLUMN report_date TO assigned_from;

ALTER TABLE report_unit_responsibles
  ADD COLUMN assigned_to date;

UPDATE report_unit_responsibles
SET assigned_to = assigned_from;

ALTER TABLE report_unit_responsibles
  ADD CONSTRAINT report_unit_responsibles_period_check
  CHECK (assigned_to IS NULL OR assigned_to >= assigned_from);

ALTER TABLE report_unit_responsibles
  DROP CONSTRAINT report_unit_responsibles_pkey;

ALTER TABLE report_unit_responsibles
  ADD PRIMARY KEY (assigned_from, report_type, unit_id, user_id);

DROP INDEX report_unit_responsibles_user_idx;

CREATE INDEX report_unit_responsibles_user_idx
  ON report_unit_responsibles(user_id, report_type, unit_id, assigned_from, assigned_to);

CREATE INDEX report_unit_responsibles_period_idx
  ON report_unit_responsibles(report_type, unit_id, assigned_from, assigned_to);
