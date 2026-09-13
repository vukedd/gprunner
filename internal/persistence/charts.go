package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/c12s/gprunner/pkg/model"
)

// ChartState is the goal the runner is converging a chart towards. The values
// are persisted, so they are pinned rather than derived from iota: reordering
// the constants must never change what an existing row means.
type ChartState int

const (
	ChartStarting ChartState = 1
	ChartStarted  ChartState = 2
	ChartStopping ChartState = 3
	ChartStopped  ChartState = 4
)

func (cs ChartState) String() string {
	switch cs {
	case ChartStarting:
		return "starting"
	case ChartStarted:
		return "started"
	case ChartStopping:
		return "stopping"
	case ChartStopped:
		return "stopped"
	default:
		return fmt.Sprintf("unknown(%d)", int(cs))
	}
}

func (s *Store) UpdateChartState(ctx context.Context, chartID string, state ChartState) (err error) {
	const q = `
		INSERT INTO chart_state (chart_id, state, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(chart_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`

	if _, err = s.db.ExecContext(ctx, q, chartID, state, time.Now().Unix()); err != nil {
		return fmt.Errorf("updating state of chart %s to %s: %w", chartID, state, err)
	}

	return nil
}

func (s *Store) GetChartState(ctx context.Context, chartID string) (state ChartState, ok bool, err error) {
	const q = `SELECT state FROM chart_state WHERE chart_id = ?`

	err = s.db.QueryRowContext(ctx, q, chartID).Scan(&state)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return 0, false, nil
		default:
			return 0, false, fmt.Errorf("looking up state of chart %s: %w", chartID, err)
		}
	}

	return state, true, nil
}

func (s *Store) ListChartStates(ctx context.Context) (map[string]ChartState, error) {
	const q = `SELECT chart_id, state FROM chart_state`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing chart states: %w", err)
	}
	defer rows.Close()

	states := make(map[string]ChartState)
	for rows.Next() {
		var id string
		var st ChartState
		if err := rows.Scan(&id, &st); err != nil {
			return nil, fmt.Errorf("reading chart state row: %w", err)
		}
		states[id] = st
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing chart states: %w", err)
	}

	return states, nil
}

func (s *Store) DeleteChartState(ctx context.Context, chartID string) error {
	const q = `DELETE FROM chart_state WHERE chart_id = ?`

	if _, err := s.db.ExecContext(ctx, q, chartID); err != nil {
		return fmt.Errorf("deleting state of chart %s: %w", chartID, err)
	}

	return nil
}

func (s *Store) SaveChart(ctx context.Context, chart model.Chart) error {
	spec, err := json.Marshal(chart)
	if err != nil {
		return fmt.Errorf("encoding chart %s: %w", chart.Metadata.ID, err)
	}

	const q = `
		INSERT INTO charts (chart_id, name, namespace, maintainer, schema_version, spec, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chart_id) DO UPDATE SET
			name = excluded.name, namespace = excluded.namespace, maintainer = excluded.maintainer,
			schema_version = excluded.schema_version, spec = excluded.spec, updated_at = excluded.updated_at`

	m := chart.Metadata
	if _, err := s.db.ExecContext(ctx, q, m.ID, m.Name, m.Namespace, m.Maintainer, chart.SchemaVersion, string(spec), time.Now().Unix()); err != nil {
		return fmt.Errorf("saving chart %s: %w", m.ID, err)
	}

	return nil
}

func (s *Store) GetChartByRef(ctx context.Context, ref model.ChartRef) (chart model.Chart, ok bool, err error) {
	const q = `
		SELECT spec FROM charts
		WHERE name = ? AND namespace = ? AND maintainer = ? AND schema_version = ?`

	var spec string
	err = s.db.QueryRowContext(ctx, q, ref.Name, ref.Namespace, ref.Maintainer, ref.SchemaVersion).Scan(&spec)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Chart{}, false, nil
		}
		return model.Chart{}, false, fmt.Errorf("looking up chart %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	if err := json.Unmarshal([]byte(spec), &chart); err != nil {
		return model.Chart{}, false, fmt.Errorf("decoding chart %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	return chart, true, nil
}
