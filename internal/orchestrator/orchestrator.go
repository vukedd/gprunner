package orchestrator

import (
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

	for _, sp := range layers.StoredProcedures {
		for _, hl := range sp.Links.HardLinks {
			ds := layers.DataSources[hl]
			if err := o.v.ValidateLink(ds); err != nil {
				return err
			}
		}

		for _, sl := range sp.Links.SoftLinks {
			ds := layers.DataSources[sl]
			if err := o.v.ValidateLink(ds); err != nil {
				return err
			}
		}
	}

	return nil
}
