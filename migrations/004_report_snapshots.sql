ALTER TABLE reports
  ADD COLUMN IF NOT EXISTS owner_name_snapshot text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS invitation_norm integer NOT NULL DEFAULT 0;

ALTER TABLE report_rows
  ADD COLUMN IF NOT EXISTS office_name_snapshot text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS office_sort_order_snapshot integer NOT NULL DEFAULT 0;

ALTER TABLE daily_office_shared
  ADD COLUMN IF NOT EXISTS updated_by_name_snapshot text NOT NULL DEFAULT '';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM settings WHERE key = 'report_snapshots_backfilled') THEN
    UPDATE reports rp
    SET owner_name_snapshot = COALESCE(NULLIF(trim(concat_ws(' ', e.last_name, e.first_name, e.middle_name)), ''), u.username)
    FROM users u
    LEFT JOIN employees e ON e.id = u.employee_id
    WHERE rp.owner_user_id = u.id AND rp.owner_name_snapshot = '';

    UPDATE reports
    SET invitation_norm = COALESCE((SELECT value::integer FROM settings WHERE key = 'invitation_threshold'), 0);

    UPDATE report_rows rr
    SET office_name_snapshot = o.name,
        office_sort_order_snapshot = o.sort_order
    FROM offices o
    WHERE rr.office_id = o.id;

    UPDATE daily_office_shared ds
    SET updated_by_name_snapshot = COALESCE(NULLIF(trim(concat_ws(' ', e.last_name, e.first_name, e.middle_name)), ''), u.username)
    FROM users u
    LEFT JOIN employees e ON e.id = u.employee_id
    WHERE ds.updated_by_user_id = u.id;

    INSERT INTO settings(key, value) VALUES ('report_snapshots_backfilled', '1');
  END IF;
END $$;
