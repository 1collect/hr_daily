-- Keep the former "everyone is assigned" behaviour for historical reports
-- through 2 September 2026. Starting with the next day, managers select
-- responsibles independently for every report date.
WITH report_dates AS (
  SELECT report_date FROM reports WHERE report_date <= DATE '2026-09-02'
  UNION
  SELECT report_date FROM main_office_reports WHERE report_date <= DATE '2026-09-02'
), rp_units AS (
  SELECT COALESCE(debtster_department_id::text, id::text) AS unit_id FROM offices
  UNION
  SELECT debtster_department_id::text FROM report_rows WHERE debtster_department_id IS NOT NULL
)
INSERT INTO report_unit_responsibles(report_date, report_type, unit_id, user_id)
SELECT d.report_date, 'rp', units.unit_id, u.id
FROM report_dates d
CROSS JOIN rp_units units
CROSS JOIN users u
WHERE u.role = 'employee' AND u.active AND NOT u.system
ON CONFLICT DO NOTHING;

WITH report_dates AS (
  SELECT report_date FROM reports WHERE report_date <= DATE '2026-09-02'
  UNION
  SELECT report_date FROM main_office_reports WHERE report_date <= DATE '2026-09-02'
)
INSERT INTO report_unit_responsibles(report_date, report_type, unit_id, user_id)
SELECT d.report_date, 'main_office', o.id::text, u.id
FROM report_dates d
CROSS JOIN main_offices o
CROSS JOIN users u
WHERE u.role = 'employee' AND u.active AND NOT u.system
ON CONFLICT DO NOTHING;
