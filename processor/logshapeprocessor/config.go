// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/vladistan/otelcol-custom/shape/severity"
)

// Config holds the processor settings. Code supplies every default; a scalar here replaces it,
// a list extends it and a map replaces it per key.
type Config struct {
	// Branches names the parsers that may claim a record. The default enables all of them.
	Branches []string `mapstructure:"branches"`
	// Klog holds the klog branch options.
	Klog KlogConfig `mapstructure:"klog"`
	// Syslog holds the syslog branch options.
	Syslog SyslogConfig `mapstructure:"syslog"`
	// Cisco holds the cisco branch options.
	Cisco CiscoConfig `mapstructure:"cisco"`
	// Logrus holds the logrus branch options.
	Logrus LogrusConfig `mapstructure:"logrus"`
	// Postgres holds the postgres branch options.
	Postgres PostgresConfig `mapstructure:"postgres"`
	// Redis holds the redis branch options.
	Redis RedisConfig `mapstructure:"redis"`
	// Nginx holds the nginx branch options.
	Nginx NginxConfig `mapstructure:"nginx"`
	// Sentry holds the sentry branch options.
	Sentry SentryConfig `mapstructure:"sentry"`
	// Uvicorn holds the uvicorn branch options.
	Uvicorn UvicornConfig `mapstructure:"uvicorn"`
	// Hints narrows a branch to records from one service, scope or source. The default is none:
	// every branch claims by line shape alone. The key is a branch name.
	Hints map[string]Hint `mapstructure:"hints"`
	// AccountAliases maps a cloud account id to a display name and replaces the default for that id. Empty by default.
	AccountAliases map[string]string `mapstructure:"account_aliases"`
	// UserAliases maps the GUID of an Identity Center user to a name for the caller of CloudTrail events. Empty by default.
	UserAliases map[string]string `mapstructure:"user_aliases"`
	// WLANSSIDs maps a WLAN id to its SSID. The cisco branch uses it when a wireless controller message names the WLAN but not the SSID. Empty by default.
	WLANSSIDs map[string]string `mapstructure:"wlan_ssids"`
	// GCPLBErrorValues extends the load balancer status detail values treated as errors.
	GCPLBErrorValues []string `mapstructure:"gcp_lb_error_values"`
	// K8s holds the options of the Kubernetes branch.
	K8s K8sConfig `mapstructure:"k8s"`
	// ServiceNameOverrides maps a detected service name to the name to emit and replaces the default for that key.
	ServiceNameOverrides map[string]string `mapstructure:"service_name_overrides"`
	// Autoscaler holds the options of the autoscaler branch.
	Autoscaler AutoscalerConfig `mapstructure:"autoscaler"`
	// SeverityFromLine holds the option that gives a record with no severity the level of its line.
	SeverityFromLine SeverityFromLineConfig `mapstructure:"severity_from_line"`
}

// SeverityFromLineConfig holds the options of the severity-from-line step.
type SeverityFromLineConfig struct {
	// Services lists the service.name values whose records arrive with no severity. A listed record that has
	// none takes the level token of its line; one with a severity keeps it. Empty by default: no record changes.
	Services []string `mapstructure:"services"`
}

// AutoscalerConfig holds the options of the autoscaler branch.
type AutoscalerConfig struct {
	// KeepRatio is the share of rows with a node count equal to the target that the branch keeps, from 0 to 1.
	// Rows with a count that differs from the target are always kept. Default 0.2.
	KeepRatio float64 `mapstructure:"keep_ratio"`
}

// K8sConfig holds the options of the Kubernetes branch.
type K8sConfig struct {
	// StaticPodNodes extends the node names that static pods are known to run on. Empty by default.
	StaticPodNodes []string `mapstructure:"static_pod_nodes"`
}

// Hint breaks a tie between branches that would claim the same line. Every field that is set must
// equal the service.name of the resource, the scope name or the log.source of the record.
type Hint struct {
	Service string `mapstructure:"service"`
	Scope   string `mapstructure:"scope"`
	Source  string `mapstructure:"source"`
}

