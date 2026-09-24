ALTER TABLE whatsapp_candidates
  DROP CONSTRAINT IF EXISTS whatsapp_candidates_status_check;

ALTER TABLE whatsapp_candidates
  ADD CONSTRAINT whatsapp_candidates_status_check
  CHECK (status IN ('new','survey_in_progress','survey_completed','contacted','rejected','hired','ignored'));
