package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/parser"
	"github.com/maypok86/otter/v2/benchmarks/simulator/internal/trace/generator"
)

type Config struct {
	Type       string   `toml:"type"`
	Name       string   `toml:"name"`
	Capacities []uint   `toml:"capacities"`
	Caches     []string `toml:"caches"`
	Limit      *uint    `toml:"limit"`

	Zipf *Zipf `toml:"zipf"`
	File *File `toml:"file"`
}

func (c *Config) validate() error {
	if c.Type != generator.ZipfType && c.Type != generator.FileType {
		return errors.New("not valid trace type")
	}

	if c.Name == "" {
		return errors.New("name is empty")
	}

	if len(c.Capacities) == 0 {
		return errors.New("capacities is empty")
	}

	if len(c.Caches) == 0 {
		return errors.New("caches is empty")
	}

	if c.Type == generator.ZipfType {
		if c.Limit == nil {
			return errors.New("unbounded zipf trace")
		}

		if c.Zipf == nil {
			return errors.New("not found parameters for zipf trace")
		}

		if c.File != nil {
			return errors.New("found parameters for trace from file, although the config is specified as zipf")
		}
	}

	if c.Type == generator.FileType {
		if c.Zipf != nil {
			return errors.New("found parameters for zipf trace, although the config is specified as file")
		}

		if c.File == nil {
			return errors.New("not found parameters for trace from file")
		}
	}

	if err := c.Zipf.validate(); err != nil {
		return err
	}

	return c.File.validate()
}

type Zipf struct {
	S    float64 `toml:"s"`
	V    float64 `toml:"v"`
	IMAX uint64  `toml:"imax"`
}

func (z *Zipf) validate() error {
	if z == nil {
		return nil
	}

	if z.S <= 1 {
		return errors.New("not valid s parameter for zipf generator. S should be > 1")
	}

	if z.V < 1 {
		return errors.New("not valid v parameter for zipf generator. V should be <= 1")
	}

	return nil
}

type FilePath struct {
	TraceType string `toml:"trace_type"`
	Path      string `toml:"path"`
}

func (fp *FilePath) validate() error {
	if fp == nil {
		return nil
	}

	if !parser.IsAvailableFormat(fp.TraceType) {
		return errors.New("not valid trace type")
	}

	return nil
}

type File struct {
	Paths []FilePath `toml:"paths"`
}

func (f *File) validate() error {
	if f == nil {
		return nil
	}

	if len(f.Paths) == 0 {
		return errors.New("paths is empty")
	}

	for _, p := range f.Paths {
		if err := p.validate(); err != nil {
			return err
		}
	}

	return nil
}

func Load(configPath string) (Config, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var c Config
	if err := toml.Unmarshal(content, &c); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return c, nil
}
