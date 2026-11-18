package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltins(t *testing.T) {
	b := Builtins()
	for _, name := range []string{"lockfiles", "snapshots"} {
		if _, ok := b[name]; !ok {
			t.Errorf("missing built-in preset %s", name)
		}
	}
	sel := func(name, path string) bool {
		for _, p := range b[name].Patterns {
			if p.Match(path) {
				return true
			}
		}
		return false
	}
	if !sel("lockfiles", "web/pnpm-lock.yaml") || !sel("lockfiles", "go.sum") || sel("lockfiles", "go.mod") {
		t.Error("lockfiles preset matches unexpectedly")
	}
	if !sel("snapshots", "a/__snapshots__/b.ts.snap") || !sel("snapshots", "x.snap") {
		t.Error("snapshots preset does not match")
	}
}

func TestParse(t *testing.T) {
	data := []byte(`
presets:
  noise:
    - '**/*.snap'
    - '!keep.snap'
  lockfiles:
    - custom.lock
`)
	s, err := Parse(data, "repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(s["noise"].Patterns) != 2 || !s["noise"].Patterns[1].Exclude || s["noise"].Source != "repo" {
		t.Errorf("unexpected noise preset: %+v", s["noise"])
	}

	merged := Merge(Builtins(), s)
	if got := merged["lockfiles"].Entries; len(got) != 1 || got[0] != "custom.lock" {
		t.Errorf("override failed: %v", got)
	}
	if _, ok := merged["snapshots"]; !ok {
		t.Error("built-in snapshots lost in merge")
	}

	if empty, err := Parse(nil, "empty"); err != nil || len(empty) != 0 {
		t.Errorf("empty config: %v %v", empty, err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"unknown key":  "preset:\n  a: [x]\n",
		"bad name":     "presets:\n  'a/b': [x]\n",
		"bad pattern":  "presets:\n  a: ['[x']\n",
		"nested":       "presets:\n  a: ['@b']\n",
		"not a list":   "presets:\n  a: x\n",
		"invalid yaml": "presets: [\n",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(data), "test"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	t.Setenv("GH_PYRA_CONFIG", path)

	s, err := LoadUser()
	if err != nil || len(s) != 0 {
		t.Fatalf("missing file: %v %v", s, err)
	}
	if err := os.WriteFile(path, []byte("presets:\n  mine: ['*.md']\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = LoadUser()
	if err != nil || s["mine"].Source != path {
		t.Fatalf("LoadUser: %v %v", s, err)
	}
}
