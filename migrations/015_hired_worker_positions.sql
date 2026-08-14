ALTER TABLE hired_workers
  ADD COLUMN position text NOT NULL DEFAULT '';

ALTER TABLE main_office_hired_workers
  ADD COLUMN position text NOT NULL DEFAULT '';
