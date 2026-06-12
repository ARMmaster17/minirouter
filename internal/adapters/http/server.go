package httpadapter

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ARMmaster17/minirouter/internal/app"
	"github.com/ARMmaster17/minirouter/internal/domain"
	"github.com/tiktoken-go/tokenizer"
)

type Server struct {
	router      *app.Router
	requestLogs app.RequestLogStore
	mux         *http.ServeMux
}

const statusClientClosedRequest = 499

var pageTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>MiniRouter Dashboard</title>
	<script src="https://unpkg.com/htmx.org@1.9.12"></script>
	<style>
		:root {
			--bg: linear-gradient(160deg, #f8fafc 0%, #e0f2fe 45%, #e2e8f0 100%);
			--card: rgba(255,255,255,0.78);
			--ink: #0f172a;
			--muted: #334155;
			--accent: #0ea5e9;
			--ok: #16a34a;
			--err: #dc2626;
		}
		body {
			margin: 0;
			font-family: "Segoe UI", "Gill Sans", sans-serif;
			color: var(--ink);
			background: var(--bg);
			min-height: 100vh;
		}
		.wrap {
			max-width: 1080px;
			margin: 0 auto;
			padding: 2rem 1rem 3rem;
		}
		h1 {
			margin: 0;
			font-size: clamp(1.5rem, 1.5vw + 1rem, 2.5rem);
			letter-spacing: 0.03em;
		}
		.subtitle {
			color: var(--muted);
			margin: 0.3rem 0 1.5rem;
		}
		.panel {
			background: var(--card);
			border: 1px solid rgba(15, 23, 42, 0.08);
			box-shadow: 0 10px 40px rgba(15, 23, 42, 0.08);
			border-radius: 16px;
			backdrop-filter: blur(4px);
			margin-bottom: 1rem;
			overflow: hidden;
		}
		.panel h2 {
			margin: 0;
			padding: 1rem 1rem 0.6rem;
			font-size: 1.05rem;
			letter-spacing: 0.02em;
		}
		.panel-body {
			padding: 0 1rem 1rem;
		}
		.stats-grid {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
			gap: 0.8rem;
		}
		.stat {
			border-radius: 12px;
			padding: 0.8rem;
			background: rgba(255,255,255,0.82);
			border: 1px solid rgba(15, 23, 42, 0.06);
		}
		.stat b {
			display: block;
			font-size: 1.2rem;
			margin-top: 0.25rem;
		}
		table {
			width: 100%;
			border-collapse: collapse;
			font-size: 0.92rem;
		}
		th, td {
			text-align: left;
			border-bottom: 1px solid rgba(15, 23, 42, 0.08);
			padding: 0.55rem;
			vertical-align: top;
		}
		th {
			color: var(--muted);
			font-size: 0.8rem;
			text-transform: uppercase;
			letter-spacing: 0.04em;
		}
		.status-ok { color: var(--ok); font-weight: 600; }
		.status-err { color: var(--err); font-weight: 600; }
	</style>
</head>
<body>
	<div class="wrap">
		<h1>MiniRouter Traffic Console</h1>
		<p class="subtitle">Live request routing overview powered by HTMX + SSE.</p>
		<div class="panel">
			<h2>Aggregate Stats</h2>
			<div class="panel-body" id="stats" hx-get="/ui/fragments/stats" hx-trigger="load, refresh from:body" hx-swap="innerHTML"></div>
		</div>
		<div class="panel">
			<h2>Active Requests By Provider</h2>
			<div class="panel-body" id="active-requests" hx-get="/ui/fragments/active-requests" hx-trigger="load, refresh from:body" hx-swap="innerHTML"></div>
		</div>
		<div class="panel">
			<h2>Recent Requests</h2>
			<div class="panel-body" id="requests" hx-get="/ui/fragments/requests" hx-trigger="load, refresh from:body" hx-swap="innerHTML"></div>
		</div>
		<div class="panel">
			<h2>Model Registry</h2>
			<div class="panel-body" id="models" hx-get="/ui/fragments/models" hx-trigger="load, refresh from:body" hx-swap="innerHTML"></div>
		</div>
	</div>
	<script>
		const stream = new EventSource('/ui/stream');
		stream.addEventListener('update', function () { htmx.trigger(document.body, 'refresh'); });
		stream.addEventListener('ping', function () { htmx.trigger(document.body, 'refresh'); });
	</script>
</body>
</html>`))

var tokenizerCache struct {
	sync.RWMutex
	codecs map[string]tokenizer.Codec
}

func New(router *app.Router, requestLogs ...app.RequestLogStore) *Server {
	tokenizerCache.codecs = make(map[string]tokenizer.Codec)
	var logs app.RequestLogStore
	if len(requestLogs) > 0 {
		logs = requestLogs[0]
	}
	server := &Server{router: router, requestLogs: logs, mux: http.NewServeMux()}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return loggingMiddleware(s.mux)
}

type responseLogger struct {
	http.ResponseWriter
	statusCode int
}

func (rl *responseLogger) WriteHeader(code int) {
	rl.statusCode = code
	rl.ResponseWriter.WriteHeader(code)
}

func (rl *responseLogger) Flush() {
	if flusher, ok := rl.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		remoteIP := r.RemoteAddr
		if ip, _, err := net.SplitHostPort(remoteIP); err == nil {
			remoteIP = ip
		}

		logger := &responseLogger{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(logger, r)

		duration := time.Since(start)
		statusColor := getStatusColor(logger.statusCode)

		log.Printf("%s %-5s %-30s %s%d%s %s (%v)",
			start.Format("15:04:05"),
			r.Method,
			r.RequestURI,
			statusColor,
			logger.statusCode,
			"\033[0m",
			remoteIP,
			duration)
	})
}

func getStatusColor(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "\033[32m" // Green
	case code >= 300 && code < 400:
		return "\033[36m" // Cyan
	case code >= 400 && code < 500:
		return "\033[33m" // Yellow
	case code >= 500:
		return "\033[31m" // Red
	default:
		return "\033[0m" // Reset
	}
}

func (s *Server) routes() {
	if s.router.Config.Server.FrontendEnabled {
		s.mux.HandleFunc("/", s.handleUIRoot)
		s.mux.HandleFunc("/ui/fragments/stats", s.handleUIStats)
		s.mux.HandleFunc("/ui/fragments/active-requests", s.handleUIActiveRequests)
		s.mux.HandleFunc("/ui/fragments/requests", s.handleUIRequests)
		s.mux.HandleFunc("/ui/fragments/models", s.handleUIModels)
		s.mux.HandleFunc("/ui/stream", s.handleUIStream)
	}
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("/v1/models", s.handleModels)
	s.mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !validateIncomingAPIKey(r, s.router.Config.Server.IncomingAPIKey) {
		writeError(w, http.StatusUnauthorized, errors.New("missing or invalid authorization bearer token"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": s.router.ListModels()})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !validateIncomingAPIKey(r, s.router.Config.Server.IncomingAPIKey) {
		writeError(w, http.StatusUnauthorized, errors.New("missing or invalid authorization bearer token"))
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return
	}
	startedAt := time.Now()
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read request body: %w", err))
		return
	}
	var req app.ChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		s.logRequest(r, req.Model, "", "", domain.ScoringResult{}, app.ChatResponse{}, http.StatusBadRequest, len(bodyBytes), 0, startedAt, err)
		return
	}
	if shouldStreamResponse(req.Stream, r.Header.Get("Accept")) {
		estimatedTokens := estimateInputTokens(req)
		req.EstimatedInputTokens = &estimatedTokens
		s.handleChatCompletionsStream(w, r, req, bodyBytes, startedAt)
		return
	}
	estimatedTokens := estimateInputTokens(req)
	req.EstimatedInputTokens = &estimatedTokens
	requestedModel := req.Model
	response, result, err := s.router.Chat(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		s.logRequest(r, requestedModel, "", extractRequestText(req), result, app.ChatResponse{}, http.StatusBadRequest, len(bodyBytes), 0, startedAt, err)
		return
	}
	written := 0
	if len(response.RawJSON) > 0 {
		written = writeJSONBytes(w, http.StatusOK, response.RawJSON)
	} else {
		content := response.Content
		payload := map[string]any{
			"id":         fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			"object":     "chat.completion",
			"created":    time.Now().Unix(),
			"model":      response.Model,
			"tier":       result.Tier,
			"confidence": result.Confidence,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": content},
				"finish_reason": "stop",
			}},
		}
		written = writeJSON(w, http.StatusOK, payload)
	}
	s.logRequest(r, requestedModel, response.Model, extractRequestText(req), result, response, http.StatusOK, len(bodyBytes), written, startedAt, nil)
}

func (s *Server) handleChatCompletionsStream(w http.ResponseWriter, r *http.Request, req app.ChatRequest, bodyBytes []byte, startedAt time.Time) {
	if handled := s.tryProxyProviderStream(w, r, req, bodyBytes, startedAt); handled {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		s.logRequest(r, req.Model, "", extractRequestText(req), domain.ScoringResult{}, app.ChatResponse{}, http.StatusInternalServerError, len(bodyBytes), 0, startedAt, errors.New("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	type chatResult struct {
		response app.ChatResponse
		result   domain.ScoringResult
		err      error
	}

	resultCh := make(chan chatResult, 1)
	requestedModel := req.Model
	go func() {
		response, result, err := s.router.Chat(r.Context(), req)
		resultCh <- chatResult{response: response, result: result, err: err}
	}()

	written := 0
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			s.logRequest(r, requestedModel, "", extractRequestText(req), domain.ScoringResult{}, app.ChatResponse{}, statusClientClosedRequest, len(bodyBytes), written, startedAt, r.Context().Err())
			return
		case <-ticker.C:
			n, _ := fmt.Fprint(w, ": ping\n\n")
			written += n
			flusher.Flush()
		case outcome := <-resultCh:
			if outcome.err != nil {
				errPayload := map[string]any{"error": map[string]any{"message": outcome.err.Error(), "type": "invalid_request_error"}}
				encodedErr, _ := json.Marshal(errPayload)
				n1, _ := fmt.Fprintf(w, "data: %s\n\n", encodedErr)
				n2, _ := fmt.Fprint(w, "data: [DONE]\n\n")
				written += n1 + n2
				flusher.Flush()
				s.logRequest(r, requestedModel, "", extractRequestText(req), outcome.result, app.ChatResponse{}, http.StatusBadRequest, len(bodyBytes), written, startedAt, outcome.err)
				return
			}

			responseModel := outcome.response.Model
			if strings.TrimSpace(responseModel) == "" {
				responseModel = requestedModel
			}
			created := time.Now().Unix()
			id := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())

			roleChunk := map[string]any{
				"id":      id,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   responseModel,
				"choices": []map[string]any{{
					"index":         0,
					"delta":         map[string]any{"role": "assistant"},
					"finish_reason": nil,
				}},
			}
			contentChunk := map[string]any{
				"id":      id,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   responseModel,
				"choices": []map[string]any{{
					"index":         0,
					"delta":         map[string]any{"content": outcome.response.Content},
					"finish_reason": nil,
				}},
			}
			stopChunk := map[string]any{
				"id":      id,
				"object":  "chat.completion.chunk",
				"created": created,
				"model":   responseModel,
				"choices": []map[string]any{{
					"index":         0,
					"delta":         map[string]any{},
					"finish_reason": "stop",
				}},
			}

			encodedRole, _ := json.Marshal(roleChunk)
			encodedContent, _ := json.Marshal(contentChunk)
			encodedStop, _ := json.Marshal(stopChunk)
			n1, _ := fmt.Fprintf(w, "data: %s\n\n", encodedRole)
			n2, _ := fmt.Fprintf(w, "data: %s\n\n", encodedContent)
			n3, _ := fmt.Fprintf(w, "data: %s\n\n", encodedStop)
			n4, _ := fmt.Fprint(w, "data: [DONE]\n\n")
			written += n1 + n2 + n3 + n4
			flusher.Flush()

			s.logRequest(r, requestedModel, outcome.response.Model, extractRequestText(req), outcome.result, outcome.response, http.StatusOK, len(bodyBytes), written, startedAt, nil)
			return
		}
	}
}

func (s *Server) tryProxyProviderStream(w http.ResponseWriter, r *http.Request, req app.ChatRequest, bodyBytes []byte, startedAt time.Time) bool {
	if s.router == nil || s.router.Providers == nil {
		return false
	}

	requestedModel := req.Model

	estimatedTokens := estimateInputTokens(req)
	req.EstimatedInputTokens = &estimatedTokens
	resolvedModel, result, err := s.router.ResolveModel(req.Model, req.Prompt, estimatedTokens)
	if err != nil {
		return false
	}
	provider, err := s.router.Providers.Resolve(resolvedModel)
	if err != nil {
		return false
	}
	streamingProvider, ok := provider.(app.StreamingProvider)
	if !ok {
		return false
	}

	req.Model = resolvedModel
	streamResponse, err := streamingProvider.ChatCompletionsStream(r.Context(), req)
	if err != nil {
		return false
	}
	defer streamResponse.Body.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return false
	}

	contentType := streamResponse.ContentType
	if strings.TrimSpace(contentType) == "" {
		contentType = "text/event-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	resolvedForLog := streamResponse.Model
	if strings.TrimSpace(resolvedForLog) == "" {
		resolvedForLog = resolvedModel
	}

	written := 0
	reader := bufio.NewReader(streamResponse.Body)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			n, writeErr := w.Write(line)
			written += n
			if writeErr != nil {
				s.logRequest(r, requestedModel, resolvedForLog, extractRequestText(req), result, app.ChatResponse{Model: resolvedForLog}, http.StatusBadGateway, len(bodyBytes), written, startedAt, writeErr)
				return true
			}
			flusher.Flush()
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			s.logRequest(r, requestedModel, resolvedForLog, extractRequestText(req), result, app.ChatResponse{Model: resolvedForLog}, http.StatusOK, len(bodyBytes), written, startedAt, nil)
			return true
		}
		s.logRequest(r, requestedModel, resolvedForLog, extractRequestText(req), result, app.ChatResponse{Model: resolvedForLog}, http.StatusBadGateway, len(bodyBytes), written, startedAt, readErr)
		return true
	}
}

func shouldStreamResponse(streamRequested bool, acceptHeader string) bool {
	if streamRequested {
		return true
	}
	for _, part := range strings.Split(acceptHeader, ",") {
		mediaType := strings.TrimSpace(part)
		if mediaType == "" {
			continue
		}
		if semi := strings.Index(mediaType, ";"); semi >= 0 {
			mediaType = strings.TrimSpace(mediaType[:semi])
		}
		if strings.EqualFold(mediaType, "text/event-stream") {
			return true
		}
	}
	return false
}

func writeStreamingResponse(w http.ResponseWriter, model, content string) int {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	payload := map[string]any{"id": fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()), "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": content}, "finish_reason": nil}}}
	encoded, _ := json.Marshal(payload)
	n1, _ := fmt.Fprintf(w, "data: %s\n\n", encoded)
	n2, _ := fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	return n1 + n2
}

func writeJSON(w http.ResponseWriter, status int, payload any) int {
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = []byte(`{"error":{"message":"failed to encode response","type":"invalid_request_error"}}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	n, _ := w.Write(append(encoded, '\n'))
	return n
}

