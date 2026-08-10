package main

import (
	"encoding/json"
	"os"

	"github.com/c12s/runner/internal/models"
)

func main() {
	data, err := os.ReadFile("/app/data/starchart.json")
	if err != nil {
		panic(err)
	}

	var chart models.Chart

	err = json.Unmarshal(data, &chart)
	if err != nil {
		panic(err)
	}

	
}
