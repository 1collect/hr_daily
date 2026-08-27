ALTER TABLE offices
  ADD COLUMN debtster_department_name text;

ALTER TABLE report_rows
  ADD COLUMN debtster_department_name text;

ALTER TABLE offices
  DROP CONSTRAINT offices_name_key;

CREATE UNIQUE INDEX offices_local_name_uidx
  ON offices(name)
  WHERE debtster_department_id IS NULL;

CREATE UNIQUE INDEX offices_debtster_department_id_uidx
  ON offices(debtster_department_id)
  WHERE debtster_department_id IS NOT NULL;

WITH department_map(debtster_department_id, department_name) AS (
  VALUES
    (11, 'SOFT'),
    (69, 'pkb_aktobe'),
    (24, 'icollect_aktobe'),
    (30, 'icollect_taldykorgan'),
    (26, 'icollect_karaganda'),
    (44, 'icollect_аlmaty2'),
    (21, 'uralsk'),
    (76, 'pkb_aktobe2'),
    (54, 'pkb_astana'),
    (70, 'pkb_karaganda'),
    (20, 'pkb_almaty1'),
    (78, 'pkb_astana2'),
    (77, 'pkb_almaty4'),
    (81, 'pkb_almaty3'),
    (55, 'icollect_almaty3'),
    (72, 'pkb_oskemen'),
    (45, 'icollect_аlmaty1'),
    (68, 'pkb_almaty2'),
    (80, 'pkb_shymkent'),
    (82, 'pkb_almaty5'),
    (83, 'pkb_almaty6'),
    (36, 'f_almaty'),
    (27, 'icollect_astana'),
    (62, 'Soft__deleted_at_20260715_115829_852777'),
    (61, 'Soft_deleted_at_20260715_115845_166653'),
    (79, 'pkb_karaganda2_deleted_at_20260715_115953_898356')
)
UPDATE offices o
SET debtster_department_name = dm.department_name
FROM department_map dm
WHERE o.debtster_department_id = dm.debtster_department_id;

UPDATE report_rows rr
SET debtster_department_name = o.debtster_department_name
FROM offices o
WHERE rr.office_id = o.id
  AND rr.debtster_department_id = o.debtster_department_id
  AND o.debtster_department_name IS NOT NULL;
