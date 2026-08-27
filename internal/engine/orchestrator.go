package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/c12s/pgrunner/internal/validation"
	"github.com/c12s/pgrunner/pkg/model"
)

type Orchestrator struct {
	v      *validation.BuildValidator
	imgDir string
	l      *slog.Logger
}

func NewOrchestrator(v *validation.BuildValidator, l *slog.Logger, imgDir string) *Orchestrator {
	return &Orchestrator{v: v, l: l, imgDir: imgDir}
}

func (o *Orchestrator) InstantiateChart(ctx context.Context, chart model.Chart) error {
	layers := chart.ChartData
	if err := o.v.ValidateLayers(layers); err != nil {
		return fmt.Errorf("an error has occurred while validating layers: %w", err)
	}

	for _, pcd := range layers.StoredProcedures {
		if err := RunLayer(pcd.Metadata.ID, pcd.Control.Memory, pcd.Control.KernelArgs, o.imgDir); err != nil {
			return fmt.Errorf("running layer %q: %w", pcd.Metadata.Name, err)
		}
	}

	for _, et := range layers.EventTriggers {
		if err := RunLayer(et.Metadata.ID, et.Control.Memory, et.Control.KernelArgs, o.imgDir); err != nil {
			return fmt.Errorf("running layer %q: %w", et.Metadata.Name, err)
		}
	}

	for _, e := range layers.Events {
		if err := RunLayer(e.Metadata.ID, e.Control.Memory, e.Control.KernelArgs, o.imgDir); err != nil {
			return fmt.Errorf("running layer %q: %w", e.Metadata.Name, err)
		}
	}

	return nil
}
