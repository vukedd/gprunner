package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/c12s/gprunner/internal/persistence"
	"github.com/c12s/gprunner/internal/resolver"
	"github.com/c12s/gprunner/pkg/model"
)

const layerPrefix = "gp"

type Orchestrator struct {
	res    *resolver.BuildResolver
	store  *persistence.Store
	imgDir string
	mqAddr string
	logger *slog.Logger
}

func NewOrchestrator(r *resolver.BuildResolver, l *slog.Logger, store *persistence.Store, imgDir, mqAddr string) *Orchestrator {
	return &Orchestrator{res: r, logger: l, store: store, imgDir: imgDir, mqAddr: mqAddr}
}

func (o *Orchestrator) InstantiateChart(ctx context.Context, chart model.Chart) error {
	layers := chart.ChartData

	imageMap, err := o.res.ResolveLayers(ctx, layers)
	if err != nil {
		return fmt.Errorf("an error has occurred while resolving layers: %w", err)
	}

	MQAddr := o.mqAddr
	topicMap, err := buildTopicMap(chart)
	if err != nil {
		o.logger.Error("error occurred while resolving topic map", "chart", chart.Metadata.Name)
		return err
	}

	if err := o.store.UpdateChartState(ctx, chart.Metadata.ID, persistence.ChartStarting); err != nil {
		o.logger.Error("error occurred while updating chart state", "chart", chart.Metadata.Name, "new_state", persistence.ChartStarting.String())
		return err
	}

	// a chart is either fully up or fully down: any failure from here on,
	// including a cancelled ctx, takes down what already booted
	for _, pcd := range layers.StoredProcedures {
		if err := ctx.Err(); err != nil {
			return o.abortStart(ctx, chart, err)
		}
		layerName := layerPrefix + "-" + chart.Metadata.ID + "-" + pcd.Metadata.ID

		if err := RunLayer(imageMap[pcd.Metadata.ID], o.imgDir, "", layerName, pcd.Control, pcd.Features, pcd.Links, layers.DataSources, nil); err != nil {
			return o.abortStart(ctx, chart, fmt.Errorf("an error has occurred while running layer %q: %w", pcd.Metadata.Name, err))
		}
	}

	for _, et := range layers.EventTriggers {
		if err := ctx.Err(); err != nil {
			return o.abortStart(ctx, chart, err)
		}
		topics, err := resolveEventLinks(et.Links.EventLinks, topicMap)
		if err != nil {
			return o.abortStart(ctx, chart, fmt.Errorf("layer %q: %w", et.Metadata.Name, err))
		}
		layerName := layerPrefix + "-" + chart.Metadata.ID + "-" + et.Metadata.ID
		if err := RunLayer(imageMap[et.Metadata.ID], o.imgDir, MQAddr, layerName, et.Control, et.Features, et.Links, layers.DataSources, topics); err != nil {
			return o.abortStart(ctx, chart, fmt.Errorf("an error has occurred while running layer %q: %w", et.Metadata.Name, err))
		}
	}

	for _, e := range layers.Events {
		if err := ctx.Err(); err != nil {
			return o.abortStart(ctx, chart, err)
		}
		layerName := layerPrefix + "-" + chart.Metadata.ID + "-" + e.Metadata.ID
		topics := []string{topicMap[e.Metadata.Name]}
		if err := RunLayer(imageMap[e.Metadata.ID], o.imgDir, MQAddr, layerName, e.Control, e.Features, model.Links{}, layers.DataSources, topics); err != nil {
			return o.abortStart(ctx, chart, fmt.Errorf("an error has occurred while running layer %q: %w", e.Metadata.Name, err))
		}
	}

	if err := o.store.UpdateChartState(ctx, chart.Metadata.ID, persistence.ChartStarted); err != nil {
		o.logger.Error("error occurred while updating chart state", "chart", chart.Metadata.Name, "new_state", persistence.ChartStarted.String())
		return err
	}

	return nil
}

func (o *Orchestrator) KillChart(ctx context.Context, chart model.Chart) error {
	if err := o.store.UpdateChartState(ctx, chart.Metadata.ID, persistence.ChartStopping); err != nil {
		o.logger.Error("error occurred while updating chart state", "chart", chart.Metadata.Name, "new_state", persistence.ChartStopping.String())
		return err
	}

	if err := o.teardownChart(ctx, chart.Metadata.ID); err != nil {
		return fmt.Errorf("an error has occurred while killing chart %q: %w", chart.Metadata.Name, err)
	}

	if err := o.store.UpdateChartState(ctx, chart.Metadata.ID, persistence.ChartStopped); err != nil {
		o.logger.Error("error occurred while updating chart state", "chart", chart.Metadata.Name, "new_state", persistence.ChartStopped.String())
		return err
	}

	return nil
}

func (o *Orchestrator) teardownChart(ctx context.Context, chartID string) error {
	machines, err := chartMachines(ctx, chartID)
	if err != nil {
		return err
	}

	for _, m := range machines {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := removeMachine(ctx, m.Name); err != nil {
			return err
		}
		o.logger.Debug("layer removed", "machine", m.Name, "was", m.Status)
	}

	return nil
}


func (o *Orchestrator) abortStart(ctx context.Context, chart model.Chart, cause error) error {
	// the caller's ctx may be what failed, so the teardown gets its own
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), machineCmdTimeout*4)
	defer cancel()

	if err := o.store.UpdateChartState(ctx, chart.Metadata.ID, persistence.ChartStopping); err != nil {
		o.logger.Error("error occurred while updating chart state", "chart", chart.Metadata.Name, "new_state", persistence.ChartStopping.String(), "err", err)
		return cause
	}

	if err := o.teardownChart(ctx, chart.Metadata.ID); err != nil {
		o.logger.Error("tearing down chart after a failed start", "chart", chart.Metadata.Name, "err", err)
		return cause
	}

	if err := o.store.UpdateChartState(ctx, chart.Metadata.ID, persistence.ChartStopped); err != nil {
		o.logger.Error("error occurred while updating chart state", "chart", chart.Metadata.Name, "new_state", persistence.ChartStopped.String(), "err", err)
	}

	return cause
}

func buildTopicMap(chart model.Chart) (map[string]string, error) {
	topicMap := make(map[string]string)

	for _, e := range chart.ChartData.Events {
		if e.Metadata.Topic == "" {
			return nil, fmt.Errorf("event %q: %w", e.Metadata.Name, ErrEventTopicEmpty)
		}
		topicMap[e.Metadata.Name] = chart.Metadata.ID + "." + e.Metadata.Topic
	}

	return topicMap, nil
}

func resolveEventLinks(eventLinks []string, topicMap map[string]string) ([]string, error) {
	topics := make([]string, 0, len(eventLinks))

	for _, name := range eventLinks {
		topic, ok := topicMap[name]
		if !ok {
			return nil, fmt.Errorf("event link %q: %w", name, ErrEventUnknown)
		}
		topics = append(topics, topic)
	}

	return topics, nil
}
