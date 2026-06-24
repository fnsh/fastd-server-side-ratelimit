package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Bandwith        BandwithConfig             `yaml:"bandwith"`
	ShaperScript    string                     `yaml:"shaper_script"`
	InterfacePrefix string                     `yaml:"interface_prefix,omitempty"`
	InterfaceSuffix string                     `yaml:"interface_suffix,omitempty"`
	TargetLimits    map[string]TargetRateLimit `yaml:"target_limits,omitempty"`
}

type BandwithConfig struct {
	Min RateLimitConfig `yaml:"min,omitempty"`
	Max RateLimitConfig `yaml:"max,omitempty"`
}

type RateLimitConfig struct {
	Download uint32 `yaml:"download"`
	Upload   uint32 `yaml:"upload"`
}

type TargetRateLimit struct {
	Target    string `yaml:"target"`
	Subtarget string `yaml:"subtarget,omitempty"`

	MinDownstreamRate uint32 `yaml:"min_downstream_rate,omitempty"`
	MaxDownstreamRate uint32 `yaml:"max_downstream_rate,omitempty"`
	MinUpstreamRate   uint32 `yaml:"min_upstream_rate,omitempty"`
	MaxUpstreamRate   uint32 `yaml:"max_upstream_rate,omitempty"`
}

func validate(cfg Config) error {
	if cfg.ShaperScript == "" {
		return fmt.Errorf("config.shaper_script must be set")
	}
	return nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func Parse(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config yaml: %w", err)
	}

	if cfg.Bandwith.Min.Download == 0 {
		return Config{}, fmt.Errorf("config.bandwith.min.download must be set and greater than zero")
	}
	if cfg.Bandwith.Min.Upload == 0 {
		return Config{}, fmt.Errorf("config.bandwith.min.upload must be set and greater than zero")
	}
	if cfg.ShaperScript == "" {
		return Config{}, fmt.Errorf("config.shaper_script must be set")
	}

	return cfg, nil
}
