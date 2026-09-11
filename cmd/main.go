package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	pgrunner "github.com/c12s/gprunner"
	"github.com/c12s/gprunner/pkg/model"
)

// A wedged connection teardown must not hold the process open forever.
const shutdownTimeout = 30 * time.Second

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if err := run(log); err != nil {
		log.Error("runner", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) (err error) {
	// signals are wired up before any long-running work: resolving layers can
	// take minutes, and until this ctx exists a Ctrl-C there kills the process
	// mid-build instead of unwinding it
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	f, err := os.Open("test_data/starchart.json")
	if err != nil {
		return fmt.Errorf("open chart: %w", err)
	}
	defer f.Close()

	var chart model.Chart
	if err := json.NewDecoder(f).Decode(&chart); err != nil {
		return fmt.Errorf("decode chart: %w", err)
	}

	cache := os.Getenv("PGRUNNER_CACHE")
	if cache == "" {
		cache = "/tmp/pgrunner-cache"
	}
	if err := os.MkdirAll(cache, 0o755); err != nil { // CacheDir must exist
		return fmt.Errorf("cache dir: %w", err)
	}

	brokerAddr := os.Getenv("PGRUNNER_BROKER")
	if brokerAddr == "" {
		brokerAddr = "nats:4222"
	}

	// left empty, the library falls back to pgrunner.DefaultBrokerSubnet
	brokerSubnet := os.Getenv("PGRUNNER_BROKER_SUBNET")

	r, err := pgrunner.New(ctx, pgrunner.Config{
		CacheDir:     cache,
		Logger:       log,
		BrokerSubnet: brokerSubnet,
		BrokerAddr:   brokerAddr,
	})
	if err != nil {
		return fmt.Errorf("new runner: %w", err)
	}

	// every path out of here past this point closes the runner, so a failed
	// instantiation still checkpoints the write-ahead log
	defer func() {
		err = errors.Join(err, closeWithin(r, shutdownTimeout))
	}()

	if err := r.InstantiateChart(ctx, chart); err != nil {

		if ctx.Err() != nil {
			stop()
			log.Info("shutting down", "during", "instantiate")
			return nil
		}
		return fmt.Errorf("instantiate: %w", err)
	}

	// the broker forwarder lives as long as this process does, so exiting here
	// would leave every long-running layer dialling an address nothing answers
	log.Info("runner ready, waiting for signal")
	<-ctx.Done()

	// hand the signals back to the runtime: a second Ctrl-C during shutdown
	// should kill the process rather than be swallowed
	stop()
	log.Info("shutting down")

	return nil
}

// closeWithin bounds Runner.Close. Close tears down the connections the
// forwarder is relaying, and a peer that never lets go of one is not a reason
// to hang exit.
func closeWithin(r *pgrunner.Runner, d time.Duration) error {
	done := make(chan error, 1)
	go func() { done <- r.Close() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("close runner: %w", err)
		}
		return nil
	case <-time.After(d):
		return fmt.Errorf("close runner: timed out after %s", d)
	}
}
