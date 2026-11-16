// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package iscsireceiver

import (
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

const scopeName = "github.com/vladistan/otelcol-custom/receiver/iscsireceiver"

// sessionSource reads the sessions once; tests replace it.
type sessionSource interface {
	sessions() ([]session, error)
}

type scraper struct {
	logger *zap.Logger
	src    sessionSource

	noSessionsLogged bool
	errorLogged      bool

	// luns is nil when the LUN series are off.
	luns         lunSource
	maxLUNs      int
	lunTimeout   time.Duration // zero means defaultLUNTimeout
	lunBusy      atomic.Bool   // a scan of the LUNs is still running
	lunErrLogged bool
	capLogged    bool
	lunWarned    map[string]bool
}

func newScraper(logger *zap.Logger, src sessionSource) *scraper {
	return &scraper{logger: logger, src: src}
}

// scrape reads the sessions. A host with none gives one info line until
// sessions appear; another error gives one warning per episode.
func (s *scraper) scrape(now time.Time) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sessions, err := s.src.sessions()
	switch {
	case errors.Is(err, errNoSessions):
		if !s.noSessionsLogged {
			s.logger.Info("No iSCSI sessions found; nothing is sent until one appears")
			s.noSessionsLogged = true
		}
		s.errorLogged = false
		return md
	case err != nil:
		if !s.errorLogged {
			s.logger.Warn("Failed to read the iSCSI sessions", zap.Error(err))
			s.errorLogged = true
		}
		return md
	}
	s.noSessionsLogged, s.errorLogged = false, false

	ts := pcommon.NewTimestampFromTime(now)
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	state := newGauge(sm, "iscsi.session.state", "1")
	conns := newGauge(sm, "iscsi.connection.state", "1")
	count := newGauge(sm, "iscsi.session.count", "{session}")

	for _, item := range sessions {
		dp := state.DataPoints().AppendEmpty()
		dp.SetTimestamp(ts)
		dp.SetIntValue(1)
		dp.Attributes().PutStr("state", item.state)
		dp.Attributes().PutStr("iscsi.target", item.target)
		dp.Attributes().PutStr("iscsi.portal", item.portal)
		dp.Attributes().PutStr("iscsi.tpgt", item.tpgt)

		for _, c := range item.connections {
			cdp := conns.DataPoints().AppendEmpty()
			cdp.SetTimestamp(ts)
			cdp.SetIntValue(1)
			cdp.Attributes().PutStr("state", c.state)
			cdp.Attributes().PutStr("iscsi.target", item.target)
			cdp.Attributes().PutStr("iscsi.portal", c.portal)
		}
	}
	cdp := count.DataPoints().AppendEmpty()
	cdp.SetTimestamp(ts)
	cdp.SetIntValue(int64(len(sessions)))

	if s.luns != nil {
		s.addLUNs(sm, ts, sessions)
	}

	if conns.DataPoints().Len() == 0 {
		removeMetric(sm, "iscsi.connection.state")
	}
	return md
}

func newGauge(sm pmetric.ScopeMetrics, name, unit string) pmetric.Gauge {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetUnit(unit)
	return m.SetEmptyGauge()
}

// removeMetric drops a metric that ended up with no data point.
func removeMetric(sm pmetric.ScopeMetrics, name string) {
	sm.Metrics().RemoveIf(func(m pmetric.Metric) bool { return m.Name() == name })
}
