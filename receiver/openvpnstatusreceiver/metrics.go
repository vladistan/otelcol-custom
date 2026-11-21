package openvpnstatusreceiver

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	bytesReceivedMetric  = "openvpn.client.bytes_received"
	bytesSentMetric      = "openvpn.client.bytes_sent"
	connectedSinceMetric = "openvpn.client.connected_since"
)

// clientTotals is what the metrics key on: a client name and its IP. Sessions of
// one name from one IP (clients behind one NAT) share it, so their bytes are
// summed and the earliest start is kept. The sum falls when one of them ends,
// with no reset marker.
type clientTotals struct {
	commonName string
	ip         string
	received   int64
	sent       int64
	start      time.Time
}

func mergeClients(clients []client) []*clientTotals {
	var out []*clientTotals
	index := map[[2]string]*clientTotals{}
	for _, c := range clients {
		key := [2]string{c.commonName, c.ip}
		s, ok := index[key]
		if !ok {
			s = &clientTotals{commonName: c.commonName, ip: c.ip}
			index[key] = s
			out = append(out, s)
		}
		s.received += c.bytesReceived
		s.sent += c.bytesSent
		if !c.connectedAt.IsZero() && (s.start.IsZero() || c.connectedAt.Before(s.start)) {
			s.start = c.connectedAt
		}
	}
	return out
}

// buildMetrics writes the two byte counters as cumulative sums that start when
// the session started, so a reconnect reads as a counter reset, and the session
// start as a gauge of Unix seconds. A series without a readable start has no
// start time and no gauge point.
func (r *statusReceiver) buildMetrics(snap snapshot) pmetric.Metrics {
	md := pmetric.NewMetrics()
	sm := md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	sm.Scope().SetName(scopeName)
	received := byteSum(sm, bytesReceivedMetric, "Bytes received from the client in the session")
	sent := byteSum(sm, bytesSentMetric, "Bytes sent to the client in the session")
	since := sm.Metrics().AppendEmpty()
	since.SetName(connectedSinceMetric)
	since.SetDescription("Start of the client session as a Unix timestamp in seconds; an absolute time, not a duration")
	since.SetUnit("s")
	sinceGauge := since.SetEmptyGauge().DataPoints()

	at := pcommon.NewTimestampFromTime(snap.at)
	for _, totals := range mergeClients(snap.clients) {
		for _, target := range []struct {
			dps   pmetric.NumberDataPointSlice
			value int64
		}{{received, totals.received}, {sent, totals.sent}} {
			dp := target.dps.AppendEmpty()
			setClient(dp, totals)
			dp.SetIntValue(target.value)
			dp.SetTimestamp(at)
			if !totals.start.IsZero() {
				dp.SetStartTimestamp(pcommon.NewTimestampFromTime(totals.start))
			}
		}
		if !totals.start.IsZero() {
			dp := sinceGauge.AppendEmpty()
			setClient(dp, totals)
			dp.SetIntValue(totals.start.Unix())
			dp.SetTimestamp(at)
		}
	}
	return md
}

func byteSum(sm pmetric.ScopeMetrics, name, description string) pmetric.NumberDataPointSlice {
	m := sm.Metrics().AppendEmpty()
	m.SetName(name)
	m.SetDescription(description)
	m.SetUnit("By")
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	return sum.DataPoints()
}

func setClient(dp pmetric.NumberDataPoint, totals *clientTotals) {
	dp.Attributes().PutStr("user.name", totals.commonName)
	dp.Attributes().PutStr("client.address", totals.ip)
	if totals.commonName == undefName {
		dp.Attributes().PutBool("unauthenticated", true)
	}
}

// undefName is the common name OpenVPN shows for a client that has not been authenticated.
const undefName = "UNDEF"
