package engine

import "errors"

var (
	ErrTargetInvalid = errors.New("target is not of the form <plat>/<arch>")

	ErrDataSourceUnknown = errors.New("link names a data source the chart does not declare")

	ErrEventUnknown = errors.New("event link names an event the chart does not declare")

	ErrEventTopicEmpty = errors.New("event declares no topic")
)
