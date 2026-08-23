package model

type Package struct {
	Index    string `json:"index"`
	Manifest string `json:"manifest"`
	Plat     string `json:"plat"`
	Version  string `json:"version"`
}
