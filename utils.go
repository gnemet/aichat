package aichat

import (
	"regexp"
	"strings"
)

// ExtractSQL extracts raw SQL from a potentially markdown-wrapped LLM response
func ExtractSQL(text string) string {
	re := regexp.MustCompile("(?s)```(?:sql)?\\n(.*?)\\n```")
	match := re.FindStringSubmatch(text)
	if len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return strings.TrimSpace(text)
}

// SubstituteLoginUser replaces {{loginuser}} placeholder with the actual username.
// Handles quoted ('{{loginuser}}'), unquoted ({{loginuser}}), and spaced variants.
// Unquoted placeholders are wrapped in SQL single quotes.
func SubstituteLoginUser(sql, user string) string {
	if user == "" {
		return sql
	}
	quotedUser := "'" + user + "'"
	// Handle already-quoted variants first (avoid double quoting)
	sql = strings.ReplaceAll(sql, "'{{loginuser}}'", quotedUser)
	sql = strings.ReplaceAll(sql, "'{{ loginuser }}'", quotedUser)
	// Then unquoted variants (wrap in quotes)
	sql = strings.ReplaceAll(sql, "{{loginuser}}", quotedUser)
	sql = strings.ReplaceAll(sql, "{{ loginuser }}", quotedUser)
	return sql
}

// IsHungarian detects Hungarian text using keyword list
func IsHungarian(text string, keywords []string) bool {
	if len(keywords) == 0 {
		return false
	}
	lower := strings.ToLower(text)
	for _, w := range keywords {
		if strings.Contains(lower, strings.ToLower(w)) {
			return true
		}
	}
	return false
}

// yamlEscape escapes double quotes in strings for YAML values
func yamlEscape(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}

// indentSQL indents each line of a multiline string for YAML block scalars
func indentSQL(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, line := range lines {
		if i > 0 {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n")
}
