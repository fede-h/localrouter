package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// State is the small JSON blob that survives across runs to give status /
// restart / use the same continuity the original Bash tool had.
type State struct {
	TrackedModel string    `json:"tracked_model"`
	LastUseAt    time.Time `json:"last_use_at"`
	LastPullAt   time.Time `json:"last_pull_at"`
	LastStartAt  time.Time `json:"last_start_at"`
}

// LoadState returns an empty State if the file is missing.
func LoadState(p Paths) (State, error) {
	var s State
	data, err := os.ReadFile(p.StateFile)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read state: %w", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse state: %w", err)
	}
	return s, nil
}

// SaveState writes the state atomically.
func SaveState(p Paths, s State) error {
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := p.StateFile + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := os.Rename(tmp, p.StateFile); err != nil {
		_ = os.Remove(p.StateFile)
		if err2 := os.Rename(tmp, p.StateFile); err2 != nil {
			return fmt.Errorf("rename state: %w", err2)
		}
	}
	return nil
}