func writeJSONBytes(w http.ResponseWriter, status int, payload []byte) int {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoded := payload
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		encoded = append(encoded, '\n')
	}
	n, _ := w.Write(encoded)
	return n
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": err.Error(), "type": "invalid_request_error"}})
}

func validateIncomingAPIKey(r *http.Request, configuredKey string) bool {
	if strings.TrimSpace(configuredKey) == "" {
		return true
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authorization, prefix))
	return provided != "" && provided == configuredKey
}

func (s *Server) handleUIRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTemplate.Execute(w, nil)
}

func (s *Server) handleUIStats(w http.ResponseWriter, _ *http.Request) {
	if s.requestLogs == nil {
		_, _ = w.Write([]byte("<p>No request log backend configured.</p>"))
		return
	}
	stats := s.requestLogs.Stats()
	successRate := 0.0
	if stats.TotalRequests > 0 {
		successRate = (float64(stats.SuccessRequests) / float64(stats.TotalRequests)) * 100
	}
	_, _ = fmt.Fprintf(w,
		"<div class=\"stats-grid\">"+
			"<div class=\"stat\"><span>Total Requests</span><b>%d</b></div>"+
			"<div class=\"stat\"><span>Success Rate</span><b>%.1f%%</b></div>"+
			"<div class=\"stat\"><span>Avg Latency</span><b>%.0f ms</b></div>"+
			"<div class=\"stat\"><span>Total Tokens</span><b>%d</b></div>"+
			"<div class=\"stat\"><span>Request Bytes</span><b>%d</b></div>"+
			"<div class=\"stat\"><span>Response Bytes</span><b>%d</b></div>"+
			"</div>",
		stats.TotalRequests,
		successRate,
		stats.AverageLatencyMS,
		stats.TotalTokens,
		stats.TotalRequestBytes,
		stats.TotalResponseBytes,
	)
	if len(stats.ByModel) > 0 {
		_, _ = w.Write([]byte("<h3>By Model</h3><table><thead><tr><th>Model</th><th>Count</th></tr></thead><tbody>"))
		keys := sortedModelKeys(stats.ByModel)
		for _, key := range keys {
			_, _ = fmt.Fprintf(w, "<tr><td>%s</td><td>%d</td></tr>", template.HTMLEscapeString(key), stats.ByModel[key])
		}
		_, _ = w.Write([]byte("</tbody></table>"))
	}
}

