ALTER TABLE hired_workers
  ADD COLUMN IF NOT EXISTS hired_at date;

ALTER TABLE main_office_hired_workers
  ADD COLUMN IF NOT EXISTS hired_at date;
