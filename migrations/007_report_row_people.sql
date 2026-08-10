CREATE TABLE IF NOT EXISTS report_row_people (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_row_id uuid NOT NULL REFERENCES report_rows(id) ON DELETE CASCADE,
  category text NOT NULL CHECK (category IN (
    'invited_candidates',
    'interviewed_candidates',
    'interns',
    'reserve_candidates',
    'dismissed_workers'
  )),
  full_name text NOT NULL CHECK (length(trim(full_name)) > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS report_row_people_row_category_idx
  ON report_row_people (report_row_id, category, created_at, id);
