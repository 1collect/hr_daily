ALTER TABLE offices
  ADD COLUMN debtster_department_id integer
  CHECK (debtster_department_id >= 0);

ALTER TABLE report_rows
  ADD COLUMN debtster_department_id integer
  CHECK (debtster_department_id >= 0);

WITH department_map(debtster_department_id, display_name) AS (
  VALUES
    (11, 'Софт'),
    (69, 'ПКБ_Актобе'),
    (24, 'Айколлект_Актобе'),
    (30, 'Айколлект_Талдыкорган'),
    (26, 'Айколлект_Караганда'),
    (44, 'Айколлект_Алматы2'),
    (21, 'Уральск'),
    (76, 'ПКБ_Актобе2'),
    (54, 'Пкб_Астана'),
    (70, 'ПКБ_Караганда'),
    (20, 'Пкб_Алматы1'),
    (78, 'Пкб_Астана2'),
    (77, 'ПКБ_Алматы4'),
    (81, 'ПКБ_Алматы3'),
    (55, 'Айколлект_Алматы3'),
    (72, 'ПКБ_Оскемен'),
    (45, 'Айколлект_Алматы1'),
    (68, 'ПКБ_Алматы2'),
    (80, 'Пкб_Шымкент'),
    (82, 'ПКБ_Алматы5'),
    (83, 'ПКБ_Алматы6'),
    (36, 'Ф-Коллект_Алматы'),
    (27, 'Айколлект_Астана'),
    (62, 'Софт_Ф-коллект'),
    (61, 'Софт_Айколлект'),
    (79, 'ПКБ_Караганда2')
)
UPDATE offices o
SET debtster_department_id = dm.debtster_department_id
FROM department_map dm
WHERE lower(btrim(o.name)) = lower(dm.display_name);

WITH department_map(debtster_department_id, display_name) AS (
  VALUES
    (11, 'Софт'),
    (69, 'ПКБ_Актобе'),
    (24, 'Айколлект_Актобе'),
    (30, 'Айколлект_Талдыкорган'),
    (26, 'Айколлект_Караганда'),
    (44, 'Айколлект_Алматы2'),
    (21, 'Уральск'),
    (76, 'ПКБ_Актобе2'),
    (54, 'Пкб_Астана'),
    (70, 'ПКБ_Караганда'),
    (20, 'Пкб_Алматы1'),
    (78, 'Пкб_Астана2'),
    (77, 'ПКБ_Алматы4'),
    (81, 'ПКБ_Алматы3'),
    (55, 'Айколлект_Алматы3'),
    (72, 'ПКБ_Оскемен'),
    (45, 'Айколлект_Алматы1'),
    (68, 'ПКБ_Алматы2'),
    (80, 'Пкб_Шымкент'),
    (82, 'ПКБ_Алматы5'),
    (83, 'ПКБ_Алматы6'),
    (36, 'Ф-Коллект_Алматы'),
    (27, 'Айколлект_Астана'),
    (62, 'Софт_Ф-коллект'),
    (61, 'Софт_Айколлект'),
    (79, 'ПКБ_Караганда2')
)
UPDATE report_rows rr
SET debtster_department_id = dm.debtster_department_id
FROM department_map dm
WHERE lower(btrim(NULLIF(rr.office_name_snapshot, ''))) = lower(dm.display_name);

UPDATE report_rows rr
SET debtster_department_id = o.debtster_department_id
FROM offices o
WHERE rr.office_id = o.id
  AND rr.debtster_department_id IS NULL
  AND o.debtster_department_id IS NOT NULL;
