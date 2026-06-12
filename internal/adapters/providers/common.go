package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ARMmaster17/minirouter/internal/app"
)

const defaultHTTPTimeout = 30 * time.Minute

func defaultClient() *http.Client {
	return &http.Client{Timeout: defaultHTTPTimeout}
}

func modelList(configured []app.Model) []app.Model {
	models := make([]app.Model, len(configured))
	copy(models, configured)
	return models
}

func configuredModelIDs(configured []app.Model) []string {
	ids := make([]string, 0, len(configured))
	for _, model := range configured {
		ids = append(ids, model.ID)
	}
	return ids
}

type ConfiguredModelsMissingError struct {
	ProviderID string
	Missing    []string
}

func (e *ConfiguredModelsMissingError) Error() string {
	missing := make([]string, len(e.Missing))
	copy(missing, e.Missing)
	sort.Strings(missing)
	return fmt.Sprintf("provider %s missing configured models in upstream response: %s", e.ProviderID, strings.Join(missing, ", "))
}

func applyConfiguredMetadata(models []app.Model, configured []app.Model) []app.Model {
	if len(models) == 0 || len(configured) == 0 {
		return models
	}
	configuredByID := make(map[string]app.Model, len(configured))
	for _, model := range configured {
		configuredByID[model.ID] = model
	}
	for index := range models {
		if override, ok := configuredByID[models[index].ID]; ok {
			if override.ContextLimit != nil {
				models[index].ContextLimit = override.ContextLimit
			}
			if override.TokenInputCost != nil {
				models[index].TokenInputCost = override.TokenInputCost
			}
			if override.TokenOutputCost != nil {
				models[index].TokenOutputCost = override.TokenOutputCost
			}
		}
	}
	return models
}

func selectConfiguredModels(providerID string, models []app.Model, configured []app.Model) ([]app.Model, error) {
	if len(configured) == 0 {
		return models, nil
	}
	byID := make(map[string]app.Model, len(models))
	for _, model := range models {
		byID[model.ID] = model
	}
	selected := make([]app.Model, 0, len(configured))
	missing := make([]string, 0)
	for _, cfgModel := range configured {
		upstream, ok := byID[cfgModel.ID]
		if !ok {
			missing = append(missing, cfgModel.ID)
			continue
		}
		selected = append(selected, upstream)
	}
	if len(missing) > 0 {
		return nil, &ConfiguredModelsMissingError{ProviderID: providerID, Missing: missing}
	}
	return selected, nil
}

func readIntField(fields map[string]any, keys ...string) *int {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		number, ok := readFloatValue(value)
		if !ok {
			continue
		}
		intValue := int(math.Round(number))
		return &intValue
	}
	return nil
}

func readCostField(fields map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		number, ok := readFloatValue(value)
		if !ok {
			continue
		}
		cost := number
		return &cost
	}
	return nil
}

func readPricingMapCost(fields map[string]any, pricingKeys []string, modelKeys []string) *float64 {
	for _, pricingKey := range pricingKeys {
		pricingRaw, ok := fields[pricingKey]
		if !ok {
			continue
		}
		pricingMap, ok := pricingRaw.(map[string]any)
		if !ok {
			continue
		}
		if value := readCostField(pricingMap, modelKeys...); value != nil {
			return value
		}
	}
	return nil
}

func readFloatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func extractPrompt(req app.ChatRequest) string {
	if strings.TrimSpace(req.Prompt) != "" {
		return req.Prompt
	}
	if len(req.Messages) == 0 {
		return ""
	}
	return req.Messages[len(req.Messages)-1].Content.TextValue()
}

func toOpenAIMessages(req app.ChatRequest) []map[string]any {
	messages := make([]map[string]any, 0, len(req.Messages))
	for _, msg := range req.Messages {
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "user"
		}
		entry := map[string]any{"role": role, "content": msg.Content}
		if strings.TrimSpace(msg.Name) != "" {
			entry["name"] = msg.Name
		}
		if strings.TrimSpace(msg.ToolCallID) != "" {
			entry["tool_call_id"] = msg.ToolCallID
		}
		messages = append(messages, entry)
	}
	if len(messages) == 0 {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": extractPrompt(req),
		})
	}
	return messages
}

func splitModelID(providerID, fullModelID string) string {
	prefix := providerID + ":"
	if strings.HasPrefix(fullModelID, prefix) {
		return strings.TrimPrefix(fullModelID, prefix)
	}
	return fullModelID
}

func joinURL(baseURL, path string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	trimmedPath := strings.TrimLeft(strings.TrimSpace(path), "/")
	if trimmedBase == "" {
		return "/" + trimmedPath
	}
	if trimmedPath == "" {
		return trimmedBase
	}
	return trimmedBase + "/" + trimmedPath
}

func addQueryParam(rawURL, key, value string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func doJSON(ctx context.Context, client *http.Client, method, url, apiKey string, body any, headers map[string]string, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("upstream status %d: %s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func doJSONRaw(ctx context.Context, client *http.Client, method, url, apiKey string, body any, headers map[string]string) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream status %d: %s", res.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return bodyBytes, nil
}
