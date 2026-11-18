// Package config loads filter presets from built-in defaults, the pull
// request's repository and the user's configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/harnyk/gh-pyra/internal/filter"
)

// RepoPath is the configuration file path inside a repository.
const RepoPath = ".github/pyra.yml"

// Preset is a named list of glob patterns.
type Preset struct {
	Name     string
	Entries  []string
	Patterns []filter.Pattern
	// Source describes where the preset was defined.
	Source string
}

// Set maps preset names to presets.
type Set map[string]Preset

// Patterns returns the parsed patterns of every preset, keyed by name.
func (s Set) Patterns() map[string][]filter.Pattern {
	out := make(map[string][]filter.Pattern, len(s))
	for name, p := range s {
		out[name] = p.Patterns
	}
	return out
}

// Sorted returns presets ordered by name.
func (s Set) Sorted() []Preset {
	return slices.SortedFunc(maps.Values(s), func(a, b Preset) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		}
		return 0
	})
}

// Merge combines sets; presets in later sets override earlier ones.
func Merge(sets ...Set) Set {
	out := Set{}
	for _, s := range sets {
		maps.Copy(out, s)
	}
	return out
}

var builtinEntries = map[string][]string{
	"lockfiles": {
		"package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock",
		"bun.lock", "bun.lockb", "deno.lock", "go.sum", "go.work.sum", "Cargo.lock",
		"poetry.lock", "uv.lock", "Pipfile.lock", "pdm.lock", "Gemfile.lock",
		"composer.lock", "mix.lock", "pubspec.lock", "Podfile.lock", "flake.lock",
		"packages.lock.json", "gradle.lockfile",
	},
	"snapshots": {"*.snap", "**/__snapshots__/**"},
}

// Builtins returns the presets shipped with gh-pyra.
func Builtins() Set {
	out := Set{}
	for name, entries := range builtinEntries {
		patterns, err := filter.ParsePreset(entries)
		if err != nil {
			panic(fmt.Sprintf("invalid built-in preset %s: %v", name, err))
		}
		out[name] = Preset{Name: name, Entries: entries, Patterns: patterns, Source: "built-in"}
	}
	return out
}

type file struct {
	Presets map[string][]string `yaml:"presets"`
}

// Parse reads presets from YAML data:
//
//	presets:
//	  noise:
//	    - '**/*.snap'
//	    - '!src/keep.snap'
func Parse(data []byte, source string) (Set, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	out := Set{}
	for name, entries := range f.Presets {
		if !filter.ValidPresetName(name) {
			return nil, fmt.Errorf("%s: invalid preset name %q", source, name)
		}
		patterns, err := filter.ParsePreset(entries)
		if err != nil {
			return nil, fmt.Errorf("%s: preset %s: %w", source, name, err)
		}
		out[name] = Preset{Name: name, Entries: entries, Patterns: patterns, Source: source}
	}
	return out, nil
}

// UserPath returns the user configuration file path:
// $GH_PYRA_CONFIG, or <user config dir>/gh-pyra/config.yml.
func UserPath() (string, error) {
	if p := os.Getenv("GH_PYRA_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gh-pyra", "config.yml"), nil
}

// LoadUser reads presets from the user configuration file. A missing file
// yields an empty set.
func LoadUser() (Set, error) {
	p, err := UserPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Set{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data, p)
}
