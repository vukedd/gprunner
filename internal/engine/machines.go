package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const machineCmdTimeout = 30 * time.Second

// machine is the slice of 'kraft ps -o json' the runner cares about.
type machine struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func listMachines(ctx context.Context) ([]machine, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, machineCmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "kraft", "ps", "-a", "-o", "json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing machines: %w — %s", err, strings.TrimSpace(stderr.String()))
	}

	// kraft prints a bare null when there is nothing to list
	var machines []machine
	if err := json.Unmarshal(out, &machines); err != nil {
		return nil, fmt.Errorf("parsing machine list: %w", err)
	}

	return machines, nil
}

func chartMachines(ctx context.Context, chartID string) ([]machine, error) {
	all, err := listMachines(ctx)
	if err != nil {
		return nil, err
	}

	prefix := layerPrefix + "-" + chartID + "-"
	var own []machine
	for _, m := range all {
		if strings.HasPrefix(m.Name, prefix) {
			own = append(own, m)
		}
	}

	return own, nil
}

func removeMachine(ctx context.Context, name string) error {
	if out, err := runMachineCmd(ctx, "stop", name); err != nil {
		return fmt.Errorf("stopping %s: %w — %s", name, err, out)
	}
	if out, err := runMachineCmd(ctx, "rm", name); err != nil {
		return fmt.Errorf("removing %s: %w — %s", name, err, out)
	}

	return nil
}

func runMachineCmd(ctx context.Context, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, machineCmdTimeout)
	defer cancel()

	out, err := exec.CommandContext(cmdCtx, "kraft", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
