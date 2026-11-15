// Package openvpnstatusreceiver polls an OpenVPN status file and emits one log
// record per connected client for each new snapshot.
//
// OpenVPN rewrites the file in place, so a snapshot counts only when its END
// line is present, and its Updated (v1) or TIME (v2, v3) value is its identity:
// an unchanged value emits nothing.
package openvpnstatusreceiver // import "github.com/vladistan/otelcol-custom/receiver/openvpnstatusreceiver"
