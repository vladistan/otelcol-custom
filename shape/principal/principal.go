// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package principal shortens cloud and Kubernetes principals and resource names for log display.
package principal

import (
	"regexp"
	"strings"
)

// MaxLen is the longest short name; a longer one is cut.
const MaxLen = 32

// Rule maps a principal containing a text to a fixed short name.
type Rule struct {
	Contains string
	Short    string
}

var defaultRules = []Rule{{Contains: "container-engine-robot", Short: "gke-robot"}}

// Shorten returns the short name of a principal. Extra rules are tried before the defaults.
func Shorten(p string, extra []Rule) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	for _, r := range append(append([]Rule{}, extra...), defaultRules...) {
		if r.Contains != "" && strings.Contains(p, r.Contains) {
			return r.Short
		}
	}
	return cut(short(p))
}

func short(p string) string {
	switch {
	case strings.HasPrefix(p, "system:serviceaccount:"):
		return p[strings.LastIndexByte(p, ':')+1:]
	case strings.HasPrefix(p, "system:node:"):
		return "node-" + strings.TrimPrefix(p, "system:node:")
	case strings.HasPrefix(p, "system:"):
		return strings.TrimPrefix(p, "system:")
	case strings.HasPrefix(p, "arn:"):
		return arnName(p)
	case strings.HasSuffix(p, ".amazonaws.com"):
		return "AWS Internal"
	}
	if local, _, ok := strings.Cut(p, "@"); ok && local != "" {
		return local
	}
	return p
}

func arnName(arn string) string {
	resource := arn[strings.LastIndexByte(arn, ':')+1:]
	if !strings.Contains(resource, "/") {
		return resource
	}
	return resource[strings.LastIndexByte(resource, '/')+1:]
}

func cut(s string) string {
	if len(s) > MaxLen {
		return s[:MaxLen]
	}
	return s
}

// AccountAlias returns the alias of an account or project id, or the id itself when it has none.
func AccountAlias(id string, aliases map[string]string) string {
	if a, ok := aliases[id]; ok && a != "" {
		return a
	}
	return id
}

// MergeAliases returns the defaults with each key of the config map replacing its default.
func MergeAliases(defaults, config map[string]string) map[string]string {
	out := make(map[string]string, len(defaults)+len(config))
	for k, v := range defaults {
		out[k] = v
	}
	for k, v := range config {
		out[k] = v
	}
	return out
}

var gkeBackend = regexp.MustCompile(`^k8s\d*-[0-9a-f]{8}-(.+)-[0-9a-f]{8}$`)

// BackendService drops the k8s1-<hash> and <hash> parts of a GCP load balancer backend service name.
func BackendService(name string) string {
	if m := gkeBackend.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

// LoadBalancerName returns the name of an ELB resource id such as "app/<name>/<id>".
func LoadBalancerName(resourceID string) string {
	parts := strings.Split(resourceID, "/")
	if len(parts) >= 3 {
		return parts[1]
	}
	return resourceID
}
