package pgrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/c12s/gprunner/internal/engine"
	"github.com/c12s/gprunner/internal/persistence"
	"github.com/c12s/gprunner/internal/resolver"
	"github.com/c12s/gprunner/pkg/model"
)

type Runner struct {
	cfg Config
	o   *engine.Orchestrator
	s   *persistence.Store
}

type Config struct {
	CacheDir string
	Logger   *slog.Logger
}

func New(ctx context.Context, cfg Config) (*Runner, error) {

	dirs, err := resolveDirs(cfg.CacheDir)
	if err != nil {
		return nil, err
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	s, err := persistence.Open(ctx, filepath.Join(dirs.db, "pgrunner.db"))
	if err != nil {
		return nil, fmt.Errorf("pgrunner: opening store: %w", err)
	}

	r := resolver.NewBuildResolver(cfg.Logger, dirs.image, dirs.build, s)
	o := engine.NewOrchestrator(r, cfg.Logger, dirs.image)

	return &Runner{cfg: cfg, o: o, s: s}, nil
}

// Close closes the image index database, releasing its file descriptors and
// checkpointing the write-ahead log. The Runner is unusable afterwards.
func (r *Runner) Close() error {
	return r.s.Close()
}

func (r *Runner) InstantiateChart(ctx context.Context, c model.Chart) error {
	return r.o.InstantiateChart(ctx, c)
}

type dirs struct {
	image string
	build string
	db    string
}

func resolveDirs(cacheDir string) (dirs, error) {
	if cacheDir == "" {
		return dirs{}, errors.New("pgrunner: CacheDir is required")
	}

	// to avoid persisting all over the place
	root, err := filepath.Abs(cacheDir)
	if err != nil {
		return dirs{}, fmt.Errorf("pgrunner: resolving CacheDir %q: %w", cacheDir, err)
	}

	fi, err := os.Stat(root)
	if err != nil {
		return dirs{}, fmt.Errorf("pgrunner: CacheDir %s: %w", root, err)
	}
	if !fi.IsDir() {
		return dirs{}, fmt.Errorf("pgrunner: CacheDir %s is not a directory", root)
	}

	d := dirs{
		image: filepath.Join(root, "images"),
		build: filepath.Join(root, "build"),
		db:    filepath.Join(root, "db"),
	}

	for _, p := range []string{d.image, d.build, d.db} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return dirs{}, fmt.Errorf("pgrunner: creating %s: %w", p, err)
		}
	}

	return d, nil
}
