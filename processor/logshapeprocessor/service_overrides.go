// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// serviceOverrides renames the service a branch derived. It is one shared step so no branch
// reads the setting. Only a non-empty resource service.name found in the map changes; host.name
// and unlisted names stay.
type serviceOverrides struct {
	branch
	overrides map[string]string
}

// withServiceOverrides returns b unchanged when there is nothing to rename.
func withServiceOverrides(b branch, overrides map[string]string) branch {
	if len(overrides) == 0 {
		return b
	}
	return &serviceOverrides{branch: b, overrides: overrides}
}

func (s *serviceOverrides) parse(r *rec) error {
	if err := s.branch.parse(r); err != nil {
		return err
	}
	attrs := r.res.Attributes()
	if v, ok := attrs.Get("service.name"); ok && v.Str() != "" {
		if to, found := s.overrides[v.Str()]; found {
			attrs.PutStr("service.name", to)
		}
	}
	return nil
}
