package gprunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/c12s/gprunner/internal/engine"
	"github.com/c12s/gprunner/internal/persistence"
	"github.com/c12s/gprunner/internal/resolver"
	"github.com/c12s/gprunner/pkg/model"
)

type Runner struct {
	cfg Config
	o   *engine.Orchestrator
	s   *persistence.Store
	f   *engine.Forwarder
}

const (
	DefaultNetworkTimeout = 1 * time.Minute
	DefaultBuildTimeout   = 5 * time.Minute

	DefaultMaxConcurrency = 5

	// determines the number of devices that can connect to the broker bridge to communicate with the broker
	DefaultBrokerSubnet = "172.200.0.1/16"
)

var (
	// ErrChartNotPulled is returned when a chart is referenced before SaveChart.
	ErrChartNotPulled = errors.New("gprunner: chart has not been pulled")

	// ErrChartRunning is returned by InstantiateChart when the chart already has
	// running layers; a chart is instantiated once at a time.
	ErrChartRunning = engine.ErrChartRunning
)

type Config struct {
	CacheDir       string
	Logger         *slog.Logger
	BrokerAddr     string
	BrokerSubnet   string
	NetworkTimeout time.Duration
	BuildTimeout   time.Duration
	MaxConcurrency int
}

func New(ctx context.Context, cfg Config) (*Runner, error) {
	// cache directory prep
	dirs, err := resolveDirs(cfg.CacheDir)
	if err != nil {
		return nil, err
	}

	// logger
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	// timeout
	if cfg.NetworkTimeout <= 0 {
		cfg.NetworkTimeout = DefaultNetworkTimeout
	}
	if cfg.BuildTimeout <= 0 {
		cfg.BuildTimeout = DefaultBuildTimeout
	}

	t := resolver.Timeouts{
		Network: cfg.NetworkTimeout,
		Build:   cfg.BuildTimeout,
	}

	// concurrency
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = DefaultMaxConcurrency
	}

	mc := resolver.MaxConcurrency{
		Run:     cfg.MaxConcurrency,
		Resolve: cfg.MaxConcurrency,
	}

	// sqlite prep
	s, err := persistence.Open(ctx, filepath.Join(dirs.db, "gprunner.db"))
	if err != nil {
		return nil, fmt.Errorf("gprunner: opening store: %w", err)
	}

	r := resolver.NewBuildResolver(cfg.Logger, dirs.image, dirs.build, s, t, mc)

	// a resolve that died with the previous process leaves clones, staging
	// directories and kraft packages behind; nothing can be resolving yet, so
	// this is the one moment they can be swept safely
	if err := r.Reclaim(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("gprunner: %w", err)
	}

	// network
	if cfg.BrokerSubnet == "" {
		cfg.BrokerSubnet = DefaultBrokerSubnet
	}

	gateway, err := r.EnsureBrokerNetwork(ctx, cfg.BrokerSubnet)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("gprunner: broker network: %w", err)
	}

	_, port, err := net.SplitHostPort(cfg.BrokerAddr)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("gprunner: BrokerAddr %q: %w", cfg.BrokerAddr, err)
	}

	// fail fast on an unreachable broker instead of at the first guest connect
	if err := probeBrokerNetwork(cfg); err != nil {
		s.Close()
		return nil, fmt.Errorf("gprunner: BrokerAddr %q unreachable: %w", cfg.BrokerAddr, err)
	}

	mqAddr := net.JoinHostPort(gateway, port)

	f, err := engine.StartForwarder(cfg.Logger, mqAddr, cfg.BrokerAddr)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("gprunner: broker forwarder: %w", err)
	}

	o := engine.NewOrchestrator(r, cfg.Logger, s, dirs.image, mqAddr)

	// a previous run may have died mid-operation, so every chart is set to
	// stopped before any caller can start new ones.
	rn := &Runner{cfg: cfg, o: o, s: s, f: f}
	if err := o.Reconcile(ctx); err != nil {
		rn.Close()
		return nil, fmt.Errorf("gprunner: %w", err)
	}

	return rn, nil
}

func probeBrokerNetwork(cfg Config) error {
	probe, err := net.DialTimeout("tcp", cfg.BrokerAddr, cfg.NetworkTimeout)
	if err != nil {
		return err
	}

	return probe.Close()
}

type dirs struct {
	image string
	build string
	db    string
}

func resolveDirs(cacheDir string) (dirs, error) {
	if cacheDir == "" {
		return dirs{}, errors.New("gprunner: CacheDir is required")
	}

	// to avoid persisting all over the place
	root, err := filepath.Abs(cacheDir)
	if err != nil {
		return dirs{}, fmt.Errorf("gprunner: resolving CacheDir %q: %w", cacheDir, err)
	}

	fi, err := os.Stat(root)
	if err != nil {
		return dirs{}, fmt.Errorf("gprunner: CacheDir %s: %w", root, err)
	}
	if !fi.IsDir() {
		return dirs{}, fmt.Errorf("gprunner: CacheDir %s is not a directory", root)
	}

	d := dirs{
		image: filepath.Join(root, "images"),
		build: filepath.Join(root, "build"),
		db:    filepath.Join(root, "db"),
	}

	for _, p := range []string{d.image, d.build, d.db} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return dirs{}, fmt.Errorf("gprunner: creating %s: %w", p, err)
		}
	}

	return d, nil
}

// Close stops the broker forwarder and closes the image index database,
// releasing its file descriptors and checkpointing the write-ahead log. The
// Runner is unusable afterwards. Running layers are left alone; the bridge
// outlives the process by design.
func (r *Runner) Close() error {
	return errors.Join(r.f.Close(), r.s.Close())
}

// SaveChart stores a chart body so it can later be started or stopped by
// reference. Saving the same chart again replaces the earlier copy.
func (r *Runner) SaveChart(ctx context.Context, chart model.Chart) error {
	return r.s.SaveChart(ctx, chart)
}

func (r *Runner) InstantiateChart(ctx context.Context, ref model.ChartRef) error {
	chart, err := r.loadChart(ctx, ref)
	if err != nil {
		return err
	}
	return r.o.InstantiateChart(ctx, chart)
}

func (r *Runner) KillChart(ctx context.Context, ref model.ChartRef) error {
	chart, err := r.loadChart(ctx, ref)
	if err != nil {
		return err
	}
	return r.o.KillChart(ctx, chart)
}

func (r *Runner) loadChart(ctx context.Context, ref model.ChartRef) (model.Chart, error) {
	chart, ok, err := r.s.GetChartByRef(ctx, ref)
	if err != nil {
		return model.Chart{}, err
	}
	if !ok {
		return model.Chart{}, fmt.Errorf("%w: %s/%s@%s", ErrChartNotPulled, ref.Namespace, ref.Name, ref.SchemaVersion)
	}
	return chart, nil
}
