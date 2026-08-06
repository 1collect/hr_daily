CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS offices (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL UNIQUE,
  sort_order integer NOT NULL DEFAULT 0,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS employees (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  first_name text NOT NULL,
  last_name text NOT NULL,
  middle_name text NOT NULL DEFAULT '',
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (first_name, last_name, middle_name)
);

CREATE TABLE IF NOT EXISTS settings (
  key text PRIMARY KEY,
  value text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS reports (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_date date NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','completed')),
  completed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS report_rows (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_id uuid NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
  office_id uuid NOT NULL REFERENCES offices(id),
  open_vacancies integer NOT NULL DEFAULT 0 CHECK (open_vacancies >= 0),
  invitation_threshold integer NOT NULL DEFAULT 0 CHECK (invitation_threshold >= 0),
  invited_candidates integer NOT NULL DEFAULT 0 CHECK (invited_candidates >= 0),
  interview_plan integer NOT NULL DEFAULT 0 CHECK (interview_plan >= 0),
  interviewed_candidates integer NOT NULL DEFAULT 0 CHECK (interviewed_candidates >= 0),
  interns integer NOT NULL DEFAULT 0 CHECK (interns >= 0),
  reserve_candidates integer NOT NULL DEFAULT 0 CHECK (reserve_candidates >= 0),
  dismissed_workers integer NOT NULL DEFAULT 0 CHECK (dismissed_workers >= 0),
  efficiency numeric(8,2) NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (report_id, office_id)
);

CREATE TABLE IF NOT EXISTS report_row_responsibles (
  report_row_id uuid NOT NULL REFERENCES report_rows(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL REFERENCES employees(id),
  PRIMARY KEY (report_row_id, employee_id)
);

CREATE TABLE IF NOT EXISTS hired_workers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_row_id uuid NOT NULL REFERENCES report_rows(id) ON DELETE CASCADE,
  full_name text NOT NULL CHECK (length(trim(full_name)) > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS import_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  file_name text NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','completed','failed')),
  total_rows integer NOT NULL DEFAULT 0,
  processed_rows integer NOT NULL DEFAULT 0,
  imported_rows integer NOT NULL DEFAULT 0,
  errors jsonb NOT NULL DEFAULT '[]'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

CREATE TABLE IF NOT EXISTS audit_log (
  id bigserial PRIMARY KEY,
  actor text NOT NULL DEFAULT 'system',
  action text NOT NULL,
  entity_type text NOT NULL,
  entity_id text,
  details jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO settings(key,value) VALUES ('invitation_threshold','16') ON CONFLICT (key) DO NOTHING;

INSERT INTO offices(name, sort_order) VALUES
('РП Ақтөбе Айколлект :',1),('РП Алматы Айколлект1',2),('РП Алматы Айколлект2',3),
('РП Алматы Айколлект3',4),('РП Астана Айколлект:',5),('РП Қарағанды Айколлект',6),
('РП Талдыкорган Айколлект :',7),('РП Уральск Айколлект:',8),('ПКБ Ақтөбе',9),
('ПКБ РП Ақтөбе2',10),('ПКБ РП Алматы1',11),('ПКБ РП Алматы2',12),('ПКБ РП Алматы3',13),
('ПКБ_УПР_АЛМАТЫ4',14),('ПКБ_УПР_АЛМАТЫ5',15),('ПКБ_УПР_АЛМАТЫ6',16),
('ПКБ_Астана:',17),('ПКБ РП Астана2',18),('ПКБ_Караганда:',19),
('ПКБ_ Усть-Каменогорск:',20),('ПКБ РП Шымкент',21),('СОФТ',22),('Ф-Коллект Алматы',23)
ON CONFLICT (name) DO NOTHING;
