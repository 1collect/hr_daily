CREATE TABLE report_unit_responsibles (
  report_type text NOT NULL CHECK (report_type IN ('rp', 'main_office')),
  unit_id text NOT NULL CHECK (length(trim(unit_id)) > 0),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  assigned_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (report_type, unit_id, user_id)
);

CREATE INDEX report_unit_responsibles_user_idx
  ON report_unit_responsibles(user_id, report_type, unit_id);

-- Preserve the behaviour that existed before assignments were introduced:
-- every current employee can continue filling every existing RP and GO company.
INSERT INTO report_unit_responsibles(report_type, unit_id, user_id)
SELECT 'rp', units.unit_id, u.id
FROM (
  SELECT COALESCE(debtster_department_id::text, id::text) AS unit_id FROM offices
  UNION
  SELECT debtster_department_id::text FROM report_rows WHERE debtster_department_id IS NOT NULL
) units
CROSS JOIN users u
WHERE u.role = 'employee' AND u.active AND NOT u.system
ON CONFLICT DO NOTHING;

INSERT INTO report_unit_responsibles(report_type, unit_id, user_id)
SELECT 'main_office', o.id::text, u.id
FROM main_offices o
CROSS JOIN users u
WHERE u.role = 'employee' AND u.active AND NOT u.system
ON CONFLICT DO NOTHING;
