package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// LoadModels returns the user's curated model list. Missing file is not an
// error; the caller just gets an empty slice and can fall back to the live
// /api/tags listing.
func LoadModels(p Paths) ([]string, error) {
	f, err := os.Open(p.ModelsFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open models list: %w", err)
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read models list: %w", err)
	}
	return out, nil
}

// SaveModels writes one tag per line, sorted by insertion order, with 0o600
// perms.
func SaveModels(p Paths, models []string) error {
	if err := p.EnsureDirs(); err != nil {
		return err
	}
	var b strings.Builder
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		b.WriteString(m)
		b.WriteByte('\n')
	}
	tmp := p.ModelsFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write temp models list: %w", err)
	}
	if err := os.Rename(tmp, p.ModelsFile); err != nil {
		_ = os.Remove(p.ModelsFile)
		if err2 := os.Rename(tmp, p.ModelsFile); err2 != nil {
			return fmt.Errorf("rename models list: %w", err2)
		}
	}
	return nil
}

// SeedModelsIfMissing writes a starter models.list the first time the user
// runs the tool, so `localrouter list` has something to show before they
// curate it.
func SeedModelsIfMissing(p Paths) (bool, error) {
	if _, err := os.Stat(p.ModelsFile); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat models list: %w", err)
	}
	seed := []string{
		"qwen2.5-coder:7b",
		"llama3.1:8b",
		"mistral:7b",
	}
	if err := SaveModels(p, seed); err != nil {
		return false, err
	}
	return true, nil
}
