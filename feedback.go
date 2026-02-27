package aichat

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SaveSQLErrorFeedback writes a structured YAML feedback file when SQL execution fails.
// These files can be reviewed to identify gaps in MCP chain instructions.
func SaveSQLErrorFeedback(corporateID, user, question, generatedSQL, pgError, ragTopics, feedbackDir string) {
	dir := resolveFeedbackDir(corporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("[AI-CHAT] feedback dir error: %v\n", err)
		return
	}

	ts := time.Now()
	filename := filepath.Join(dir, fmt.Sprintf("%s-%s-sqlerr.yaml", ts.Format("20060102_150405"), user))

	content := fmt.Sprintf(`type: sql_error
timestamp: "%s"
user: "%s"
question: "%s"
generated_sql: |
  %s
pg_error: "%s"
rag_topics: "%s"
`, ts.Format(time.RFC3339), user, yamlEscape(question), indentSQL(generatedSQL), yamlEscape(pgError), yamlEscape(ragTopics))

	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		fmt.Printf("[AI-CHAT] feedback write error: %v\n", err)
	} else {
		fmt.Printf("[AI-CHAT] SQL error feedback saved: %s\n", filename)
	}
}

// SaveZeroResultWarning writes a warning YAML when SQL execution succeeds but returns 0 rows.
// This helps identify logic flaws or hallucinations in the generated SQL filters.
func SaveZeroResultWarning(corporateID, user, question, generatedSQL, ragTopics, feedbackDir string) {
	dir := resolveFeedbackDir(corporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("[AI-CHAT] warning dir error: %v\n", err)
		return
	}

	ts := time.Now()
	filename := filepath.Join(dir, fmt.Sprintf("%s-%s-warning.yaml", ts.Format("20060102_150405"), user))

	content := fmt.Sprintf(`type: zero_result_warning
timestamp: "%s"
user: "%s"
question: "%s"
generated_sql: |
  %s
message: "SQL executed successfully but returned 0 rows. Check WHERE clause logic for hallucinated filters."
rag_topics: "%s"
`, ts.Format(time.RFC3339), user, yamlEscape(question), indentSQL(generatedSQL), yamlEscape(ragTopics))

	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		fmt.Printf("[AI-CHAT] warning write error: %v\n", err)
	} else {
		fmt.Printf("[AI-CHAT] Zero-result warning saved: %s\n", filename)
	}
}

// SaveAuditLog writes query metadata to the rag.audit_log table.
func SaveAuditLog(db *sql.DB, user string, result *PipelineResult) {
	if db == nil {
		return
	}

	status := "success"
	errMsg := ""
	if result.Error != "" {
		status = "error"
		errMsg = result.Error
	}

	query := `
		INSERT INTO rag.audit_log (
			hl_question, generated_sql, executed_sql, is_modified, 
			execution_time_ms, result_count, status, error_message, 
			username, prompt_tokens, completion_tokens, total_tokens, cost
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	isModified := strings.TrimSpace(result.GeneratedSQL) != strings.TrimSpace(result.UserMessage)

	_, err := db.Exec(query,
		result.UserMessage, result.GeneratedSQL, result.GeneratedSQL, isModified,
		result.Duration.Milliseconds(), result.RowCount, status, errMsg,
		user, result.PromptTokens, result.CompletionTokens, result.TotalTokens, result.Cost)

	if err != nil {
		fmt.Printf("[AI-CHAT] audit log error: %v\n", err)
	}
}

// SaveTrainingEntry appends a corrected Q&A pair to training JSONL for Ollama fine-tuning.
// Called when an auto-fix retry succeeds — these are "hard" questions the LLM got wrong initially.
func SaveTrainingEntry(corporateID, question, correctedSQL, feedbackDir string) {
	dir := resolveFeedbackDir(corporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("[AI-CHAT] training dir error: %v\n", err)
		return
	}

	trainingFile := filepath.Join(dir, "training_auto.jsonl")

	entry := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": "You are a SQL expert for the JiraMntr DWH. Generate PostgreSQL SQL queries using dwh.* helper functions. Use 'me' as the default user parameter. Always use upper_inf(valid_period) for historized tables."},
			{"role": "user", "content": question},
			{"role": "assistant", "content": "```sql\n" + correctedSQL + "\n```"},
		},
		"source":    "auto_retry",
		"timestamp": time.Now().Format(time.RFC3339),
	}

	jsonLine, err := json.Marshal(entry)
	if err != nil {
		fmt.Printf("[AI-CHAT] training marshal error: %v\n", err)
		return
	}

	f, err := os.OpenFile(trainingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("[AI-CHAT] training file error: %v\n", err)
		return
	}
	defer f.Close()

	f.Write(jsonLine)
	f.Write([]byte("\n"))
	fmt.Printf("[AI-CHAT] 📝 Training entry saved: %s\n", trainingFile)
}

// SaveManualFeedback saves the last Q&A as a YAML feedback file.
func SaveManualFeedback(user string, result *PipelineResult, feedbackDir string) {
	dir := resolveFeedbackDir(result.CorporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("[AI-CHAT] feedback dir error: %v\n", err)
		return
	}

	ts := time.Now()
	filename := filepath.Join(dir, fmt.Sprintf("%s-%s-manual.yaml", ts.Format("20060102_150405"), user))

	content := fmt.Sprintf(`type: manual
timestamp: "%s"
user: "%s"
question: "%s"
generated_sql: |
  %s
answer: |
  %s
rag_topics: "%s"
`, ts.Format(time.RFC3339), user, yamlEscape(result.UserMessage), indentSQL(result.GeneratedSQL), indentSQL(result.Answer), yamlEscape(result.RAGTopics))

	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		fmt.Printf("[AI-CHAT] feedback write error: %v\n", err)
	} else {
		fmt.Printf("[AI-CHAT] Manual feedback saved: %s\n", filename)
	}

	SaveTrainingEntry(result.CorporateID, result.UserMessage, result.GeneratedSQL, feedbackDir)
}

func resolveFeedbackDir(corporateID, feedbackDir string) string {
	if feedbackDir != "" {
		return feedbackDir
	}
	if corporateID == "" {
		corporateID = "ulyssys"
	}
	return filepath.Join("data", corporateID, "feedback")
}
