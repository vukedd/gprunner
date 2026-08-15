package orchestration

import (
	"fmt"

	"github.com/c12s/runner/internal/model"
	"github.com/c12s/runner/internal/validation"
)

type Orchestrator struct {
	v *validation.BuildValidator
}

func NewOrchestrator(v *validation.BuildValidator) *Orchestrator {
	return &Orchestrator{v: v}
}

func (o *Orchestrator) InstantiateChart(chart model.Chart) error {
	layers := chart.ChartData
	if err := o.v.ValidateLayers(layers); err != nil {
		return fmt.Errorf("an error has occurred while validating layers: %w", err)
	}

	for _, pcd := range layers.StoredProcedures {
		if err := RunLayer(pcd.Metadata.ID, pcd.Control.Memory, pcd.Control.KernelArgs); err != nil {
			return fmt.Errorf("running layer %q: %w", pcd.Metadata.Name, err)
		}
	}

	for _, et := range layers.EventTriggers {
		if err := RunLayer(et.Metadata.ID, et.Control.Memory, et.Control.KernelArgs); err != nil {
			return fmt.Errorf("running layer %q: %w", et.Metadata.Name, err)
		}
	}

	for _, e := range layers.Events {
		if err := RunLayer(e.Metadata.ID, e.Control.Memory, e.Control.KernelArgs); err != nil {
			return fmt.Errorf("running layer %q: %w", e.Metadata.Name, err)
		}
	}

	return nil
}
