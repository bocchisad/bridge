package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const AppName = "bridge"

type Config struct {
	DeviceName string `json:"device_name"`
	InboxDir   string `json:"inbox_dir"`
	RelayURL   string `json:"relay_url,omitempty"`
	RelayToken string `json:"relay_token,omitempty"`
	ListenPort int    `json:"listen_port"`
	APIPort    int    `json:"api_port"`
	AutoAccept bool   `json:"auto_accept"`
}

func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DeviceName: hostname(),
		InboxDir:   filepath.Join(home, "Downloads", AppName),
		ListenPort: 7331,
		APIPort:    7332,
		AutoAccept: false,
	}
}

func Dir() (string, error) {
	if override := os.Getenv("BRIDGE_CONFIG_DIR"); override != "" {
		if err := os.MkdirAll(override, 0o700); err != nil {
			return "", err
		}
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", AppName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Load() (Config, error) {
	cfg := Default()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, Save(cfg)
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if cfg.ListenPort == 0 {
		cfg.ListenPort = 7331
	}
	if cfg.APIPort == 0 {
		cfg.APIPort = 7332
	}
	if cfg.InboxDir == "" {
		home, _ := os.UserHomeDir()
		cfg.InboxDir = filepath.Join(home, "Downloads", AppName)
	}
	if cfg.DeviceName == "" {
		cfg.DeviceName = hostname()
	}
	return cfg, nil
}

func Save(cfg Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "bridge-device"
	}
	return h
}
