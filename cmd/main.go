package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/c12s/runner/internal/model"
	"github.com/c12s/runner/internal/orchestrator"
	"github.com/c12s/runner/internal/validation"
)

func main() {
	data, err := os.ReadFile("/app/data/starchart.json")
	if err != nil {
		panic(err)
	}

	var chart model.Chart

	if err = json.Unmarshal(data, &chart); err != nil {
		panic(err)
	}

	v := validation.New()
	o := orchestrator.New(v)

	if err = o.InstantiateChart(chart); err != nil {
		fmt.Print(err)
		return
	}
}
