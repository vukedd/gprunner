package resolver

import "errors"

var (
	ErrNoSource = errors.New("layer has no image reference and no build config")

	ErrEmptyCommand = errors.New("layer has an empty build command")

	ErrNoTarget = errors.New("layer declares no targets")

	ErrPlatUnavailable = errors.New("image publishes no package for provided platform")

	ErrNoRemoteRef = errors.New("could not resolve HEAD on remote")

	ErrDataSourceMissing = errors.New("data source path not found")

	ErrDataSourceType = errors.New("invalid data source type")

	ErrDataSourcePermission = errors.New("data source path not readable/writable")

	ErrBrokerSubnetInvalid = errors.New("broker subnet is not a usable IPv4 CIDR")

	ErrBrokerSubnetInUse = errors.New("broker subnet overlaps an existing network")

	ErrBrokerNetworkMismatch = errors.New("broker bridge exists with a different subnet")

	ErrNetworkPermission = errors.New("bridge management requires NET_ADMIN")
)
