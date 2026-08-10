UPDATE report_rows
SET efficiency = CASE
  WHEN interview_plan > 0 THEN round(interviewed_candidates * 100.0 / interview_plan, 2)
  ELSE 0
END,
updated_at = now()
WHERE efficiency IS DISTINCT FROM CASE
  WHEN interview_plan > 0 THEN round(interviewed_candidates * 100.0 / interview_plan, 2)
  ELSE 0
END;
