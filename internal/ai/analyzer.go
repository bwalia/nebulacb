package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
)

// Analyzer provides AI-powered log analysis, troubleshooting, and recommendations.
type Analyzer struct {
	config    models.AIConfig
	collector *metrics.Collector
	client    *http.Client

	mu       sync.RWMutex
	insights []models.AIInsight
	history  []models.AIAnalysisResponse
}

// NewAnalyzer creates a new AI analyzer.
func NewAnalyzer(cfg models.AIConfig, collector *metrics.Collector) *Analyzer {
	return &Analyzer{
		config:    cfg,
		collector: collector,
		client:    &http.Client{Timeout: 120 * time.Second},
		insights:  make([]models.AIInsight, 0),
		history:   make([]models.AIAnalysisResponse, 0),
	}
}

// Analyze sends logs/errors/metrics to the configured AI model and returns insights.
func (a *Analyzer) Analyze(ctx context.Context, req models.AIAnalysisRequest) (*models.AIAnalysisResponse, error) {
	if !a.config.Enabled {
		return nil, fmt.Errorf("AI analysis is disabled")
	}

	prompt := a.buildPrompt(req)
	start := time.Now()

	var rawReply string
	var tokensUsed int
	var err error

	switch strings.ToLower(a.config.Provider) {
	case "anthropic":
		rawReply, tokensUsed, err = a.callAnthropic(ctx, prompt)
	case "openai":
		rawReply, tokensUsed, err = a.callOpenAI(ctx, prompt)
	case "ollama":
		rawReply, tokensUsed, err = a.callOllama(ctx, prompt)
	case "custom":
		rawReply, tokensUsed, err = a.callCustom(ctx, prompt)
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", a.config.Provider)
	}

	if err != nil {
		return nil, fmt.Errorf("AI API call failed: %w", err)
	}

	insight := a.parseInsight(rawReply, req)
	duration := time.Since(start).Seconds()

	resp := &models.AIAnalysisResponse{
		Insight:    insight,
		RawReply:   rawReply,
		TokensUsed: tokensUsed,
		Duration:   duration,
	}

	a.mu.Lock()
	a.insights = append(a.insights, insight)
	if len(a.insights) > 100 {
		a.insights = a.insights[len(a.insights)-100:]
	}
	a.history = append(a.history, *resp)
	if len(a.history) > 50 {
		a.history = a.history[len(a.history)-50:]
	}
	a.mu.Unlock()

	log.Printf("[AI] Analysis completed in %.1fs (tokens: %d, provider: %s)", duration, tokensUsed, a.config.Provider)
	return resp, nil
}

// AutoAnalyze runs analysis on current cluster state and alerts.
func (a *Analyzer) AutoAnalyze(ctx context.Context) (*models.AIAnalysisResponse, error) {
	state := a.collector.GetDashboardState()

	// Build context from current state
	var contextParts []string
	for name, cm := range state.Clusters {
		status := "healthy"
		if !cm.Healthy {
			status = "UNHEALTHY"
		}
		contextParts = append(contextParts, fmt.Sprintf(
			"Cluster %s: %s, nodes=%d, docs=%d, ops/s=%.0f, mem=%.0f/%.0fMB, edition=%s, platform=%s",
			name, status, len(cm.Nodes), cm.TotalDocs, cm.OpsPerSec,
			cm.TotalMemUsedMB, cm.TotalMemTotalMB, cm.Edition, cm.Platform,
		))
	}

	// Add alerts
	for _, alert := range state.Alerts {
		if !alert.Resolved {
			contextParts = append(contextParts, fmt.Sprintf("ALERT [%s] %s: %s", alert.Severity, alert.Title, alert.Message))
		}
	}

	req := models.AIAnalysisRequest{
		Type:    "cluster_health",
		Context: strings.Join(contextParts, "\n"),
	}

	return a.Analyze(ctx, req)
}

// GetInsights returns recent AI insights.
func (a *Analyzer) GetInsights() []models.AIInsight {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]models.AIInsight, len(a.insights))
	copy(result, a.insights)
	return result
}

// GetHistory returns recent analysis history.
func (a *Analyzer) GetHistory() []models.AIAnalysisResponse {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]models.AIAnalysisResponse, len(a.history))
	copy(result, a.history)
	return result
}

