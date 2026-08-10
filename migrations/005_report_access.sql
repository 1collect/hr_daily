CREATE TABLE IF NOT EXISTS report_access_grants (
  report_date date NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  granted_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (report_date, user_id)
);

CREATE INDEX IF NOT EXISTS report_access_grants_active_idx
  ON report_access_grants (user_id, report_date, expires_at);
