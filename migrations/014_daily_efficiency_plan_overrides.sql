CREATE TABLE daily_efficiency_plan_overrides (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  report_date date NOT NULL,
  report_type text NOT NULL CHECK (report_type IN ('rp', 'main_office')),
  plan_count integer NOT NULL CHECK (plan_count >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, report_date, report_type)
);

CREATE INDEX daily_efficiency_plan_overrides_date_idx
  ON daily_efficiency_plan_overrides(report_date, report_type);
