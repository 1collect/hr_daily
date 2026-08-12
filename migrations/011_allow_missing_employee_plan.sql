ALTER TABLE employee_efficiency_plans
  DROP CONSTRAINT employee_efficiency_plans_plan_count_check;

ALTER TABLE employee_efficiency_plans
  ADD CONSTRAINT employee_efficiency_plans_plan_count_check CHECK (plan_count >= 0);
