// Custom main.go for otelcol-custom with Sentry integration
// This file replaces the OCB-generated main.go to add error tracking
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	envprovider "go.opentelemetry.io/collector/confmap/provider/envprovider"
	fileprovider "go.opentelemetry.io/collector/confmap/provider/fileprovider"
	httpprovider "go.opentelemetry.io/collector/confmap/provider/httpprovider"
	httpsprovider "go.opentelemetry.io/collector/confmap/provider/httpsprovider"
	yamlprovider "go.opentelemetry.io/collector/confmap/provider/yamlprovider"
	"go.opentelemetry.io/collector/otelcol"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	// Custom processors
	"github.com/vladistan/otelcol-custom/processor/resourceversionprocessor"

	// Custom stanza operators - import for side effects (registers operator via init())
	_ "github.com/vladistan/otelcol-custom/stanza/operator/transformer/ansistrip"
	_ "github.com/vladistan/otelcol-custom/stanza/operator/transformer/arraytostring"
)

// Build-time variables (set via -ldflags)
var (
	version     = "dev"
	commit      = "unknown"
	serviceName = "otelcol-custom" // Override per profile: otelcol-edge, otelcol-scrape, otelcol-gateway
)

// Sentry configuration - hardcoded public DSN for this collector.
// Set OTELCOL_CUSTOM_DISABLE_TELEMETRY to any non-empty value to opt out.
const (
	SentryDSN = "https://b7dc01ba283fe00b2ab6a5882a172e12@o4508594232426496.ingest.us.sentry.io/4512084966178816"
)

// serviceDescription returns a description based on service name
func serviceDescription() string {
	switch serviceName {
	case "otelcol-edge":
		return "Edge collector for VMs and bare metal hosts"
	case "otelcol-scrape":
		return "Cloud source scraping collector (AWS, GCP, Azure)"
	case "otelcol-gateway":
		return "Gateway collector with custom processors"
	default:
		return "Custom OpenTelemetry Collector distribution with extended log-parsing processors"
	}
}

// sentryZapHook creates a zap hook that sends error-level logs to Sentry
// This captures collector internal errors that wouldn't otherwise reach Sentry
func sentryZapHook(entry zapcore.Entry) error {
	// Only capture error and higher severity
	if entry.Level < zapcore.ErrorLevel {
		return nil
	}

	// Skip known noisy errors
	if filterNoiseErrors(entry.Message) {
		return nil
	}

	// Create Sentry event from zap entry
	event := sentry.NewEvent()
	event.Level = zapLevelToSentry(entry.Level)
	event.Message = entry.Message
	event.Logger = entry.LoggerName
	event.Timestamp = entry.Time

	// Add context as extra data
	event.Extra["caller"] = entry.Caller.String()
	if entry.Stack != "" {
		event.Extra["stack"] = entry.Stack
	}

	// Capture the event
	sentry.CaptureEvent(event)

	return nil
}

// zapLevelToSentry converts zap log level to Sentry level
func zapLevelToSentry(level zapcore.Level) sentry.Level {
	switch level {
	case zapcore.DebugLevel:
		return sentry.LevelDebug
	case zapcore.InfoLevel:
		return sentry.LevelInfo
	case zapcore.WarnLevel:
		return sentry.LevelWarning
	case zapcore.ErrorLevel:
		return sentry.LevelError
	case zapcore.DPanicLevel, zapcore.PanicLevel, zapcore.FatalLevel:
		return sentry.LevelFatal
	default:
		return sentry.LevelInfo
	}
}

// sentryCoreWrapper wraps a zapcore.Core to capture errors to Sentry
// This captures the full log entry including fields
type sentryCoreWrapper struct {
	zapcore.Core
	fields []zapcore.Field
}

func newSentryCoreWrapper(core zapcore.Core) zapcore.Core {
	return &sentryCoreWrapper{Core: core}
}

func (c *sentryCoreWrapper) With(fields []zapcore.Field) zapcore.Core {
	return &sentryCoreWrapper{
		Core:   c.Core.With(fields),
		fields: append(c.fields, fields...),
	}
}

func (c *sentryCoreWrapper) Check(entry zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(entry.Level) {
		return ce.AddCore(entry, c)
	}
	return ce
}

func (c *sentryCoreWrapper) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// Write to underlying core first
	err := c.Core.Write(entry, fields)

	// Capture errors to Sentry (skip known noisy errors)
	if entry.Level >= zapcore.ErrorLevel && !filterNoiseErrors(entry.Message) {
		event := sentry.NewEvent()
		event.Level = zapLevelToSentry(entry.Level)
		event.Message = entry.Message
		event.Logger = entry.LoggerName
		event.Timestamp = entry.Time

		// Add caller info
		if entry.Caller.Defined {
			event.Extra["caller"] = entry.Caller.String()
		}

		// Add stack trace if available
		if entry.Stack != "" {
			event.Extra["stack"] = entry.Stack
		}

		// Add all fields as extra data
		allFields := append(c.fields, fields...)
		for _, field := range allFields {
			// Convert field to string representation
			switch field.Type {
			case zapcore.StringType:
				event.Extra[field.Key] = field.String
			case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
				event.Extra[field.Key] = field.Integer
			case zapcore.ErrorType:
				if field.Interface != nil {
					event.Extra[field.Key] = field.Interface.(error).Error()
					// If this is the main error, use it for grouping
					if field.Key == "error" {
						event.Exception = []sentry.Exception{{
							Type:  "Error",
							Value: field.Interface.(error).Error(),
						}}
					}
				}
			default:
				if field.Interface != nil {
					event.Extra[field.Key] = fmt.Sprintf("%v", field.Interface)
				} else if field.String != "" {
					event.Extra[field.Key] = field.String
				}
			}
		}

		// Extract component info for better grouping
		if kind, ok := event.Extra["kind"].(string); ok {
			event.Tags["component.kind"] = kind
		}
		if name, ok := event.Extra["name"].(string); ok {
			event.Tags["component.name"] = name
		}
		if dataType, ok := event.Extra["data_type"].(string); ok {
			event.Tags["data_type"] = dataType
		}

		// Set fingerprint for better grouping (group by message and caller)
		event.Fingerprint = []string{
			entry.Message,
			entry.Caller.File,
		}

		sentry.CaptureEvent(event)
	}

	return err
}

