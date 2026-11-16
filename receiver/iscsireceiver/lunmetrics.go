// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"math"
	"strconv"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const (
	metricLUNLimit = "hw.logical_disk.limit"
	metricLUNState = "iscsi.lun.state"
	typeLUN        = "logical_disk"
)

// hwState maps a SCSI device state to hw.state. ok is false for a state the table does not know,
// which gets no hw.status point. "blocked" and "quiesce" are the states of a session that is
// recovering: the LUN is there and does not take I/O.
func hwState(state string) (string, bool) {
	switch state {
	case "running":
		return hwschema.StateOK, true
	case "blocked", "quiesce", "created", "created-blocked":
		return hwschema.StateDegraded, true
	case "offline", "transport-offline", "cancel", "deleted":
		return hwschema.StateFailed, true
	}
	return "", false
}

const defaultLUNTimeout = 10 * time.Second

var (
	errLUNScanTimeout = errors.New("reading the LUNs took longer than the time allowed")
	errLUNScanBusy    = errors.New("an earlier read of the LUNs has not returned")
)

// scanLUNs lists the LUNs but gives up after the timeout, so a sysfs read that never returns
// cannot hold the scrape. The read goes on in its own goroutine; until it returns no new one
// starts, so a stuck read costs one goroutine.
func (s *scraper) scanLUNs() ([]lun, error) {
	if !s.lunBusy.CompareAndSwap(false, true) {
		return nil, errLUNScanBusy
	}
	type result struct {
		luns []lun
		err  error
	}
	done := make(chan result, 1)
	go func() {
		found, err := s.luns.luns()
		s.lunBusy.Store(false)
		done <- result{found, err}
	}()
	timeout := s.lunTimeout
	if timeout <= 0 {
		timeout = defaultLUNTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-done:
		return res.luns, res.err
	case <-timer.C:
		return nil, errLUNScanTimeout
	}
}

// addLUNs adds the LUN series of this scrape. A failed listing is one warning for each run of
// failures; the session series are sent either way.
func (s *scraper) addLUNs(sm pmetric.ScopeMetrics, ts pcommon.Timestamp, sessions []session) {
	all, err := s.scanLUNs()
	if err != nil {
		if !s.lunErrLogged {
			s.logger.Warn("Failed to read the iSCSI LUNs", zap.Error(err))
			s.lunErrLogged = true
		}
		return
	}
	s.lunErrLogged = false

	byID := make(map[int]session, len(sessions))
	for _, ses := range sessions {
		byID[ses.id] = ses
	}
	s.emitLUNs(sm, ts, s.pickLUNs(all, byID), byID)
	for _, name := range []string{hwschema.MetricStatus, metricLUNLimit, metricLUNState} {
		dropIfEmpty(sm, name)
	}
}

// pickLUNs leaves out a LUN whose session was not read and keeps at most maxLUNs, in session and
// LUN order; one info line says when the cap cuts.
func (s *scraper) pickLUNs(all []lun, sessions map[int]session) []lun {
	var picked []lun
	for _, l := range all {
		if _, ok := sessions[l.session]; ok {
			picked = append(picked, l)
		} else {
			s.logger.Debug("Skipping a LUN whose iSCSI session was not read", zap.Int("session", l.session), zap.Int("lun", l.id))
		}
	}
	if len(picked) <= s.maxLUNs {
		s.capLogged = false
		return picked
	}
	if !s.capLogged {
		s.logger.Info("More iSCSI LUNs than max_luns: the rest are not reported",
			zap.Int("found", len(picked)), zap.Int("max_luns", s.maxLUNs))
		s.capLogged = true
	}
	return picked[:s.maxLUNs]
}

// emitLUNs adds the points of the picked LUNs and warns once for each LUN that could not be read
// completely, until it reads well again.
func (s *scraper) emitLUNs(sm pmetric.ScopeMetrics, ts pcommon.Timestamp, picked []lun, sessions map[int]session) {
	status := newGauge(sm, hwschema.MetricStatus, hwschema.UnitRatio)
	limit := newGauge(sm, metricLUNLimit, hwschema.UnitBytes)
	stateGauge := newGauge(sm, metricLUNState, hwschema.UnitRatio)
	warned := map[string]bool{}
	for _, l := range picked {
		ses := sessions[l.session]
		id := ses.target + "/" + ses.portal + "/" + strconv.Itoa(l.id)
		if l.err != nil {
			warned[id] = true
			if !s.lunWarned[id] {
				s.logger.Warn("An iSCSI LUN cannot be read completely; its state is reported as unknown where it could not be read",
					zap.String("target", ses.target), zap.Int("lun", l.id), zap.String("device", l.device), zap.Error(l.err))
			}
		}
		addLUNStatus(status, ts, id, ses, l)
		if l.hasSize {
			addLUNLimit(limit, ts, id, ses, l)
		}
		addLUNInfo(stateGauge, ts, ses, l)
	}
	s.lunWarned = warned
}

func addLUNLimit(g pmetric.Gauge, ts pcommon.Timestamp, id string, ses session, l lun) {
	dp := g.DataPoints().AppendEmpty()
	dp.SetTimestamp(ts)
	dp.SetIntValue(int64(min(l.sizeBytes, math.MaxInt64))) //nolint:gosec // clamped
	putIdentity(dp, id, ses, l)
}

func addLUNInfo(g pmetric.Gauge, ts pcommon.Timestamp, ses session, l lun) {
	dp := g.DataPoints().AppendEmpty()
	dp.SetTimestamp(ts)
	dp.SetIntValue(1)
	attrs := dp.Attributes()
	attrs.PutStr("state", l.state)
	attrs.PutStr("iscsi.target", ses.target)
	attrs.PutStr("iscsi.portal", ses.portal)
	attrs.PutStr("iscsi.lun", strconv.Itoa(l.id))
	attrs.PutStr("iscsi.device", l.device)
	if l.holder != "" {
		attrs.PutStr("iscsi.holder", l.holder)
	}
	if l.holders > 1 {
		attrs.PutStr("iscsi.holders", strconv.Itoa(l.holders))
	}
}

func addLUNStatus(g pmetric.Gauge, ts pcommon.Timestamp, id string, ses session, l lun) {
	current, ok := hwState(l.state)
	if !ok {
		return
	}
	for _, state := range hwschema.States {
		dp := g.DataPoints().AppendEmpty()
		dp.SetTimestamp(ts)
		if state == current {
			dp.SetIntValue(1)
		} else {
			dp.SetIntValue(0)
		}
		putIdentity(dp, id, ses, l)
		dp.Attributes().PutStr(hwschema.AttrState, state)
	}
}

// putIdentity sets hw.id, hw.name, hw.type and hw.parent: the id is target, portal and LUN, so
// two paths to one LUN are two series; the name is the block device, the parent the target.
func putIdentity(dp pmetric.NumberDataPoint, id string, ses session, l lun) {
	attrs := dp.Attributes()
	attrs.PutStr(hwschema.AttrID, id)
	attrs.PutStr(hwschema.AttrName, l.device)
	attrs.PutStr(hwschema.AttrType, typeLUN)
	attrs.PutStr(hwschema.AttrParent, ses.target)
}

// dropIfEmpty removes a metric that ended up with no data point.
func dropIfEmpty(sm pmetric.ScopeMetrics, name string) {
	sm.Metrics().RemoveIf(func(m pmetric.Metric) bool {
		return m.Name() == name && m.Gauge().DataPoints().Len() == 0
	})
}
