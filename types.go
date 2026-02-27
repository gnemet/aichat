package aichat

import (
	"context"
	"time"
)

// AIClient generates content via LLM (Gemini, Ollama, etc.)
type AIClient interface {
	GenerateContent(ctx context.Context, prompt string, history []Message,
		systemPrompt, providerOverride, modelOverride string) (*AIResult, error)
}

// RAGProvider builds context from vector embeddings
type RAGProvider interface {
	BuildContext(ctx context.Context, question string) (string, error)
	BuildContextWithMeta(ctx context.Context, question string) (RAGResult, error)
	IsRelevancyGateEnabled() bool
}

// RAGResult carries the context string plus metadata about the matched collection
type RAGResult struct {
	Context    string // formatted RAG context text
	Collection string // primary matched collection (e.g. "dwh", "hr", "git")
}

// CollectionRoute maps a RAG collection to provider/model per pipeline stage
type CollectionRoute struct {
	SQLProvider    string `yaml:"sql_provider" json:"sql_provider"`
	SQLModel       string `yaml:"sql_model" json:"sql_model"`
	RepairProvider string `yaml:"repair_provider" json:"repair_provider"`
	RepairModel    string `yaml:"repair_model" json:"repair_model"`
	ChatProvider   string `yaml:"chat_provider" json:"chat_provider"`
	ChatModel      string `yaml:"chat_model" json:"chat_model"`
}

// SQLExecutor runs SQL with optional RLS
type SQLExecutor interface {
	Execute(user, query string, rls bool) (rows [][]string, cols []string, err error)
}

// Message represents a conversation message for multi-turn
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AIResult is the response from an AI provider
type AIResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
}

// PipelineResult is the full output of the NL → SQL → Execute → NL pipeline
type PipelineResult struct {
	UserMessage  string
	Answer       string     // NL synthesis answer (Stage 3)
	GeneratedSQL string     // SQL from Stage 1
	Columns      []string   // Result columns
	Rows         [][]string // Result rows (max 100)
	RowCount     int        // Total result rows
	RetryCount   int        // Number of SQL fix retries (0 = first attempt worked)
	Duration     time.Duration
	Error        string
	RAGTopics    string // Matched RAG topics for debug
	CorporateID  string // Multi-corporate context
	// Usage stats (populated if available)
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
}

// PipelineOptions controls optional pipeline behavior
type PipelineOptions struct {
	Feedback      bool   // Save feedback/training files on errors
	Repair        bool   // Auto-retry SQL errors via LLM fix
	RLS           bool   // Enable Row-Level Security
	CorporateID   string // Corporate identifier
	TodayOverride string // Override CURRENT_DATE for testing
	Lang          string // Language override ("hu", "en")
}

// PipelineConfig holds non-interface configuration for the pipeline
type PipelineConfig struct {
	SQLSystemPrompt   string   // System prompt for SQL generation
	ChatPersona       string   // Persona for NL synthesis
	PersonaOverride   string   // Active persona chip content (overrides ChatPersona when set)
	HungarianKeywords []string // Keywords for Hungarian detection
	FeedbackDir       string   // Directory for feedback files (default: "data/{corp}/feedback")
	// Stage-specific provider/model overrides (student/teacher architecture)
	SQLProviderOverride    string // e.g. "ollama" — provider for NL→SQL stage
	SQLModelOverride       string // e.g. "qwen2.5-coder:7b"
	RepairProviderOverride string // e.g. "gemini" — teacher for SQL repair
	RepairModelOverride    string
	ChatProviderOverride   string // e.g. "ollama" — provider for NL synthesis
	ChatModelOverride      string
	// User's UI selection — only applied when RAG finds NO match (direct NL path).
	UserProviderOverride string
	UserModelOverride    string
	// Collection-based model routing: collection name → stage overrides
	CollectionRoutes map[string]CollectionRoute
}

// DefaultOptions returns PipelineOptions with all features enabled
func DefaultOptions() PipelineOptions {
	return PipelineOptions{Feedback: true, Repair: true, RLS: true, CorporateID: "ulyssys"}
}

// DefaultConfig returns a PipelineConfig with sensible defaults loaded from embedded persona files.
func DefaultConfig() PipelineConfig {
	return PipelineConfig{
		SQLSystemPrompt: LoadPersona("sql"),
		ChatPersona:     LoadPersona("chat"),
	}
}
