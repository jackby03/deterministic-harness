package purity

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file is the boundary, made executable.
//
// The framework (`harness/`) must not know the name of the project it verifies.
// Every project noun — file names, paths, tool names, module paths — belongs to
// the profile (`gates.yaml`), to the binary wiring (`cmd/`), or to the repo's own
// governance (`constitution.md`, `AGENTS.md`). When a noun leaks into the library
// the framework stops being reusable *and* the profile stops being the single
// source of truth, in one move.
//
// The list below is not a style rule: each entry is a fact that must be declared
// in exactly one place, and this test names which place.

// projectNouns are strings that only exist because this repo exists.
var projectNouns = []string{
	"waffynx",
	"jackby03",
	"ngx_",
	"internal/engine",
	"internal/policy",
	"internal/appsec",
	"internal/parsers",
	"modules/",
	"deploy/",
	"configs/",
	"specs/",
	"constitution.md",
	"AGENTS.md",
	"FEATURE_MAP.md",
	".golangci.yml",
}

// ecosystemFiles are generic names any project of that ecosystem may have. They
// are allowed because they are convention, not identity.
var ecosystemFiles = map[string]string{
	"Makefile":          "generic build file",
	"third_party":       "vendored tree convention",
	"node_modules":      "JS convention",
	"testdata":          "Go convention",
	"vendor":            "Go convention",
	"dist":              "build output convention",
	"bin":               "build output convention",
	".artifacts":        "output convention used by this framework",
	".github/workflows": "CI convention",
}

// modulePath is this repository's own import prefix. It necessarily contains the
// owner's handle, so it is neutralised before scanning: a noun inside the module
// path is the repo's name, while the same noun inside a project path is a leak.
const modulePath = "github.com/jackby03/deterministic-harness"

// exemptFiles may carry project nouns because they are the wiring: the CLI of
// the project, not the library.
var exemptFiles = map[string]bool{
	"cmd/harness-facts/main.go":    true, // defaults live here, overridable
	"cmd/harness-verify/main.go":   true,
	"cmd/harness-validate/main.go": true,
}

func TestFrameworkHasNoProjectNouns(t *testing.T) {
	root := findModuleRoot(t)

	var offenders []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if exemptFiles[rel] {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		clean := strings.ReplaceAll(string(body), modulePath, "MODULE")
		lower := strings.ToLower(clean)
		for _, noun := range projectNouns {
			if ecosystemFiles[noun] != "" {
				continue
			}
			if strings.Contains(lower, strings.ToLower(noun)) {
				offenders = append(offenders, rel+": "+noun)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(offenders) > 0 {
		t.Fatalf("el framework nombra al proyecto; movelo al perfil o a cmd/:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// TestProfileCarriesTheProjectNouns is the other half: the nouns must appear
// somewhere, and that somewhere is the profile. If this fails, the boundary moved
// in the wrong direction — the framework forgot what the project is.
func TestProfileCarriesTheProjectNouns(t *testing.T) {
	root := findModuleRoot(t)

	// The nouns must live in *a* profile, and the canonical specimen is the
	// example that ships with the repo. When the harness runs from a project
	// checkout, that project's profile is the specimen instead.
	candidates := []string{
		filepath.Join(root, "examples", "waffynx", "gates.yaml"),
		filepath.Join(filepath.Dir(root), "gates.yaml"),
	}
	var text string
	for _, c := range candidates {
		if body, err := os.ReadFile(c); err == nil {
			text = string(body)
			break
		}
	}
	if text == "" {
		t.Skip("no specimen profile found")
	}

	for _, want := range []string{"internal/engine", "modules/ngx_waffynx", "constitution.md"} {
		if !strings.Contains(text, want) {
			t.Errorf("el perfil no declara %q: el proyecto perdió un hecho", want)
		}
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
