package app

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

type traineeMatch struct {
	FullName string `json:"full_name"`
	Score    int    `json:"score"`
}

// Read the actual candidate lists, excluding dismissed workers and future reports.
// Keep original names for display; normalization belongs only to the search index.
func (a *App) loadTraineeSearchNames(ctx context.Context, date string) ([]string, error) {
	rows, err := a.db.Query(ctx, `SELECT DISTINCT p.full_name
		FROM report_row_people p
		JOIN report_rows rr ON rr.id=p.report_row_id
		JOIN reports r ON r.id=rr.report_id
		WHERE r.report_date <= $1 AND p.category IN
		('invited_candidates','interviewed_candidates','interns','reserve_candidates')
		ORDER BY p.full_name`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

var nameTransliteration = strings.NewReplacer(
	"ә", "а", "ғ", "г", "қ", "к", "ң", "н", "ө", "о", "ұ", "у", "ү", "у", "һ", "х", "і", "и",
)
var nameLatin = strings.NewReplacer(
	"а", "a", "б", "b", "в", "v", "г", "g", "д", "d", "е", "e", "ё", "yo", "ж", "zh", "з", "z", "и", "i", "й", "i",
	"к", "k", "л", "l", "м", "m", "н", "n", "о", "o", "п", "p", "р", "r", "с", "s", "т", "t", "у", "u", "ф", "f",
	"х", "kh", "ц", "ts", "ч", "ch", "ш", "sh", "щ", "shch", "ъ", "", "ы", "y", "ь", "", "э", "e", "ю", "yu", "я", "ya",
)

func nameTokens(name string) []string {
	name = nameLatin.Replace(nameTransliteration.Replace(strings.ToLower(name)))
	return strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) })
}

// Bigram postings shortlist names before the more expensive token alignment.
// Single letters are not indexed: an initial alone is insufficient evidence.
func nameGrams(tokens []string) map[string]struct{} {
	grams := map[string]struct{}{}
	for _, token := range tokens {
		runes := []rune(token)
		for i := 1; i < len(runes); i++ {
			grams[string(runes[i-1:i+1])] = struct{}{}
		}
	}
	return grams
}

type traineeNameIndex struct {
	names    []string
	tokens   [][]string
	postings map[string][]int
}

func newTraineeNameIndex(names []string) traineeNameIndex {
	idx := traineeNameIndex{names: names, postings: map[string][]int{}}
	for id, name := range names {
		tokens := nameTokens(name)
		idx.tokens = append(idx.tokens, tokens)
		for gram := range nameGrams(tokens) {
			idx.postings[gram] = append(idx.postings[gram], id)
		}
	}
	return idx
}

func (idx traineeNameIndex) search(name string, threshold int) []traineeMatch {
	tokens := nameTokens(name)
	ids := map[int]struct{}{}
	for gram := range nameGrams(tokens) {
		for _, id := range idx.postings[gram] {
			ids[id] = struct{}{}
		}
	}
	matches := []traineeMatch{}
	for id := range ids {
		score := nameSimilarity(tokens, idx.tokens[id])
		if score >= threshold {
			matches = append(matches, traineeMatch{idx.names[id], score})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].FullName < matches[j].FullName
	})
	return matches
}

func tokenSimilarity(a, b string) float64 {
	if a == b && len([]rune(a)) > 1 {
		return 1
	}
	x, y := []rune(a), []rune(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	if len(x) == 1 || len(y) == 1 {
		if x[0] == y[0] {
			return .65
		}
		return 0
	}
	// Short words must match exactly; tolerate at most two edits in longer ones.
	if min(len(x), len(y)) < 4 || absInt(len(x)-len(y)) > 2 {
		return 0
	}
	prev := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, c := range x {
		cur := make([]int, len(y)+1)
		cur[0] = i + 1
		for j, d := range y {
			cost := 0
			if c != d {
				cost = 1
			}
			cur[j+1] = min(cur[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = cur
	}
	distance := prev[len(y)]
	if distance > 2 {
		return 0
	}
	score := 1 - float64(distance)/float64(max(len(x), len(y)))
	if score < .7 {
		return 0
	}
	return score
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// One-to-one maximum-weight alignment makes word order irrelevant and prevents
// the same word from supplying both the first-name and surname evidence.
func nameSimilarity(a, b []string) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(a) == 0 || len(b) > 10 {
		return 0
	}
	if len(a) == 1 {
		best := 0.0
		for _, token := range b {
			best = math.Max(best, tokenSimilarity(a[0], token))
		}
		return int(math.Round(best * 54))
	}
	dp := map[int]float64{0: 0}
	for _, left := range a {
		next := map[int]float64{}
		for mask, sum := range dp {
			if old, ok := next[mask]; !ok || sum > old {
				next[mask] = sum
			}
			for j, right := range b {
				if mask&(1<<j) != 0 {
					continue
				}
				value := tokenSimilarity(left, right)
				if value == 0 {
					continue
				}
				key := mask | (1 << j)
				if old, ok := next[key]; !ok || sum+value > old {
					next[key] = sum + value
				}
			}
		}
		dp = next
	}
	best := 0.0
	for _, sum := range dp {
		best = math.Max(best, sum)
	}
	// Require more than one full word's worth of evidence, even at a low threshold.
	if best <= 1 {
		return int(math.Round(best * 54))
	}
	score := 100 * best / float64(len(a))
	if len(a) != len(b) {
		score *= .94
	}
	// All-initial names never become high-confidence matches.
	fullA, fullB := false, false
	for _, v := range a {
		fullA = fullA || len([]rune(v)) > 1
	}
	for _, v := range b {
		fullB = fullB || len([]rune(v)) > 1
	}
	if !fullA || !fullB {
		score = math.Min(score, 54)
	}
	return int(math.Round(score))
}
