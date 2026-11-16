// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package main

import "sort"

// The nineteen read-only DCMD opcodes the tool may send. Values come from the FreeBSD header
// sys/dev/mfi/mfireg.h; LD_LIST_QUERY is from the Linux driver header.
const (
	opCtrlGetInfo        uint32 = 0x01010000
	opCtrlGetProps       uint32 = 0x01020100
	opEventGetInfo       uint32 = 0x01040100
	opEventGet           uint32 = 0x01040300
	opPRGetStatus        uint32 = 0x01070100
	opPRGetProperties    uint32 = 0x01070200
	opPDGetList          uint32 = 0x02010000
	opPDListQuery        uint32 = 0x02010100
	opPDGetInfo          uint32 = 0x02020000
	opPDGetProgress      uint32 = 0x02060000
	opLDGetList          uint32 = 0x03010000
	opLDListQuery        uint32 = 0x03010100
	opLDGetInfo          uint32 = 0x03020000
	opLDGetProp          uint32 = 0x03030000
	opCfgRead            uint32 = 0x04010000
	opBBUGetStatus       uint32 = 0x05010000
	opBBUGetCapacityInfo uint32 = 0x05020000
	opBBUGetDesignInfo   uint32 = 0x05030000
	opBBUGetProp         uint32 = 0x05050100
)

// allowed maps each opcode a frame may carry to its name; every other opcode is refused.
var allowed = map[uint32]string{
	opCtrlGetInfo:        "CTRL_GETINFO",
	opCtrlGetProps:       "CTRL_GET_PROPS",
	opEventGetInfo:       "CTRL_EVENT_GETINFO",
	opEventGet:           "CTRL_EVENT_GET",
	opPRGetStatus:        "PR_GET_STATUS",
	opPRGetProperties:    "PR_GET_PROPERTIES",
	opPDGetList:          "PD_GET_LIST",
	opPDListQuery:        "PD_LIST_QUERY",
	opPDGetInfo:          "PD_GET_INFO",
	opPDGetProgress:      "PD_GET_PROGRESS",
	opLDGetList:          "LD_GET_LIST",
	opLDListQuery:        "LD_LIST_QUERY",
	opLDGetInfo:          "LD_GET_INFO",
	opLDGetProp:          "LD_GET_PROP",
	opCfgRead:            "CFG_READ",
	opBBUGetStatus:       "BBU_GET_STATUS",
	opBBUGetCapacityInfo: "BBU_GET_CAPACITY_INFO",
	opBBUGetDesignInfo:   "BBU_GET_DESIGN_INFO",
	opBBUGetProp:         "BBU_GET_PROP",
}

func allowedOpcodes() []uint32 {
	out := make([]uint32, 0, len(allowed))
	for op := range allowed {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