func (a *Analyzer) buildPrompt(req models.AIAnalysisRequest) string {
	var sb strings.Builder
	sb.WriteString("You are NebulaCB AI — an expert Couchbase cluster management assistant. ")
	sb.WriteString("Analyze the following and provide actionable insights.\n\n")

	switch req.Type {
	case "logs":
		sb.WriteString("## Log Analysis\n")
		sb.WriteString("Analyze these Couchbase cluster logs for errors, warnings, and anomalies:\n\n")
		for _, l := range req.Logs {
			sb.WriteString(l)
			sb.WriteString("\n")
		}
	case "error":
		sb.WriteString("## Error Troubleshooting\n")
		sb.WriteString(fmt.Sprintf("Error: %s\n\n", req.ErrorMsg))
		if req.ClusterName != "" {
			sb.WriteString(fmt.Sprintf("Cluster: %s\n", req.ClusterName))
		}
		sb.WriteString("Provide: root cause, immediate fix, and prevention steps.\n")
	case "performance":
		sb.WriteString("## Performance Analysis\n")
		sb.WriteString("Analyze these Couchbase cluster metrics and identify bottlenecks:\n\n")
		sb.WriteString(req.MetricsJSON)
		sb.WriteString("\n\nProvide: bottleneck identification, tuning recommendations, and capacity planning advice.\n")
	case "cluster_health":
		sb.WriteString("## Cluster Health Analysis\n")
		sb.WriteString(req.Context)
		sb.WriteString("\n\nProvide: health assessment, risk factors, and recommendations for each cluster.\n")
	case "troubleshoot":
		sb.WriteString("## Troubleshooting\n")
		if req.Question != "" {
			sb.WriteString(fmt.Sprintf("Question: %s\n\n", req.Question))
		}
		if req.Context != "" {
			sb.WriteString(fmt.Sprintf("Context:\n%s\n\n", req.Context))
		}
		sb.WriteString("Provide step-by-step troubleshooting guidance.\n")
	default:
		sb.WriteString(req.Context)
	}

	sb.WriteString("\n\nRespond in this JSON format:\n")
	sb.WriteString(`{"severity":"info|warning|critical","title":"Brief title","summary":"One paragraph summary","suggestions":["action 1","action 2"]}`)

	return sb.String()
}

func (a *Analyzer) parseInsight(rawReply string, req models.AIAnalysisRequest) models.AIInsight {
	insight := models.AIInsight{
		ID:         fmt.Sprintf("ai-%d", time.Now().UnixNano()),
		Type:       req.Type,
		Cluster:    req.ClusterName,
		Confidence: 0.8,
		Timestamp:  time.Now(),
	}

	// Try to parse structured JSON from the reply
	var parsed struct {
		Severity    string   `json:"severity"`
		Title       string   `json:"title"`
		Summary     string   `json:"summary"`
		Suggestions []string `json:"suggestions"`
	}

	// Try to find JSON in the reply
	jsonStart := strings.Index(rawReply, "{")
	jsonEnd := strings.LastIndex(rawReply, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		jsonStr := rawReply[jsonStart : jsonEnd+1]
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err == nil {
			insight.Severity = parsed.Severity
			insight.Title = parsed.Title
			insight.Summary = parsed.Summary
			insight.Suggestions = parsed.Suggestions
			insight.Confidence = 0.9
			return insight
		}
	}

	// Fallback: use raw reply as summary
	insight.Severity = "info"
	insight.Title = fmt.Sprintf("AI Analysis: %s", req.Type)
	insight.Summary = rawReply
	if len(insight.Summary) > 500 {
		insight.Summary = insight.Summary[:500] + "..."
	}
	insight.Details = rawReply

	return insight
}

// ─── Provider Implementations ───────────────────────────────────────────────

func (a *Analyzer) callAnthropic(ctx context.Context, prompt string) (string, int, error) {
	endpoint := "https://api.anthropic.com/v1/messages"
	if a.config.APIEndpoint != "" {
		endpoint = a.config.APIEndpoint
	}

	model := a.config.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	maxTokens := a.config.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", 0, fmt.Errorf("Anthropic API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, err
	}

	text := ""
	if len(result.Content) > 0 {
		text = result.Content[0].Text
	}
	return text, result.Usage.InputTokens + result.Usage.OutputTokens, nil
}

func (a *Analyzer) callOpenAI(ctx context.Context, prompt string) (string, int, error) {
	endpoint := "https://api.openai.com/v1/chat/completions"
	if a.config.APIEndpoint != "" {
		endpoint = a.config.APIEndpoint
	}

	model := a.config.Model
	if model == "" {
		model = "gpt-4"
	}
	maxTokens := a.config.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": "You are NebulaCB AI, an expert Couchbase cluster management assistant."},
			{"role": "user", "content": prompt},
		},
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", 0, fmt.Errorf("OpenAI API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, err
	}

	text := ""
	if len(result.Choices) > 0 {
		text = result.Choices[0].Message.Content
	}
	return text, result.Usage.TotalTokens, nil
}

func (a *Analyzer) callOllama(ctx context.Context, prompt string) (string, int, error) {
	endpoint := a.config.APIEndpoint
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/api/generate"

	model := a.config.Model
	if model == "" {
		model = "llama3"
	}

	body := map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", 0, fmt.Errorf("Ollama API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Response       string `json:"response"`
		PromptEvalCount int   `json:"prompt_eval_count"`
		EvalCount      int    `json:"eval_count"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, err
	}

	return result.Response, result.PromptEvalCount + result.EvalCount, nil
}

func (a *Analyzer) callCustom(ctx context.Context, prompt string) (string, int, error) {
	if a.config.APIEndpoint == "" {
		return "", 0, fmt.Errorf("custom provider requires api_endpoint")
	}

	body := map[string]interface{}{
		"model":   a.config.Model,
		"prompt":  prompt,
		"max_tokens": a.config.MaxTokens,
	}

	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", a.config.APIEndpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", 0, fmt.Errorf("custom API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// Try common response formats
	var generic map[string]interface{}
	if err := json.Unmarshal(respBody, &generic); err != nil {
		return string(respBody), 0, nil
	}

	// Try "response" field
	if r, ok := generic["response"].(string); ok {
		return r, 0, nil
	}
	// Try "content" field
	if c, ok := generic["content"].(string); ok {
		return c, 0, nil
	}
	// Try "text" field
	if t, ok := generic["text"].(string); ok {
		return t, 0, nil
	}

	return string(respBody), 0, nil
}
