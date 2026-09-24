UPDATE whatsapp_waba_configs
SET transport_mode='whatsapp'
WHERE transport_mode='terminal';

ALTER TABLE whatsapp_waba_configs
  DROP CONSTRAINT IF EXISTS whatsapp_waba_configs_transport_mode_check;

ALTER TABLE whatsapp_waba_configs
  ADD CONSTRAINT whatsapp_waba_configs_transport_mode_check
  CHECK (transport_mode IN ('whatsapp','whatsapp_test'));
