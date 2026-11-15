// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package fswatchreceiver

import "strings"

const (
	// maxPlainPath is the longest path CreateFile takes without the \\?\ prefix.
	maxPlainPath = 248
	extendedPath = `\\?\`
	extendedUNC  = `\\?\UNC\`
)

func slashesToBackslashes(p string) string { return strings.ReplaceAll(p, "/", `\`) }

// isUNC is true for \\server\share, with either slash, and for \\?\UNC\ paths.
func isUNC(p string) bool {
	n := slashesToBackslashes(p)
	if strings.HasPrefix(strings.ToUpper(n), strings.ToUpper(extendedUNC)) {
		return true
	}
	return strings.HasPrefix(n, `\\`) && !strings.HasPrefix(n, extendedPath)
}

// normalizeRoot gives the form used to compare roots: lower case, backslashes, no trailing separator.
func normalizeRoot(p string) string {
	return strings.TrimRight(strings.ToLower(slashesToBackslashes(p)), `\`)
}

func rootKey(root string) string {
	return "fswatch/" + normalizeRoot(root)
}

func longPath(p string) string {
	if len(p) <= maxPlainPath || strings.HasPrefix(p, extendedPath) {
		return p
	}
	if isUNC(p) {
		return extendedUNC + strings.TrimPrefix(p, `\\`)
	}
	return extendedPath + p
}
