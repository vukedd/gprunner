package validation

import (
	"errors"
	"fmt"

	"github.com/c12s/runner/internal/model"
	"golang.org/x/sys/unix"
)

var (
	UnknownDataSourceType = errors.New("unknown data source type")
)

type BuildValidator struct{}

func New() *BuildValidator {
	return &BuildValidator{}
}

func (b *BuildValidator) ValidateLink(ds model.DataSource) error {
	switch ds.Type {
	case model.DataSourceFileType:
		if err := checkFileAccess(ds.Path); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid data source type: %v", ds.Type)
	}

	return nil
}

func checkFileAccess(path string) error {
	if exists := unix.Access(path, unix.F_OK) == nil; !exists {
		return fmt.Errorf("data source on path: %s doesn't exist or isn't accessible", path)
	}

	return nil
}
