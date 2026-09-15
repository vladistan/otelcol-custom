// Package normalizedloadprocessor implements a processor that creates normalized
// load average metrics (load / CPU count) from existing load average metrics.
//
// This allows comparison of load across systems with different CPU counts.
// A normalized load of 1.0 means the system is at 100% CPU utilization.
//
// Metrics produced:
//   - system.cpu.load_average.normalized.1m
//   - system.cpu.load_average.normalized.5m
//   - system.cpu.load_average.normalized.15m
package normalizedloadprocessor // import "github.com/vladistan/otelcol-custom/processor/normalizedloadprocessor"
