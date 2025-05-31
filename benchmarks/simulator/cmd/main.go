package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/config"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/simulator"
)

func main() {
	var configPath string

	flag.StringVar(&configPath, "config", "configs/zipf.toml", "Path to configuration file")
	flag.Parse()

	if err := run(configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
	c, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := simulator.New(c)
	if err != nil {
		return fmt.Errorf("create simulator: %w", err)
	}

	if err := app.Simulate(); err != nil {
		return fmt.Errorf("simulate trace: %w", err)
	}

	return nil
}
