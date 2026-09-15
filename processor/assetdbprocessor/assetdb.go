// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package assetdbprocessor

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Asset represents a single asset entry in the database.
type Asset struct {
	MAC      string `yaml:"mac"`
	Hostname string `yaml:"hostname"`
	Location string `yaml:"location"`
}

// AssetDatabase represents the YAML asset database structure.
type AssetDatabase struct {
	Assets []Asset `yaml:"assets"`
}

// assetDB holds the loaded asset data with a map for fast lookups.
type assetDB struct {
	// assets maps normalized MAC address to Asset
	assets map[string]*Asset
}

// loadAssetDB loads the asset database from a YAML file.
func loadAssetDB(path string) (*assetDB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read asset database file: %w", err)
	}

	var db AssetDatabase
	if err := yaml.Unmarshal(data, &db); err != nil {
		return nil, fmt.Errorf("failed to parse asset database YAML: %w", err)
	}

	// Build lookup map with normalized MAC addresses
	assets := make(map[string]*Asset)
	for i := range db.Assets {
		asset := &db.Assets[i]
		normalizedMAC := normalizeMAC(asset.MAC)
		assets[normalizedMAC] = asset
	}

	return &assetDB{assets: assets}, nil
}

// lookup finds an asset by MAC address.
// Returns nil if not found.
func (db *assetDB) lookup(mac string) *Asset {
	normalizedMAC := normalizeMAC(mac)
	return db.assets[normalizedMAC]
}

// size returns the number of assets in the database.
func (db *assetDB) size() int {
	return len(db.assets)
}

// normalizeMAC normalizes a MAC address to lowercase with colons.
// Handles formats: aa:bb:cc:dd:ee:ff, AA-BB-CC-DD-EE-FF, aabbccddeeff, etc.
func normalizeMAC(mac string) string {
	if mac == "" {
		return ""
	}

	// Remove all separators and convert to lowercase
	cleaned := strings.ToLower(mac)
	cleaned = strings.ReplaceAll(cleaned, ":", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, ".", "")

	// If not exactly 12 hex characters, return as-is (invalid MAC)
	if len(cleaned) != 12 || !isHex(cleaned) {
		return strings.ToLower(mac)
	}

	// Format as aa:bb:cc:dd:ee:ff
	return fmt.Sprintf("%s:%s:%s:%s:%s:%s",
		cleaned[0:2], cleaned[2:4], cleaned[4:6],
		cleaned[6:8], cleaned[8:10], cleaned[10:12])
}

var hexPattern = regexp.MustCompile(`^[0-9a-f]+$`)

// isHex checks if a string contains only hexadecimal characters.
func isHex(s string) bool {
	return hexPattern.MatchString(s)
}
