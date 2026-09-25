CREATE TABLE companies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL CHECK (length(trim(name)) > 0),
  normalized_name text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE bitrix_hr_requests
  ADD COLUMN company_id uuid REFERENCES companies(id) ON DELETE SET NULL;

CREATE INDEX bitrix_hr_requests_company_idx ON bitrix_hr_requests(company_id);

INSERT INTO companies(name, normalized_name)
SELECT DISTINCT ON (lower(regexp_replace(trim(parsed.parts[1]), '\s+', ' ', 'g')))
  trim(parsed.parts[1]),
  lower(regexp_replace(trim(parsed.parts[1]), '\s+', ' ', 'g'))
FROM bitrix_hr_requests request
CROSS JOIN LATERAL regexp_match(request.details, '(?m)^\s*1\.[^:\r\n]*:\s*(.+)$') AS parsed(parts)
WHERE trim(parsed.parts[1]) <> ''
ORDER BY lower(regexp_replace(trim(parsed.parts[1]), '\s+', ' ', 'g')), trim(parsed.parts[1])
ON CONFLICT (normalized_name) DO NOTHING;

WITH parsed_requests AS (
  SELECT bitrix_request_id,
    trim((regexp_match(details, '(?m)^\s*1\.[^:\r\n]*:\s*(.+)$'))[1]) AS company_name
  FROM bitrix_hr_requests
)
UPDATE bitrix_hr_requests request
SET company_id = company.id
FROM parsed_requests parsed
JOIN companies company
  ON company.normalized_name = lower(regexp_replace(parsed.company_name, '\s+', ' ', 'g'))
WHERE request.bitrix_request_id = parsed.bitrix_request_id
  AND parsed.company_name <> '';