func (s *Server) handleUIRequests(w http.ResponseWriter, _ *http.Request) {
	if s.requestLogs == nil {
		_, _ = w.Write([]byte("<p>No request log backend configured.</p>"))
		return
	}
	entries := s.requestLogs.Recent(50)
	if len(entries) == 0 {
		_, _ = w.Write([]byte("<p>No requests captured yet.</p>"))
		return
	}
	_, _ = w.Write([]byte("<table><thead><tr><th>Time</th><th>Model</th><th>Tier</th><th>Status</th><th>Tokens</th><th>Bytes In/Out</th><th>Latency</th></tr></thead><tbody>"))
	for _, entry := range entries {
		statusClass := "status-ok"
		if entry.Status == domain.RequestStatusError {
			statusClass = "status-err"
		}
		_, _ = fmt.Fprintf(w,
			"<tr><td>%s</td><td>%s</td><td>%s</td><td class=\"%s\">%s</td><td>%d (%s)</td><td>%d / %d</td><td>%d ms</td></tr>",
			entry.CreatedAt.Format(time.RFC3339),
			template.HTMLEscapeString(entry.ResolvedModel),
			template.HTMLEscapeString(string(entry.Tier)),
			statusClass,
			template.HTMLEscapeString(string(entry.Status)),
			entry.TotalTokens,
			template.HTMLEscapeString(string(entry.TokenSource)),
			entry.RequestBytes,
			entry.ResponseBytes,
			entry.Duration.Milliseconds(),
		)
	}
	_, _ = w.Write([]byte("</tbody></table>"))
}

