package admin

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllTemplatesCompile(t *testing.T) {
	set := NewTemplateSet()

	err := fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".html") {
			return nil
		}
		relName := filepath.Base(path)
		t.Run(relName, func(t *testing.T) {
			tpl, err := set.FromFile(relName)
			if err != nil {
				t.Fatalf("Failed to compile template %s: %v", relName, err)
			}
			if tpl == nil {
				t.Fatalf("Template %s compiled to nil", relName)
			}
		})
		return nil
	})

	if err != nil {
		t.Fatalf("WalkDir failed: %v", err)
	}
}
