package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	pgrunner "github.com/c12s/gprunner"
	"github.com/c12s/gprunner/pkg/model"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	f, err := os.Open("test_data/starchart.json")
	if err != nil {
		log.Error("open chart", "err", err)
		os.Exit(1)
	}
	defer f.Close()

	var chart model.Chart
	if err := json.NewDecoder(f).Decode(&chart); err != nil {
		log.Error("decode chart", "err", err)
		os.Exit(1)
	}

	cache := os.Getenv("PGRUNNER_CACHE")
	if cache == "" {
		cache = "/tmp/pgrunner-cache"
	}
	if err := os.MkdirAll(cache, 0o755); err != nil { // CacheDir must exist
		log.Error("cache dir", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	r, err := pgrunner.New(ctx, pgrunner.Config{CacheDir: cache, Logger: log})
	if err != nil {
		log.Error("new runner", "err", err)
		os.Exit(1)
	}

	if err := r.InstantiateChart(ctx, chart); err != nil {
		log.Error("instantiate", "err", err)
		os.Exit(1)
	}

}
