// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartreceiver

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sysfsWith(block, nvme []string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for _, name := range block {
		fsys["block/"+name+"/size"] = &fstest.MapFile{Data: []byte("1\n")}
	}
	for _, name := range nvme {
		fsys["class/nvme/"+name+"/model"] = &fstest.MapFile{Data: []byte("x\n")}
	}
	return fsys
}

func names(devs []device) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		out = append(out, d.name)
	}
	return out
}

func TestDiscover_KeepsWholeSataAndNVMeControllersOnly(t *testing.T) {
	fsys := sysfsWith(
		[]string{"sda", "sda1", "loop0", "dm-0", "md0", "zd0", "ram0", "sr0", "nvme0c0n1", "nvme0n1"},
		[]string{"nvme0"},
	)

	devs, err := discover(fsys)

	require.NoError(t, err)
	assert.Equal(t, []string{"nvme0", "sda"}, names(devs))
}

func TestDiscover_KindFollowsTheSource(t *testing.T) {
	devs, err := discover(sysfsWith([]string{"sda", "vda"}, []string{"nvme0", "nvme1"}))

	require.NoError(t, err)
	kinds := map[string]deviceKind{}
	for _, d := range devs {
		kinds[d.name] = d.kind
	}
	assert.Equal(t, kindNVMe, kinds["nvme0"])
	assert.Equal(t, kindNVMe, kinds["nvme1"])
	assert.Equal(t, kindSATA, kinds["sda"])
	assert.Equal(t, kindSATA, kinds["vda"])
}

func TestDiscover_PartitionMarkerFileSkipsADevice(t *testing.T) {
	fsys := sysfsWith([]string{"sdaa", "sdb"}, nil)
	fsys["block/sdaa/partition"] = &fstest.MapFile{Data: []byte("1\n")}

	devs, err := discover(fsys)

	require.NoError(t, err)
	assert.Equal(t, []string{"sdb"}, names(devs))
}

func TestDiscover_LongSdNamesAreKept(t *testing.T) {
	devs, err := discover(sysfsWith([]string{"sdz", "sdaa", "sdaa1"}, nil))

	require.NoError(t, err)
	assert.Equal(t, []string{"sdaa", "sdz"}, names(devs))
}

func TestDiscover_MissingNVMeClassDirIsNotAnError(t *testing.T) {
	devs, err := discover(sysfsWith([]string{"sda"}, nil))

	require.NoError(t, err)
	assert.Equal(t, []string{"sda"}, names(devs))
}

func TestDiscover_MissingBlockDirIsAnError(t *testing.T) {
	_, err := discover(fstest.MapFS{})

	require.Error(t, err)
}

func TestSelect_IncludeAndExclude(t *testing.T) {
	devs := []device{{"nvme0", kindNVMe}, {"nvme1", kindNVMe}, {"sda", kindSATA}, {"sdb", kindSATA}}
	cases := map[string]struct {
		include, exclude []string
		want             []string
	}{
		"empty include reads all":     {nil, nil, []string{"nvme0", "nvme1", "sda", "sdb"}},
		"include NVMe controllers":    {[]string{"^nvme[0-9]+$"}, nil, []string{"nvme0", "nvme1"}},
		"exclude a named device":      {nil, []string{"^sdb$"}, []string{"nvme0", "nvme1", "sda"}},
		"exclude wins over include":   {[]string{"^sd"}, []string{"^sda$"}, []string{"sdb"}},
		"two include expressions":     {[]string{"^nvme0$", "^sda$"}, nil, []string{"nvme0", "sda"}},
		"include that matches no one": {[]string{"^zzz$"}, nil, []string{}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sel, err := newSelector(c.include, c.exclude)
			require.NoError(t, err)

			assert.Equal(t, c.want, names(sel.filter(devs)))
		})
	}
}
