// Package assetdbprocessor implements a processor that enriches telemetry
// with data from an Asset Database.
//
// The processor loads a YAML file containing asset information and enriches
// telemetry (logs, metrics, traces) by looking up MAC addresses and adding
// corresponding hostname and location attributes.
//
// Configuration:
//
//	assetdb:
//	  database_path: /etc/otelcol/assets.yaml
//	  source_attribute: net.host.mac  # optional, defaults to net.host.mac
//
// YAML Schema:
//
//	assets:
//	  - mac: "aa:bb:cc:dd:ee:ff"
//	    hostname: "switch-core-01"
//	    location: "dc1-rack-a1"
//
// Enrichment adds:
//   - host.name: from hostname field
//   - host.geo.description: from location field
//
// MAC addresses are normalized to lowercase with colons (aa:bb:cc:dd:ee:ff)
// for consistent lookups regardless of input format.
package assetdbprocessor // import "github.com/vladistan/otelcol-custom/processor/assetdbprocessor"