func (c *sentryCoreWrapper) Sync() error {
	// Flush Sentry before syncing underlying core
	sentry.Flush(2 * time.Second)
	return c.Core.Sync()
}

// createSentryLoggingOptions creates zap options that integrate with Sentry
func createSentryLoggingOptions() []zap.Option {
	return []zap.Option{
		zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return newSentryCoreWrapper(core)
		}),
		// Also add simple hook as backup
		zap.Hooks(sentryZapHook),
	}
}

// filterNoiseErrors checks if an error message is a known noisy error that shouldn't be sent to Sentry
func filterNoiseErrors(msg string) bool {
	noisePatterns := []string{
		// Add patterns for known noisy errors that don't need Sentry alerts
		"context canceled",
		"connection reset by peer",
		"regex pattern does not match",           // Expected when log format doesn't match parser
		"no such file or directory",              // Filesystem paths that don't exist or are inaccessible
		"failed to read usage",                   // Hostmetrics filesystem scraper permission errors
		"flushing combined logs",                 // Recombine operator flush on non-matching entries
		"error reading process name for pid",     // Short-lived processes in hostmetrics
		"signal: terminated",                     // Collector restart/shutdown
		"journalctl command exited",              // Journald receiver restart
		"failed to process entry",                // Stanza operator entry processing (handled by on_error: send)
		"failed to write entry",                  // Stanza operator write error (cascades from regex failures)
		"does not contain the combine_field",     // Entries without MESSAGE field (kernel messages)
		"does not contain the source_identifier", // Entries without _PID (kernel messages)
		"may be pooled with other sources",       // Warning about entries without source_identifier
	}
	for _, pattern := range noisePatterns {
		if strings.Contains(strings.ToLower(msg), pattern) {
			return true
		}
	}
	return false
}

func main() {
	// Set collector version for resourceversion processor
	resourceversionprocessor.SetCollectorVersion(version)

	// Get environment from env var, default to "production"
	environment := os.Getenv("SENTRY_ENVIRONMENT")
	if environment == "" {
		environment = "production"
	}

	// Initialize Sentry for error tracking. Telemetry is on by default;
	// set OTELCOL_CUSTOM_DISABLE_TELEMETRY to any non-empty value to opt out.
	if SentryDSN != "" && SentryDSN != "PLACEHOLDER_DSN" && os.Getenv("OTELCOL_CUSTOM_DISABLE_TELEMETRY") == "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              SentryDSN,
			Environment:      environment,
			Release:          serviceName + "@" + version,
			Debug:            os.Getenv("SENTRY_DEBUG") == "true",
			EnableTracing:    false, // Collector handles its own tracing
			AttachStacktrace: true,
		})
		if err != nil {
			log.Printf("WARNING: Failed to initialize Sentry: %v", err)
		} else {
			log.Printf("Sentry initialized: service=%s, environment=%s, release=%s@%s", serviceName, environment, serviceName, version)
			// Send startup event to Sentry for verification
			hostname, _ := os.Hostname()
			sentry.CaptureMessage(fmt.Sprintf("%s started on %s", serviceName, hostname))
		}
		defer sentry.Flush(2 * time.Second)
	}

	// Set up panic recovery to report to Sentry
	defer func() {
		if r := recover(); r != nil {
			sentry.CurrentHub().Recover(r)
			sentry.Flush(2 * time.Second)
			panic(r) // Re-panic after reporting
		}
	}()

	// Build collector settings
	info := component.BuildInfo{
		Command:     serviceName,
		Description: serviceDescription(),
		Version:     version,
	}

	set := otelcol.CollectorSettings{
		BuildInfo: info,
		Factories: components,
		// Add Sentry logging integration to capture error-level logs
		LoggingOptions: createSentryLoggingOptions(),
		ConfigProviderSettings: otelcol.ConfigProviderSettings{
			ResolverSettings: confmap.ResolverSettings{
				ProviderFactories: []confmap.ProviderFactory{
					envprovider.NewFactory(),
					fileprovider.NewFactory(),
					httpprovider.NewFactory(),
					httpsprovider.NewFactory(),
					yamlprovider.NewFactory(),
				},
			},
		},
	}

	// Run the collector
	if err := run(set); err != nil {
		sentry.CaptureException(err)
		sentry.Flush(2 * time.Second)
		log.Fatalf("Collector failed: %v", err)
	}
}

func runInteractive(params otelcol.CollectorSettings) error {
	cmd := otelcol.NewCommand(params)
	if err := cmd.Execute(); err != nil {
		log.Fatalf("collector server run finished with error: %v", err)
	}
	return nil
}
