package app

import (
	"context"
	"testing"
)

func TestMockProviderStaticModelsAndResponses(t *testing.T) {
	provider := NewMockProvider("mock:mock", []string{"mock-chat"}, map[string]string{"mock:mock:mock-chat": "hello"})

	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if !provider.CanHandle("mock:mock:mock-chat") {
		t.Fatalf("expected provider to handle prefixed model id")
	}

	response, err := provider.ChatCompletions(context.Background(), ChatRequest{Model: "mock:mock:mock-chat", Prompt: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "hello" {
		t.Fatalf("expected configured response, got %q", response.Content)
	}
}

func TestProviderRegistryResolve(t *testing.T) {
	provider := NewMockProvider("mock:mock", []string{"mock-chat"}, nil)
	registry := NewProviderRegistry(provider)

	resolved, err := registry.Resolve("mock:mock:mock-chat")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID() != "mock:mock" {
		t.Fatalf("expected provider to resolve, got %s", resolved.ID())
	}
}
