// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan

package winlogprocessor

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const realCacheLabels = "[256us, 1ms, 4ms, 16ms, 64ms, 128ms, 256ms, 2000ms, 6000ms, 10000ms, 20000ms, 20000+ms]"

func intList(values [12]int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// realKernelCache105 has the shape of a captured Kernel-Cache 105 event: the
// period, the device GUID and four families of 12 bucket values. counts fills
// the blocking write counts; the other families are all zero. Host names are neutral.
func realKernelCache105(counts [12]int) receiverEvent {
	var zero [12]int
	data := map[string]any{
		"PeriodDurationMicroSec": "3650362155",
		"VolumeDeviceGuid":       "{00000000-0000-0000-0000-000000000000}",
	}
	for i := 0; i < 12; i++ {
		n := strconv.Itoa(i + 1)
		data["SynchronousWriteIoCountsBucket"+n] = strconv.Itoa(counts[i])
		data["SynchronousWriteTotalLatencyBucket"+n] = "0"
		data["SynchronousWriteNonBlockingIoCountsBucket"+n] = "0"
		data["SynchronousWriteNonBlockingTotalLatencyBucket"+n] = "0"
	}
	indent := strings.Repeat(" ", 12)
	message := "Volume Periodic Cache Write Latency Information:\n\n" +
		indent + "Device GUID: {00000000-0000-0000-0000-000000000000}\n" +
		indent + "Period Duration (microseconds): 3650362155\n\n" +
		indent + "Latency Bucket Values:  " + realCacheLabels + "\n\n" +
		indent + "Synchronous Write IO Counts: " + intList(counts) + "\n" +
		indent + "Synchronous Write IO Total Latencies (us): " + intList(zero) + "\n" +
		indent + "Synchronous Write Non-Blocking IO Counts: " + intList(zero) + "\n" +
		indent + "Synchronous Write Non-Blocking IO Total Latencies (us): " + intList(zero)
	return receiverEvent{
		channel:   "Microsoft-Windows-Kernel-Cache/Operational",
		provider:  "Microsoft-Windows-Kernel-Cache",
		computer:  "dc01.example.org",
		id:        105,
		recordID:  7731,
		level:     "Information",
		task:      "105",
		opcode:    "Info",
		userID:    "S-1-5-18",
		processID: 4,
		threadID:  728,
		message:   message,
		eventData: data,
	}
}

func cacheBody(t *testing.T, event receiverEvent) plog.LogRecord {
	t.Helper()
	return shapeCfg(t, &Config{}, event)
}

func TestRealKernelCache105WithNoWriteIOSaysSo(t *testing.T) {
	event := realKernelCache105([12]int{})
	record := cacheBody(t, event)
	want := "Cache write latency over 3650 s: no write IO"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message = %q, want the rendered message", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
	if got := attrString(t, record, "winlog.event_data.SynchronousWriteIoCountsBucket1"); got != "0" {
		t.Errorf("the bucket keys must stay attributes, got %q", got)
	}
}

func TestRealKernelCache105WithTrafficNamesTheSlowestBucket(t *testing.T) {
	record := cacheBody(t, realKernelCache105([12]int{4392, 143, 0, 0, 0, 0, 0, 85, 0, 0, 0, 0}))
	want := "Cache write latency over 3650 s: 4620 sync writes, slowest bucket 2000ms"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealKernelCache105SlowestBucketIsTheHighestIndexWithACount(t *testing.T) {
	cases := map[[12]int]string{
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7}: "Cache write latency over 3650 s: 7 sync writes, slowest bucket 20000+ms",
		{5, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0}: "Cache write latency over 3650 s: 7 sync writes, slowest bucket 4ms",
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}: "Cache write latency over 3650 s: 1 sync writes, slowest bucket 256us",
	}
	for counts, want := range cases {
		if got := cacheBody(t, realKernelCache105(counts)).Body().Str(); got != want {
			t.Errorf("counts %v: body = %q, want %q", counts, got, want)
		}
	}
}

func TestRealKernelCache105UsesTheFixedLabelsWhenTheMessageHasNoBucketList(t *testing.T) {
	event := realKernelCache105([12]int{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9, 0})
	event.message = "Volume Periodic Cache Write Latency Information."
	want := "Cache write latency over 3650 s: 12 sync writes, slowest bucket 20000ms"
	if got := cacheBody(t, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealKernelCache105IgnoresTheNonBlockingFamily(t *testing.T) {
	event := realKernelCache105([12]int{})
	event.eventData["SynchronousWriteNonBlockingIoCountsBucket3"] = "40"
	want := "Cache write latency over 3650 s: no write IO"
	if got := cacheBody(t, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestRealKernelCache105KeepsTheBodyWhenADataFieldIsMissingOrUnparsable(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"a count that is not a number":  func(d map[string]any) { d["SynchronousWriteIoCountsBucket4"] = "many" },
		"a negative count":              func(d map[string]any) { d["SynchronousWriteIoCountsBucket4"] = "-1" },
		"a missing bucket":              func(d map[string]any) { delete(d, "SynchronousWriteIoCountsBucket12") },
		"no bucket keys at all":         func(d map[string]any) { clearBuckets(d) },
		"a period that is not a number": func(d map[string]any) { d["PeriodDurationMicroSec"] = "an hour" },
		"a missing period":              func(d map[string]any) { delete(d, "PeriodDurationMicroSec") },
	}
	for name, mutate := range mutations {
		event := realKernelCache105([12]int{1})
		mutate(event.eventData)
		record := cacheBody(t, event)
		if got := record.Body().Str(); got != event.message {
			t.Errorf("%s: body = %q, want the message kept", name, got)
		}
		if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
			t.Errorf("%s: vstan.winlog.message is set although no summary was made", name)
		}
	}
}

func clearBuckets(d map[string]any) {
	for i := 1; i <= 12; i++ {
		delete(d, fmt.Sprintf("SynchronousWriteIoCountsBucket%d", i))
	}
}
