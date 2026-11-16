// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"go.uber.org/zap"
)

// lunSource reads the LUNs of the iSCSI sessions once; tests replace it.
type lunSource interface {
	luns() ([]lun, error)
}

// lun is one logical unit of one session, seen as a SCSI device. state is the SCSI device state
// ("unknown" when it cannot be told), device the block device of this path, holder the
// first device-mapper device that sits on top of it, if any, and holders how many there are.
// err says what could not be read.
type lun struct {
	session   int
	id        int
	device    string
	holder    string
	holders   int
	state     string
	sizeBytes uint64
	hasSize   bool
	err       error
}

const (
	classSCSIDevice = "scsi_device"
	stateUnknown    = "unknown"
	sectorBytes     = 512
	scsiTypeDisk    = 0
	scsiTypeRBC     = 14 // simplified direct-access device
)

// The files and directories the LUN reader opens, below scsi_device/<H:C:T:L>/device.
const (
	fileType   = "type"
	fileSize   = "size"
	dirBlock   = "block"
	dirHolders = "holders"
)

var (
	scsiName = regexp.MustCompile(`^\d+:\d+:\d+:\d+$`)
	// lunLink picks the session and the LUN out of the link of a scsi_device entry. A local disk
	// has no session in its path and does not match.
	lunLink = regexp.MustCompile(`/session(\d+)/target\d+:\d+:\d+/\d+:\d+:\d+:(\d+)/scsi_device/`)

	errNoBlockDevice = errors.New("the SCSI device has no block device")
)

// readOutcome says what readLUN made of a device directory.
type readOutcome int

const (
	lunRead     readOutcome = iota // a disk: the LUN is returned, with its error if it was not read completely
	lunNotDisk                     // another SCSI device type: left out
	lunVanished                    // the device went away while it was read: left out
)

// vanished reports an error that means the device is gone, not unreadable.
func vanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ESTALE)
}

// luns lists the iSCSI LUNs of the host in session and LUN order. Entries that are not iSCSI
// devices or not disks are left out, and so is a device that vanishes while it is read (one info
// line for each such device until it is back); a LUN that cannot be read completely is returned
// with its error and the state "unknown".
func (r *sysfsReader) luns() ([]lun, error) {
	base := filepath.Join(r.root, classSCSIDevice)
	entries, err := r.readDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list the SCSI devices: %w", err)
	}
	var out []lun
	gone := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !scsiName.MatchString(name) {
			continue
		}
		link, err := r.readlink(filepath.Join(base, name))
		if err != nil {
			r.logger.Debug("Cannot follow a SCSI device link", zap.String("device", name), zap.Error(err))
			continue
		}
		m := lunLink.FindStringSubmatch(link)
		if m == nil {
			continue
		}
		session, _ := strconv.Atoi(m[1])
		id, _ := strconv.Atoi(m[2])
		l, outcome := r.readLUN(filepath.Join(base, name, "device"), session, id)
		switch outcome {
		case lunRead:
			out = append(out, l)
		case lunVanished:
			gone[name] = true
			if !r.goneLogged[name] {
				r.logger.Info("A SCSI device disappeared while it was read and is not reported", zap.String("device", name))
			}
		case lunNotDisk:
		}
	}
	r.goneLogged = gone
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].session != out[j].session {
			return out[i].session < out[j].session
		}
		return out[i].id < out[j].id
	})
	return out, nil
}

// readLUN reads one SCSI device directory. A device whose type file is gone is lunVanished; any
// other failure to read the type keeps the LUN, as unknown.
func (r *sysfsReader) readLUN(dir string, session, id int) (l lun, outcome readOutcome) {
	l = lun{session: session, id: id, state: stateUnknown}
	var problems []error

	typ, err := r.text(filepath.Join(dir, fileType))
	if err != nil {
		if vanished(err) {
			return lun{}, lunVanished
		}
		problems = append(problems, err)
	} else if n, parseErr := strconv.Atoi(typ); parseErr != nil {
		problems = append(problems, fmt.Errorf("the SCSI type %q is not a number: %w", typ, parseErr))
	} else if n != scsiTypeDisk && n != scsiTypeRBC {
		return lun{}, lunNotDisk
	}

	state, err := r.text(filepath.Join(dir, fileState))
	if err != nil {
		problems = append(problems, err)
	} else if state != "" {
		l.state = strings.ToLower(state)
	}

	blocks, err := r.readDir(filepath.Join(dir, dirBlock))
	switch {
	case err != nil:
		problems = append(problems, err)
	case len(blocks) == 0:
		problems = append(problems, errNoBlockDevice)
	default:
		l.device = blocks[0].Name()
	}

	if l.device == "" {
		l.state = stateUnknown
	} else {
		devDir := filepath.Join(dir, dirBlock, l.device)
		if sectors, err := r.sectors(filepath.Join(devDir, fileSize)); err != nil {
			problems = append(problems, err)
		} else {
			l.sizeBytes, l.hasSize = sectorsToBytes(sectors), true
		}
		// A block device nothing sits on may have no holders directory.
		holders, err := r.readDir(filepath.Join(devDir, dirHolders))
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			problems = append(problems, err)
		case len(holders) > 0:
			l.holder, l.holders = holders[0].Name(), len(holders)
		}
	}
	l.err = errors.Join(problems...)
	return l, lunRead
}

func (r *sysfsReader) sectors(path string) (uint64, error) {
	s, err := r.text(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("the size %q is not a number: %w", s, err)
	}
	return n, nil
}

// sectorsToBytes converts 512 byte sectors to bytes and saturates at the largest 64-bit value.
func sectorsToBytes(n uint64) uint64 {
	if n > math.MaxUint64/sectorBytes {
		return math.MaxUint64
	}
	return n * sectorBytes
}
