package filter

import "testing"

func mustSelect(t *testing.T, args, excludes []string, wildcard bool, presets map[string][]Pattern) Selector {
	t.Helper()
	spec, err := Parse(args, excludes, wildcard)
	if err != nil {
		t.Fatalf("Parse(%q, %q): %v", args, excludes, err)
	}
	sel, err := spec.Resolve(presets)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return sel
}

func TestSelect(t *testing.T) {
	noise, err := ParsePreset([]string{"*.snap", "pnpm-lock.yaml", "!keep.snap"})
	if err != nil {
		t.Fatal(err)
	}
	presets := map[string][]Pattern{"noise": noise}

	cases := []struct {
		name     string
		args     []string
		excludes []string
		wildcard bool
		path     string
		want     bool
	}{
		{"substring", []string{".spec."}, nil, false, "src/a.spec.ts", true},
		{"substring miss", []string{".spec."}, nil, false, "src/xspecy.ts", false},
		{"literal star", []string{"*.ts"}, nil, false, "src/a.ts", false},
		{"any of several", []string{"nope", "lock"}, nil, false, "pnpm-lock.yaml", true},

		{"basename glob in subdir", []string{"*.spec.ts"}, nil, true, "src/deep/a.spec.ts", true},
		{"path glob no cross dir", []string{"src/*.ts"}, nil, true, "src/deep/a.ts", false},
		{"doublestar", []string{"src/**/*.ts"}, nil, true, "src/deep/er/a.ts", true},
		{"anchored to full path", []string{"generated/**"}, nil, true, "pkg/generated/y.go", false},
		{"braces", []string{"*.{yaml,yml}"}, nil, true, "ci/a.yml", true},
		{"wildcard is not substring", []string{"spec"}, nil, true, "a.spec.ts", false},

		{"exclude wins", []string{"src/", "!src/core/"}, nil, false, "src/core/a.go", false},
		{"exclude other", []string{"src/", "!src/core/"}, nil, false, "src/ui/a.go", true},
		{"only excludes select rest", []string{"!src/"}, nil, false, "README.md", true},
		{"only excludes drop match", []string{"!src/"}, nil, false, "src/a.go", false},
		{"exclude flag", nil, []string{"src/"}, false, "src/a.go", false},
		{"exclude flag keeps rest", nil, []string{"src/"}, false, "docs/a.md", true},
		{"exclude flag is literal", nil, []string{"!a"}, false, "a.go", true},
		{"glob exclude", []string{"!**/*_test.go"}, nil, true, "pkg/x_test.go", false},
		{"escaped bang", []string{`\!important`}, nil, false, "docs/!important.md", true},
		{"escaped at", []string{`\@types`}, nil, false, "node_modules/@types/x.d.ts", true},

		{"preset", []string{"@noise"}, nil, false, "ui/__snapshots__/a.snap", true},
		{"preset miss", []string{"@noise"}, nil, false, "ui/a.ts", false},
		{"preset exclude", []string{"@noise"}, nil, false, "keep.snap", false},
		{"preset is glob without -w", []string{"@noise"}, nil, false, "x.snap", true},
		{"preset plus filter", []string{"@noise", "README"}, nil, false, "README.md", true},
		{"preset with exclude flag", []string{"@noise"}, []string{"ui/"}, false, "ui/a.snap", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel := mustSelect(t, tc.args, tc.excludes, tc.wildcard, presets)
			if got := sel.Select(tc.path); got != tc.want {
				t.Errorf("Select(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestEmptyPresetSelectsNothing(t *testing.T) {
	sel := mustSelect(t, []string{"@empty"}, nil, false, map[string][]Pattern{"empty": nil})
	if sel.Select("anything") {
		t.Fatal("empty preset must not select every file")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		excludes []string
		wildcard bool
	}{
		{"empty filter", []string{""}, nil, false},
		{"empty exclude", []string{"!"}, nil, false},
		{"empty exclude flag", nil, []string{""}, false},
		{"bad glob", []string{"[a"}, nil, true},
		{"bad glob exclude", []string{"![a"}, nil, true},
		{"bad glob exclude flag", nil, []string{"[a"}, true},
		{"empty preset name", []string{"@"}, nil, false},
		{"bad preset name", []string{"@a/b"}, nil, false},
		{"negated preset", []string{"!@noise"}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.args, tc.excludes, tc.wildcard); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := Parse([]string{"[a"}, nil, false); err != nil {
		t.Errorf("bracket must be literal without wildcard: %v", err)
	}
}

func TestResolveUnknownPreset(t *testing.T) {
	spec, err := Parse([]string{"@nope"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spec.Resolve(nil); err == nil {
		t.Fatal("expected unknown preset error")
	}
}

func TestParsePresetErrors(t *testing.T) {
	for _, entries := range [][]string{{"@other"}, {"!@other"}, {""}, {"[a"}} {
		if _, err := ParsePreset(entries); err == nil {
			t.Errorf("ParsePreset(%q) succeeded", entries)
		}
	}
}
