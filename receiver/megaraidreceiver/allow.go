// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"fmt"
	"sort"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// The nineteen read-only DCMD opcodes the receiver may send. Values come from the FreeBSD header
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

// denyTable names every other opcode of mfireg.h. None is ever sent: each one changes the array,
// the drives, the battery, the firmware or the availability of the controller, or blocks inside
// the driver while it holds the per-controller semaphore. The allow-list is what stops a frame;
// this table documents what the list keeps out, and a test checks that it is complete.
var denyTable = map[uint32]string{
	0x0100e100: "CTRL_MFI_HOST_MEM_ALLOC",
	0x010e0201: "CTRL_MFC_DEFAULTS_GET",
	0x010e0202: "CTRL_MFC_DEFAULTS_SET",
	0x01101000: "CTRL_FLUSHCACHE",
	0x01020200: "CTRL_SET_PROPS",
	0x01050000: "CTRL_SHUTDOWN",
	0x01040500: "CTRL_EVENT_WAIT",
	0x01070300: "PR_SET_PROPERTIES",
	0x01070400: "PR_START",
	0x01070500: "PR_STOP",
	0x01080201: "TIME_SECS_GET",
	0x010f0100: "FLASH_FW_OPEN",
	0x010f0200: "FLASH_FW_DOWNLOAD",
	0x010f0300: "FLASH_FW_FLASH",
	0x010f0400: "FLASH_FW_CLOSE",
	0x02030100: "PD_STATE_SET",
	0x02040100: "PD_REBUILD_START",
	0x02040200: "PD_REBUILD_ABORT",
	0x02050100: "PD_CLEAR_START",
	0x02050200: "PD_CLEAR_ABORT",
	0x02070100: "PD_LOCATE_START",
	0x02070200: "PD_LOCATE_STOP",
	0x0300e101: "LD_MAP_GET_INFO",
	0x0300e102: "LD_SYNC",
	0x03040000: "LD_SET_PROP",
	0x03060100: "LD_INIT_START",
	0x03090000: "LD_DELETE",
	0x04020000: "CFG_ADD",
	0x04030000: "CFG_CLEAR",
	0x04040000: "CFG_MAKE_SPARE",
	0x04050000: "CFG_REMOVE_SPARE",
	0x04060100: "CFG_FOREIGN_SCAN",
	0x04060200: "CFG_FOREIGN_DISPLAY",
	0x04060300: "CFG_FOREIGN_PREVIEW",
	0x04060400: "CFG_FOREIGN_IMPORT",
	0x04060500: "CFG_FOREIGN_CLEAR",
	0x05040000: "BBU_START_LEARN",
	0x05050200: "BBU_SET_PROP",
	0x08000000: "CLUSTER",
	0x08010100: "CLUSTER_RESET_ALL",
	0x08010200: "CLUSTER_RESET_LD",
}

