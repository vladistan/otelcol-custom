// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package ethtoolreceiver

import (
	"context"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"golang.org/x/sys/unix"
)

// kernel plays the driver side of SIOCETHTOOL over the buffers the reader hands in.
type kernel struct {
	driver     string
	names      []string
	values     []uint64
	ssetMask   uint64 // returned in GSSET_INFO; 2 means ETH_SS_STATS is supported
	headerOnly int    // when set, only the headers report this count; no data is written
	errno      map[uint32]unix.Errno
	lastIface  string
	lastCmds   []uint32
}

func (k *kernel) ioctl(ifr *ifreq) error {
	k.lastIface = strings.TrimRight(string(ifr.name[:]), "\x00")
	cmd := *(*uint32)(ifr.data)
	k.lastCmds = append(k.lastCmds, cmd)
	if e, ok := k.errno[cmd]; ok {
		return e
	}
	switch cmd {
	case unix.ETHTOOL_GDRVINFO:
		info := (*unix.EthtoolDrvinfo)(ifr.data)
		copy(info.Driver[:], k.driver)
	case unix.ETHTOOL_GSSET_INFO:
		buf := unsafe.Slice((*byte)(ifr.data), 24)
		native.PutUint64(buf[8:], k.ssetMask)
		native.PutUint32(buf[16:], uint32(len(k.names)))
	case unix.ETHTOOL_GSTRINGS:
		if k.headerOnly > 0 {
			native.PutUint32(unsafe.Slice((*byte)(ifr.data), 12)[8:], uint32(k.headerOnly))
			return nil
		}
		buf := unsafe.Slice((*byte)(ifr.data), 12+len(k.names)*ethGstringLen)
		native.PutUint32(buf[8:], uint32(len(k.names)))
		for i, n := range k.names {
			copy(buf[12+i*ethGstringLen:12+(i+1)*ethGstringLen], n)
		}
	case unix.ETHTOOL_GSTATS:
		if k.headerOnly > 0 {
			native.PutUint32(unsafe.Slice((*byte)(ifr.data), 8)[4:], uint32(k.headerOnly))
			return nil
		}
		buf := unsafe.Slice((*byte)(ifr.data), 8+len(k.values)*8)
		native.PutUint32(buf[4:], uint32(len(k.values)))
		for i, v := range k.values {
			native.PutUint64(buf[8+i*8:], v)
		}
	}
	return nil
}

func newTestNIC(k *kernel) *linuxNIC {
	return &linuxNIC{ioctl: k.ioctl, list: func() ([]string, error) { return []string{"eth0"}, nil }}
}

func enaKernel() *kernel {
	return &kernel{
		driver:   "ena",
		names:    append([]string{"tx_timeout"}, allowanceNames...),
		values:   []uint64{0, 1, 2, 3, 4, 5},
		ssetMask: 1 << ethSSStats,
	}
}

func TestLinuxDriverReadsTheNullPaddedName(t *testing.T) {
	k := enaKernel()

	got, err := newTestNIC(k).driver("eth0")

	require.NoError(t, err)
	assert.Equal(t, "ena", got)
	assert.Equal(t, "eth0", k.lastIface)
	assert.Equal(t, []uint32{unix.ETHTOOL_GDRVINFO}, k.lastCmds)
}

func TestLinuxStatCountUsesTheStatsStringSet(t *testing.T) {
	got, err := newTestNIC(enaKernel()).statCount("eth0")

	require.NoError(t, err)
	assert.Equal(t, 6, got)
}

func TestLinuxStatCountWithoutTheStatsSetIsNotSupported(t *testing.T) {
	k := enaKernel()
	k.ssetMask = 0

	_, err := newTestNIC(k).statCount("eth0")

	assert.ErrorIs(t, err, errNotSupported)
}

func TestLinuxStatNamesAndValuesRoundTrip(t *testing.T) {
	nic := newTestNIC(enaKernel())

	names, err := nic.statNames("eth0", 6)
	require.NoError(t, err)
	values, err := nic.statValues("eth0", 6)
	require.NoError(t, err)

	assert.Equal(t, append([]string{"tx_timeout"}, allowanceNames...), names)
	assert.Equal(t, []uint64{0, 1, 2, 3, 4, 5}, values)
}

func TestLinuxStatNamesAndValuesAcceptAGrowthInsideTheHeadroom(t *testing.T) {
	k := enaKernel()
	nic := newTestNIC(k)

	names, err := nic.statNames("eth0", 4)
	require.NoError(t, err)
	values, err := nic.statValues("eth0", 4)
	require.NoError(t, err)

	assert.Len(t, names, 6, "the kernel count wins over the requested one")
	assert.Len(t, values, 6)
}

