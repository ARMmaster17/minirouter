package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

const (
	miniRouterURL    = "http://localhost:8080/v1"
	miniRouterModel  = "gemini:gemini:models/gemini-2.5-flash"
	miniRouterAPIKey = "password123"
	maxTurns         = 6
)

type weatherArgs struct {
	City string `json:"city"`
	Unit string `json:"unit"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := openai.NewClient(
		option.WithBaseURL(miniRouterURL),
		option.WithAPIKey(miniRouterAPIKey),
	)

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("You are testing Minirouter. Always place your private reasoning inside <thinking>...</thinking> tags, then provide a concise final answer after that. If tools are available and relevant, call them."),
		openai.UserMessage("Please check weather in London and summarize it. Use the tool before answering."),
	}

	tools := []openai.ChatCompletionToolParam{{
		Function: shared.FunctionDefinitionParam{
			Name:        "lookup_weather",
			Description: openai.String("Returns fake weather data for test verification."),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{
						"type":        "string",
						"description": "City name to look up.",
					},
					"unit": map[string]any{
						"type":        "string",
						"description": "celsius or fahrenheit",
					},
				},
				"required":             []string{"city"},
				"additionalProperties": false,
			},
			Strict: openai.Bool(true),
		},
	}}

	for turn := 1; turn <= maxTurns; turn++ {
		response, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    shared.ChatModel(miniRouterModel),
			Messages: messages,
			Tools:    tools,
			ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String(string(openai.ChatCompletionToolChoiceOptionAutoAuto)),
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "chat completion error (turn %d): %v\n", turn, err)
			os.Exit(1)
		}
		if len(response.Choices) == 0 {
			fmt.Fprintf(os.Stderr, "chat completion returned no choices (turn %d)\n", turn)
			os.Exit(1)
		}

		choice := response.Choices[0]
		content := strings.TrimSpace(choice.Message.Content)
		if content != "" {
			fmt.Printf("\nassistant turn %d:\n%s\n", turn, content)
			hasThinking := strings.Contains(content, "<thinking>") && strings.Contains(content, "</thinking>")
			fmt.Printf("thinking tags present: %t\n", hasThinking)
		}
		if strings.TrimSpace(choice.Message.Refusal) != "" {
			fmt.Printf("\nassistant refusal turn %d:\n%s\n", turn, strings.TrimSpace(choice.Message.Refusal))
		}
		if content == "" && strings.TrimSpace(choice.Message.Refusal) == "" {
			encodedMessage, _ := json.MarshalIndent(choice.Message, "", "  ")
			fmt.Printf("\nassistant turn %d returned empty content. raw message:\n%s\n", turn, string(encodedMessage))
		}

		if len(choice.Message.ToolCalls) == 0 {
			if content == "" {
				messages = append(messages, choice.Message.ToParam())
				messages = append(messages, openai.UserMessage("Please provide your final response now. Include <thinking>...</thinking> tags followed by a concise answer."))
				fmt.Println("\nmodel returned no tool calls and empty content; sent a follow-up prompt")
				continue
			}
			fmt.Println("\nno more tool calls; done")
			return
		}

		messages = append(messages, choice.Message.ToParam())
		for _, call := range choice.Message.ToolCalls {
			result, toolErr := runTool(call.Function.Name, call.Function.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf(`{"error":%q}`, toolErr.Error())
			}
			fmt.Printf("\ntool call: %s\narguments: %s\nresult: %s\n", call.Function.Name, call.Function.Arguments, result)
			messages = append(messages, openai.ToolMessage(result, call.ID))
		}
	}

	fmt.Printf("\nreached max turns (%d)\n", maxTurns)
}

func runTool(name, rawArgs string) (string, error) {
	switch name {
	case "lookup_weather":
		var args weatherArgs
		if strings.TrimSpace(rawArgs) != "" {
			if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
				return "", fmt.Errorf("invalid lookup_weather args: %w", err)
			}
		}
		if strings.TrimSpace(args.City) == "" {
			args.City = "unknown"
		}
		if strings.TrimSpace(args.Unit) == "" {
			args.Unit = "celsius"
		}
		payload := map[string]any{
			"city":          args.City,
			"unit":          args.Unit,
			"temperature":   18,
			"condition":     "partly cloudy",
			"source":        "minirouter-toolcheck",
			"observed_at":   time.Now().UTC().Format(time.RFC3339),
			"thinking_hint": "tool response returned for <thinking> tag validation",
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal lookup_weather result: %w", err)
		}
		return string(encoded), nil
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}
