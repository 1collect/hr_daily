CREATE TABLE IF NOT EXISTS daily_office_shared (
  report_date date NOT NULL,
  office_id uuid NOT NULL REFERENCES offices(id) ON DELETE CASCADE,
  open_vacancies integer NOT NULL DEFAULT 0 CHECK (open_vacancies >= 0),
  updated_by_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (report_date, office_id)
);

INSERT INTO daily_office_shared(report_date,office_id,open_vacancies,updated_by_user_id,updated_at)
SELECT DISTINCT ON (rp.report_date,rr.office_id) rp.report_date,rr.office_id,rr.open_vacancies,rp.owner_user_id,rr.updated_at
FROM reports rp JOIN report_rows rr ON rr.report_id=rp.id
WHERE rp.owner_user_id IS NOT NULL AND rr.open_vacancies > 0
ORDER BY rp.report_date,rr.office_id,rr.updated_at DESC
ON CONFLICT (report_date,office_id) DO NOTHING;
