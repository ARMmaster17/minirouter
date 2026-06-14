package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesEnvironmentOverrides(t *testing.T) {
	t.Setenv("MINIROUTER_SERVER_ADDR", ":9090")
	t.Setenv("MINIROUTER_FRONTEND_ENABLED", "false")
	t.Setenv("MINIROUTER_INCOMING_API_KEY", "incoming-from-env")
	t.Setenv("MINIROUTER_ROUTING_CONFIDENCE_THRESHOLD", "0.77")
	t.Setenv("MINIROUTER_ROUTING_DEBUG", "true")
	t.Setenv("MINIROUTER_PAYLOAD_DEBUG", "true")
	t.Setenv("MINIROUTER_GEMINI_API_KEY", "env-key")
	t.Setenv("MINIROUTER_GEMINI_URL", "https://example.invalid")

	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	content := []byte(`{
	"server": { "addr": ":8081", "frontendEnabled": true, "incomingAPIKey": "incoming-from-file" },
	"providers": [
		{
			"kind": "gemini",
			"name": "gemini",
			"apiKey": "file-key",
			"url": "https://file.invalid",
			"thrashLimit": 2,
			"parallelLimit": 4,
			"models": [{ "id": "gemini-2.5-flash", "contextLimit": 1048576 }],
			"enabled": true
		}
	]
}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Fatalf("expected server override, got %s", cfg.Server.Addr)
	}
	if cfg.Server.FrontendEnabled {
		t.Fatalf("expected frontend enabled override to false")
	}
	if cfg.Server.IncomingAPIKey != "incoming-from-env" {
		t.Fatalf("expected incoming api key override, got %s", cfg.Server.IncomingAPIKey)
	}
	if cfg.Routing.Scoring.ConfidenceThreshold != 0.77 {
		t.Fatalf("expected confidence override, got %v", cfg.Routing.Scoring.ConfidenceThreshold)
	}
	if !cfg.Routing.Debug {
		t.Fatalf("expected routing debug override to be true")
	}
	if !cfg.Routing.PayloadDebug {
		t.Fatalf("expected payload debug override to be true")
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].APIKey != "env-key" {
		t.Fatalf("expected provider api key override, got %s", cfg.Providers[0].APIKey)
	}
	if cfg.Providers[0].URL != "https://example.invalid" {
		t.Fatalf("expected provider url override, got %s", cfg.Providers[0].URL)
	}
	if cfg.Providers[0].Models[0].ContextLimit == nil || *cfg.Providers[0].Models[0].ContextLimit != 1048576 {
		t.Fatalf("expected model context limit to remain configured")
	}
	if cfg.Providers[0].ThrashLimit == nil || *cfg.Providers[0].ThrashLimit != 2 {
		t.Fatalf("expected provider thrash limit to load from config")
	}
	if cfg.Providers[0].ParallelLimit == nil || *cfg.Providers[0].ParallelLimit != 4 {
		t.Fatalf("expected provider parallel limit to load from config")
	}
}

func TestDefaultFrontendEnabled(t *testing.T) {
	cfg := Default()
	if !cfg.Server.FrontendEnabled {
		t.Fatalf("expected frontend to be enabled by default")
	}
}

func TestLoadParsesOptionalModelTokenCosts(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.json")
	content := []byte(`{
	"providers": [
		{
			"kind": "openai",
			"name": "openai",
			"apiKey": "x",
			"url": "https://example.invalid/v1",
			"models": [
				"gpt-free",
				{ "id": "gpt-paid", "tokenInputCost": 0.15, "tokenOutputCost": 0.45 }
			],
			"enabled": true
		}
	]
}`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 1 || len(cfg.Providers[0].Models) != 2 {
		t.Fatalf("expected two models to parse, got %+v", cfg.Providers)
	}
	free := cfg.Providers[0].Models[0]
	if free.TokenInputCost != nil || free.TokenOutputCost != nil {
		t.Fatalf("expected free model costs to remain nil")
	}
	paid := cfg.Providers[0].Models[1]
	if paid.TokenInputCost == nil || *paid.TokenInputCost != 0.15 {
		t.Fatalf("expected tokenInputCost to parse, got %+v", paid.TokenInputCost)
	}
	if paid.TokenOutputCost == nil || *paid.TokenOutputCost != 0.45 {
		t.Fatalf("expected tokenOutputCost to parse, got %+v", paid.TokenOutputCost)
	}
}
