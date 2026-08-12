CREATE TABLE employee_efficiency_plans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_count integer NOT NULL CHECK (plan_count > 0),
  effective_from date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, effective_from)
);

CREATE INDEX employee_efficiency_plans_lookup_idx
  ON employee_efficiency_plans(user_id, effective_from DESC);

INSERT INTO employee_efficiency_plans(user_id, plan_count, effective_from)
SELECT id, 16, DATE '1970-01-01'
FROM users
WHERE role = 'employee' AND NOT system
ON CONFLICT (user_id, effective_from) DO NOTHING;

UPDATE report_rows rr
SET efficiency = CASE
  WHEN p.plan_count > 0 THEN round(rr.interviewed_candidates * 100.0 / p.plan_count, 2)
  ELSE 0
END,
updated_at = now()
FROM reports rp
JOIN LATERAL (
  SELECT plan_count
  FROM employee_efficiency_plans
  WHERE user_id = rp.owner_user_id AND effective_from <= rp.report_date
  ORDER BY effective_from DESC
  LIMIT 1
) p ON true
WHERE rr.report_id = rp.id;