func TestLinuxStatCountLargerThanTheBufferIsACountChange(t *testing.T) {
	k := enaKernel()
	k.headerOnly = 4 * statHeadroom
	nic := newTestNIC(k)

	_, namesErr := nic.statNames("eth0", 1)
	_, valuesErr := nic.statValues("eth0", 1)

	assert.ErrorIs(t, namesErr, errCountChanged)
	assert.ErrorIs(t, valuesErr, errCountChanged)
}

func TestLinuxErrnoMapping(t *testing.T) {
	tests := map[string]struct {
		errno unix.Errno
		want  error
	}{
		"EOPNOTSUPP": {unix.EOPNOTSUPP, errNotSupported},
		"ENODEV":     {unix.ENODEV, errNoDevice},
		"EPERM":      {unix.EPERM, errPermission},
		"EACCES":     {unix.EACCES, errPermission},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			k := enaKernel()
			k.errno = map[uint32]unix.Errno{
				unix.ETHTOOL_GDRVINFO:   tt.errno,
				unix.ETHTOOL_GSSET_INFO: tt.errno,
				unix.ETHTOOL_GSTRINGS:   tt.errno,
				unix.ETHTOOL_GSTATS:     tt.errno,
			}
			nic := newTestNIC(k)

			_, derr := nic.driver("eth0")
			_, cerr := nic.statCount("eth0")
			_, nerr := nic.statNames("eth0", 1)
			_, verr := nic.statValues("eth0", 1)

			for _, err := range []error{derr, cerr, nerr, verr} {
				assert.ErrorIs(t, err, tt.want)
			}
		})
	}
}

func TestLinuxOtherErrnoIsReturnedWithTheInterfaceName(t *testing.T) {
	k := enaKernel()
	k.errno = map[uint32]unix.Errno{unix.ETHTOOL_GDRVINFO: unix.EIO}

	_, err := newTestNIC(k).driver("eth0")

	require.Error(t, err)
	assert.ErrorIs(t, err, unix.EIO)
	assert.Contains(t, err.Error(), "eth0")
	for _, sentinel := range []error{errNotSupported, errNoDevice, errPermission, errCountChanged} {
		assert.NotErrorIs(t, err, sentinel)
	}
}

func TestLinuxInterfaceNameTooLongIsRejectedBeforeTheCall(t *testing.T) {
	k := enaKernel()

	_, err := newTestNIC(k).driver(strings.Repeat("x", unix.IFNAMSIZ))

	require.ErrorContains(t, err, "longer than")
	assert.Empty(t, k.lastCmds)
}

func TestLinuxInterfacesComesFromTheListFunction(t *testing.T) {
	got, err := newTestNIC(enaKernel()).interfaces()

	require.NoError(t, err)
	assert.Equal(t, []string{"eth0"}, got)
}

func TestLinuxCloseWithoutASocketIsClean(t *testing.T) {
	require.NoError(t, newTestNIC(enaKernel()).close())
}

// The real socket and ioctl run on the test host. Whether lo answers GDRVINFO depends
// on the kernel, so only the interface list and the unknown-interface errno are asserted.
func newRealNIC(t *testing.T) nicReader {
	t.Helper()
	nic, err := newNIC()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, nic.close()) })
	return nic
}

func TestLinuxRealNICListsLoopback(t *testing.T) {
	ifaces, err := newRealNIC(t).interfaces()

	require.NoError(t, err)
	assert.Contains(t, ifaces, "lo")
}

func TestLinuxRealNICReportsAnUnknownInterfaceAsNoDevice(t *testing.T) {
	_, err := newRealNIC(t).driver("no-such-if0")

	assert.ErrorIs(t, err, errNoDevice)
}

func TestLinuxStatNameThatFillsTheWholeFieldIsKept(t *testing.T) {
	k := enaKernel()
	long := strings.Repeat("n", ethGstringLen)
	k.names = []string{long}
	k.values = []uint64{7}

	names, err := newTestNIC(k).statNames("eth0", 1)

	require.NoError(t, err)
	assert.Equal(t, []string{long}, names)
}

func TestLinuxFactoryCreatesAReceiverThatShutsDownCleanly(t *testing.T) {
	f := NewFactory()

	r, err := f.CreateMetrics(context.Background(), receivertest.NewNopSettings(f.Type()), f.CreateDefaultConfig(), consumertest.NewNop())

	require.NoError(t, err)
	require.NoError(t, r.Shutdown(context.Background()))
}

func TestLinuxBootTimeIsInThePastAndAfterTheEpochOfTheKernelAge(t *testing.T) {
	var info unix.Sysinfo_t
	require.NoError(t, unix.Sysinfo(&info))

	boot, err := newTestNIC(enaKernel()).bootTime()

	require.NoError(t, err)
	uptime := time.Duration(info.Uptime) * time.Second
	assert.WithinDuration(t, time.Now().Add(-uptime), boot, 5*time.Second)
}
