package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/c12s/gprunner/internal/persistence"
)


func (o *Orchestrator) Reconcile(ctx context.Context) error {
	states, err := o.store.ListChartStates(ctx)
	if err != nil {
		return fmt.Errorf("repair: %w", err)
	}

	machines, err := listMachines(ctx)
	if err != nil {
		return fmt.Errorf("repair: %w", err)
	}

	handled := make(map[string]bool)

	// charts the store believes are up or in transition
	for chartID, state := range states {
		if state == persistence.ChartStopped {
			continue
		}
		handled[chartID] = true

		o.logger.Warn("chart found in a non-stopped state after restart, taking it down", "chart", chartID, "state", state.String())

		if err := o.teardownChart(ctx, chartID); err != nil {
			o.logger.Error("repair: tearing down chart", "chart", chartID, "err", err)
			continue
		}
		if err := o.store.UpdateChartState(ctx, chartID, persistence.ChartStopped); err != nil {
			o.logger.Error("repair: marking chart stopped", "chart", chartID, "err", err)
		}
	}

	for _, m := range machines {
		if !strings.HasPrefix(m.Name, layerPrefix+nameSep) || ownedByHandledChart(m.Name, handled) {
			continue
		}

		o.logger.Warn("orphaned layer found after restart, removing", "machine", m.Name, "was", m.Status)

		if err := removeMachine(ctx, m.Name); err != nil {
			o.logger.Error("repair: removing orphan", "machine", m.Name, "err", err)
		}
	}

	return nil
}

// ownedByHandledChart reports whether a machine belongs to a chart whose
// teardown already ran above.
func ownedByHandledChart(name string, handled map[string]bool) bool {
	for chartID := range handled {
		if strings.HasPrefix(name, chartPrefix(chartID)) {
			return true
		}
	}

	return false
}
