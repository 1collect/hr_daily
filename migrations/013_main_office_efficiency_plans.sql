CREATE TABLE main_office_employee_efficiency_plans (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_count integer NOT NULL CHECK (plan_count >= 0),
  effective_from date NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, effective_from)
);

CREATE INDEX main_office_employee_plans_lookup_idx
  ON main_office_employee_efficiency_plans(user_id, effective_from DESC);
