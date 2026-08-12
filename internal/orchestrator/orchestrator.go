package orchestrator

import (
	"fmt"

	"github.com/c12s/runner/internal/model"
	"github.com/c12s/runner/internal/validation"
)

type Orchestrator struct {
	v *validation.BuildValidator
}

func New(v *validation.BuildValidator) *Orchestrator {
	return &Orchestrator{v: v}
}

func (o *Orchestrator) InstantiateChart(chart model.Chart) error {
	layers := chart.ChartData
	if err := o.v.ValidateLayers(layers); err != nil {
		return fmt.Errorf("an error has occurred while validating data sources: %w", err)
	}

	return nil
}
