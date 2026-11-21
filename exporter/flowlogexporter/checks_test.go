// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore netflow flowlog clickhouseexporter

package flowlogexporter

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// root is the tool directory that holds the manifests, the reference configs and the docs.
const root = "../.."

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)
	return string(b)
}

func TestManifest_TheGatewayListsTheReceiverAndTheExporterWithTheirLocalReplaces(t *testing.T) {
	gateway := readFile(t, "builder-gateway.yaml")

	for _, line := range []string{
		"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/netflowreceiver => ../../receiver/netflowreceiver",
		"github.com/vladistan/otelcol-custom/exporter/flowlogexporter => ../../exporter/flowlogexporter",
		"gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/netflowreceiver v0.164.1",
		"gomod: github.com/vladistan/otelcol-custom/exporter/flowlogexporter v0.0.0",
	} {
		assert.Contains(t, gateway, line)
	}
}

func TestManifest_NoEdgeManifestListsTheExporter(t *testing.T) {
	for _, name := range []string{"builder-edge.yaml", "builder-edge-static.yaml", "builder-scrape.yaml"} {
		assert.NotContains(t, readFile(t, name), "flowlogexporter", name)
	}
}

type flowConfig struct {
	Receivers map[string]map[string]any `yaml:"receivers"`
	Exporters map[string]struct {
		Endpoint     string `yaml:"endpoint"`
		Password     string `yaml:"password"`
		SendingQueue struct {
			QueueSize int `yaml:"queue_size"`
		} `yaml:"sending_queue"`
		RetryOnFailure struct {
			MaxElapsedTime string `yaml:"max_elapsed_time"`
		} `yaml:"retry_on_failure"`
	} `yaml:"exporters"`
	Service struct {
		Pipelines map[string]struct {
			Receivers []string `yaml:"receivers"`
			Exporters []string `yaml:"exporters"`
		} `yaml:"pipelines"`
	} `yaml:"service"`
}

func loadFlowConfig(t *testing.T) flowConfig {
	t.Helper()
	var cfg flowConfig
	require.NoError(t, yaml.Unmarshal([]byte(readFile(t, "test/configs/gateway-flowlog.yaml")), &cfg))
	return cfg
}

func TestReferenceConfig_HoldsTheThreeFlowBlocks(t *testing.T) {
	cfg := loadFlowConfig(t)

	assert.Contains(t, cfg.Receivers, "netflow")
	assert.Contains(t, cfg.Exporters, "flowlog")
	assert.Contains(t, cfg.Service.Pipelines, "logs/flowlog")
}

func TestReferenceConfig_NetflowSchemeOnUDP2055AndNoSFlow(t *testing.T) {
	cfg := loadFlowConfig(t)

	assert.Equal(t, "netflow", cfg.Receivers["netflow"]["scheme"])
	assert.Equal(t, 2055, cfg.Receivers["netflow"]["port"])
	assert.NotContains(t, strings.ToLower(readFile(t, "test/configs/gateway-flowlog.yaml")), "scheme: sflow")
	assert.Len(t, cfg.Receivers, 1)
}

func TestReferenceConfig_OnlyTheFlowPipelineUsesTheReceiverAndTheExporter(t *testing.T) {
	cfg := loadFlowConfig(t)

	for name, p := range cfg.Service.Pipelines {
		if name == "logs/flowlog" {
			assert.Equal(t, []string{"netflow"}, p.Receivers)
			assert.Equal(t, []string{"flowlog"}, p.Exporters)
			continue
		}
		assert.NotContains(t, p.Receivers, "netflow", name)
		assert.NotContains(t, p.Exporters, "flowlog", name)
	}
}

func TestReferenceConfig_HasABoundedQueueAndALimitedRetryAndNoPasswordValue(t *testing.T) {
	exp := loadFlowConfig(t).Exporters["flowlog"]

	assert.Positive(t, exp.SendingQueue.QueueSize)
	assert.NotEmpty(t, exp.RetryOnFailure.MaxElapsedTime)
	assert.Regexp(t, `^\$\{env:[A-Z_]+\}$`, exp.Password, "the password is an environment variable")
	assert.Contains(t, exp.Endpoint, ".invalid:", "the endpoint is a placeholder")
}

var forbiddenStatement = regexp.MustCompile(`\b(SELECT|CREATE|ALTER|DROP|DELETE|TRUNCATE|UPDATE|OPTIMIZE)\b`)

func TestSource_OnlyInsertStatementsAndNoOttlOrContribExporter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	var statements int
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		src := string(b)
		assert.NotRegexp(t, forbiddenStatement, src, f)
		assert.NotContains(t, src, "clickhouseexporter", f)
		assert.NotContains(t, strings.ToLower(src), "ottl", f)
		assert.NotContains(t, src, "transform", f)
		statements += strings.Count(src, "INSERT INTO")
	}
	assert.Equal(t, 1, statements, "one statement, an INSERT")
}

func TestDocs_StateTheTransportTheRestartGapTheGrantAndTheReason(t *testing.T) {
	docs := readFile(t, "docs/flowlog.md")

	for _, want := range []string{
		"UDP 2055", "otel-lg", "externalTrafficPolicy", "`Local`", "keeps the source address",
		"restart", "templates", "one replica", "otelcol_netflow_template_not_found",
		"60 columns", "`INSERT` on `flowlogs.flow_log`", "outside this repository", "sFlow is not decoded", "no earlier flow history",
		"writes only the OpenTelemetry log schema", "`bytes`", "`dscp`", "otelcol_flowlog_rows_dropped",
	} {
		assert.Contains(t, docs, want)
	}
}

var requirementID = regexp.MustCompile(`\b(FR|AC|OQ|EC|AD)-\d+\b|\bD-\d{3}\b`)

func TestDocs_AndReadmeHoldNoRequirementIdentifier(t *testing.T) {
	for _, rel := range []string{"docs/flowlog.md", "exporter/flowlogexporter/README.md", "test/configs/gateway-flowlog.yaml"} {
		assert.NotRegexp(t, requirementID, readFile(t, rel), rel)
	}
}

func TestChangelog_NamesTheExporterTheRestartGapAndTheOneReplica(t *testing.T) {
	log := readFile(t, "CHANGELOG.md")

	for _, want := range []string{"`flowlog` exporter", "restart", "one replica", "`netflow` receiver"} {
		assert.Contains(t, log, want)
	}
}
