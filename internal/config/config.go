// Package config holds the persisted user settings for localrouter and the
// OS-agnostic on-disk layout (config + cache + state directories).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const AppName = "localrouter"

// Config is the user-editable shape persisted to config.json.
//
// Defaults are applied by Load() so existing files do not need to grow new
// fields when we add them — they simply pick up the zero value and the
// default takes over.
type Config struct {
	// ListenAddr is where the local proxy binds, e.g. "localhost:11434".
	ListenAddr string `json:"listen_addr"`

	// RemoteURL is the upstream Ollama base URL, e.g. "http://192.168.1.50:11434".
	RemoteURL string `json:"remote_url"`

	// AutoPull controls whether missing models are pulled on the remote on
	// demand when an intercepted /api/chat or /api/generate request comes
	// in.
	AutoPull bool `json:"auto_pull"`

	// PullTimeoutSecs caps each /api/pull request. Default 1800 (30 min).
	PullTimeoutSecs int `json:"pull_timeout_secs"`

	// ReachTimeoutMs caps the TCP liveness probe used by `status` and
	// pre-flight checks. Default 1500.
	ReachTimeoutMs int `json:"reach_timeout_ms"`

	// DefaultModel is the tag preferred by `localrouter use` when no model
	// is given on the command line. Empty means "no default".
	DefaultModel string `json:"default_model"`
}

// Defaults returns the built-in defaults used when the config file is absent
// or a field is the zero value.
func Defaults() Config {
	return Config{
		ListenAddr:      "localhost:11434",
		RemoteURL:       "",
		AutoPull:        true,
		PullTimeoutSecs: 1800,
		ReachTimeoutMs:  1500,
		DefaultModel:    "",
	}
}

// Paths bundles the resolved on-disk locations for the running user.
type Paths struct {
	ConfigDir  string // os.UserConfigDir/localrouter
	CacheDir   string // os.UserCacheDir/localrouter
	ConfigFile string // ConfigDir/config.json
	ModelsFile string // ConfigDir/models.list
	StateFile  string // CacheDir/state.json   (tracks last-used model, pid, etc.)
	PIDFile    string // CacheDir/localrouter.pid
	LogFile    string // CacheDir/localrouter.log
}

// ResolvePaths returns the cross-platform on-disk layout. It does not create
// any directories.
func ResolvePaths() (Paths, error) {
	cfgRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user config dir: %w", err)
	}
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user cache dir: %w", err)
	}
	cfgDir := filepath.Join(cfgRoot, AppName)
	cacheDir := filepath.Join(cacheRoot, AppName)
	return Paths{
		ConfigDir:  cfgDir,
		CacheDir:   cacheDir,
		ConfigFile: filepath.Join(cfgDir, "config.json"),
		ModelsFile: filepath.Join(cfgDir, "models.list"),
		StateFile:  filepath.Join(cacheDir, "state.json"),
		PIDFile:    filepath.Join(cacheDir, "localrouter.pid"),
		LogFile:    filepath.Join(cacheDir, "localrouter.log"),
	}, nil
}

// EnsureDirs creates the config + cache directories with 0o700 perms (best
// effort on Windows, where ACLs do the actual work).
func (p Paths) EnsureDirs() error {
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.MkdirAll(p.CacheDir, 0o700); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	return nil
}

// LoadFile reads config.json without applying environment overrides. Use
// this when you intend to modify and Save the config back — it would be
// surprising if a transient env var leaked into the persisted file.
//
// A missing file returns Defaults() with no error so callers can treat
// first-run as normal.
func LoadFile(p Paths) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(p.ConfigFile)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// First run: leave defaults in place.
	case err != nil:
		return cfg, fmt.Errorf("read config: %w", err)
	default:
		var loaded Config
		if err := json.Unmarshal(data, &loaded); err != nil {
			return cfg, fmt.Errorf("parse config: %w", err)
		}
		cfg = merge(cfg, loaded)
	}
	return cfg, nil
}

// Load is LoadFile plus environment-variable overrides:
//
//	LOCALROUTER_LISTEN, LOCALROUTER_REMOTE, LOCALROUTER_AUTO_PULL,
//	LOCALROUTER_DEFAULT_MODEL, LOCALROUTER_PULL_TIMEOUT, LOCALROUTER_REACH_TIMEOUT_MS
func Load(p Paths) (Config, error) {
	cfg, err := LoadFile(p)
	if err != nil {
		return cfg, err
	}
	applyEnv(&cfg)
	return cfg, nil
}

// Save writes the config atomically (write to temp + rename).
func Save(p Paths, cfg Config) error {
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	tmp := p.ConfigFile + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, p.ConfigFile); err != nil {
		// Windows can refuse rename-over-existing in some FS configs; fall
		// back to remove + rename so first-class atomicity is not lost on
		// POSIX but the call still succeeds elsewhere.
		_ = os.Remove(p.ConfigFile)
		if err2 := os.Rename(tmp, p.ConfigFile); err2 != nil {
			return fmt.Errorf("rename config: %w", err2)
		}
	}
	return nil
}

// PullTimeout returns the configured pull timeout as a Duration.
func (c Config) PullTimeout() time.Duration {
	if c.PullTimeoutSecs <= 0 {
		return time.Duration(Defaults().PullTimeoutSecs) * time.Second
	}
	return time.Duration(c.PullTimeoutSecs) * time.Second
}

// ReachTimeout returns the configured probe timeout.
func (c Config) ReachTimeout() time.Duration {
	if c.ReachTimeoutMs <= 0 {
		return time.Duration(Defaults().ReachTimeoutMs) * time.Millisecond
	}
	return time.Duration(c.ReachTimeoutMs) * time.Millisecond
}

func merge(base, in Config) Config {
	out := base
	if in.ListenAddr != "" {
		out.ListenAddr = in.ListenAddr
	}
	if in.RemoteURL != "" {
		out.RemoteURL = in.RemoteURL
	}
	if in.PullTimeoutSecs != 0 {
		out.PullTimeoutSecs = in.PullTimeoutSecs
	}
	if in.ReachTimeoutMs != 0 {
		out.ReachTimeoutMs = in.ReachTimeoutMs
	}
	if in.DefaultModel != "" {
		out.DefaultModel = in.DefaultModel
	}
	// AutoPull is bool so we can't tell "not set" from "set to false" via
	// the zero value. We accept that and treat the loaded value as
	// authoritative.
	out.AutoPull = in.AutoPull
	return out
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("LOCALROUTER_LISTEN"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("LOCALROUTER_REMOTE"); v != "" {
		cfg.RemoteURL = v
	}
	if v := os.Getenv("LOCALROUTER_DEFAULT_MODEL"); v != "" {
		cfg.DefaultModel = v
	}
	if v := os.Getenv("LOCALROUTER_AUTO_PULL"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.AutoPull = b
		}
	}
	if v := os.Getenv("LOCALROUTER_PULL_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PullTimeoutSecs = n
		}
	}
	if v := os.Getenv("LOCALROUTER_REACH_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.ReachTimeoutMs = n
		}
	}
}
