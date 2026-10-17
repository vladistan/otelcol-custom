// Modified by Vlad Korolev, 2026-10: import comment and unused scope name removed.
// Original: https://github.com/pmady/otel-gpu-receiver@ac756935ca040923d98af5a0d0547dca02a3b23b/metadata.go

package gpureceiver

import (
	"go.opentelemetry.io/collector/component"
)

var typeStr = component.MustNewType("gpu")

const (
	MetricsStability = component.StabilityLevelAlpha
)
