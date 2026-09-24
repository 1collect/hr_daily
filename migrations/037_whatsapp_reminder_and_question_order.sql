-- Ask about the final study year immediately after whether the candidate studies.
-- Shift together first so the unique position constraint stays valid.
UPDATE whatsapp_questions
SET position = position + 100
WHERE key IN ('is_final_year', 'criminal_record', 'bank_restrictions', 'last_job');

UPDATE whatsapp_questions
SET position = CASE key
  WHEN 'is_final_year' THEN 4
  WHEN 'criminal_record' THEN 5
  WHEN 'bank_restrictions' THEN 6
  WHEN 'last_job' THEN 7
END
WHERE key IN ('is_final_year', 'criminal_record', 'bank_restrictions', 'last_job');

UPDATE whatsapp_questions
SET text = 'Вы на последнем курсе?'
WHERE key = 'is_final_year';
