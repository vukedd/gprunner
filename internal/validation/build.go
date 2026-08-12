package validation

import (
	"fmt"

	"github.com/c12s/runner/internal/model"
	"golang.org/x/sys/unix"
)

type BuildValidator struct{}

func New() *BuildValidator {
	return &BuildValidator{}
}

// general
func (v *BuildValidator) ValidateLayers(layers model.ChartConfig) error {
	procedures, triggers, events := layers.StoredProcedures, layers.EventTriggers, layers.Events
	datasources := layers.DataSources

	for _, pcd := range procedures {
		if err := v.validateLayer(pcd.Metadata, pcd.Links, datasources); err != nil {
			return err
		}
	}

	for _, et := range triggers {
		if err := v.validateLayer(et.Metadata, et.Links, datasources); err != nil {
			return err
		}
	}

	// event structure doesn't contain links so empty struct is passed
	for _, e := range events {
		if err := v.validateLayer(e.Metadata, model.Links{}, datasources); err != nil {
			return err
		}
	}

	return nil
}

func (v *BuildValidator) validateLayer(md model.LayerMetadata, links model.Links, datasources map[string]model.DataSource) error {
	if err := v.validateLinks(links, md, datasources); err != nil {
		return err
	}
	return v.validateLayerImage(md)
}

// data source validation
func (v *BuildValidator) validateLinks(links model.Links, md model.LayerMetadata, datasources map[string]model.DataSource) error {
	for _, hl := range links.HardLinks {
		ds := datasources[hl]
		if err := v.validateDataSource(ds); err != nil {
			return fmt.Errorf("hard link validation failed: %v\n", err)
		}

	}

	for _, sl := range links.SoftLinks {
		ds := datasources[sl]
		if err := v.validateDataSource(ds); err != nil {
			fmt.Printf("soft link validation failed: %v\n", err)
		}
	}

	return nil
}

func (v *BuildValidator) validateDataSource(ds model.DataSource) error {
	switch ds.Type {
	case model.DataSourceFileType:
		if err := checkFileAccess(ds); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid data source type: %v", ds.Type)
	}

	return nil
}

func checkFileAccess(ds model.DataSource) error {
	if exists := unix.Access(ds.Path, unix.F_OK) == nil; !exists {
		return fmt.Errorf("an error has occurred while validating %s on path %s", ds.Name, ds.Path)
	}

	return nil
}

// image validation
func (v *BuildValidator) validateLayerImage(md model.LayerMetadata) error {
	if md.Image == "" {
		fmt.Println("doesn't have an image reference")
	} else {
		fmt.Printf("%s", md.Image)
	}

	return nil
}
