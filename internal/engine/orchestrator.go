package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/c12s/gprunner/internal/resolver"
	"github.com/c12s/gprunner/pkg/model"
)

const layerPrefix = "gp"

type Orchestrator struct {
	r      *resolver.BuildResolver
	imgDir string
	mqAddr string
	l      *slog.Logger
}

func NewOrchestrator(r *resolver.BuildResolver, l *slog.Logger, imgDir, mqAddr string) *Orchestrator {
	return &Orchestrator{r: r, l: l, imgDir: imgDir, mqAddr: mqAddr}
}

func (o *Orchestrator) InstantiateChart(ctx context.Context, chart model.Chart) error {
	layers := chart.ChartData

	imageMap, err := o.r.ResolveLayers(ctx, layers)
	if err != nil {
		return fmt.Errorf("an error has occurred while resolving layers: %w", err)
	}

	MQAddr := o.mqAddr
	topicMap, err := buildTopicMap(chart)
	if err != nil {
		return err
	}

	// a cancelled ctx stops us from booting anything further; the layers already
	// up are left running, the same way they outlive the process itself
	for _, pcd := range layers.StoredProcedures {
		if err := ctx.Err(); err != nil {
			return err
		}
		layerName := layerPrefix + "-" + chart.Metadata.ID + "-" + pcd.Metadata.ID

		if err := RunLayer(imageMap[pcd.Metadata.ID], o.imgDir, "", layerName, pcd.Control, pcd.Features, pcd.Links, layers.DataSources, nil); err != nil {
			return fmt.Errorf("an error has occurred while running layer %q: %w", pcd.Metadata.Name, err)
		}
	}

	for _, et := range layers.EventTriggers {
		if err := ctx.Err(); err != nil {
			return err
		}
		topics, err := resolveEventLinks(et.Links.EventLinks, topicMap)
		if err != nil {
			return fmt.Errorf("layer %q: %w", et.Metadata.Name, err)
		}
		layerName := layerPrefix + "-" + chart.Metadata.ID + "-" + et.Metadata.ID
		if err := RunLayer(imageMap[et.Metadata.ID], o.imgDir, MQAddr, layerName, et.Control, et.Features, et.Links, layers.DataSources, topics); err != nil {
			return fmt.Errorf("an error has occurred while running layer %q: %w", et.Metadata.Name, err)
		}
	}

	for _, e := range layers.Events {
		if err := ctx.Err(); err != nil {
			return err
		}
		layerName := layerPrefix + "-" + chart.Metadata.ID + "-" + e.Metadata.ID
		topics := []string{topicMap[e.Metadata.Name]}
		if err := RunLayer(imageMap[e.Metadata.ID], o.imgDir, MQAddr, layerName, e.Control, e.Features, model.Links{}, layers.DataSources, topics); err != nil {
			return fmt.Errorf("an error has occurred while running layer %q: %w", e.Metadata.Name, err)
		}
	}

	return nil
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