func (s *Server) handleUIActiveRequests(w http.ResponseWriter, _ *http.Request) {
	if s.router == nil {
		_, _ = w.Write([]byte("<p>Router unavailable.</p>"))
		return
	}
	if s.router.ActiveRequests == nil {
		_, _ = w.Write([]byte("<p>Active request counter unavailable.</p>"))
		return
	}

	providers := make(map[string]struct{})
	for _, provider := range s.router.Config.Providers {
		providerID := strings.TrimSpace(provider.Kind) + ":" + strings.TrimSpace(provider.Name)
		providerID = strings.Trim(providerID, ":")
		if providerID == "" {
			continue
		}
		providers[providerID] = struct{}{}
	}
	for providerID := range s.router.ModelRegistry() {
		providerID = strings.TrimSpace(providerID)
		if providerID == "" || providerID == "unknown" || providerID == "router" {
			continue
		}
		providers[providerID] = struct{}{}
	}

	providerIDs := make([]string, 0, len(providers))
	for providerID := range providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	total := 0
	_, _ = w.Write([]byte("<div class=\"stats-grid\">"))
	for _, providerID := range providerIDs {
		count := s.router.ActiveRequests.Count(providerID)
		total += count
		_, _ = fmt.Fprintf(w,
			"<div class=\"stat\"><span>%s</span><b>%d</b></div>",
			template.HTMLEscapeString(providerID),
			count,
		)
	}
	_, _ = fmt.Fprintf(w, "<div class=\"stat\"><span>Total Active</span><b>%d</b></div>", total)
	_, _ = w.Write([]byte("</div>"))
}

