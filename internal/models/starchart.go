package models

// Chart represents the entire starchart JSON structure
type Chart struct {
	APIVersion    string      `json:"apiVersion"`
	SchemaVersion string      `json:"schemaVersion"`
	Metadata      Metadata    `json:"metadata"`
	ChartData     ChartConfig `json:"chart"`
}

// Metadata contains chart metadata
type Metadata struct {
	Labels      map[string]string `json:"labels"`
	Tags        map[string]string `json:"tags"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Maintainer  string            `json:"maintainer"`
	Description string            `json:"description"`
	Visibility  string            `json:"visibility"`
	Engine      string            `json:"engine"`
}

// ChartConfig contains the chart configuration with data sources, procedures, triggers, and events
type ChartConfig struct {
	DataSources      map[string]DataSource      `json:"dataSources"`
	StoredProcedures map[string]StoredProcedure `json:"storedProcedures"`
	EventTriggers    map[string]EventTrigger    `json:"eventTriggers"`
	Events           map[string]Event           `json:"events"`
}

// DataSource represents a data source
type DataSource struct {
	Labels       map[string]string `json:"labels"`
	Tags         map[string]string `json:"tags"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Path         string            `json:"path"`
	ResourceName string            `json:"resourceName"`
	Description  string            `json:"description"`
}

// StoredProcedure represents a stored procedure
type StoredProcedure struct {
	Metadata ProcedureMetadata `json:"metadata"`
	Control  Control           `json:"control"`
	Features Features          `json:"features"`
	Links    Links             `json:"links"`
}

// ProcedureMetadata contains metadata for procedures, triggers, and events
type ProcedureMetadata struct {
	Labels      map[string]string `json:"labels"`
	Tags        map[string]string `json:"tags"`
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Build       *BuildConfig      `json:"build"`
	Prefix      string            `json:"prefix"`
	Topic       string            `json:"topic"`
	Description string            `json:"description"`
}

// BuildConfig contains build configuration
type BuildConfig struct {
	Pull    string `json:"pull"`
	Workdir string `json:"workdir"`
	Command string `json:"command"`
}

// Control contains control settings for execution
type Control struct {
	DisableVirtualization bool   `json:"disableVirtualization"`
	RunDetached           bool   `json:"runDetached"`
	RemoveOnStop          bool   `json:"removeOnStop"`
	Memory                string `json:"memory"`
	KernelArgs            string `json:"kernelArgs"`
}

// Features contains feature configurations
type Features struct {
	Networks []string `json:"networks"`
	Ports    []string `json:"ports"`
	Volumes  []string `json:"volumes"`
	Targets  []string `json:"targets"`
	EnvVars  []string `json:"envVars"`
}

// Links contains resource links
type Links struct {
	SoftLinks  []string `json:"softLinks"`
	HardLinks  []string `json:"hardLinks"`
	EventLinks []string `json:"eventLinks"`
}

// EventTrigger represents an event trigger
type EventTrigger struct {
	Metadata ProcedureMetadata `json:"metadata"`
	Control  Control           `json:"control"`
	Features Features          `json:"features"`
	Links    Links             `json:"links"`
}

// Event represents an event
type Event struct {
	Metadata ProcedureMetadata `json:"metadata"`
	Control  Control           `json:"control"`
	Features Features          `json:"features"`
}
