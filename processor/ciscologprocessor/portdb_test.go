// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ciscologprocessor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPortDB(t *testing.T) {
	// Create temp YAML file
	content := `ports:
  switch-a.home.example.com:
    gi1: "Uplink to Core"
    gi2: "Server Rack 1"
    gi4: "AP Office"
  switch-b.home.example.com:
    GigabitEthernet0/1: "Living Room AP"
    GigabitEthernet0/3: "Office Desktop"
  switch-c-1:
    "1": "Port 1 - Workstation"
    "2": "Port 2 - Printer"
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "ports.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	db, err := loadPortDB(tmpFile)
	require.NoError(t, err)
	assert.NotNil(t, db)
	assert.Equal(t, 7, db.Size()) // 3 + 2 + 2 entries
}

func TestPortDB_Lookup(t *testing.T) {
	// Create temp YAML file
	content := `ports:
  switch-a.home.example.com:
    gi1: "Uplink to Core"
    gi4: "AP Office"
  switch-b.home.example.com:
    GigabitEthernet0/1: "Living Room AP"
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "ports.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	db, err := loadPortDB(tmpFile)
	require.NoError(t, err)

	tests := []struct {
		name     string
		hostname string
		port     string
		expected string
	}{
		{
			name:     "exact_match",
			hostname: "switch-a.home.example.com",
			port:     "gi4",
			expected: "AP Office",
		},
		{
			name:     "short_hostname_match",
			hostname: "switch-a",
			port:     "gi1",
			expected: "Uplink to Core",
		},
		{
			name:     "case_insensitive_hostname",
			hostname: "SWITCH-A.home.example.com",
			port:     "gi4",
			expected: "AP Office",
		},
		{
			name:     "case_insensitive_port",
			hostname: "switch-b.home.example.com",
			port:     "gigabitethernet0/1",
			expected: "Living Room AP",
		},
		{
			name:     "not_found_hostname",
			hostname: "unknown-switch",
			port:     "gi1",
			expected: "",
		},
		{
			name:     "not_found_port",
			hostname: "switch-a",
			port:     "gi99",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := db.Lookup(tt.hostname, tt.port)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLoadPortDB_EmptyPath(t *testing.T) {
	db, err := loadPortDB("")
	assert.NoError(t, err)
	assert.Nil(t, db)
}

func TestLoadPortDB_FileNotFound(t *testing.T) {
	_, err := loadPortDB("/nonexistent/path/ports.yaml")
	assert.Error(t, err)
}

func TestLoadPortDB_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "invalid.yaml")
	err := os.WriteFile(tmpFile, []byte("not: valid: yaml: content:::"), 0644)
	require.NoError(t, err)

	_, err = loadPortDB(tmpFile)
	assert.Error(t, err)
}

func TestPortDB_Size_Nil(t *testing.T) {
	var db *portDB
	assert.Equal(t, 0, db.Size())
}

func TestPortDB_Lookup_Nil(t *testing.T) {
	var db *portDB
	assert.Equal(t, "", db.Lookup("host", "port"))
}
