// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import "strconv"

// attributeInfo names an ATA SMART attribute and, for the counters of data moved, the bytes in one
// unit of its raw value.
type attributeInfo struct {
	name        string
	ioUnitBytes uint64
}

var attributeTable = map[uint8]attributeInfo{
	1:   {name: "Raw_Read_Error_Rate"},
	3:   {name: "Spin_Up_Time"},
	4:   {name: "Start_Stop_Count"},
	5:   {name: "Reallocated_Sector_Ct"},
	7:   {name: "Seek_Error_Rate"},
	9:   {name: "Power_On_Hours"},
	10:  {name: "Spin_Retry_Count"},
	12:  {name: "Power_Cycle_Count"},
	177: {name: "Wear_Leveling_Count"},
	179: {name: "Used_Rsvd_Blk_Cnt_Tot"},
	181: {name: "Program_Fail_Cnt_Total"},
	182: {name: "Erase_Fail_Count_Total"},
	183: {name: "Runtime_Bad_Block"},
	187: {name: "Reported_Uncorrect"},
	188: {name: "Command_Timeout"},
	190: {name: "Airflow_Temperature_Cel"},
	191: {name: "G-Sense_Error_Rate"},
	192: {name: "Power-Off_Retract_Count"},
	193: {name: "Load_Cycle_Count"},
	194: {name: "Temperature_Celsius"},
	195: {name: "Hardware_ECC_Recovered"},
	196: {name: "Reallocated_Event_Count"},
	197: {name: "Current_Pending_Sector"},
	198: {name: "Offline_Uncorrectable"},
	199: {name: "UDMA_CRC_Error_Count"},
	200: {name: "Multi_Zone_Error_Rate"},
	202: {name: "Percent_Lifetime_Remain"},
	231: {name: "SSD_Life_Left"},
	232: {name: "Available_Reservd_Space"},
	233: {name: "Media_Wearout_Indicator"},
	241: {name: "Total_LBAs_Written", ioUnitBytes: 512},
	242: {name: "Total_LBAs_Read", ioUnitBytes: 512},
}

// defaultAttributeIDs is the list read with attributes: default.
var defaultAttributeIDs = []uint8{1, 5, 9, 12, 177, 179, 181, 182, 187, 188, 190, 194, 196, 197, 198, 199, 231, 233, 241, 242}

// DefaultAttributeIDs returns the 20 attribute ids the receiver reads by default.
func DefaultAttributeIDs() []uint8 {
	return append([]uint8(nil), defaultAttributeIDs...)
}

// AttributeName returns the name of an attribute id, or Attribute_<id> when the table lacks it.
func AttributeName(id uint8) string {
	if info, ok := attributeTable[id]; ok {
		return info.name
	}
	return "Attribute_" + strconv.Itoa(int(id))
}

// IOUnitBytes returns the bytes in one unit of the raw value of a counter of data moved, and false for
// an attribute whose table entry names no unit.
func IOUnitBytes(id uint8) (uint64, bool) {
	info, ok := attributeTable[id]
	if !ok || info.ioUnitBytes == 0 {
		return 0, false
	}
	return info.ioUnitBytes, true
}
