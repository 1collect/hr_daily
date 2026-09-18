CREATE TABLE trainee_correction_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_date date NOT NULL,
  department text NOT NULL,
  reporter_id integer,
  debtster_trainee_id integer,
  debtster_full_name text NOT NULL,
  debtster_source text NOT NULL,
  local_record jsonb NOT NULL CHECK (jsonb_typeof(local_record) = 'object'),
  proposed_change jsonb NOT NULL CHECK (jsonb_typeof(proposed_change) = 'object'),
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
  requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','processing','applied','rejected','failed')),
  debtster_note text NOT NULL DEFAULT '' CHECK (length(debtster_note) <= 1000),
  processed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX trainee_correction_requests_pending_uidx
  ON trainee_correction_requests(report_date, department, reporter_id, debtster_trainee_id, debtster_full_name)
  WHERE status IN ('pending','processing');

CREATE INDEX trainee_correction_requests_status_idx
  ON trainee_correction_requests(status, created_at DESC);
