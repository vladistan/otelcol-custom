// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
)

const (
	startAtEnd       = "end"
	startAtBeginning = "beginning"

	minBufferSize = 4096
	maxBufferSize = 1 << 20
)

var driveRoot = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// RootConfig is one watched root with its own service name and exclusions.
type RootConfig struct {
	// Path is absolute, with a drive letter or a UNC prefix.
	Path string `mapstructure:"path"`
	// Name is the service.name of the root's records; empty uses the lower-cased last path element.
	Name string `mapstructure:"name"`
	// Exclude replaces the default list when set, even when empty. Globs match the root-relative path.
	Exclude []string `mapstructure:"exclude"`
}

type Config struct {
	// Roots are the watched roots with a name and an exclude list each.
	Roots []RootConfig `mapstructure:"roots"`
	// Paths are roots without a name: absolute, with a drive letter or a UNC prefix. They share Exclude.
	Paths []string `mapstructure:"paths"`
	// Exclude holds doublestar globs matched case-insensitively against the root-relative path of each Paths root.
	Exclude []string `mapstructure:"exclude"`
	// StartAt applies to a root with no saved sentinel: end emits no history, beginning emits every file.
	StartAt string `mapstructure:"start_at"`
	// StorageID names the storage extension that keeps one sentinel per root; empty keeps it in memory.
	StorageID *component.ID `mapstructure:"storage"`
	// Settle is how long a size must stay unchanged before the write-complete record goes out; 0 turns it off.
	Settle time.Duration `mapstructure:"settle"`
	// SettleTimeout is how long a file is watched for growth; then the write-complete record goes out
	// with the last size seen as file.size.
	SettleTimeout  time.Duration `mapstructure:"settle_timeout"`
	MaxCatchup     time.Duration `mapstructure:"max_catchup"`
	BufferSize     int           `mapstructure:"buffer_size"`
	MaxRescans     int           `mapstructure:"max_rescans"`
	RescanInterval time.Duration `mapstructure:"rescan_interval"`
}

var _ component.Config = (*Config)(nil)

func (cfg *Config) Validate() error {
	var errs []error
	errs = append(errs, cfg.validatePaths()...)
	if cfg.Settle < 0 {
		errs = append(errs, errors.New("settle must not be negative"))
	}
	if cfg.SettleTimeout < cfg.Settle {
		errs = append(errs, fmt.Errorf("settle_timeout (%s) must be at least settle (%s)", cfg.SettleTimeout, cfg.Settle))
	}
	if cfg.StartAt != startAtEnd && cfg.StartAt != startAtBeginning {
		errs = append(errs, fmt.Errorf("start_at must be %q or %q, got %q", startAtEnd, startAtBeginning, cfg.StartAt))
	}
	if cfg.BufferSize < minBufferSize || cfg.BufferSize > maxBufferSize {
		errs = append(errs, fmt.Errorf("buffer_size must be between %d and %d bytes", minBufferSize, maxBufferSize))
	}
	if cfg.MaxRescans < 1 {
		errs = append(errs, errors.New("max_rescans must be at least 1"))
	}
	if cfg.MaxCatchup < 0 {
		errs = append(errs, errors.New("max_catchup must not be negative"))
	}
	if cfg.RescanInterval < 0 {
		errs = append(errs, errors.New("rescan_interval must not be negative"))
	}
	return errors.Join(append(errs, cfg.validateExclude()...)...)
}

// validateExclude checks the shared list once and each root's own list by its position and path.
func (cfg *Config) validateExclude() []error {
	var errs []error
	switch {
	case len(cfg.Paths) > 0:
		if _, err := newMatcher(cfg.Exclude); err != nil {
			errs = append(errs, fmt.Errorf("exclude: %w", err))
		}
	case cfg.Exclude != nil:
		errs = append(errs, errors.New("exclude applies to paths only: set exclude on each entry of roots"))
	}
	for i, r := range cfg.Roots {
		if _, err := newMatcher(r.patternsOrDefault()); err != nil {
			errs = append(errs, fmt.Errorf("roots[%d] %q: %w", i, r.Path, err))
		}
	}
	return errs
}

