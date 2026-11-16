// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

// errDriveGone means the controller answered status 12, device not found: the drive is dropped
// from the SMART list for this scrape.
var errDriveGone = errors.New("the controller does not know the drive (status 12)")

// passTransport sends allow-listed ATA commands to one drive as ATA PASS-THROUGH(16) in an
// MFI_CMD_PD_SCSI_IO frame. The frame layout is not confirmed on a controller. The allow list is
// checked here and again before the frame reaches the kernel.
type passTransport struct {
	dev    device
	target uint8
}

// ATA implements smartdecode.ATATransport.
func (t passTransport) ATA(cmd smartdecode.ATACommand) (smartdecode.ATAResult, error) {
	if err := smartdecode.CheckATA(cmd); err != nil {
		return smartdecode.ATAResult{}, err
	}
	resp, err := t.dev.pass(passRequest{target: t.target, cdb: ataCDB(cmd), cdbLen: maxCDBLen, dataLen: cmd.DataLen})
	if err != nil {
		return smartdecode.ATAResult{}, err
	}
	switch {
	case resp.status == statusDeviceMissing:
		return smartdecode.ATAResult{}, errDriveGone
	case resp.status == statusOK, resp.status == statusSCSIError && cmd.WantRegisters:
	default:
		return smartdecode.ATAResult{}, fmt.Errorf("pass-through status %d", resp.status)
	}
	res := smartdecode.ATAResult{Data: resp.data}
	if cmd.WantRegisters {
		res.Registers, res.HaveRegisters = smartdecode.ParseATASense(resp.sense)
	}
	return res, nil
}

// smartEligible says whether a drive may be asked for SMART: it must be a disk the controller
// has configured or can use, and not be rebuilding, copying back, failed, or under patrol read.
func smartEligible(p pdRecord) bool {
	switch p.info.FWState {
	case pdOnline, pdHotSpare, pdUnconfiguredGood, pdSystem:
	default:
		return false
	}
	return len(p.info.Progress) == 0
}

// readSmart reads the SATA drives one at a time, each at most once every smart_interval. A drive
// that was not due keeps its last result. SAS drives are not read in this stage.
func (s *scraper) readSmart(dev device, c controller, st *ctrlState, snap *snapshot, now time.Time) {
	for i := range snap.pds {
		p := &snap.pds[i]
		id := p.addr.DeviceID
		if !smartEligible(*p) {
			continue
		}
		if !strings.EqualFold(p.info.Vendor, "ATA") {
			p.smart = &smartResult{state: smartNotATA}
			continue
		}
		if id > 0xFF {
			s.warnOnce(st, fmt.Sprintf("smart-id-%d", id), "A drive id is above 255 and cannot be addressed for SMART",
				zap.String("controller", c.id()), zap.Uint16("device_id", id))
			continue
		}
		if last, ok := st.smartLast[id]; ok && now.Sub(last) < s.cfg.SmartInterval {
			p.smart = st.smartCache[id]
			continue
		}
		res, stop := s.readOneSmart(dev, c, st, uint8(id))
		st.smartLast[id] = now
		st.smartCache[id] = res
		p.smart = res
		if stop {
			return
		}
	}
}

// asleep reports whether CHECK POWER MODE shows a drive that is in standby or whose power state
// is unknown. The controller may return no ATA status registers, and a drive is then left alone:
// the check fails closed, so SMART never wakes a spun-down drive. An answer of 0x00 or 0x01 is
// standby; any other value is awake.
func asleep(tr passTransport) (standby, unknown bool, err error) {
	res, err := tr.ATA(smartdecode.CheckPowerModeCommand())
	if err != nil {
		return false, false, fmt.Errorf("check power mode: %w", err)
	}
	if !res.HaveRegisters {
		return false, true, nil
	}
	return res.Registers.SectorCount <= 0x01, false, nil
}

// readOneSmart reads one drive. stop is true when the controller is unusable and the rest of the
// drives must wait for the next scrape.
func (s *scraper) readOneSmart(dev device, c controller, st *ctrlState, target uint8) (res *smartResult, stop bool) {
	tr := passTransport{dev: dev, target: target}
	standby, unknown, err := asleep(tr)
	switch {
	case err != nil:
	case standby:
		return &smartResult{state: smartStandby}, false
	case unknown:
		s.warnOnce(st, fmt.Sprintf("smart-power-%d", target), "The controller gave no power state for a drive, so SMART leaves it alone",
			zap.String("controller", c.id()), zap.Uint8("device_id", target))
		return &smartResult{state: smartUnavailable}, false
	}
	var data smartdecode.ATAData
	if err == nil {
		data, err = smartdecode.ReadATA(tr, smartdecode.ATAOptions{})
	}
	switch {
	case err == nil:
		state := smartOK
		if data.Health == smartdecode.HealthPredictedFailure {
			state = smartPredicted
		}
		return &smartResult{state: state, data: &data}, false
	case errors.Is(err, smartdecode.ErrSMARTUnsupported):
		return &smartResult{state: smartUnsupported}, false
	case errors.Is(err, smartdecode.ErrSMARTDisabled):
		return &smartResult{state: smartOff}, false
	case errors.Is(err, smartdecode.ErrStandby):
		return &smartResult{state: smartStandby}, false
	case errors.Is(err, errDriveGone):
		return nil, false
	}
	s.warnOnce(st, fmt.Sprintf("smart-%d-%v", target, errClass(err)), "A SMART read through the controller failed",
		zap.String("controller", c.id()), zap.Uint8("device_id", target), zap.Error(err))
	return &smartResult{state: smartUnavailable}, skipsScrape(err)
}
