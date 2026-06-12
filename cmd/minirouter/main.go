package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	httpadapter "github.com/ARMmaster17/minirouter/internal/adapters/http"
	logadapters "github.com/ARMmaster17/minirouter/internal/adapters/logs"
	provideradapters "github.com/ARMmaster17/minirouter/internal/adapters/providers"
	requestadapters "github.com/ARMmaster17/minirouter/internal/adapters/requests"
	"github.com/ARMmaster17/minirouter/internal/app"
	"github.com/ARMmaster17/minirouter/internal/config"
	"github.com/ARMmaster17/minirouter/internal/domain"
)

func main() {
	configPath := os.Getenv("MINIROUTER_CONFIG")
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	providers, err := provideradapters.Build(cfg)
	if err != nil {
		log.Fatalf("build providers: %v", err)
	}
	for _, provider := range providers {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, err := provider.Models(ctx)
		cancel()
		if err != nil {
			var missing *provideradapters.ConfiguredModelsMissingError
			if errors.As(err, &missing) {
				log.Fatalf("provider metadata preload failed: %v", err)
			}
			log.Printf("warn: provider metadata preload failed for %s: %v", provider.ID(), err)
		}
	}
	if len(providers) == 0 {
		mock := app.NewMockProvider("mock:mock", []string{"mock-chat", "mock-reasoning"}, map[string]string{"mock:mock:mock-chat": "hello from mock provider"})
		cfg.Routing.Tiers[domain.TierSimple] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
		cfg.Routing.Tiers[domain.TierMedium] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
		cfg.Routing.Tiers[domain.TierComplex] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
		cfg.Routing.Tiers[domain.TierReasoning] = domain.TierConfig{Models: []string{"mock:mock:mock-chat"}}
		providers = append(providers, mock)
	}
	requestLogs := logadapters.NewInMemoryRequestLogStore(1000)
	activeRequests := requestadapters.NewInMemoryActiveRequestCounter()
	router := app.NewRouter(cfg, app.NewStaticCatalog(cfg), providers...).WithRequestLogStore(requestLogs).WithActiveRequestCounter(activeRequests)
	server := httpadapter.New(router, requestLogs)
	log.Printf("minirouter listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, server.Handler()); err != nil {
		log.Fatal(err)
	}
}
