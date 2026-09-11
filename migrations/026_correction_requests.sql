CREATE TABLE correction_requests (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_date date NOT NULL,
  report_type text NOT NULL CHECK (report_type IN ('rp','main_office')),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  note text NOT NULL CHECK (length(trim(note)) BETWEEN 5 AND 1000),
  changes jsonb NOT NULL CHECK (jsonb_typeof(changes) = 'array' AND jsonb_array_length(changes) > 0),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  reviewed_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  review_note text NOT NULL DEFAULT '' CHECK (length(review_note) <= 1000),
  reviewed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX correction_requests_pending_uidx
  ON correction_requests(user_id, report_date, report_type)
  WHERE status = 'pending';

CREATE INDEX correction_requests_queue_idx
  ON correction_requests(status, created_at DESC);

CREATE INDEX correction_requests_user_idx
  ON correction_requests(user_id, created_at DESC);
