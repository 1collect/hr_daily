CREATE TABLE bitrix_hr_requests (
  bitrix_request_id bigint PRIMARY KEY,
  title text NOT NULL DEFAULT '',
  stage_id text NOT NULL DEFAULT '',
  created_time text NOT NULL DEFAULT '',
  created_by_id bigint NOT NULL DEFAULT 0,
  created_by_name text NOT NULL DEFAULT '',
  assigned_by_id bigint NOT NULL DEFAULT 0,
  assigned_by_name text NOT NULL DEFAULT '',
  department_code text NOT NULL DEFAULT '',
  details text NOT NULL DEFAULT '',
  raw_payload jsonb NOT NULL,
  synced_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE main_office_vacancies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  bitrix_request_id bigint NOT NULL UNIQUE REFERENCES bitrix_hr_requests(bitrix_request_id) ON DELETE CASCADE,
  title text NOT NULL CHECK (length(trim(title)) > 0),
  requested_count integer NOT NULL DEFAULT 1 CHECK (requested_count > 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX main_office_vacancies_title_idx ON main_office_vacancies(title);

CREATE TABLE main_office_candidates (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  vacancy_id uuid NOT NULL REFERENCES main_office_vacancies(id) ON DELETE CASCADE,
  full_name text NOT NULL CHECK (length(trim(full_name)) > 0),
  invited_at date NOT NULL,
  status text NOT NULL DEFAULT 'invited'
    CHECK (status IN ('invited','interviewed','internship','hired','rejected')),
  internship_at date,
  hired_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX main_office_candidates_vacancy_status_idx
  ON main_office_candidates(vacancy_id,status,created_at,id);
