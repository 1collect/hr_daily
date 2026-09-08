CREATE TABLE debtster_daily_trainees (
  report_date date NOT NULL,
  debtster_department_id integer NOT NULL CHECK (debtster_department_id > 0),
  trainees_count integer NOT NULL CHECK (trainees_count >= 0),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (report_date, debtster_department_id)
);
