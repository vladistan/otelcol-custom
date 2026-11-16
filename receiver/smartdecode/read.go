// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package smartdecode

import "fmt"

// ATAOptions tunes ReadATA.
type ATAOptions struct {
	// SkipIfStandby sends CHECK POWER MODE first and stops with ErrStandby when the device is in standby.
	SkipIfStandby bool
}

// ATAData is what ReadATA decodes from one SATA device.
type ATAData struct {
	Identify   Identify
	Attributes []Attribute
	Health     Health
	// StatsTemperature is the current temperature from the Device Statistics log, read only when the
	// attribute table has neither attribute 190 nor 194.
	StatsTemperature    int
	HasStatsTemperature bool
}

func sendATA(tr ATATransport, cmd ATACommand) (ATAResult, error) {
	if err := CheckATA(cmd); err != nil {
		return ATAResult{}, err
	}
	return tr.ATA(cmd)
}

// ReadATA reads one SATA device through a transport: optionally CHECK POWER MODE, then IDENTIFY DEVICE,
// SMART READ DATA, SMART READ THRESHOLDS, SMART RETURN STATUS, and READ LOG EXT only when the table
// holds no temperature. A device without SMART or with SMART disabled ends the read with
// ErrSMARTUnsupported or ErrSMARTDisabled; nothing ever enables SMART. A transport error is returned
// wrapped, so errors.Is finds ErrTimeout, ErrPermission and ErrUnsupported.
func ReadATA(tr ATATransport, opt ATAOptions) (ATAData, error) {
	var out ATAData
	if opt.SkipIfStandby {
		res, err := sendATA(tr, CheckPowerModeCommand())
		if err != nil {
			return out, fmt.Errorf("check power mode: %w", err)
		}
		if res.HaveRegisters && res.Registers.SectorCount <= 0x01 {
			return out, ErrStandby
		}
	}

	res, err := sendATA(tr, IdentifyCommand())
	if err != nil {
		return out, fmt.Errorf("identify device: %w", err)
	}
	if out.Identify, err = ParseIdentify(res.Data); err != nil {
		return out, err
	}
	switch {
	case !out.Identify.SMARTSupported:
		return out, ErrSMARTUnsupported
	case !out.Identify.SMARTEnabled:
		return out, ErrSMARTDisabled
	}

	data, err := sendATA(tr, SmartReadDataCommand())
	if err != nil {
		return out, fmt.Errorf("smart read data: %w", err)
	}
	thresholds, err := sendATA(tr, SmartReadThresholdsCommand())
	if err != nil {
		return out, fmt.Errorf("smart read thresholds: %w", err)
	}
	if out.Attributes, err = ParseSmartData(data.Data, thresholds.Data); err != nil {
		return out, err
	}

	status, err := sendATA(tr, SmartReturnStatusCommand())
	if err != nil {
		return out, fmt.Errorf("smart return status: %w", err)
	}
	if status.HaveRegisters {
		if health, perr := ParseReturnStatus(status.Registers); perr == nil {
			out.Health = health
		}
	}

	if !hasTemperatureAttribute(out.Attributes) {
		page, err := sendATA(tr, DeviceStatisticsCommand(pageTemperature))
		if err != nil {
			return out, fmt.Errorf("device statistics: %w", err)
		}
		out.StatsTemperature, out.HasStatsTemperature = ParseDeviceStatisticsTemperature(page.Data)
	}
	return out, nil
}

func hasTemperatureAttribute(attrs []Attribute) bool {
	for _, a := range attrs {
		if a.ID == 190 || a.ID == 194 {
			return true
		}
	}
	return false
}

// NVMeData is what ReadNVMe decodes from one controller.
type NVMeData struct {
	Identify NVMeIdentify
	Smart    NVMeSmart
}

func sendNVMe(tr NVMeTransport, cmd NVMeCommand) ([]byte, error) {
	if err := CheckNVMe(cmd); err != nil {
		return nil, err
	}
	return tr.Admin(cmd)
}

// ReadNVMe reads one controller through a transport: Identify Controller, then the SMART/Health log.
func ReadNVMe(tr NVMeTransport) (NVMeData, error) {
	var out NVMeData
	buf, err := sendNVMe(tr, IdentifyControllerCommand())
	if err != nil {
		return out, fmt.Errorf("identify controller: %w", err)
	}
	if out.Identify, err = ParseNVMeIdentify(buf); err != nil {
		return out, err
	}
	buf, err = sendNVMe(tr, SmartLogCommand())
	if err != nil {
		return out, fmt.Errorf("smart log: %w", err)
	}
	if out.Smart, err = ParseNVMeSmartLog(buf); err != nil {
		return out, err
	}
	return out, nil
}
