ALTER TABLE correction_requests DROP CONSTRAINT correction_requests_note_check;
ALTER TABLE correction_requests ADD CONSTRAINT correction_requests_note_check CHECK (length(trim(note)) <= 1000);
ALTER TABLE correction_requests ALTER COLUMN note SET DEFAULT '';
