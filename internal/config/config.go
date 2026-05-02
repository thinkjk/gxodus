package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type NotifyConfig struct {
	OnAuthExpired   string `toml:"on_auth_expired"`
	OnExportStarted string `toml:"on_export_started"`
	OnExportComplete string `toml:"on_export_complete"`
	OnError         string `toml:"on_error"`
}

type Config struct {
	OutputDir    string       `toml:"output_dir"`
	PollInterval string       `toml:"poll_interval"`
	Extract      bool         `toml:"extract"`
	KeepZip      bool         `toml:"keep_zip"`
	FileSize     string       `toml:"file_size"`
	FileType     string       `toml:"file_type"`
	Frequency    string       `toml:"frequency"`
	ActivityLogs bool         `toml:"activity_logs"`
	Notify       NotifyConfig `toml:"notify"`
}

func DefaultConfig() *Config {
	return &Config{
		OutputDir:    filepath.Join(homeDir(), "gxodus-exports"),
		PollInterval: "5m",
		Extract:      false,
		KeepZip:      true,
		FileSize:     "2GB",
		FileType:     "zip",
		Frequency:    "once",
		ActivityLogs: true,
	}
}

func ConfigDir() string {
	if dir := os.Getenv("GXODUS_CONFIG_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "gxodus")
	}
	return filepath.Join(homeDir(), ".config", "gxodus")
}

func DefaultConfigPath() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

func EnsureConfigDir() error {
	return os.MkdirAll(ConfigDir(), 0700)
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		path = DefaultConfigPath()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return cfg, nil
}

func (c *Config) PollDuration() (time.Duration, error) {
	return time.ParseDuration(c.PollInterval)
}

func (c *Config) ResolveOutputDir() string {
	if filepath.IsAbs(c.OutputDir) {
		return c.OutputDir
	}
	dir := c.OutputDir
	if len(dir) > 1 && dir[:2] == "~/" {
		dir = filepath.Join(homeDir(), dir[2:])
	}
	return dir
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
