package chat

import (
	"html/template"
	"sort"
	"strings"
	"unicode"

	"github.com/sociolytik/odoo-clone/internal/auth"
)

// RenderMentions HTML-escapes body and highlights any "@Full Name" that
// exactly matches one of users' Name (the app has no separate @handle —
// the composer's mention dropdown always inserts a user's real full name,
// so matching against it directly is reliable). Returns the safe HTML for
// display plus the distinct set of mentioned user ids, in first-seen
// order, for the caller to notify.
func RenderMentions(body string, users []auth.User) (template.HTML, []int64) {
	// Longest name first, so "Ayo Khumalo" isn't shadowed by a shorter
	// "Ayo" belonging to someone else.
	sorted := make([]auth.User, len(users))
	copy(sorted, users)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i].Name) > len(sorted[j].Name) })

	var out strings.Builder
	seen := make(map[int64]bool)
	var mentioned []int64

	runes := []rune(body)
	i := 0
	for i < len(runes) {
		if runes[i] == '@' {
			if u, n := matchMentionAt(runes[i+1:], sorted); n > 0 {
				out.WriteString(`<span class="mention">@`)
				out.WriteString(template.HTMLEscapeString(u.Name))
				out.WriteString(`</span>`)
				if !seen[u.ID] {
					seen[u.ID] = true
					mentioned = append(mentioned, u.ID)
				}
				i += 1 + n
				continue
			}
		}
		out.WriteString(template.HTMLEscapeString(string(runes[i])))
		i++
	}
	return template.HTML(out.String()), mentioned
}

// matchMentionAt reports whether rest begins with some user's full name
// (case-insensitive) followed by a word boundary — so "@Ayo Khumalo2"
// doesn't wrongly match "Ayo Khumalo".
func matchMentionAt(rest []rune, sorted []auth.User) (auth.User, int) {
	for _, u := range sorted {
		nameRunes := []rune(u.Name)
		if len(nameRunes) == 0 || len(rest) < len(nameRunes) {
			continue
		}
		if !strings.EqualFold(string(rest[:len(nameRunes)]), u.Name) {
			continue
		}
		if len(rest) > len(nameRunes) {
			next := rest[len(nameRunes)]
			if unicode.IsLetter(next) || unicode.IsDigit(next) {
				continue
			}
		}
		return u, len(nameRunes)
	}
	return auth.User{}, 0
}
