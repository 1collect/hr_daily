CREATE TABLE main_offices (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL UNIQUE,
  sort_order integer NOT NULL DEFAULT 0,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE main_office_reports (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_date date NOT NULL,
  owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  owner_name_snapshot text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (report_date, owner_user_id)
);

CREATE INDEX main_office_reports_owner_date_idx
  ON main_office_reports(owner_user_id, report_date);

CREATE TABLE main_office_report_rows (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_id uuid NOT NULL REFERENCES main_office_reports(id) ON DELETE CASCADE,
  main_office_id uuid NOT NULL REFERENCES main_offices(id),
  main_office_name_snapshot text NOT NULL DEFAULT '',
  main_office_sort_order_snapshot integer NOT NULL DEFAULT 0,
  open_vacancies integer NOT NULL DEFAULT 0 CHECK (open_vacancies >= 0),
  invited_candidates integer NOT NULL DEFAULT 0 CHECK (invited_candidates >= 0),
  interviewed_candidates integer NOT NULL DEFAULT 0 CHECK (interviewed_candidates >= 0),
  interns integer NOT NULL DEFAULT 0 CHECK (interns >= 0),
  reserve_candidates integer NOT NULL DEFAULT 0 CHECK (reserve_candidates >= 0),
  dismissed_workers integer NOT NULL DEFAULT 0 CHECK (dismissed_workers >= 0),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (report_id, main_office_id)
);

CREATE TABLE main_office_hired_workers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_row_id uuid NOT NULL REFERENCES main_office_report_rows(id) ON DELETE CASCADE,
  full_name text NOT NULL CHECK (length(trim(full_name)) > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE main_office_report_row_people (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  report_row_id uuid NOT NULL REFERENCES main_office_report_rows(id) ON DELETE CASCADE,
  category text NOT NULL CHECK (category IN (
    'invited_candidates',
    'interviewed_candidates',
    'interns',
    'reserve_candidates',
    'dismissed_workers'
  )),
  full_name text NOT NULL CHECK (length(trim(full_name)) > 0),
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX main_office_people_row_category_idx
  ON main_office_report_row_people(report_row_id, category, created_at, id);

CREATE TABLE main_office_daily_shared (
  report_date date NOT NULL,
  main_office_id uuid NOT NULL REFERENCES main_offices(id) ON DELETE CASCADE,
  open_vacancies integer NOT NULL DEFAULT 0 CHECK (open_vacancies >= 0),
  updated_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_by_name_snapshot text NOT NULL DEFAULT '',
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (report_date, main_office_id)
);
