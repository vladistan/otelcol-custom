// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sqlserver_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/sqlserverreceiver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/provider/envprovider"
	"go.opentelemetry.io/collector/confmap/provider/fileprovider"
	"go.opentelemetry.io/collector/confmap/provider/yamlprovider"
	"go.yaml.in/yaml/v3"
)

const (
	samplePath = "../configs/windows-sqlserver.yaml"
	docPath    = "../../docs/sqlserver-windows.md"
	// The password path as the sample writes it; a test swaps it for a fixture file.
	samplePasswordPath = "C:/ProgramData/otelcol-edge/secrets/sqlserver-password" // pragma: allowlist secret
	fixturePassword    = "Fixture-Pass-123"                                       // pragma: allowlist secret
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// rawSample is the sample as written: references are not resolved.
func rawSample(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(readFile(t, samplePath)), &doc))
	return doc
}

func section(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := doc
	for _, key := range path {
		next, ok := cur[key].(map[string]any)
		require.True(t, ok, "missing %s in %v", key, path)
		cur = next
	}
	return cur
}

// resolveSample resolves ${file:...} and ${env:...} in the sample with the given providers, reading the
// password from a fixture file.
func resolveSample(t *testing.T, factories ...confmap.ProviderFactory) (map[string]any, error) {
	t.Helper()
	pwFile := filepath.Join(t.TempDir(), "sqlserver-password")
	require.NoError(t, os.WriteFile(pwFile, []byte(fixturePassword+"\n"), 0o600))
	text := strings.ReplaceAll(readFile(t, samplePath), samplePasswordPath, filepath.ToSlash(pwFile))
	factories = append(factories, yamlprovider.NewFactory())
	resolver, err := confmap.NewResolver(confmap.ResolverSettings{URIs: []string{"yaml:" + text}, ProviderFactories: factories})
	require.NoError(t, err)
	conf, err := resolver.Resolve(context.Background())
	if err != nil {
		return nil, err
	}
	return conf.ToStringMap(), nil
}

func unmarshal(t *testing.T, cfg component.Config, from map[string]any) {
	t.Helper()
	require.NoError(t, confmap.NewFromStringMap(from).Unmarshal(cfg))
}

func TestSample_TheSQLServerReceiverLoadsWithBothQueryEventsOnAndNoQueryPlanEvent(t *testing.T) {
	conf, err := resolveSample(t, fileprovider.NewFactory(), envprovider.NewFactory())
	require.NoError(t, err)
	factory := sqlserverreceiver.NewFactory()
	cfg := factory.CreateDefaultConfig()
	unmarshal(t, cfg, section(t, conf, "receivers", "sqlserver"))

	require.NoError(t, cfg.(interface{ Validate() error }).Validate())
	c := cfg.(*sqlserverreceiver.Config)
	assert.True(t, c.LogsBuilderConfig.Events.DbServerTopQuery.Enabled, "slow queries are marked present")
	assert.True(t, c.LogsBuilderConfig.Events.DbServerQuerySample.Enabled)
	assert.False(t, c.LogsBuilderConfig.Events.DbServerQueryPlan.Enabled, "plan text stays off")
	assert.False(t, c.LogsBuilderConfig.Events.DbServerProcedureDefinition.Enabled, "procedure text stays off")
	assert.True(t, c.MetricsBuilderConfig.Metrics.SqlserverOsWaitDuration.Enabled, "waits")
	assert.NotContains(t, c.DataSource, "${")
	dsn, err := msdsn.Parse(c.DataSource)
	require.NoError(t, err, "the driver accepts the connection string")
	assert.Equal(t, fixturePassword, dsn.Password, "the password comes from the file; the driver trims its newline")
	assert.Equal(t, "localhost", dsn.Host)
	assert.Equal(t, uint64(1433), dsn.Port)
	assert.True(t, dsn.TrustServerCertificate)
	assert.Equal(t, msdsn.Encryption(msdsn.EncryptionRequired), dsn.Encryption)
}

func TestSample_AStaleEnabledKeyUnderTopQueryCollectionIsRejected(t *testing.T) {
	conf, err := resolveSample(t, fileprovider.NewFactory(), envprovider.NewFactory())
	require.NoError(t, err)
	sec := section(t, conf, "receivers", "sqlserver")
	sec["top_query_collection"] = map[string]any{"enabled": true}
	cfg := sqlserverreceiver.NewFactory().CreateDefaultConfig()
	assert.Error(t, confmap.NewFromStringMap(sec).Unmarshal(cfg), "contrib v0.164.1 has no enabled key there; events switch the collections on")
}

func TestSample_TheFileProviderResolvesThePasswordAndABuildWithoutItFails(t *testing.T) {
	_, err := resolveSample(t, envprovider.NewFactory())
	require.Error(t, err)
	assert.Regexp(t, `(?i)scheme.*\bfile\b|\bfile\b.*(scheme|provider)`, err.Error(), "an unknown provider names the scheme")
}

func TestSample_ThePerformanceCountersUseThePrefixOfTheNamedInstance(t *testing.T) {
	counters := readFile(t, samplePath)
	assert.Contains(t, counters, "object: 'MSSQL$")
	assert.Contains(t, counters, "SQL Statistics")
	assert.Contains(t, counters, "General Statistics")
	assert.Contains(t, counters, "Batch Requests/sec")
	assert.Contains(t, counters, "Logins/sec")
}

func utf16le(text string, bom bool) []byte {
	units := utf16.Encode([]rune(text))
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func TestDocs_TheGuideStatesTheLoginTheSecretAndTheLimits(t *testing.T) {
	doc := readFile(t, docPath)
	for _, want := range []string{
		// grants and login
		"VIEW SERVER STATE", "VIEW SERVER PERFORMANCE STATE", "VIEW ANY DATABASE", "VIEW ANY DEFINITION", "CONNECT SQL",
		"read-only", "7 of 7 databases", "needs no new grant",
		// authentication and stability
		"Upstream documents no Windows (integrated) authentication", "SQL login",
		"development stability",
		// query text and retention
		"Query text is off", "Retention of these two streams is set by the owners of the infrastructure that stores them",
		// counters
		"configuration only", "no custom code", "MSSQL$",
		// password file
		"readable only by Administrators and SYSTEM", "no password in the service environment", "static", "no rotation",
		"${file:",
		// encryption
		"ForceEncryption", "TrustServerCertificate",
		// scope and version
		"one host", "SQL Server 2025 Express", "major version 17", "nothing from this instance is collected before ",
		"ClickHouse",
		// allowlist, error log and providers
		"keeps only these attributes", "ERRORLOG.1", "not read", "byte order mark", "yaml:", "https:",
	} {
		assert.Contains(t, doc, want)
	}
}

// changelogSection returns the text of the section that starts with heading, up to the next release heading.
func changelogSection(t *testing.T, log, heading string) string {
	t.Helper()
	start := strings.Index(log, heading)
	require.NotEqual(t, -1, start, "the changelog has a %q section", heading)
	rest := log[start+3:]
	if end := strings.Index(rest, "\n## ["); end != -1 {
		rest = rest[:end]
	}
	return rest
}

func TestChangelog_HasAGrowthRowForSQLServer(t *testing.T) {
	log := readFile(t, "../../CHANGELOG.md")
	version := strings.TrimSpace(readFile(t, "../../VERSION"))
	head := changelogSection(t, log, "## [Unreleased]") + changelogSection(t, log, "## ["+version+"]")
	assert.Contains(t, head, "`sqlserver`")
	assert.Regexp(t, `otelcol-edge-\d+\.\d+\.\d+-windows-amd64\.exe`, head)
}
