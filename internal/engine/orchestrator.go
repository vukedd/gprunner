package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/c12s/gprunner/internal/resolver"
	"github.com/c12s/gprunner/pkg/model"
)

type Orchestrator struct {
	r      *resolver.BuildResolver
	imgDir string
	l      *slog.Logger
}

func NewOrchestrator(r *resolver.BuildResolver, l *slog.Logger, imgDir string) *Orchestrator {
	return &Orchestrator{r: r, l: l, imgDir: imgDir}
}

func (o *Orchestrator) InstantiateChart(ctx context.Context, chart model.Chart) error {
	layers := chart.ChartData

	imageMap, err := o.r.ResolveLayers(ctx, layers)
	if err != nil {
		return fmt.Errorf("an error has occurred while resolving layers: %w", err)
	}

	for _, pcd := range layers.StoredProcedures {
		if err := RunLayer(imageMap[pcd.Metadata.ID], pcd.Control.Memory, pcd.Control.KernelArgs, o.imgDir); err != nil {
			return fmt.Errorf("an error has occurred while running layer %q: %w", pcd.Metadata.Name, err)
		}
	}

	for _, et := range layers.EventTriggers {
		if err := RunLayer(imageMap[et.Metadata.ID], et.Control.Memory, et.Control.KernelArgs, o.imgDir); err != nil {
			return fmt.Errorf("an error has occurred while running layer %q: %w", et.Metadata.Name, err)
		}
	}

	for _, e := range layers.Events {
		if err := RunLayer(imageMap[e.Metadata.ID], e.Control.Memory, e.Control.KernelArgs, o.imgDir); err != nil {
			return fmt.Errorf("an error has occurred while running layer %q: %w", e.Metadata.Name, err)
		}
	}

	return nil
}