func allowedOpcodes() []uint32 {
	out := make([]uint32, 0, len(allowed))
	for op := range allowed {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SCSI and ATA bytes of the pass-through CDBs. Only INQUIRY(6), ATA PASS-THROUGH(16) carrying
// one of the read-only commands of smartdecode.CheckATA, and LOG SENSE(10) of one of the pages
// of smartdecode.CheckSCSI pass.
const (
	scsiInquiry        = 0x12
	scsiLogSense       = 0x4D
	logSenseLen        = 10
	logSenseCumulative = 0x40 // byte 2: page control 01, the cumulative values
	pageCodeMask       = 0x3F
	ataPassThrough16   = 0x85
	protoNonData       = 3
	protoPIOIn         = 4
	flagPIOIn          = 0x0E // T_DIR from the device, BYTE_BLOCK blocks, T_LENGTH in the sector count
	flagCheckCond      = 0x20
	extendBit          = 0x01 // byte 1: the command is a 48-bit command
	deviceLBA          = 0x40
	inquiryMin         = 36
	inquiryMax         = 96
)

// ataCDB builds the 16 byte ATA PASS-THROUGH CDB of a command.
func ataCDB(cmd smartdecode.ATACommand) [maxCDBLen]byte {
	var cdb [maxCDBLen]byte
	cdb[0] = ataPassThrough16
	if cmd.Protocol == smartdecode.ProtoPIOIn {
		cdb[1] = protoPIOIn << 1
		cdb[2] = flagPIOIn
	} else {
		cdb[1] = protoNonData << 1
	}
	if cmd.WantRegisters {
		cdb[2] |= flagCheckCond
	}
	if cmd.Is48Bit() {
		cdb[1] |= extendBit
	}
	cdb[4] = cmd.Features
	cdb[6] = cmd.SectorCount
	cdb[8] = cmd.LBALow
	cdb[10] = cmd.LBAMid
	cdb[12] = cmd.LBAHigh
	cdb[13] = cmd.Device | deviceLBA
	cdb[14] = cmd.Command
	return cdb
}

// logSenseCDB builds the 10 byte LOG SENSE CDB of a command: the cumulative values of one page,
// subpage 0, from the first parameter, for the allocation length of the command.
func logSenseCDB(cmd smartdecode.SCSICommand) [maxCDBLen]byte {
	var cdb [maxCDBLen]byte
	cdb[0] = scsiLogSense
	cdb[2] = logSenseCumulative | cmd.Page&pageCodeMask
	cdb[7] = byte(cmd.DataLen >> 8) //nolint:gosec // checkLogSenseCDB bounds it
	cdb[8] = byte(cmd.DataLen)      //nolint:gosec // checkLogSenseCDB bounds it
	return cdb
}

// checkLogSenseCDB passes LOG SENSE only with every field the receiver does not use at zero, the
// cumulative page control, and a page and length that smartdecode.CheckSCSI accepts.
func checkLogSenseCDB(cdb []byte, cdbLen, dataLen int) error {
	if cdbLen != logSenseLen {
		return fmt.Errorf("LOG SENSE has length %d", cdbLen)
	}
	if cdb[1] != 0 || cdb[2]&^pageCodeMask != logSenseCumulative || cdb[3] != 0 || cdb[4] != 0 || cdb[5] != 0 || cdb[6] != 0 ||
		cdb[9] != 0 || !allZero(cdb[logSenseLen:]) {
		return fmt.Errorf("LOG SENSE carries fields other than the page and the length")
	}
	if int(cdb[7])<<8|int(cdb[8]) != dataLen {
		return fmt.Errorf("LOG SENSE allocation length %d does not match the buffer of %d", int(cdb[7])<<8|int(cdb[8]), dataLen)
	}
	return smartdecode.CheckSCSI(smartdecode.SCSICommand{Page: cdb[2] & pageCodeMask, DataLen: dataLen})
}

// inquiryCDB builds the 6 byte standard INQUIRY CDB for n bytes of answer.
func inquiryCDB(n int) [maxCDBLen]byte {
	var cdb [maxCDBLen]byte
	cdb[0] = scsiInquiry
	cdb[4] = byte(n) //nolint:gosec // checkPassCDB bounds it
	return cdb
}

// checkPassCDB returns nil only for the CDBs the receiver sends: standard INQUIRY, and ATA PASS-THROUGH(16)
// of IDENTIFY DEVICE, SMART READ DATA, SMART READ THRESHOLDS, SMART RETURN STATUS, CHECK POWER MODE
// and READ LOG EXT of the Device Statistics log, in the read direction.
func checkPassCDB(cdb []byte, cdbLen, dataLen int) error {
	if len(cdb) != maxCDBLen {
		return fmt.Errorf("cdb buffer is %d bytes, want %d", len(cdb), maxCDBLen)
	}
	switch cdb[0] {
	case scsiInquiry:
		if cdbLen != 6 || cdb[1] != 0 || cdb[2] != 0 || cdb[3] != 0 || cdb[5] != 0 || !allZero(cdb[6:]) {
			return fmt.Errorf("INQUIRY carries fields other than the length")
		}
		if int(cdb[4]) != dataLen || dataLen < inquiryMin || dataLen > inquiryMax {
			return fmt.Errorf("INQUIRY length %d with a buffer of %d is outside %d..%d", cdb[4], dataLen, inquiryMin, inquiryMax)
		}
		return nil
	case ataPassThrough16:
		return checkATACDB(cdb, cdbLen, dataLen)
	case scsiLogSense:
		return checkLogSenseCDB(cdb, cdbLen, dataLen)
	}
	return fmt.Errorf("SCSI opcode %#02x is not on the CDB allow-list", cdb[0])
}

func checkATACDB(cdb []byte, cdbLen, dataLen int) error {
	if cdbLen != maxCDBLen {
		return fmt.Errorf("ATA PASS-THROUGH(16) has length %d", cdbLen)
	}
	if cdb[3] != 0 || cdb[5] != 0 || cdb[7] != 0 || cdb[9] != 0 || cdb[11] != 0 || cdb[15] != 0 || cdb[13] != deviceLBA {
		return fmt.Errorf("ATA PASS-THROUGH carries fields the allowed commands do not use")
	}
	cmd := smartdecode.ATACommand{
		Command: cdb[14], Features: cdb[4], SectorCount: cdb[6],
		LBALow: cdb[8], LBAMid: cdb[10], LBAHigh: cdb[12],
		DataLen: dataLen, WantRegisters: cdb[2]&flagCheckCond != 0,
	}
	// Byte 1 is the protocol with the EXTEND bit; byte 2 is the direction flags with CK_COND.
	proto, extend, flags := cdb[1]&^extendBit, cdb[1]&extendBit != 0, cdb[2]&^flagCheckCond
	switch {
	case proto == protoPIOIn<<1 && flags == flagPIOIn:
		cmd.Protocol = smartdecode.ProtoPIOIn
	case proto == protoNonData<<1 && flags == 0:
		cmd.Protocol = smartdecode.ProtoNonData
	default:
		return fmt.Errorf("ATA protocol byte %#02x flags %#02x is not allowed", cdb[1], cdb[2])
	}
	// CheckATA pins every field of the six commands: command, features, registers, data length,
	// protocol and the check-condition flag, so the direction always matches the command.
	if err := smartdecode.CheckATA(cmd); err != nil {
		return err
	}
	if extend != cmd.Is48Bit() {
		return fmt.Errorf("ATA command %#02x has the EXTEND bit %v, want %v", cmd.Command, extend, cmd.Is48Bit())
	}
	return nil
}