func (r RootConfig) patternsOrDefault() []string {
	if r.Exclude == nil {
		return defaultExclude
	}
	return r.Exclude
}

// defaultExclude lists the names of Google Drive temp files and Windows folder files; a root with
// no exclude list skips them at any depth.
var defaultExclude = []string{
	`**/.tmp.driveupload`, `**/.tmp.driveupload/**`,
	`**/.tmp.drivedownload`, `**/.tmp.drivedownload/**`,
	`**/desktop.ini`, `**/index.db-journal`,
}

// rootSpec is one root after the two config forms are merged.
type rootSpec struct {
	path    string
	name    string
	exclude []string // nil means the default list
}

func (s rootSpec) patterns() []string {
	if s.exclude == nil {
		return defaultExclude
	}
	return s.exclude
}

// rootSpecs lists the Paths roots, which share Exclude, and then the Roots.
func (cfg *Config) rootSpecs() []rootSpec {
	specs := make([]rootSpec, 0, len(cfg.Paths)+len(cfg.Roots))
	for _, p := range cfg.Paths {
		specs = append(specs, rootSpec{path: p, name: defaultRootName(p), exclude: cfg.Exclude})
	}
	for _, r := range cfg.Roots {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = defaultRootName(r.Path)
		}
		specs = append(specs, rootSpec{path: r.Path, name: name, exclude: r.Exclude})
	}
	return specs
}

// defaultRootName is the lower-cased last element of a root; a bare drive gives its letter.
func defaultRootName(root string) string {
	n := strings.TrimPrefix(strings.TrimPrefix(slashesToBackslashes(root), `\\?\UNC\`), `\\?\`)
	parts := strings.FieldsFunc(n, func(r rune) bool { return r == '\\' })
	if len(parts) == 0 {
		return typeStr
	}
	return strings.ToLower(strings.TrimSuffix(parts[len(parts)-1], ":"))
}

func (cfg *Config) validatePaths() []error {
	specs := cfg.rootSpecs()
	if len(specs) == 0 {
		return []error{errors.New("paths must hold at least one root (or set roots)")}
	}
	var errs []error
	normalizedRoots := make([]string, 0, len(specs))
	for _, spec := range specs {
		path := spec.path
		if !driveRoot.MatchString(path) && !isUNC(path) {
			errs = append(errs, fmt.Errorf("path %q must be absolute: a drive letter or a UNC prefix", path))
			continue
		}
		if strings.ContainsAny(path, "*?[{") {
			errs = append(errs, fmt.Errorf("path %q holds a glob character", path))
			continue
		}
		normalizedRoots = append(normalizedRoots, normalizeRoot(path))
	}
	for i, a := range normalizedRoots {
		for _, b := range normalizedRoots[i+1:] {
			switch {
			case a == b:
				errs = append(errs, fmt.Errorf("duplicate root %q", a))
			case strings.HasPrefix(b, a+`\`):
				errs = append(errs, fmt.Errorf("root %q is nested in root %q", b, a))
			case strings.HasPrefix(a, b+`\`):
				errs = append(errs, fmt.Errorf("root %q is nested in root %q", a, b))
			}
		}
	}
	return append(errs, duplicateNames(specs)...)
}

// duplicateNames reports roots whose service names are equal ignoring case: they would share one
// service.name and one counter series. A default name that collides needs an explicit name.
func duplicateNames(specs []rootSpec) []error {
	var errs []error
	first := map[string]string{}
	for _, spec := range specs {
		key := strings.ToLower(spec.name)
		if other, dup := first[key]; dup {
			errs = append(errs, fmt.Errorf("duplicate root name %q: roots %q and %q; set name on one of them", spec.name, other, spec.path))
			continue
		}
		first[key] = spec.path
	}
	return errs
}
