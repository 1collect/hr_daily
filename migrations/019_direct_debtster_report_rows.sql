ALTER TABLE report_rows
  ALTER COLUMN office_id DROP NOT NULL,
  ADD COLUMN planned_reserve integer NOT NULL DEFAULT 0 CHECK (planned_reserve >= 0);

CREATE UNIQUE INDEX report_rows_report_debtster_department_uidx
  ON report_rows(report_id, debtster_department_id)
  WHERE debtster_department_id IS NOT NULL;

UPDATE report_rows rr
SET open_vacancies = ds.open_vacancies,
    planned_reserve = ds.planned_reserve
FROM reports rp, daily_office_shared ds
WHERE rr.report_id = rp.id
  AND ds.report_date = rp.report_date
  AND ds.office_id = rr.office_id;
