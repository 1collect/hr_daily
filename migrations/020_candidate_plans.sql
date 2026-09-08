CREATE TABLE employee_invitation_plans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_count integer NOT NULL CHECK (plan_count >= 0),
  effective_from date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, effective_from)
);

CREATE INDEX employee_invitation_plans_lookup_idx
  ON employee_invitation_plans(user_id, effective_from DESC);

CREATE TABLE main_office_employee_invitation_plans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_count integer NOT NULL CHECK (plan_count >= 0),
  effective_from date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, effective_from)
);

CREATE INDEX main_office_employee_invitation_plans_lookup_idx
  ON main_office_employee_invitation_plans(user_id, effective_from DESC);

ALTER TABLE daily_efficiency_plan_overrides
  ADD COLUMN invited_plan_count integer CHECK (invited_plan_count >= 0);

COMMENT ON COLUMN daily_efficiency_plan_overrides.plan_count IS 'План по принятым кандидатам';
COMMENT ON COLUMN daily_efficiency_plan_overrides.invited_plan_count IS 'План по приглашенным кандидатам';

UPDATE report_rows rr
SET efficiency = CASE
  WHEN plans.plan_count > 0 THEN round((SELECT count(*) FROM hired_workers hw WHERE hw.report_row_id = rr.id) * 100.0 / plans.plan_count, 2)
  ELSE 0
END,
updated_at = now()
FROM reports rp
JOIN LATERAL (
  SELECT COALESCE(
    (SELECT d.plan_count FROM daily_efficiency_plan_overrides d
      WHERE d.user_id = rp.owner_user_id AND d.report_date = rp.report_date AND d.report_type = 'rp'),
    (SELECT p.plan_count FROM employee_efficiency_plans p
      WHERE p.user_id = rp.owner_user_id AND p.effective_from <= rp.report_date
      ORDER BY p.effective_from DESC LIMIT 1),
    0
  ) AS plan_count
) plans ON true
WHERE rr.report_id = rp.id;
