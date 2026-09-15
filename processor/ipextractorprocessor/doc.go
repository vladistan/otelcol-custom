// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

// Package ipextractorprocessor extracts IP addresses from log records.
//
// The processor searches log body and/or attributes for IP addresses in
// standard formats (IPv4 dotted decimal, optionally IPv6) and stores
// the extracted IP(s) in a configurable target attribute.
//
// Configuration:
//
//	processors:
//	  ipextractor:
//	    target_attribute: client.ip    # Where to store extracted IP
//	    search_body: true              # Search log body
//	    search_attributes: true        # Search log attributes
//	    extract_all: false             # First IP only (vs comma-separated)
//	    extract_ipv6: false            # Also extract IPv6 addresses
//
// This processor is designed to work alongside macextractorprocessor,
// allowing extraction of both client.mac and client.ip from logs like
// DHCP messages.
package ipextractorprocessor
