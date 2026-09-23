CREATE TABLE IF NOT EXISTS permissions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL UNIQUE,
  name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS permission_user (
  permission_id uuid NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (permission_id, user_id)
);

INSERT INTO permissions(code, name) VALUES
  ('reports.rp.view', 'Просмотр отчётов РП'),
  ('reports.main_office.view', 'Просмотр отчётов ГО')
ON CONFLICT (code) DO UPDATE SET name=EXCLUDED.name;

INSERT INTO permission_user(permission_id, user_id)
SELECT p.id, u.id
FROM permissions p
JOIN users u ON u.role='employee' AND u.active AND NOT u.system
WHERE p.code IN ('reports.rp.view', 'reports.main_office.view')
ON CONFLICT DO NOTHING;
