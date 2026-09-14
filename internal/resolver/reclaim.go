package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (r *BuildResolver) Reclaim(ctx context.Context) error {
	if err := r.reclaimBuildDir(); err != nil {
		return err
	}

	r.reclaimPackages(ctx)

	return nil
}

func (r *BuildResolver) reclaimBuildDir() error {
	entries, err := os.ReadDir(r.bldDir)
	if err != nil {
		return fmt.Errorf("reclaim: reading build dir %s: %w", r.bldDir, err)
	}

	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), packagePrefix) {
			continue // not ours: the build dir is inside CacheDir, but stay conservative
		}

		p := filepath.Join(r.bldDir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			r.logger.Warn("reclaim: removing build leftover", "path", p, "err", err)
			continue
		}
		r.logger.Info("reclaim: removed build leftover", "path", p)
	}

	return nil
}

// reclaimPackages drops the resolver's temporary packages from kraft's store.
func (r *BuildResolver) reclaimPackages(ctx context.Context) {
	lsCtx, cancel := context.WithTimeout(ctx, r.timeouts.Network)
	defer cancel()

	out, err := exec.CommandContext(lsCtx, "kraft", "pkg", "ls", "--local", "--all", "-o", "json").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			r.logger.Warn("reclaim: listing kraft packages", "err", err, "output", strings.TrimSpace(string(ee.Stderr)))
		} else {
			r.logger.Warn("reclaim: listing kraft packages", "err", err)
		}
		return
	}

	// an empty store yields a log line on stdout ('no packages found') rather
	// than JSON; anything that does not start like a JSON array is taken as empty
	trimmed := strings.TrimSpace(string(out))
	if !strings.HasPrefix(trimmed, "[") {
		return
	}

	var pkgs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(trimmed), &pkgs); err != nil {
		r.logger.Warn("reclaim: parsing kraft package list", "err", err)
		return
	}

	seen := make(map[string]bool)
	for _, p := range pkgs {
		// one package can be listed once per platform; remove by name once
		if !strings.HasPrefix(p.Name, packagePrefix) || seen[p.Name] {
			continue
		}
		seen[p.Name] = true

		rmCtx, cancelRm := context.WithTimeout(ctx, r.timeouts.Network)
		out, err := exec.CommandContext(rmCtx, "kraft", "pkg", "remove", "--no-prompt", "-n", p.Name).CombinedOutput()
		cancelRm()
		if err != nil {
			r.logger.Warn("reclaim: removing kraft package", "pkg", p.Name, "err", err, "output", strings.TrimSpace(string(out)))
			continue
		}
		r.logger.Info("reclaim: removed kraft package", "pkg", p.Name)
	}
}
