// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package smartreceiver

import (
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

func testBus(t *testing.T) (*systemBus, *[]string, *[]int) {
	t.Helper()
	var opened []string
	var closed []int
	return &systemBus{
		sysfs:  sysfsWith([]string{"sda", "sdb"}, []string{"nvme0"}),
		devDir: "/dev",
		open: func(path string, flags int) (int, error) {
			opened = append(opened, path)
			if flags&unix.O_ACCMODE != unix.O_RDONLY {
				t.Errorf("%s opened for writing", path)
			}
			return 42, nil
		},
		closeF: func(fd int) error { closed = append(closed, fd); return nil },
		ioctl:  func(int, uintptr, unsafe.Pointer) (int, error) { return 0, nil },
	}, &opened, &closed
}

func TestSystemBus_DiscoverListsTheFixtureTree(t *testing.T) {
	b, _, _ := testBus(t)

	devs, err := b.discover()

	require.NoError(t, err)
	assert.Equal(t, []string{"nvme0", "sda", "sdb"}, names(devs))
}

func TestSystemBus_OpenATAUsesTheBlockDeviceReadOnlyAndCloses(t *testing.T) {
	b, opened, closed := testBus(t)

	tr, closer, err := b.openATA("sda", time.Second)

	require.NoError(t, err)
	assert.IsType(t, &sgTransport{}, tr)
	assert.Equal(t, []string{"/dev/sda"}, *opened)
	require.NoError(t, closer.Close())
	assert.Equal(t, []int{42}, *closed)
}

func TestSystemBus_OpenNVMeUsesTheControllerDevice(t *testing.T) {
	b, opened, _ := testBus(t)

	tr, _, err := b.openNVMe("nvme0", time.Second)

	require.NoError(t, err)
	assert.IsType(t, &nvmeTransport{}, tr)
	assert.Equal(t, []string{"/dev/nvme0"}, *opened)
}

func TestSystemBus_OpenFlagsAreReadOnlyCloseOnExecAndNonBlockingForSATA(t *testing.T) {
	var got []int
	b, _, _ := testBus(t)
	b.open = func(_ string, flags int) (int, error) { got = append(got, flags); return 42, nil }

	_, _, err := b.openATA("sda", time.Second)
	require.NoError(t, err)
	_, _, err = b.openNVMe("nvme0", time.Second)
	require.NoError(t, err)

	require.Len(t, got, 2)
	assert.Equal(t, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, got[0])
	assert.Equal(t, unix.O_RDONLY|unix.O_CLOEXEC, got[1])
}

func TestSystemBus_AnOpenRefusedForPermissionIsFileAccessAndPermission(t *testing.T) {
	for _, errno := range []error{unix.EACCES, unix.EPERM} {
		b, _, _ := testBus(t)
		b.open = func(string, int) (int, error) { return 0, errno }

		_, _, err := b.openNVMe("nvme0", time.Second)

		assert.ErrorIs(t, err, smartdecode.ErrFileAccess, errno.Error())
		assert.ErrorIs(t, err, smartdecode.ErrPermission, errno.Error())
	}
}

func TestSystemBus_ADeviceWithACommandStillInTheKernelIsNotOpenedAgain(t *testing.T) {
	b, opened, _ := testBus(t)
	b.inflightFor("sda").acquire() // a transport's goroutine holds it until its ioctl returns

	_, _, err := b.openATA("sda", time.Second)

	assert.ErrorIs(t, err, smartdecode.ErrTimeout)
	assert.Empty(t, *opened)
	_, _, err = b.openATA("sdb", time.Second)
	assert.NoError(t, err, "another device is not affected")
}

func TestSystemBus_OpenErrorsAreMapped(t *testing.T) {
	cases := map[error]error{
		unix.EACCES: smartdecode.ErrPermission,
		unix.EPERM:  smartdecode.ErrPermission,
		unix.ENXIO:  smartdecode.ErrUnsupported,
	}
	for errno, want := range cases {
		b, _, _ := testBus(t)
		b.open = func(string, int) (int, error) { return 0, errno }

		_, _, err := b.openATA("sda", time.Second)

		assert.ErrorIs(t, err, want, errno.Error())
		assert.Contains(t, err.Error(), "/dev/sda")
	}
}
