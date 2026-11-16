// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// controller is one SCSI host driven by megaraid_sas.
type controller struct {
	HostNo int
	PCI    string
}

var (
	hostDir = regexp.MustCompile(`^host([0-9]+)$`)
	pciAddr = regexp.MustCompile(`[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]`)
)

// findControllers lists the hosts of /sys/class/scsi_host whose proc_name is megaraid_sas.
func findControllers(root string) ([]controller, error) {
	base := filepath.Join(root, "class", "scsi_host")
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []controller
	for _, e := range entries {
		m := hostDir.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join(base, e.Name(), "proc_name"))
		if err != nil || strings.TrimSpace(string(body)) != "megaraid_sas" {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		c := controller{HostNo: n}
		if real, err := filepath.EvalSymlinks(filepath.Join(base, e.Name())); err == nil {
			if all := pciAddr.FindAllString(real, -1); len(all) > 0 {
				c.PCI = all[len(all)-1]
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostNo < out[j].HostNo })
	return out, nil
}