func (s *Server) handleUIModels(w http.ResponseWriter, _ *http.Request) {
	if s.router == nil {
		_, _ = w.Write([]byte("<p>Router unavailable.</p>"))
		return
	}
	registry := s.router.ModelRegistry()
	if len(registry) == 0 {
		_, _ = w.Write([]byte("<p>No models available in registry.</p>"))
		return
	}
	providerIDs := make([]string, 0, len(registry))
	for providerID := range registry {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	for _, providerID := range providerIDs {
		models := registry[providerID]
		_, _ = fmt.Fprintf(w, "<h3>%s</h3>", template.HTMLEscapeString(providerID))
		_, _ = w.Write([]byte("<table><thead><tr><th>Model ID</th><th>Object</th><th>Owned By</th><th>Provider</th><th>Alias</th><th>Context Limit</th><th>Token Input Cost / 1M</th><th>Token Output Cost / 1M</th></tr></thead><tbody>"))
		for _, model := range models {
			_, _ = fmt.Fprintf(w,
				"<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%t</td><td>%s</td><td>%s</td><td>%s</td></tr>",
				template.HTMLEscapeString(model.ID),
				template.HTMLEscapeString(model.Object),
				template.HTMLEscapeString(model.OwnedBy),
				template.HTMLEscapeString(model.Provider),
				model.Alias,
				template.HTMLEscapeString(optionalIntString(model.ContextLimit)),
				template.HTMLEscapeString(optionalFloatString(model.TokenInputCost)),
				template.HTMLEscapeString(optionalFloatString(model.TokenOutputCost)),
			)
		}
		_, _ = w.Write([]byte("</tbody></table>"))
	}
}

func (s *Server) handleUIStream(w http.ResponseWriter, r *http.Request) {
	if s.requestLogs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("request log backend unavailable"))
		return
	}
	notifications, unsubscribe := s.requestLogs.Subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	_, _ = fmt.Fprint(w, "event: ping\ndata: {}\n\n")
	flusher.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-notifications:
			_, _ = fmt.Fprint(w, "event: update\ndata: {}\n\n")
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprint(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) logRequest(r *http.Request, requestedModel, resolvedModel, requestText string, result domain.ScoringResult, response app.ChatResponse, httpStatus int, requestBytes, responseBytes int, startedAt time.Time, chatErr error) {
	if s.requestLogs == nil {
		return
	}
	providerID := providerIDFromModel(resolvedModel)
	promptTokens, completionTokens, totalTokens, tokenSource := tokensForExchange(requestedModel, resolvedModel, requestText, response)
	status := domain.RequestStatusSuccess
	errText := ""
	if chatErr != nil {
		status = domain.RequestStatusError
		errText = chatErr.Error()
	}
	entry := domain.RequestLogEntry{
		ID:               fmt.Sprintf("req-%d", time.Now().UnixNano()),
		CreatedAt:        time.Now(),
		Method:           r.Method,
		Path:             r.URL.Path,
		RequestedModel:   requestedModel,
		ResolvedModel:    resolvedModel,
		ProviderID:       providerID,
		Tier:             result.Tier,
		Confidence:       result.Confidence,
		Status:           status,
		HTTPStatus:       httpStatus,
		Error:            errText,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		TokenSource:      tokenSource,
		RequestBytes:     requestBytes,
		ResponseBytes:    responseBytes,
		Duration:         time.Since(startedAt),
	}
	_ = s.requestLogs.Append(r.Context(), entry)
}

