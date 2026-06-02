package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/arbazkhan971/bharatcode/internal/util/fsext"
)

// Save serialises cfg as indented JSON and writes it atomically to
// the file at scope. Sensitive fields (APIKeyEnv values, never the
// keys themselves) are preserved as `${ENV_NAME}` placeholders.
// Save creates the parent directory with 0o755 if missing. The
// file is written with 0o600 since it may contain machine-local
// paths.
func Save(ctx context.Context, cfg *Config, scope Scope) error {
	if cfg == nil {
		return fmt.Errorf("cannot save nil config")
	}

	var path string
	if scope == ScopeGlobal {
		path = GlobalPath()
	} else if scope == ScopeProject {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current working directory for project scope: %w", err)
		}
		path = ProjectPath(cwd)
		if path == "" {
			path = filepath.Join(cwd, ".bharatcode.json")
		}
	} else {
		return fmt.Errorf("invalid scope: %d", scope)
	}

	// Ensure parent directory exists with 0o755.
	dir := filepath.Dir(path)
	if err := fsext.EnsureDir(dir, 0o755); err != nil {
		return fmt.Errorf("ensuring parent directory exists for %s: %w", path, err)
	}

	// Serialize cfg to indented JSON.
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling configuration: %w", err)
	}
	// Append newline to end of file for cleanliness.
	data = append(data, '\n')

	// Write atomically with 0o600 permission.
	if err := fsext.AtomicWrite(path, data, 0o600); err != nil {
		return fmt.Errorf("atomic writing config to %s: %w", path, err)
	}

	return nil
}