// UvicornConfig holds the options of the uvicorn branch; every bool defaults to true.
type UvicornConfig struct {
	// ExtractHTTPFields writes the request and response fields of access lines as attributes.
	ExtractHTTPFields bool `mapstructure:"extract_http_fields"`
	// RewriteMessages replaces the body of an access line with METHOD path status.
	RewriteMessages bool `mapstructure:"rewrite_messages"`
	// AdjustSeverity sets the severity from the level prefix and, for access lines, the status.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// SentryConfig holds the options of the sentry branch.
type SentryConfig struct {
	// SourceAttribute is an attribute that holds the line; the body is the fallback.
	SourceAttribute string `mapstructure:"source_attribute"`
	// SetServiceName writes service.name when the resource has none.
	SetServiceName bool `mapstructure:"set_service_name"`
	// ServiceName is the service.name written when SetServiceName is on. Default sentry.
	ServiceName string `mapstructure:"service_name"`
	// ParseTimestamp puts the time of the line on the date of the record.
	ParseTimestamp bool `mapstructure:"parse_timestamp"`
	// PreserveOriginal keeps the raw line in log.record.original.
	PreserveOriginal bool `mapstructure:"preserve_original"`
	// ExtractHTTPFields writes the request and response fields of access lines as attributes.
	ExtractHTTPFields bool `mapstructure:"extract_http_fields"`
}

// NginxConfig holds the options of the nginx branch.
type NginxConfig struct {
	// SourceAttribute is an attribute that holds the line; the body is the fallback.
	SourceAttribute string `mapstructure:"source_attribute"`
	// SetServiceName writes service.name when the resource has none.
	SetServiceName bool `mapstructure:"set_service_name"`
	// ServiceName is the service.name written when SetServiceName is on. Default nginx.
	ServiceName string `mapstructure:"service_name"`
	// ParseTimestamp sets the record time from the time of the line.
	ParseTimestamp bool `mapstructure:"parse_timestamp"`
}

// RedisConfig holds the options of the redis branch.
type RedisConfig struct {
	// ServiceName is the service.name written when the resource has none. Default redis.
	ServiceName string `mapstructure:"service_name"`
	// SourceAttribute is an attribute that holds the line; the body is the fallback.
	SourceAttribute string `mapstructure:"source_attribute"`
	// SetServiceName writes service.name when the resource has none.
	SetServiceName bool `mapstructure:"set_service_name"`
	// PreserveOriginal keeps the raw line in log.record.original.
	PreserveOriginal bool `mapstructure:"preserve_original"`
	// RewriteMessages replaces the body with the message text.
	RewriteMessages bool `mapstructure:"rewrite_messages"`
	// AdjustSeverity sets the severity from the level symbol.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// PostgresConfig holds the options of the postgres branch; every bool defaults to true.
type PostgresConfig struct {
	// RewriteMessages replaces the body with the message text.
	RewriteMessages bool `mapstructure:"rewrite_messages"`
	// ExtractDetails writes the DETAIL, STATEMENT, HINT and CONTEXT lines as attributes.
	ExtractDetails bool `mapstructure:"extract_details"`
	// AdjustSeverity sets the severity from the level of the line.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
}

// LogrusConfig holds the options of the logrus branch, which also reads zap JSON and zap console lines.
type LogrusConfig struct {
	// RewriteMessages replaces the body with the message field, with the error appended.
	RewriteMessages bool `mapstructure:"rewrite_messages"`
	// AdjustSeverity sets the severity from the level of the line.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
	// ParseTimestamp sets the record time from the time of the line.
	ParseTimestamp bool `mapstructure:"parse_timestamp"`
}

// CiscoConfig holds the options of the cisco branch; every bool defaults to true.
type CiscoConfig struct {
	// SourceAttribute holds the raw line; the body is the fallback.
	SourceAttribute string `mapstructure:"source_attribute"`
	// SetHostname sets host.name from net.peer.name when the resource has none.
	SetHostname bool `mapstructure:"set_hostname"`
	// CleanBody removes the syslog priority prefix from the body.
	CleanBody bool `mapstructure:"clean_body"`
	// RewriteMessages replaces the body with the message part of IOS, switch, HP and WLC lines.
	RewriteMessages bool `mapstructure:"rewrite_messages"`
	// AdjustSeverity sets WARN for interface and topology events and reads the Cisco level otherwise.
	AdjustSeverity bool `mapstructure:"adjust_severity"`
	// PortDatabasePath is an optional YAML file of port aliases per host.
	PortDatabasePath string `mapstructure:"port_database_path"`
	// AccessPointDowngrade lists message fragments of routine access point kernel lines, matched without
	// case. A matching line drops from the device WARN to INFO. A list set here replaces the built-in list.
	AccessPointDowngrade []string `mapstructure:"access_point_downgrade"`
}

// SyslogConfig holds the options of the syslog branch; every bool defaults to true.
type SyslogConfig struct {
	// SourceAttribute holds the raw syslog line, as the udp and tcp receivers set it.
	SourceAttribute string `mapstructure:"source_attribute"`
	// SetHostname sets host.name from the HOSTNAME of the line.
	SetHostname bool `mapstructure:"set_hostname"`
	// SetServiceName sets service.name from the tag of the line.
	SetServiceName bool `mapstructure:"set_service_name"`
	// MapSeverity maps the syslog priority to the severity.
	MapSeverity bool `mapstructure:"map_severity"`
	// PreserveOriginal keeps the raw line in log.record.original.
	PreserveOriginal bool `mapstructure:"preserve_original"`
	// CleanBody replaces the body with the message part of the line.
	CleanBody bool `mapstructure:"clean_body"`
}

// KlogConfig holds the options of the klog branch.
type KlogConfig struct {
	// Year replaces the year taken from the record time (klog lines carry month and day only).
	Year int `mapstructure:"year"`
	// StaticPodPrefixes extends the static pod name prefixes used to infer the node.
	StaticPodPrefixes []string `mapstructure:"static_pod_prefixes"`
	// LevelOverrides maps a klog letter (I, W, E, F) to a level word and replaces the default for that letter.
	LevelOverrides map[string]string `mapstructure:"level_overrides"`
}

// Validate rejects an unknown branch name or level word.
func (c *Config) Validate() error {
	for _, b := range c.Branches {
		if !slices.Contains(branchNames, b) {
			return fmt.Errorf("unknown branch %q, known branches: %s", b, strings.Join(branchNames, ", "))
		}
	}
	for name := range c.Hints {
		if !slices.Contains(branchNames, name) {
			return fmt.Errorf("hints: unknown branch %q, known branches: %s", name, strings.Join(branchNames, ", "))
		}
	}
	for letter, word := range c.Klog.LevelOverrides {
		if _, ok := severity.FromSource(severity.Word, word); !ok {
			return fmt.Errorf("klog.level_overrides[%q]: unknown level %q", letter, word)
		}
	}
	for id, alias := range c.AccountAliases {
		if id == "" {
			return errors.New("account_aliases: empty account id")
		}
		if alias == "" {
			return fmt.Errorf("account_aliases[%q]: empty alias", id)
		}
	}
	for guid, name := range c.UserAliases {
		if guid == "" {
			return errors.New("user_aliases: empty user id")
		}
		if name == "" {
			return fmt.Errorf("user_aliases[%q]: empty name", guid)
		}
	}
	for id, ssid := range c.WLANSSIDs {
		if id == "" {
			return errors.New("wlan_ssids: empty WLAN id")
		}
		if _, err := strconv.ParseUint(id, 10, 32); err != nil {
			return fmt.Errorf("wlan_ssids[%q]: the WLAN id must be a number", id)
		}
		if ssid == "" {
			return fmt.Errorf("wlan_ssids[%q]: empty SSID", id)
		}
	}
	for i, v := range c.GCPLBErrorValues {
		if v == "" {
			return fmt.Errorf("gcp_lb_error_values[%d]: empty value", i)
		}
	}
	for from, to := range c.ServiceNameOverrides {
		if from == "" {
			return errors.New("service_name_overrides: empty service name")
		}
		if to == "" {
			return fmt.Errorf("service_name_overrides[%q]: empty replacement", from)
		}
	}
	if r := c.Autoscaler.KeepRatio; math.IsNaN(r) || r < 0 || r > 1 {
		return fmt.Errorf("autoscaler.keep_ratio: %v is outside 0 to 1", r)
	}
	for i, s := range c.SeverityFromLine.Services {
		if s == "" {
			return fmt.Errorf("severity_from_line.services[%d]: empty service name", i)
		}
	}
	return nil
}

func (c *Config) enabled(name string) bool { return slices.Contains(c.Branches, name) }