func tokensForExchange(requestedModel, resolvedModel, requestText string, response app.ChatResponse) (int, int, int, domain.TokenSource) {
	if response.Usage != nil {
		prompt := response.Usage.PromptTokens
		completion := response.Usage.CompletionTokens
		total := response.Usage.TotalTokens
		if total == 0 {
			total = prompt + completion
		}
		if prompt > 0 || completion > 0 || total > 0 {
			return prompt, completion, total, domain.TokenSourceProvider
		}
	}
	modelForTokenizer := resolvedModel
	if modelForTokenizer == "" {
		modelForTokenizer = requestedModel
	}
	prompt, promptOK := countTokensWithTokenizer(modelForTokenizer, requestText)
	completion, completionOK := countTokensWithTokenizer(modelForTokenizer, response.Content)
	if promptOK || completionOK {
		total := prompt + completion
		return prompt, completion, total, domain.TokenSourceTokenizer
	}
	prompt = heuristicTokenCount(requestText)
	completion = heuristicTokenCount(response.Content)
	return prompt, completion, prompt + completion, domain.TokenSourceHeuristic
}

func extractRequestText(req app.ChatRequest) string {
	if strings.TrimSpace(req.Prompt) != "" {
		return req.Prompt
	}
	if len(req.Messages) == 0 {
		return ""
	}
	return req.Messages[len(req.Messages)-1].Content.TextValue()
}

