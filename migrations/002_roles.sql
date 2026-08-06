CREATE TABLE IF NOT EXISTS users (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  employee_id uuid UNIQUE REFERENCES employees(id) ON DELETE SET NULL,
  username text NOT NULL,
  password_hash text NOT NULL,
  role text NOT NULL CHECK (role IN ('superadmin','admin','employee')),
  active boolean NOT NULL DEFAULT true,
  system boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_uidx ON users (lower(username));

ALTER TABLE reports ADD COLUMN IF NOT EXISTS owner_user_id uuid REFERENCES users(id);
ALTER TABLE reports DROP CONSTRAINT IF EXISTS reports_report_date_key;
CREATE UNIQUE INDEX IF NOT EXISTS reports_date_owner_uidx ON reports(report_date, owner_user_id) WHERE owner_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS reports_owner_date_idx ON reports(owner_user_id, report_date);
