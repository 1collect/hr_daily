CREATE TABLE IF NOT EXISTS whatsapp_waba_configs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  employee_id uuid NOT NULL UNIQUE REFERENCES employees(id) ON DELETE CASCADE,
  phone_number text NOT NULL,
  display_name text NOT NULL DEFAULT '',
  waba_id text NOT NULL DEFAULT '',
  phone_number_id text NOT NULL DEFAULT '',
  access_token text NOT NULL DEFAULT '',
  verify_token text NOT NULL DEFAULT '',
  app_secret text NOT NULL DEFAULT '',
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS whatsapp_waba_configs_employee_idx ON whatsapp_waba_configs(employee_id);
