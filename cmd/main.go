package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/c12s/gprunner"
	"github.com/c12s/gprunner/cmd/api"
	"github.com/c12s/gprunner/cmd/server"
	"github.com/c12s/gprunner/cmd/starmap"
)

const (
	// A wedged connection teardown must not hold the process open forever.
	shutdownTimeout = 30 * time.Second

	defaultPort = "50052"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if err := run(log); err != nil {
		log.Error("runner", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cache := os.Getenv("GPRUNNER_CACHE")
	if cache == "" {
		cache = "/tmp/gprunner-cache"
	}
	if err := os.MkdirAll(cache, 0o755); err != nil { // CacheDir must exist
		return fmt.Errorf("cache dir: %w", err)
	}

	brokerAddr := os.Getenv("GPRUNNER_BROKER")
	if brokerAddr == "" {
		brokerAddr = "nats:4222"
	}

	// left empty, the library falls back to GPRUNNER.DefaultBrokerSubnet
	brokerSubnet := os.Getenv("GPRUNNER_BROKER_SUBNET")

	starmapAddr := os.Getenv("STARMAP_ADDR")
	if starmapAddr == "" {
		return errors.New("STARMAP_ADDR is required")
	}

	port := os.Getenv("GPRUNNER_PORT")
	if port == "" {
		port = defaultPort
	}

	r, err := gprunner.New(ctx, gprunner.Config{
		CacheDir:     cache,
		Logger:       log,
		BrokerSubnet: brokerSubnet,
		BrokerAddr:   brokerAddr,
	})
	if err != nil {
		return fmt.Errorf("new runner: %w", err)
	}

	// every path out of here closes the runner, so a failed start still
	// checkpoints the write-ahead log. Deferred first so it runs last: the
	// gRPC server must be stopped before the runner it calls into goes away
	defer func() {
		err = errors.Join(err, closeWithin(r, shutdownTimeout))
	}()

	sm, err := starmap.Dial(starmapAddr)
	if err != nil {
		return err
	}
	defer sm.Close()

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return fmt.Errorf("listen on :%s: %w", port, err)
	}

	grpcServer := grpc.NewServer()
	api.RegisterRunnerServiceServer(grpcServer, server.New(r, sm, log))
	reflection.Register(grpcServer)

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcServer.Serve(ln) }()

	log.Info("runner listening", "port", port, "starmap", starmapAddr, "broker", brokerAddr)

	// the broker forwarder lives as long as this process does, so the service
	// stays up until told otherwise; every long-running layer depends on it
	select {
	case err := <-serveErr:
		return fmt.Errorf("grpc server: %w", err)
	case <-ctx.Done():
	}

	// hand the signals back to the runtime: a second Ctrl-C during shutdown
	// should kill the process rather than be swallowed
	stop()
	log.Info("shutting down")

	// stop taking new calls and let in-flight ones (a chart mid-boot) finish;
	// a call that will not finish in time is cut off rather than holding the
	// process open
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(shutdownTimeout):
		log.Warn("grpc calls still in flight, forcing stop")
		grpcServer.Stop()
	}

	return nil
}

// closeWithin bounds Runner.Close. Close tears down the connections the
// forwarder is relaying, and a peer that never lets go of one is not a reason
// to hang exit.
func closeWithin(r *gprunner.Runner, d time.Duration) error {
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
