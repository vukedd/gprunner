package resolver

import (
	"fmt"

	"github.com/c12s/gprunner/pkg/model"
	"golang.org/x/sys/unix"
)

func (r *BuildResolver) resolveLinks(links model.Links, md model.LayerMetadata, datasources map[string]model.DataSource, topicMap map[string]string) error {
	for _, hl := range links.HardLinks {
		ds := datasources[hl]
		if err := r.resolveDataSource(ds, true); err != nil {
			return fmt.Errorf("hard link validation failed: %w", err)
		}
	}

	// soft links aren't mandatory for layer start up
	for _, sl := range links.SoftLinks {
		ds := datasources[sl]
		if err := r.resolveDataSource(ds, false); err != nil {
			r.logger.Warn("soft link validation failed", "layer", md.Name, "dataSource", sl, "err", err)
		}
	}

	for _, el := range links.EventLinks {
		_, exists := topicMap[el]
		if !exists {
			return fmt.Errorf("%w layer name: %s, event name: %s", ErrEventUnknown, md.Name, el)
		}
	}

	return nil
}

func (r *BuildResolver) resolveDataSource(ds model.DataSource, isHardLinked bool) error {
	switch ds.Type {
	case model.DataSourceFileType:
		if !resolveDirectory(ds.Path) {
			return fmt.Errorf("data source %s on path %s: %w", ds.Name, ds.Path, ErrDataSourceMissing)
		}
		// R is 4, W IS 2
		mode := uint32(unix.R_OK)
		if isHardLinked {
			// 0b100
			// or
			// 0b010
			// 0b110 = 6 = RW
			mode |= unix.W_OK
		}
		if unix.Access(ds.Path, mode) != nil {
			return fmt.Errorf("data source %s on path %s: %w", ds.Name, ds.Path, ErrDataSourcePermission)
		}
	default:
		return fmt.Errorf("data source %s of type %q: %w", ds.Name, ds.Type, ErrDataSourceType)
	}

	return nil
}

func resolveDirectory(dirPath string) bool {
	return unix.Access(dirPath, unix.F_OK) == nil
}
