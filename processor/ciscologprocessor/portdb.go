// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// PortDatabase represents the YAML port database structure.
// Per-device structure allows same port name to have different meanings on different switches.
type PortDatabase struct {
	// Ports maps hostname -> port name -> friendly alias
	Ports map[string]map[string]string `yaml:"ports"`
}

// portDB holds the loaded port data with nested maps for fast lookups.
type portDB struct {
	// ports maps hostname -> port name -> alias
	ports map[string]map[string]string
}

// loadPortDB loads the port database from a YAML file.
// Returns nil if path is empty (port database is optional).
func loadPortDB(path string) (*portDB, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read port database file: %w", err)
	}

	var db PortDatabase
	if err := yaml.Unmarshal(data, &db); err != nil {
		return nil, fmt.Errorf("failed to parse port database YAML: %w", err)
	}

	// Normalize all keys to lowercase for case-insensitive lookup
	ports := make(map[string]map[string]string)
	for hostname, portMap := range db.Ports {
		normalizedHostname := normalizeHostname(hostname)
		normalizedPorts := make(map[string]string)
		for portName, alias := range portMap {
			normalizedPortName := normalizePortName(portName)
			normalizedPorts[normalizedPortName] = alias
		}
		ports[normalizedHostname] = normalizedPorts
	}

	return &portDB{ports: ports}, nil
}

// Lookup finds a port alias by hostname and port name.
// Returns empty string if not found.
// Supports matching by:
// - Exact hostname match
// - Short hostname match (e.g., "switch-1" matches "switch-1.domain.com")
func (db *portDB) Lookup(hostname, portName string) string {
	if db == nil || db.ports == nil {
		return ""
	}

	normalizedHostname := normalizeHostname(hostname)
	normalizedPortName := normalizePortName(portName)

	// Try exact hostname match first
	if portMap, ok := db.ports[normalizedHostname]; ok {
		if alias, ok := portMap[normalizedPortName]; ok {
			return alias
		}
	}

	// Try matching by short hostname
	// If input is short (e.g., "switch-1"), try to find keys where short form matches
	shortInputHostname := extractShortHostname(normalizedHostname)
	for dbHostname, portMap := range db.ports {
		shortDbHostname := extractShortHostname(dbHostname)
		if shortDbHostname == shortInputHostname || shortDbHostname == normalizedHostname {
			if alias, ok := portMap[normalizedPortName]; ok {
				return alias
			}
		}
	}

	return ""
}

// Size returns the total number of port entries across all hosts.
func (db *portDB) Size() int {
	if db == nil || db.ports == nil {
		return 0
	}
	count := 0
	for _, portMap := range db.ports {
		count += len(portMap)
	}
	return count
}

// normalizeHostname normalizes a hostname for lookup.
func normalizeHostname(hostname string) string {
	return strings.ToLower(strings.TrimSpace(hostname))
}

// normalizePortName normalizes a port name for lookup.
// Handles various formats: GigabitEthernet0/1, Gi0/1, gi1, 1, etc.
func normalizePortName(portName string) string {
	return strings.ToLower(strings.TrimSpace(portName))
}

// extractShortHostname extracts the short hostname from a FQDN.
// e.g., "switch-b.home.example.com" -> "switch-b"
func extractShortHostname(hostname string) string {
	if idx := strings.Index(hostname, "."); idx > 0 {
		return hostname[:idx]
	}
	return hostname
}
