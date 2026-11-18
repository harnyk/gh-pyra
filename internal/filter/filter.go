// Package filter parses file filters and selects pull request files with them.
//
// A filter is one of:
//
//	PATTERN    include files matching PATTERN
//	!PATTERN   exclude files matching PATTERN
//	@NAME      include the patterns of preset NAME
//
// A leading backslash ("\!", "\@") makes the next character literal.
// PATTERN is a literal substring of the path, or a glob pattern when the
// filter is parsed in wildcard mode. Preset patterns are always globs.
package filter

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Pattern is a single include or exclude rule.
type Pattern struct {
	Text    string
	Glob    bool
	Exclude bool
}

// Match reports whether the pattern matches the file path (ignoring Exclude).
//
// A glob without "/" is also matched against the file's base name, so
// "*.spec.ts" selects such files in any directory.
func (p Pattern) Match(filePath string) bool {
	if !p.Glob {
		return strings.Contains(filePath, p.Text)
	}
	if doublestar.MatchUnvalidated(p.Text, filePath) {
		return true
	}
	return !strings.Contains(p.Text, "/") && doublestar.MatchUnvalidated(p.Text, path.Base(filePath))
}

// Spec is a parsed set of filters, before presets are expanded.
type Spec struct {
	Patterns []Pattern
	// Presets lists referenced preset names in order of appearance.
	Presets []string
}

// HasIncludes reports whether the spec restricts files with include rules.
// A spec with only excludes (or nothing) selects every file not excluded.
func (s Spec) HasIncludes() bool {
	if len(s.Presets) > 0 {
		return true
	}
	for _, p := range s.Patterns {
		if !p.Exclude {
			return true
		}
	}
	return false
}

// IsEmpty reports whether the spec has no rules at all.
func (s Spec) IsEmpty() bool {
	return len(s.Patterns) == 0 && len(s.Presets) == 0
}

var presetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// ValidPresetName reports whether name can be used as a preset name.
func ValidPresetName(name string) bool {
	return presetName.MatchString(name)
}

// Parse parses command-line filters. Each entry of excludes is an extra
// exclude pattern taken literally (no "!" or "@" prefix handling).
func Parse(args, excludes []string, wildcard bool) (Spec, error) {
	var s Spec
	for _, arg := range args {
		if name, ok := strings.CutPrefix(arg, "@"); ok {
			if !ValidPresetName(name) {
				return Spec{}, fmt.Errorf("invalid preset reference %q", arg)
			}
			s.Presets = append(s.Presets, name)
			continue
		}
		if strings.HasPrefix(arg, "!@") {
			return Spec{}, fmt.Errorf("presets cannot be excluded: %q", arg)
		}
		p, err := parsePattern(arg, wildcard)
		if err != nil {
			return Spec{}, err
		}
		s.Patterns = append(s.Patterns, p)
	}
	for _, x := range excludes {
		p, err := newPattern(x, wildcard, true)
		if err != nil {
			return Spec{}, err
		}
		s.Patterns = append(s.Patterns, p)
	}
	return s, nil
}

// ParsePreset parses the patterns of a preset. They are always globs and
// may use "!" excludes, but cannot reference other presets.
func ParsePreset(entries []string) ([]Pattern, error) {
	var out []Pattern
	for _, e := range entries {
		if strings.HasPrefix(e, "@") || strings.HasPrefix(e, "!@") {
			return nil, fmt.Errorf("presets cannot reference other presets: %q", e)
		}
		p, err := parsePattern(e, true)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func parsePattern(arg string, glob bool) (Pattern, error) {
	exclude := false
	text := arg
	switch {
	case strings.HasPrefix(arg, `\!`), strings.HasPrefix(arg, `\@`):
		text = arg[1:]
	case strings.HasPrefix(arg, "!"):
		exclude = true
		text = arg[1:]
	}
	return newPattern(text, glob, exclude)
}

func newPattern(text string, glob, exclude bool) (Pattern, error) {
	if text == "" {
		if exclude {
			return Pattern{}, errors.New("exclude pattern must not be empty")
		}
		return Pattern{}, errors.New("filter must not be empty")
	}
	if glob && !doublestar.ValidatePattern(text) {
		return Pattern{}, fmt.Errorf("invalid wildcard pattern %q", text)
	}
	return Pattern{Text: text, Glob: glob, Exclude: exclude}, nil
}

// Selector decides which files are selected: a file is selected when it
// matches any include (or there are no include rules) and no exclude.
type Selector struct {
	includeAll bool
	includes   []Pattern
	excludes   []Pattern
}

// Resolve expands presets and builds a Selector. presets maps preset names to
// their parsed patterns.
func (s Spec) Resolve(presets map[string][]Pattern) (Selector, error) {
	sel := Selector{includeAll: !s.HasIncludes()}
	add := func(p Pattern) {
		if p.Exclude {
			sel.excludes = append(sel.excludes, p)
		} else {
			sel.includes = append(sel.includes, p)
		}
	}
	for _, p := range s.Patterns {
		add(p)
	}
	for _, name := range s.Presets {
		patterns, ok := presets[name]
		if !ok {
			return Selector{}, fmt.Errorf("unknown preset @%s", name)
		}
		for _, p := range patterns {
			add(p)
		}
	}
	return sel, nil
}

// Select reports whether the file path is selected.
func (s Selector) Select(filePath string) bool {
	for _, p := range s.excludes {
		if p.Match(filePath) {
			return false
		}
	}
	if s.includeAll {
		return true
	}
	for _, p := range s.includes {
		if p.Match(filePath) {
			return true
		}
	}
	return false
}
