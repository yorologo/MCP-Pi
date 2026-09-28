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

func TestTargetPrivilegeScopeValueIsEscapedExactlyOnce(t *testing.T) {
	body, err := fs.ReadFile(templatesFS, "templates/target_form.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	if strings.Contains(src, "scope.value|e") {
		t.Fatal("privilege scope value must rely on pongo2 autoescape; explicit escape double-encodes JSON")
	}
	if !strings.Contains(src, `value="{{ scope.value }}"`) {
		t.Fatal("privilege scope option must render the JSON value through normal autoescape")
	}
}
