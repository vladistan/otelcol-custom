// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
)

type deviceKind int

const (
	kindSATA deviceKind = iota
	kindNVMe
)

// device is one disk the receiver may read, named as the kernel names it.
type device struct {
	name string
	kind deviceKind
}

// wholeDisk matches the names of whole SCSI-class disks: sd, hd, vd and xvd with letters only. A name
// with digits after the letters is a partition.
var wholeDisk = regexp.MustCompile(`^(sd|hd|vd|xvd)[a-z]+$`)

// discover lists the disks from /sys/block and /sys/class/nvme, given as a file system rooted at /sys.
// It keeps whole SATA-class disks and NVMe controllers, and skips partitions, loop, dm-*, md*, zd*, ram*,
// optical devices and the namespace devices of an NVMe controller. It sends nothing to any disk.
func discover(sysfs fs.FS) ([]device, error) {
	blocks, err := fs.ReadDir(sysfs, "block")
	if err != nil {
		return nil, fmt.Errorf("list /sys/block: %w", err)
	}
	var out []device
	for _, entry := range blocks {
		name := entry.Name()
		if !wholeDisk.MatchString(name) {
			continue
		}
		if _, err := fs.Stat(sysfs, "block/"+name+"/partition"); err == nil {
			continue
		}
		out = append(out, device{name: name, kind: kindSATA})
	}
	controllers, err := fs.ReadDir(sysfs, "class/nvme")
	if err == nil {
		for _, entry := range controllers {
			name := entry.Name()
			if isController(name) {
				out = append(out, device{name: name, kind: kindNVMe})
			}
		}
	}
	slices.SortFunc(out, func(a, b device) int { return strings.Compare(a.name, b.name) })
	return out, nil
}

var controllerName = regexp.MustCompile(`^nvme[0-9]+$`)

func isController(name string) bool { return controllerName.MatchString(name) }

// selector applies include and exclude.
type selector struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

func newSelector(include, exclude []string) (selector, error) {
	var s selector
	for _, expr := range include {
		re, err := regexp.Compile(expr)
		if err != nil {
			return selector{}, err
		}
		s.include = append(s.include, re)
	}
	for _, expr := range exclude {
		re, err := regexp.Compile(expr)
		if err != nil {
			return selector{}, err
		}
		s.exclude = append(s.exclude, re)
	}
	return s, nil
}

func (s selector) filter(devs []device) []device {
	out := []device{}
	for _, d := range devs {
		if len(s.include) > 0 && !matchesAny(s.include, d.name) {
			continue
		}
		if matchesAny(s.exclude, d.name) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func matchesAny(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
