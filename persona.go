package aichat

import (
	"embed"
	"fmt"
	"strings"
	"sync"
)

//go:embed persona/*.md
var personaFS embed.FS

// personaCache stores parsed persona content keyed by short name
var (
	personaCache   = make(map[string]string)
	personaCacheMu sync.Mutex
)

// Persona short-name → filename mapping
var personaFiles = map[string]string{
	"sql":    "persona/sql_persona.md",
	"chat":   "persona/chat_persona.md",
	"direct": "persona/direct_persona.md",
}

// LoadPersona returns the system prompt content for a named persona.
// It reads from the embedded persona/*.md files and caches the result.
// Valid names: "sql", "chat", "direct".
func LoadPersona(name string) string {
	personaCacheMu.Lock()
	defer personaCacheMu.Unlock()

	if cached, ok := personaCache[name]; ok {
		return cached
	}

	filename, ok := personaFiles[name]
	if !ok {
		fmt.Printf("[AI-CHAT] Unknown persona: %q — returning empty\n", name)
		return ""
	}

	data, err := personaFS.ReadFile(filename)
	if err != nil {
		fmt.Printf("[AI-CHAT] Failed to read embedded persona %q: %v\n", filename, err)
		return ""
	}

	content := ParsePersonaContent(string(data))
	personaCache[name] = content
	fmt.Printf("[AI-CHAT] Loaded persona %q (%d bytes)\n", name, len(content))
	return content
}

// ParsePersonaContent extracts the content after the "## System Prompt" header.
// If no such header is found, returns the full content (trimmed).
func ParsePersonaContent(raw string) string {
	lines := strings.Split(raw, "\n")
	inSystem := false
	var sysLines []string

	for _, l := range lines {
		if strings.TrimSpace(l) == "## System Prompt" {
			inSystem = true
			continue
		}
		if inSystem {
			sysLines = append(sysLines, l)
		}
	}

	if len(sysLines) > 0 {
		return strings.TrimSpace(strings.Join(sysLines, "\n"))
	}
	// Fallback: return everything (trimmed)
	return strings.TrimSpace(raw)
}
