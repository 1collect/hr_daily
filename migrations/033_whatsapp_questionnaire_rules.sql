INSERT INTO whatsapp_questions(text, answer_type, key, position)
VALUES ('Вы на последнем курсе?', 'yes_no', 'is_final_year', 7)
ON CONFLICT (key) DO UPDATE SET text=EXCLUDED.text, answer_type=EXCLUDED.answer_type, position=EXCLUDED.position;

UPDATE whatsapp_questions final_year
SET show_if_question_id = studying.id, show_if_answer = 'Да'
FROM whatsapp_questions studying
WHERE final_year.key='is_final_year' AND studying.key='studying';
