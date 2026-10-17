// Modified by Vlad Korolev, 2026-10: import comment removed; package doc rewritten for lazy NVML init.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/doc.go

// Package gpureceiver implements an OpenTelemetry Collector receiver that
// collects GPU metrics from NVIDIA GPUs through NVML. NVML is opened on the
// first scrape, so hosts without NVIDIA hardware start normally.
package gpureceiver
