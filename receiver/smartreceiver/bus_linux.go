// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// systemBus lists disks from /sys and opens them under /dev.
type systemBus struct {
	sysfs  fs.FS
	devDir string
	open   func(path string, flags int) (int, error)
	closeF func(fd int) error
	ioctl  ioctlFunc

	// inflight holds one *inflight per device name; see the inflight type.
	inflight sync.Map
}

func (b *systemBus) inflightFor(name string) *inflight {
	v, _ := b.inflight.LoadOrStore(name, new(inflight))
	f, _ := v.(*inflight) // only *inflight values are stored
	return f
}

func newSystemBus() (bus, error) {
	return &systemBus{
		sysfs:  os.DirFS("/sys"),
		devDir: "/dev",
		open:   func(path string, flags int) (int, error) { return unix.Open(path, flags, 0) },
		closeF: unix.Close,
		ioctl:  sysIoctl,
	}, nil
}

func (b *systemBus) discover() ([]device, error) { return discover(b.sysfs) }

type fdCloser struct {
	fd     int
	closeF func(int) error
}

func (c fdCloser) Close() error { return c.closeF(c.fd) }

func (b *systemBus) openDevice(name string, flags int) (int, io.Closer, error) {
	if b.inflightFor(name).held() {
		return 0, nil, errStillRunning()
	}
	path := filepath.Join(b.devDir, name)
	fd, err := b.open(path, flags|unix.O_CLOEXEC)
	if err != nil {
		return 0, nil, fmt.Errorf("open %s: %w", path, mapOpenErr(err))
	}
	return fd, fdCloser{fd, b.closeF}, nil
}

func (b *systemBus) openATA(name string, timeout time.Duration) (smartdecode.ATATransport, io.Closer, error) {
	fd, closer, err := b.openDevice(name, unix.O_RDONLY|unix.O_NONBLOCK)
	if err != nil {
		return nil, nil, err
	}
	return &sgTransport{fd: fd, timeout: timeout, ioctl: b.ioctl, inflight: b.inflightFor(name)}, closer, nil
}

func (b *systemBus) openNVMe(name string, timeout time.Duration) (smartdecode.NVMeTransport, io.Closer, error) {
	fd, closer, err := b.openDevice(name, unix.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	return &nvmeTransport{fd: fd, timeout: timeout, ioctl: b.ioctl, inflight: b.inflightFor(name)}, closer, nil
}
