package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// DefaultPath returns the conventional config path:
// $XDG_CONFIG_HOME/pwled/config.toml, falling back to ~/.config/pwled/config.toml.
func DefaultPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "pwled", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".config", "pwled", "config.toml"), nil
}

// LoadOrCreate reads the TOML config at path. If the file does not exist, the
// parent directory is created (mode 0755) and a default config is written.
// Either way the returned Config is validated before return.
func LoadOrCreate(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		def := Default()
		if err := Save(path, def); err != nil {
			return Config{}, fmt.Errorf("seed default config at %s: %w", path, err)
		}
		return def, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	// Start from defaults so omitted fields keep sensible values.
	cfg := Default()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	migrate(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return cfg, nil
}

// migrate fills in fields added in later versions for configs written by
// earlier versions. The TOML library replaces the whole `presets` array on
// unmarshal (rather than merging element-by-element with the defaults), so
// presets stored without newer fields would otherwise fail validation.
//
// Heuristic: a preset whose Activity block has TimeoutSeconds == 0 is
// definitely missing the section (0 is outside the valid range), so we copy
// in the default Activity values.
func migrate(c *Config) {
	def := DefaultDSP()
	for i := range c.Presets {
		if c.Presets[i].Settings.Activity.TimeoutSeconds == 0 {
			c.Presets[i].Settings.Activity = def.Activity
		}
	}
}

// Save writes cfg to path atomically (write-temp + rename). The parent
// directory is created if it doesn't exist. The config must be valid; callers
// who want lenient writes should call Validate themselves first.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config.toml.*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
