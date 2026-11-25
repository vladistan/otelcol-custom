// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package flowlogexporter

// protocolNumbers maps the IANA protocol names the receiver writes to their numbers.
var protocolNumbers = map[string]int32{
	"hopopt": 0, "icmp": 1, "igmp": 2, "ggp": 3, "ipv4": 4, "st": 5, "tcp": 6, "cbt": 7, "egp": 8, "igp": 9,
	"pup": 12, "udp": 17, "xns-idp": 22, "rdp": 27, "iso-tp4": 29, "dccp": 33, "xtp": 36, "ddp": 37,
	"ipv6": 41, "ipv6-route": 43, "ipv6-frag": 44, "idrp": 45, "rsvp": 46, "gre": 47, "dsr": 48, "esp": 50,
	"ah": 51, "narp": 54, "ipv6-icmp": 58, "ipv6-nonxt": 59, "ipv6-opts": 60, "eigrp": 88, "ospfigp": 89,
	"ax.25": 93, "ipip": 94, "etherip": 97, "encap": 98, "pim": 103, "ipcomp": 108, "vrrp": 112, "l2tp": 115,
	"stp": 118, "ptp": 123, "isis over ipv4": 124, "sctp": 132, "fc": 133, "mobility header": 135,
	"udplite": 136, "mpls-in-ip": 137, "manet": 138, "hip": 139, "shim6": 140, "wesp": 141, "rohc": 142,
	"ethernet": 143, "nsh": 145,
}