func requestInputText(req app.ChatRequest) string {
	if len(req.Messages) == 0 {
		return extractRequestText(req)
	}
	parts := make([]string, 0, len(req.Messages)+1)
	for _, message := range req.Messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "user"
		}
		parts = append(parts, role+": "+message.Content.TextValue())
	}
	if strings.TrimSpace(req.Prompt) != "" {
		parts = append(parts, "prompt: "+req.Prompt)
	}
	return strings.Join(parts, "\n")
}

func estimateInputTokens(req app.ChatRequest) int {
	text := requestInputText(req)
	count, ok := countTokensWithTokenizer(req.Model, text)
	if ok {
		return count
	}
	return heuristicTokenCount(text)
}

func heuristicTokenCount(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	return len(trimmed)/4 + 1
}

func countTokensWithTokenizer(modelID, text string) (int, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, true
	}
	codec := codecForModel(modelID)
	if codec == nil {
		return 0, false
	}
	count, err := codec.Count(trimmed)
	if err != nil {
		return 0, false
	}
	return count, true
}

func codecForModel(modelID string) tokenizer.Codec {
	model := splitModelLeaf(modelID)
	tokenizerCache.RLock()
	if codec, ok := tokenizerCache.codecs[model]; ok {
		tokenizerCache.RUnlock()
		return codec
	}
	tokenizerCache.RUnlock()
	codec, err := tokenizer.ForModel(tokenizer.Model(model))
	if err != nil {
		codec, err = tokenizer.Get(tokenizer.Cl100kBase)
		if err != nil {
			return nil
		}
	}
	tokenizerCache.Lock()
	tokenizerCache.codecs[model] = codec
	tokenizerCache.Unlock()
	return codec
}

func splitModelLeaf(modelID string) string {
	idx := strings.LastIndex(modelID, ":")
	if idx < 0 || idx == len(modelID)-1 {
		return modelID
	}
	return modelID[idx+1:]
}

func providerIDFromModel(modelID string) string {
	idx := strings.LastIndex(modelID, ":")
	if idx <= 0 {
		return ""
	}
	return modelID[:idx]
}

func sortedModelKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func optionalIntString(value *int) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *value)
}

func optionalFloatString(value *float64) string {
	if value == nil {
		return "0 (free/default)"
	}
	return fmt.Sprintf("%.6f", *value)
}
