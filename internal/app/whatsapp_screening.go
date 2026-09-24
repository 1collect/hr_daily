package app

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	whatsappProfanity        = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:нахуй|нахер|пизд\p{L}*|бляд\p{L}*|ху[йяеё]\p{L}*|уеб\p{L}*|[её]бан\p{L}*|fuck|fucking|bitch)(?:$|[^\p{L}])`)
	whatsappInsult           = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:ты|вы|бот|рекрутер|рекрутеры)\s+(?:просто\s+|вообще\s+)?(?:идиот|идиоты|дебил|дебилы|мудак|мудаки|дурак|дураки|тупой|тупая|тупые)(?:$|[^\p{L}])`)
	whatsappQuestionInsult   = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:тупые|идиотские|дурацкие)\s+вопросы(?:$|[^\p{L}])`)
	whatsappStandaloneInsult = regexp.MustCompile(`(?i)^(?:просто\s+)?(?:идиот|идиоты|дебил|дебилы|мудак|мудаки|дурак|дураки|тупой|тупая|тупые|stupid|idiot)$`)
	whatsappNamePart         = regexp.MustCompile(`^[\p{L}]+(?:[-'’][\p{L}]+)*$`)
	whatsappAge              = regexp.MustCompile(`(?i)^(\d{1,3})(?:\s*(?:лет|года|год))?$`)
)

// Ignore only clear abuse or an explicit request to stop the conversation.
// Unclear or off-topic messages remain in the conversation but never become answers.
func isWhatsAppIgnoreMessage(text string) bool {
	value := strings.ToLower(strings.TrimSpace(text))
	if value == "" {
		return false
	}
	if whatsappProfanity.MatchString(value) {
		return true
	}
	if len([]rune(value)) <= 120 && (whatsappInsult.MatchString(value) || whatsappQuestionInsult.MatchString(value) || whatsappStandaloneInsult.MatchString(strings.Trim(value, " .,!?:;…\n\r\t"))) {
		return true
	}
	value = strings.Trim(value, " .,!?:;…\n\r\t")
	switch value {
	case "стоп", "stop", "отмена", "отстаньте", "не пишите мне", "не связывайтесь со мной", "удалите мой номер", "отпишите меня", "не интересно", "неинтересно":
		return true
	}
	return false
}

func normalizeWhatsAppCandidateAnswer(q whatsappQuestion, answer string) (string, error) {
	answer = strings.TrimSpace(answer)
	if q.Key == "age" {
		if match := whatsappAge.FindStringSubmatch(answer); match != nil {
			answer = match[1]
		}
	}
	normalized, err := normalizeWhatsAppAnswer(q.Type, answer)
	if err != nil {
		return "", err
	}
	if isWhatsAppIgnoreMessage(normalized) {
		return "", fmt.Errorf("ответ не относится к анкете")
	}
	switch q.Key {
	case "full_name":
		parts := strings.Fields(normalized)
		if len(parts) < 2 || len(parts) > 4 || len([]rune(normalized)) > 120 {
			return "", fmt.Errorf("укажите полное ФИО")
		}
		for _, part := range parts {
			if !whatsappNamePart.MatchString(part) || whatsappStandaloneInsult.MatchString(strings.ToLower(part)) {
				return "", fmt.Errorf("укажите полное ФИО")
			}
		}
		switch strings.ToLower(parts[0]) {
		case "привет", "здравствуйте", "добрый", "как", "ты", "вы", "бот", "мне", "можно", "пожалуйста", "не", "нет", "да", "фамилия", "имя", "фио", "меня", "скажите", "можете", "работал", "учусь", "хочу", "есть":
			return "", fmt.Errorf("укажите полное ФИО")
		}
	case "age":
		age, _ := strconv.Atoi(normalized)
		if age < 14 || age > 100 {
			return "", fmt.Errorf("укажите возраст от 14 до 100 лет")
		}
	case "last_job":
		if normalized == "" || len([]rune(normalized)) > 200 || !strings.ContainsFunc(normalized, unicode.IsLetter) {
			return "", fmt.Errorf("укажите последнее место работы или «не работал»")
		}
		switch strings.ToLower(normalized) {
		case "привет", "здравствуйте", "алло", "как дела", "добрый день":
			return "", fmt.Errorf("укажите последнее место работы или «не работал»")
		}
	}
	return normalized, nil
}
