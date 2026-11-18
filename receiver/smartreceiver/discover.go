// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
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
	// nsBytes is the size of the NVMe namespaces from sysfs, used when the controller reports no capacity.
	nsBytes uint64
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
	var problems []error
	controllers, err := fs.ReadDir(sysfs, "class/nvme")
	switch {
	case err == nil:
		for _, entry := range controllers {
			name := entry.Name()
			if !isController(name) {
				continue
			}
			size, sizeErr := namespaceBytes(sysfs, name)
			if sizeErr != nil {
				problems = append(problems, sizeErr)
			}
			out = append(out, device{name: name, kind: kindNVMe, nsBytes: size})
		}
	case !errors.Is(err, fs.ErrNotExist):
		problems = append(problems, fmt.Errorf("list /sys/class/nvme: %w", err))
	}
	slices.SortFunc(out, func(a, b device) int { return strings.Compare(a.name, b.name) })
	if len(problems) > 0 {
		return out, fmt.Errorf("%w: %w", errPartialList, errors.Join(problems...))
	}
	return out, nil
}

// errPartialList wraps the cause when discover returns the disks it found but could not list all of them
// or read all of their sizes. A host without NVMe is not such a case.
var errPartialList = errors.New("the disk list is incomplete")

// sectorBytes is the unit of the size file of a block device.
const sectorBytes = 512

// namespaceBytes adds up the namespaces of one NVMe controller. A path of a multipath namespace is
// not counted. A namespace whose size does not read or parse adds nothing and is named in the error.
func namespaceBytes(sysfs fs.FS, ctrl string) (uint64, error) {
	entries, err := fs.ReadDir(sysfs, "class/nvme/"+ctrl)
	if err != nil {
		return 0, fmt.Errorf("list the namespaces of %s: %w", ctrl, err)
	}
	var total uint64
	var problems []error
	for _, entry := range entries {
		if !namespaceName.MatchString(entry.Name()) {
			continue
		}
		raw, err := fs.ReadFile(sysfs, "class/nvme/"+ctrl+"/"+entry.Name()+"/size")
		if err != nil {
			problems = append(problems, fmt.Errorf("read the size of %s: %w", entry.Name(), err))
			continue
		}
		sectors, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || sectors > ^uint64(0)/sectorBytes-total/sectorBytes {
			problems = append(problems, fmt.Errorf("the size of %s is not a usable number: %q", entry.Name(), strings.TrimSpace(string(raw))))
			continue
		}
		total += sectors * sectorBytes
	}
	return total, errors.Join(problems...)
}

var namespaceName = regexp.MustCompile(`^nvme[0-9]+n[0-9]+$`)

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
			return selector{}, fmt.Errorf("include pattern %q: %w", expr, err)
		}
		s.include = append(s.include, re)
	}
	for _, expr := range exclude {
		re, err := regexp.Compile(expr)
		if err != nil {
			return selector{}, fmt.Errorf("exclude pattern %q: %w", expr, err)
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
