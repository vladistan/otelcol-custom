// Package ipsecprocessor implements a processor that parses strongSwan/IPSec
// logs for VPN monitoring.
//
// Supported log formats:
//   - charon: 14[IKE] IKE_SA vpn-client[123] established
//   - charon: 14[IKE] received AUTH_FAILED notify error
//
// Parsed fields:
//   - ipsec.daemon, ipsec.thread_id, ipsec.subsystem
//   - ipsec.connection_name, ipsec.event_type, ipsec.peer
package ipsecprocessor // import "github.com/vladistan/otelcol-custom/processor/ipsecprocessor"

// TODO: Implement in Phase 3.6
