package aichat

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// emit sends a stage event to the observer if one is configured. Nil-safe.
func emit(obs PipelineObserver, event StageEvent) {
	if obs != nil {
		obs.OnStageEvent(event)
	}
}

// RunPipeline executes the full NL → SQL → Execute → NL pipeline.
// This is the unified pipeline used by both JIRAMNTR and Johanna.
// Pass history to enable multi-turn conversation memory within a session.
func RunPipeline(ctx context.Context, client AIClient, ragProvider RAGProvider, executor SQLExecutor,
	user, question string, history []Message, cfg PipelineConfig, opts PipelineOptions) PipelineResult {

	start := time.Now()
	result := PipelineResult{UserMessage: question, CorporateID: opts.CorporateID}
	obs := opts.Observer

	// ── Stage 0: Build RAG context (with collection metadata) ──
	ragStart := time.Now()
	emit(obs, StageEvent{Stage: "rag", Status: "start", Message: "Building RAG context"})

	ragMeta, err := ragProvider.BuildContextWithMeta(ctx, question)
	if err != nil {
		emit(obs, StageEvent{Stage: "rag", Status: "error", Message: fmt.Sprintf("RAG warning: %v", err), Duration: time.Since(ragStart).Milliseconds()})
	} else {
		emit(obs, StageEvent{Stage: "rag", Status: "done", Message: "RAG context built", Duration: time.Since(ragStart).Milliseconds(),
			Meta: map[string]string{"collection": ragMeta.Collection}})
	}
	ragContext := ragMeta.Context

	// ── SQL Follow-up: reuse previous RAG context when current query has no match ──
	if ragContext == "" && opts.LastResultHadSQL && opts.LastRAGContext != "" {
		ragContext = opts.LastRAGContext
		emit(obs, StageEvent{Stage: "rag", Status: "done", Message: fmt.Sprintf("SQL follow-up: reusing previous RAG context (%d bytes)", len(ragContext))})
	}

	var ragTopics []string
	if ragContext != "" {
		for _, line := range strings.Split(ragContext, "\n") {
			if strings.HasPrefix(line, "### ") {
				ragTopics = append(ragTopics, strings.TrimPrefix(line, "### "))
			}
		}
	}
	result.RAGTopics = strings.Join(ragTopics, ", ")
	result.RAGContext = ragContext // Store for follow-up reuse

	// ── Collection-based model routing ──
	// If RAG matched a collection and a route is configured, override stage models
	if ragMeta.Collection != "" && cfg.CollectionRoutes != nil {
		if route, ok := cfg.CollectionRoutes[ragMeta.Collection]; ok {
			emit(obs, StageEvent{Stage: "rag", Status: "done", Message: fmt.Sprintf("Collection route: %q — applying model overrides", ragMeta.Collection)})
			if route.SQLProvider != "" {
				cfg.SQLProviderOverride = route.SQLProvider
			}
			if route.SQLModel != "" {
				cfg.SQLModelOverride = route.SQLModel
			}
			if route.RepairProvider != "" {
				cfg.RepairProviderOverride = route.RepairProvider
			}
			if route.RepairModel != "" {
				cfg.RepairModelOverride = route.RepairModel
			}
			if route.ChatProvider != "" {
				cfg.ChatProviderOverride = route.ChatProvider
			}
			if route.ChatModel != "" {
				cfg.ChatModelOverride = route.ChatModel
			}
		}
	}

	// ── Relevancy Gate: skip SQL pipeline for non-DWH questions ──
	// Bypass gate if previous turn was SQL (follow-up like "tavaly?" should stay in SQL pipeline)
	if ragContext == "" && ragProvider.IsRelevancyGateEnabled() && !opts.LastResultHadSQL {
		emit(obs, StageEvent{Stage: "relevancy_gate", Status: "skip", Message: "No RAG match → direct LLM answer"})

		langName := resolveLang(opts.Lang, question, cfg.HungarianKeywords)

		var directPersona string
		if cfg.PersonaOverride != "" {
			directPersona = cfg.PersonaOverride
		} else if cfg.DirectPersona != "" {
			directPersona = cfg.DirectPersona
		} else {
			emit(obs, StageEvent{Stage: "relevancy_gate", Status: "error", Message: "No direct persona configured — using empty system prompt"})
		}
		directPersona = strings.ReplaceAll(directPersona, "{lang}", langName)

		// Direct NL path: honor user's UI provider/model selection
		directProvider := cfg.ChatProviderOverride
		directModel := cfg.ChatModelOverride
		if cfg.UserProviderOverride != "" {
			directProvider = cfg.UserProviderOverride
		}
		if cfg.UserModelOverride != "" {
			directModel = cfg.UserModelOverride
		}

		aiStart := time.Now()
		emit(obs, StageEvent{Stage: "ai_generate", Status: "start", Message: "Generating direct AI response",
			Meta: map[string]string{"provider": directProvider, "model": directModel}})

		directResult, err := client.GenerateContent(ctx, question, history, directPersona, directProvider, directModel)
		if err != nil {
			result.Error = fmt.Sprintf("LLM error: %v", err)
			emit(obs, StageEvent{Stage: "ai_generate", Status: "error", Message: fmt.Sprintf("LLM error: %v", err), Duration: time.Since(aiStart).Milliseconds()})
		} else {
			result.Answer = directResult.Content
			emit(obs, StageEvent{Stage: "ai_generate", Status: "done", Message: "Direct AI response generated", Duration: time.Since(aiStart).Milliseconds()})
		}
		result.Duration = time.Since(start)
		return result
	}

	emit(obs, StageEvent{Stage: "relevancy_gate", Status: "done", Message: "RAG match found — entering SQL pipeline"})

	// ── Stage 1: NL → SQL (RAG + LLM) ──
	var sqlPrompt strings.Builder
	if ragContext != "" {
		sqlPrompt.WriteString(ragContext)
		sqlPrompt.WriteString("\n\n")
	}
	sqlPrompt.WriteString(fmt.Sprintf("### [SESSION CONTEXT]\n- Login user (\"me\", \"my\", \"I\", \"én\", \"nekem\"): %s\n", user))
	if opts.TodayOverride != "" {
		sqlPrompt.WriteString(fmt.Sprintf("- Today's date: %s (use '%s'::date instead of CURRENT_DATE)\n", opts.TodayOverride, opts.TodayOverride))
	}
	sqlPrompt.WriteString("\n")
	sqlPrompt.WriteString(fmt.Sprintf("### [USER REQUEST]\n%s", question))

	sqlSysPrompt := cfg.SQLSystemPrompt
	if sqlSysPrompt == "" {
		emit(obs, StageEvent{Stage: "nl_to_sql", Status: "error", Message: "No SQL system prompt configured — using empty system prompt"})
	}

	sqlStart := time.Now()
	emit(obs, StageEvent{Stage: "nl_to_sql", Status: "start", Message: fmt.Sprintf("Generating SQL for: %s", question),
		Meta: map[string]string{"provider": cfg.SQLProviderOverride, "model": cfg.SQLModelOverride, "user": user}})

	sqlResult, err := client.GenerateContent(ctx, sqlPrompt.String(), history, sqlSysPrompt, cfg.SQLProviderOverride, cfg.SQLModelOverride)
	if err != nil {
		result.Error = fmt.Sprintf("AI generation error: %v", err)
		result.Duration = time.Since(start)
		emit(obs, StageEvent{Stage: "nl_to_sql", Status: "error", Message: fmt.Sprintf("AI generation error: %v", err), Duration: time.Since(sqlStart).Milliseconds()})
		return result
	}

	emit(obs, StageEvent{Stage: "nl_to_sql", Status: "done", Message: "SQL generated", Duration: time.Since(sqlStart).Milliseconds()})

	result.PromptTokens = sqlResult.PromptTokens
	result.CompletionTokens = sqlResult.CompletionTokens
	result.TotalTokens = sqlResult.TotalTokens
	result.Cost = sqlResult.Cost

	generatedSQL := ExtractSQL(sqlResult.Content)

	// Intercept non-SQL responses (e.g. irrelevant questions where LLM refuses safely)
	upperSQL := strings.ToUpper(generatedSQL)
	if !strings.Contains(upperSQL, "SELECT") && !strings.Contains(upperSQL, "WITH") {
		result.Answer = generatedSQL
		result.GeneratedSQL = ""
		result.Duration = time.Since(start)
		emit(obs, StageEvent{Stage: "nl_to_sql", Status: "skip", Message: "Non-SQL response intercepted — returning as direct answer"})
		return result
	}

	// Detect constant-only SELECT (e.g. SELECT 'Sorry...' AS message)
	if !strings.Contains(upperSQL, "FROM") {
		constantRe := regexp.MustCompile(`(?i)SELECT\s+'([^']+)'`)
		if m := constantRe.FindStringSubmatch(generatedSQL); len(m) > 1 {
			result.Answer = m[1]
		} else {
			result.Answer = generatedSQL
		}
		result.GeneratedSQL = ""
		result.Duration = time.Since(start)
		emit(obs, StageEvent{Stage: "nl_to_sql", Status: "skip", Message: "Constant-only SELECT (no FROM) — returning as direct answer"})
		return result
	}

	// Replace template variables with actual values
	generatedSQL = SubstituteLoginUser(generatedSQL, user)
	if opts.TodayOverride != "" {
		generatedSQL = strings.ReplaceAll(generatedSQL, "CURRENT_DATE", fmt.Sprintf("'%s'::date", opts.TodayOverride))
	}
	result.GeneratedSQL = generatedSQL

	// ── Stage 2: Execute SQL (with auto-retry) ──
	maxRetries := 0
	if opts.Repair {
		maxRetries = 2
	}
	var retryLog []string
	var rows [][]string
	var cols []string
	var execErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		execStart := time.Now()
		emit(obs, StageEvent{Stage: "execute", Status: "start", Message: fmt.Sprintf("Executing SQL (attempt %d/%d)", attempt+1, maxRetries+1)})

		rows, cols, execErr = executor.Execute(user, generatedSQL, opts.RLS)
		if execErr == nil {
			emit(obs, StageEvent{Stage: "execute", Status: "done",
				Message:  fmt.Sprintf("SQL executed successfully (%d rows)", len(rows)),
				Duration: time.Since(execStart).Milliseconds(),
				Meta:     map[string]string{"row_count": fmt.Sprintf("%d", len(rows))}})
			if attempt > 0 {
				retryLog = append(retryLog, fmt.Sprintf("✅ Query succeeded after %d retry", attempt))
				if opts.Feedback {
					go SaveTrainingEntry(opts.CorporateID, question, generatedSQL, cfg.FeedbackDir)
				}
			}
			break
		}

		emit(obs, StageEvent{Stage: "execute", Status: "error",
			Message:  fmt.Sprintf("SQL error (attempt %d/%d): %v", attempt+1, maxRetries+1, execErr),
			Duration: time.Since(execStart).Milliseconds()})

		if attempt == maxRetries {
			retryLog = append(retryLog, fmt.Sprintf("❌ All %d attempts failed", maxRetries+1))
			result.Error = fmt.Sprintf("SQL execution failed after %d attempts: %v", maxRetries+1, execErr)
			result.Answer = strings.Join(retryLog, "\n") + fmt.Sprintf("\n\nI generated this SQL but it failed to execute:\n\n```sql\n%s\n```\n\nError: %s", generatedSQL, execErr.Error())
			result.Duration = time.Since(start)
			if opts.Feedback {
				go SaveSQLErrorFeedback(opts.DB, opts.CorporateID, user, question, generatedSQL, execErr.Error(), result.RAGTopics, cfg.FeedbackDir)
			}
			return result
		}

		// ── Stage 2.5: Ask LLM to fix the SQL ──
		retryLog = append(retryLog, fmt.Sprintf("⚠️ SQL error: %s — retrying...", execErr.Error()))

		repairStart := time.Now()
		emit(obs, StageEvent{Stage: "repair", Status: "start", Message: fmt.Sprintf("Asking LLM to fix SQL (retry %d/%d)", attempt+1, maxRetries),
			Meta: map[string]string{"provider": cfg.RepairProviderOverride, "model": cfg.RepairModelOverride}})

		fixPrompt := fmt.Sprintf(
			"The following SQL query failed with a PostgreSQL error.\n\n"+
				"Original question: %s\n\n"+
				"Failed SQL:\n```sql\n%s\n```\n\n"+
				"Error: %s\n\n"+
				"Fix the SQL and return ONLY the corrected query. Common fixes:\n"+
				"- Use dwh.dim_user_h (not dim_user) with upper_inf(valid_period)\n"+
				"- Use alias 'u' for dim_user_h, 'i' for dim_issue_h, 'w' for fact_daily_worklogs_h\n"+
				"- ltree_path is on dim_user_h, not dim_issue_h\n"+
				"- TSTZRANGE columns need timestamptz, not date\n"+
				"- For user/team queries, use dwh.user_get_*() functions\n",
			question, generatedSQL, execErr.Error())

		fixResult, fixErr := client.GenerateContent(ctx, fixPrompt, nil, sqlSysPrompt, cfg.RepairProviderOverride, cfg.RepairModelOverride)
		if fixErr != nil {
			retryLog = append(retryLog, fmt.Sprintf("❌ LLM fix failed: %v", fixErr))
			result.Error = fmt.Sprintf("SQL fix attempt failed: %v", fixErr)
			result.Answer = strings.Join(retryLog, "\n") + fmt.Sprintf("\n\nOriginal SQL:\n```sql\n%s\n```\nError: %s", generatedSQL, execErr.Error())
			result.Duration = time.Since(start)
			emit(obs, StageEvent{Stage: "repair", Status: "error", Message: fmt.Sprintf("LLM fix failed: %v", fixErr), Duration: time.Since(repairStart).Milliseconds()})
			if opts.Feedback {
				go SaveSQLErrorFeedback(opts.DB, opts.CorporateID, user, question, generatedSQL, execErr.Error(), result.RAGTopics, cfg.FeedbackDir)
			}
			return result
		}

		emit(obs, StageEvent{Stage: "repair", Status: "done", Message: "SQL repaired by LLM", Duration: time.Since(repairStart).Milliseconds()})

		result.PromptTokens += fixResult.PromptTokens
		result.CompletionTokens += fixResult.CompletionTokens
		result.TotalTokens += fixResult.TotalTokens
		result.Cost += fixResult.Cost

		generatedSQL = ExtractSQL(fixResult.Content)
		generatedSQL = SubstituteLoginUser(generatedSQL, user)
		if opts.TodayOverride != "" {
			generatedSQL = strings.ReplaceAll(generatedSQL, "CURRENT_DATE", fmt.Sprintf("'%s'::date", opts.TodayOverride))
		}
		result.GeneratedSQL = generatedSQL
		result.RetryCount = attempt + 1
	}

	result.Columns = cols
	result.Rows = rows
	result.RowCount = len(rows)

	if len(rows) == 0 && opts.Feedback {
		go SaveZeroResultWarning(opts.DB, opts.CorporateID, user, question, generatedSQL, result.RAGTopics, cfg.FeedbackDir)
	}

	// Build result table text for synthesis
	var resultText strings.Builder
	resultText.WriteString(fmt.Sprintf("Query returned %d rows.\n", len(rows)))
	resultText.WriteString("Columns: " + strings.Join(cols, " | ") + "\n")
	for i, row := range rows {
		if i >= 30 {
			resultText.WriteString("... (truncated)\n")
			break
		}
		resultText.WriteString(strings.Join(row, " | ") + "\n")
	}

	// ── Stage 3: SQL Results → NL Answer (Persona-driven) ──
	langName := resolveLang(opts.Lang, question, cfg.HungarianKeywords)

	persona := cfg.ChatPersona
	if persona == "" {
		emit(obs, StageEvent{Stage: "synthesis", Status: "error", Message: "No chat persona configured — using empty system prompt"})
	}
	persona = strings.ReplaceAll(persona, "{lang}", langName)
	persona = strings.ReplaceAll(persona, "{user}", user)
	persona = strings.ReplaceAll(persona, "{question}", question)
	persona = strings.ReplaceAll(persona, "{row_count}", fmt.Sprintf("%d", len(rows)))
	if cfg.PersonaOverride != "" {
		persona = cfg.PersonaOverride
	}

	synthesisPrompt := fmt.Sprintf(
		"The user asked: \"%s\"\n\n"+
			"Here are the SQL query results (%d rows):\n%s\n\n"+
			"Provide a natural language answer.",
		question, len(rows), resultText.String())

	synthStart := time.Now()
	emit(obs, StageEvent{Stage: "synthesis", Status: "start", Message: "Synthesizing NL answer",
		Meta: map[string]string{"provider": cfg.ChatProviderOverride, "model": cfg.ChatModelOverride, "row_count": fmt.Sprintf("%d", len(rows))}})

	synthResult, err := client.GenerateContent(ctx, synthesisPrompt, history, persona, cfg.ChatProviderOverride, cfg.ChatModelOverride)
	if err != nil {
		result.Answer = resultText.String()
		emit(obs, StageEvent{Stage: "synthesis", Status: "error", Message: fmt.Sprintf("Synthesis error: %v — returning raw results", err), Duration: time.Since(synthStart).Milliseconds()})
	} else {
		result.Answer = synthResult.Content
		result.PromptTokens += synthResult.PromptTokens
		result.CompletionTokens += synthResult.CompletionTokens
		result.TotalTokens += synthResult.TotalTokens
		result.Cost += synthResult.Cost
		emit(obs, StageEvent{Stage: "synthesis", Status: "done", Message: "NL answer synthesized", Duration: time.Since(synthStart).Milliseconds()})
	}

	// Prepend retry status if SQL was auto-fixed
	if len(retryLog) > 0 {
		result.Answer = strings.Join(retryLog, "\n") + "\n\n" + result.Answer
	}

	result.Duration = time.Since(start)
	emit(obs, StageEvent{Stage: "pipeline", Status: "done", Message: fmt.Sprintf("Pipeline complete in %dms", result.Duration.Milliseconds()),
		Duration: result.Duration.Milliseconds()})

	return result
}

// resolveLang determines the display language name
func resolveLang(lang, question string, hungarianKeywords []string) string {
	if lang == "hu" || IsHungarian(question, hungarianKeywords) {
		return "Hungarian"
	}
	return "English"
}
