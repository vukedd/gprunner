package model

type DataSourceType string

const (
	DataSourceFileType DataSourceType = "file"
)

type Chart struct {
	APIVersion    string      `json:"apiVersion"`
	SchemaVersion string      `json:"schemaVersion"`
	Metadata      Metadata    `json:"metadata"`
	ChartData     ChartConfig `json:"chart"`
}

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

type ChartConfig struct {
	DataSources      map[string]DataSource      `json:"dataSources"`
	StoredProcedures map[string]StoredProcedure `json:"storedProcedures"`
	EventTriggers    map[string]EventTrigger    `json:"eventTriggers"`
	Events           map[string]Event           `json:"events"`
}

type DataSource struct {
	Labels       map[string]string `json:"labels"`
	Tags         map[string]string `json:"tags"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         DataSourceType    `json:"type"`
	Path         string            `json:"path"`
	ResourceName string            `json:"resourceName"`
	Description  string            `json:"description"`
}

type StoredProcedure struct {
	Metadata LayerMetadata `json:"metadata"`
	Control  Control       `json:"control"`
	Features Features      `json:"features"`
	Links    Links         `json:"links"`
}

type LayerMetadata struct {
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

type BuildConfig struct {
	Pull    string `json:"pull"`
	Workdir string `json:"workdir"`
	Command string `json:"command"`
}

type Control struct {
	DisableVirtualization bool   `json:"disableVirtualization"`
	RunDetached           bool   `json:"runDetached"`
	RemoveOnStop          bool   `json:"removeOnStop"`
	Memory                string `json:"memory"`
	KernelArgs            string `json:"kernelArgs"`
}

type Features struct {
	Networks []string `json:"networks"`
	Ports    []string `json:"ports"`
	Volumes  []string `json:"volumes"`
	Targets  []string `json:"targets"`
	EnvVars  []string `json:"envVars"`
}

type Links struct {
	SoftLinks  []string `json:"softLinks"`
	HardLinks  []string `json:"hardLinks"`
	EventLinks []string `json:"eventLinks"`
}

type EventTrigger struct {
	Metadata LayerMetadata `json:"metadata"`
	Control  Control       `json:"control"`
	Features Features      `json:"features"`
	Links    Links         `json:"links"`
}

type Event struct {
	Metadata LayerMetadata `json:"metadata"`
	Control  Control       `json:"control"`
	Features Features      `json:"features"`
}
