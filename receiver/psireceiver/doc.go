// Package psireceiver implements a receiver that collects PSI (Pressure Stall Information)
// metrics from Linux systems via /proc/pressure/{cpu,memory,io}.
//
// PSI provides insights into resource contention - when tasks are stalled waiting
// for CPU, memory, or I/O. This is valuable for understanding system saturation
// beyond traditional utilization metrics.
//
// Metrics produced:
//   - system.pressure.cpu.some.{avg10,avg60,avg300}
//   - system.pressure.memory.some.{avg10,avg60,avg300}
//   - system.pressure.memory.full.{avg10,avg60,avg300}
//   - system.pressure.io.some.{avg10,avg60,avg300}
//   - system.pressure.io.full.{avg10,avg60,avg300}
//
// Note: CPU pressure only has "some" metrics (no "full" because CPU is always shared).
package psireceiver // import "github.com/vladistan/otelcol-custom/receiver/psireceiver"
