package aichat

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SaveSQLErrorFeedback writes a structured YAML feedback file AND inserts into mcp.feedback
// when SQL execution fails. The DB insert is the primary record; YAML is kept as backup.
func SaveSQLErrorFeedback(db *sql.DB, corporateID, user, question, generatedSQL, pgError, ragTopics, feedbackDir string) {
	// Insert into mcp.feedback (primary)
	if db != nil {
		tenant := corporateID
		if tenant == "" {
			tenant = "ulyssys"
		}
		payload, _ := json.Marshal(map[string]string{
			"tenant":        tenant,
			"username":      user,
			"type":          "sqlerr",
			"message":       "Auto: SQL execution failed",
			"question":      question,
			"generated_sql": generatedSQL,
			"pg_error":      pgError,
		})
		_, err := db.Exec(`SELECT mcp.insert_feedback($1::jsonb)`, string(payload))
		if err != nil {
			slog.Error("feedback DB insert failed", "error", err)
		} else {
			slog.Info("SQL error feedback saved to mcp.feedback", "user", user)
		}
	}

	// Write YAML backup
	dir := resolveFeedbackDir(corporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("feedback dir error", "error", err)
		return
	}

	ts := time.Now()
	filename := filepath.Join(dir, ts.Format("20060102_150405")+"-"+user+"-sqlerr.yaml")

	content, _ := json.Marshal(map[string]string{
		"type":          "sql_error",
		"timestamp":     ts.Format(time.RFC3339),
		"user":          user,
		"question":      question,
		"generated_sql": generatedSQL,
		"pg_error":      pgError,
		"rag_topics":    ragTopics,
	})

	if err := os.WriteFile(filename, content, 0644); err != nil {
		slog.Error("feedback write error", "error", err)
	} else {
		slog.Info("SQL error feedback saved", "file", filename)
	}
}

// SaveZeroResultWarning writes a warning YAML AND inserts into mcp.feedback when SQL
// execution succeeds but returns 0 rows. Helps identify logic flaws or hallucinations.
func SaveZeroResultWarning(db *sql.DB, corporateID, user, question, generatedSQL, ragTopics, feedbackDir string) {
	// Insert into mcp.feedback (primary)
	if db != nil {
		tenant := corporateID
		if tenant == "" {
			tenant = "ulyssys"
		}
		payload, _ := json.Marshal(map[string]string{
			"tenant":        tenant,
			"username":      user,
			"type":          "warning",
			"message":       "Auto: SQL returned 0 rows — check WHERE clause",
			"question":      question,
			"generated_sql": generatedSQL,
		})
		_, err := db.Exec(`SELECT mcp.insert_feedback($1::jsonb)`, string(payload))
		if err != nil {
			slog.Error("warning DB insert failed", "error", err)
		} else {
			slog.Info("Zero-result warning saved to mcp.feedback", "user", user)
		}
	}

	// Write YAML backup
	dir := resolveFeedbackDir(corporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("warning dir error", "error", err)
		return
	}

	ts := time.Now()
	filename := filepath.Join(dir, ts.Format("20060102_150405")+"-"+user+"-warning.yaml")

	content, _ := json.Marshal(map[string]string{
		"type":          "zero_result_warning",
		"timestamp":     ts.Format(time.RFC3339),
		"user":          user,
		"question":      question,
		"generated_sql": generatedSQL,
		"message":       "SQL executed successfully but returned 0 rows",
		"rag_topics":    ragTopics,
	})

	if err := os.WriteFile(filename, content, 0644); err != nil {
		slog.Error("warning write error", "error", err)
	} else {
		slog.Info("Zero-result warning saved", "file", filename)
	}
}

// SaveAuditLog writes query metadata via mcp.insert_audit_aisql(JSONB) CRUD function.
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

	isModified := strings.TrimSpace(result.GeneratedSQL) != strings.TrimSpace(result.UserMessage)

	data := map[string]interface{}{
		"hl_question":       result.UserMessage,
		"generated_sql":     result.GeneratedSQL,
		"executed_sql":      result.GeneratedSQL,
		"is_modified":       isModified,
		"execution_time_ms": result.Duration.Milliseconds(),
		"result_count":      result.RowCount,
		"status":            status,
		"error_message":     errMsg,
		"username":          user,
		"prompt_tokens":     result.PromptTokens,
		"completion_tokens": result.CompletionTokens,
		"total_tokens":      result.TotalTokens,
		"cost":              result.Cost,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		slog.Error("audit log marshal error", "error", err)
		return
	}

	_, err = db.Exec(`SELECT mcp.insert_audit_aisql($1::jsonb)`, string(jsonData))
	if err != nil {
		slog.Error("audit log insert error", "error", err)
	}
}

// SaveTrainingEntry appends a corrected Q&A pair to training JSONL for Ollama fine-tuning.
// Called when an auto-fix retry succeeds — these are "hard" questions the LLM got wrong initially.
func SaveTrainingEntry(corporateID, question, correctedSQL, feedbackDir string) {
	dir := resolveFeedbackDir(corporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("training dir error", "error", err)
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
		slog.Error("training marshal error", "error", err)
		return
	}

	f, err := os.OpenFile(trainingFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Error("training file error", "error", err)
		return
	}
	defer f.Close()

	f.Write(jsonLine)
	f.Write([]byte("\n"))
	slog.Info("Training entry saved", "file", trainingFile)
}

// SaveManualFeedback saves the last Q&A as a JSON feedback file.
func SaveManualFeedback(user string, result *PipelineResult, feedbackDir string) {
	dir := resolveFeedbackDir(result.CorporateID, feedbackDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("feedback dir error", "error", err)
		return
	}

	ts := time.Now()
	filename := filepath.Join(dir, ts.Format("20060102_150405")+"-"+user+"-manual.json")

	content, _ := json.Marshal(map[string]string{
		"type":          "manual",
		"timestamp":     ts.Format(time.RFC3339),
		"user":          user,
		"question":      result.UserMessage,
		"generated_sql": result.GeneratedSQL,
		"answer":        result.Answer,
		"rag_topics":    result.RAGTopics,
	})

	if err := os.WriteFile(filename, content, 0644); err != nil {
		slog.Error("feedback write error", "error", err)
	} else {
		slog.Info("Manual feedback saved", "file", filename)
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
