// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package filterlogprocessor parses pfSense/OPNsense filterlog entries.
//
// The filterlog service on pfSense/OPNsense outputs CSV-formatted packet filter
// logs. This processor parses those logs and extracts structured fields.
//
// # Input Format
//
// Filterlog entries are comma-separated with the following fields:
//
//	rule,subrule,anchor,tracker,interface,reason,action,direction,ipversion,
//	tos,ecn,ttl,id,offset,flags,proto_id,proto_name,length,src_ip,dst_ip,
//	src_port,dst_port,datalen,tcp_flags,seq,ack,window,urg,options
//
// Example:
//
//	11,,,02f4bab031b57d1e30553ce08e0ec131,em1,match,block,in,4,0x0,,244,13943,0,none,6,tcp,44,91.148.190.150,24.2.177.2,50406,47574,0,S,2500738723,,1025,,mss
//
// # Output Attributes
//
// OTEL Semantic Conventions:
//   - network.interface.name: Interface name (em1, igb0)
//   - network.transport: Protocol (tcp, udp, icmp)
//   - network.type: IP version (ipv4, ipv6)
//   - source.port: Source port number
//   - destination.ip: Destination IP address
//   - destination.port: Destination port number
//   - event.action: Firewall action (block, pass)
//
// Packet Filter specific (pf.* namespace):
//   - pf.rule.id: Rule number that matched
//   - pf.rule.tracker: Rule tracker UUID
//   - pf.direction: Packet direction (inbound, outbound)
//   - pf.reason: Match reason (match, bad-offset, etc.)
//   - pf.tcp.flags: TCP flags (S, SA, A, F, R, P, U)
//   - pf.ip.ttl: IP Time-To-Live value
//
// # Clean Message Format
//
// The body is rewritten to a human-readable format:
//
//	BLOCK em1 inbound tcp 91.148.190.150:50406 → 24.2.177.2:47574 [SYN] (rule 11)
//
// # Configuration
//
//	processors:
//	  filterlog:
//	    source_attribute: "original_message"  # Where to find raw message
//	    rewrite_messages: true                # Rewrite body to clean format
package filterlogprocessor
