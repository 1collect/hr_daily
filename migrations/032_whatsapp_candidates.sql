ALTER TABLE whatsapp_waba_configs
  ADD COLUMN IF NOT EXISTS transport_mode text NOT NULL DEFAULT 'whatsapp'
    CHECK (transport_mode IN ('terminal','whatsapp','whatsapp_test'));

CREATE TABLE IF NOT EXISTS whatsapp_candidates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  config_id uuid NOT NULL REFERENCES whatsapp_waba_configs(id) ON DELETE RESTRICT,
  external_id text NOT NULL,
  display_name text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT 'new'
    CHECK (status IN ('new','survey_in_progress','survey_completed','contacted','rejected','hired')),
  current_question_id uuid,
  survey_started_at timestamptz,
  survey_completed_at timestamptz,
  typing_until timestamptz,
  question_needs_prompt boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (config_id, external_id)
);

CREATE TABLE IF NOT EXISTS whatsapp_questions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  text text NOT NULL,
  answer_type text NOT NULL DEFAULT 'text' CHECK (answer_type IN ('text','number','yes_no')),
  show_if_question_id uuid REFERENCES whatsapp_questions(id) ON DELETE RESTRICT,
  show_if_answer text NOT NULL DEFAULT '',
  key text NOT NULL UNIQUE,
  position integer NOT NULL UNIQUE,
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE whatsapp_candidates
  DROP CONSTRAINT IF EXISTS whatsapp_candidates_current_question_id_fkey;
ALTER TABLE whatsapp_candidates
  ADD CONSTRAINT whatsapp_candidates_current_question_id_fkey
  FOREIGN KEY (current_question_id) REFERENCES whatsapp_questions(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS whatsapp_answers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  candidate_id uuid NOT NULL REFERENCES whatsapp_candidates(id) ON DELETE CASCADE,
  question_id uuid NOT NULL REFERENCES whatsapp_questions(id) ON DELETE RESTRICT,
  text text NOT NULL,
  answered_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (candidate_id, question_id)
);

CREATE TABLE IF NOT EXISTS whatsapp_messages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  candidate_id uuid NOT NULL REFERENCES whatsapp_candidates(id) ON DELETE CASCADE,
  direction text NOT NULL CHECK (direction IN ('incoming','outgoing')),
  text text NOT NULL,
  transport text NOT NULL,
  external_message_id text UNIQUE,
  sent_at timestamptz NOT NULL DEFAULT now(),
  metadata jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS whatsapp_candidates_updated_idx
  ON whatsapp_candidates(config_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS whatsapp_messages_candidate_idx
  ON whatsapp_messages(candidate_id, sent_at, id);

INSERT INTO whatsapp_questions(text, answer_type, key, position)
VALUES
  ('Ваше полное ФИО?', 'text', 'full_name', 1),
  ('Сколько Вам лет?', 'number', 'age', 2),
  ('Учитесь ли Вы сейчас?', 'yes_no', 'studying', 3),
  ('Имеется ли у Вас судимость?', 'yes_no', 'criminal_record', 4),
  ('Имеется ли арест или ограничение на банковских счетах?', 'yes_no', 'bank_restrictions', 5),
  ('Ваше последнее место работы?', 'text', 'last_job', 6)
ON CONFLICT (key) DO UPDATE SET text=EXCLUDED.text, answer_type=EXCLUDED.answer_type, position=EXCLUDED.position;

INSERT INTO permissions(code, name)
VALUES ('candidates.whatsapp.view', 'Кандидаты WhatsApp')
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name;
