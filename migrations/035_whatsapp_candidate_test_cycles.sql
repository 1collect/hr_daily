ALTER TABLE whatsapp_candidates
  DROP CONSTRAINT IF EXISTS whatsapp_candidates_config_id_external_id_key;

CREATE INDEX IF NOT EXISTS whatsapp_candidates_config_external_created_idx
  ON whatsapp_candidates(config_id, external_id, created_at DESC, id DESC);
