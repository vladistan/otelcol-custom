// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package sqlserver holds the checks of the SQL Server sample config for the Windows edge build.
package sqlserver

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// reference is a ${file:...} or ${env:...} value, the only accepted form of a password.
var reference = regexp.MustCompile(`\$\{(?:file|env):[^}\s]+\}`)

// A connection string password: the key password or pwd with an equals sign in a key-value string (ADO,
// ODBC) or a URL query. The value is a braced run, as ODBC writes it, or runs to the next ; or &.
var keyedPassword = regexp.MustCompile(`(?i)\b(?:password|pwd)\s*=\s*(\{[^}]*\}|[^;&]*)`)

// A URL password: the part between the colon and the at sign of the user information.
var userinfoPassword = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/@?#\s:]*:([^@/?#\s]*)@`)

// placeholder replaces an accepted reference before a string is read, so a path with a colon or a slash
// inside the reference does not end a URL password early.
const placeholder = "REFERENCE"

// LiteralSecrets walks a whole config and returns one line for each literal password: the password key of
// a sqlserver receiver, and any string value anywhere that holds a password or pwd key with a literal value,
// or a URL with a password in its user information.
func LiteralSecrets(config map[string]any) []string {
	var hits []string
	walk("", config, false, &hits)
	sort.Strings(hits)
	return hits
}

func walk(path string, v any, inSQLServer bool, hits *[]string) {
	switch val := v.(type) {
	case map[string]any:
		for key, child := range val {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			sql := inSQLServer
			if path == "receivers" && isSQLServer(key) {
				sql = true
			}
			if sql && (strings.EqualFold(key, "password") || strings.EqualFold(key, "pwd")) {
				if s, ok := child.(string); !ok || !isReference(s) {
					*hits = append(*hits, fmt.Sprintf("%s is a literal", childPath))
				}
				continue
			}
			walk(childPath, child, sql, hits)
		}
	case []any:
		for i, child := range val {
			walk(fmt.Sprintf("%s[%d]", path, i), child, inSQLServer, hits)
		}
	case string:
		if literalInString(val) {
			*hits = append(*hits, fmt.Sprintf("%s holds a literal password", path))
		}
	}
}

func isReference(s string) bool {
	s = strings.TrimSpace(s)
	return reference.MatchString(s) && reference.ReplaceAllString(s, "") == ""
}

func literalInString(s string) bool {
	s = reference.ReplaceAllString(s, placeholder)
	for _, m := range keyedPassword.FindAllStringSubmatch(s, -1) {
		if strings.TrimSpace(m[1]) != placeholder {
			return true
		}
	}
	for _, m := range userinfoPassword.FindAllStringSubmatch(s, -1) {
		if m[1] != placeholder {
			return true
		}
	}
	return false
}
