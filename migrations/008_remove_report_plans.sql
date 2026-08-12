UPDATE report_rows
SET efficiency = CASE
  WHEN invited_candidates > 0 THEN round(interviewed_candidates * 100.0 / invited_candidates, 2)
  ELSE 0
END,
updated_at = now();

ALTER TABLE report_rows
  DROP COLUMN IF EXISTS invitation_threshold,
  DROP COLUMN IF EXISTS interview_plan;

ALTER TABLE reports
  DROP COLUMN IF EXISTS invitation_norm;

DELETE FROM settings WHERE key = 'invitation_threshold';
