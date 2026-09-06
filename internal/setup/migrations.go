package setup

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// WriteEmbeddedMigrations extracts bundled SQL migrations into dest.
func WriteEmbeddedMigrations(dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(migrationsFS, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := migrationsFS.ReadFile(path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, filepath.Base(path))
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}
