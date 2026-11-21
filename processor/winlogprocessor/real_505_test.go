// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore vstan storahci Storport

package winlogprocessor

import (
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

var realStorPortSuccess = [12]int{4392, 143, 85, 297, 274, 169, 187, 272, 0, 0, 0, 0}

var realStorPortLatency = [12]int{46595, 745589, 1549957, 39585779, 107399112, 165253674, 336690092, 2289749263, 0, 0, 0, 0}

func plainList(values [12]int) string {
	return strings.Trim(intList(values), "[]")
}

// realStorPort505 has the shape of a captured StorPort 505 event: the padded
// VendorId and SerialNumber, the 36 bucket values and the totals are the real
// ones. failed fills the failed IO buckets (all zero in the real row). Any
// non-zero failed value is invented: the real row has no failed I/O, so replace
// it when a real row exists. Names, GUIDs and the serial number are neutral.
func realStorPort505(failed [12]int) receiverEvent {
	data := map[string]any{
		"TotalIoCount": "5819", "TotalReadBytes": "110592", "TotalWriteBytes": "88438784",
		"MaxReadWriteLatency_100ns": "19151564", "MaxUnmapLatency_100ns": "6139519",
		"MaxOutstandingCount": "31", "DeviceQueueIoBusyCount": "4", "SystemUptime_s": "1186129",
		"PortNumber": "2", "PathID": "0", "TargetID": "0", "LUN": "0", "BusType": "11",
		"MiniportName": "storahci", "ProductId": "HARDDISK", "VendorId": "QEMU     ",
		"SerialNumber": "SN00000001          ", "BootDevice": "true", "FirmwareRevision": "1.0",
		"IoTimeout_s": "30", "Version": "11",
		"AdapterGuid":     "{00000000-0000-0000-0000-000000000001}",
		"ClassDeviceGuid": "{00000000-0000-0000-0000-000000000002}",
	}
	for i := 0; i < 12; i++ {
		n := strconv.Itoa(i + 1)
		data["BucketIoSuccess"+n] = strconv.Itoa(realStorPortSuccess[i])
		data["BucketIoFailed"+n] = strconv.Itoa(failed[i])
		data["BucketIoLatency"+n+"_100ns"] = strconv.Itoa(realStorPortLatency[i])
	}
	lines := []string{
		"Performance summary for Storport Device (Port = 2, Path = 0, Target = 0, Lun = 0) whose Corresponding Class Disk Device Guid is {00000000-0000-0000-0000-000000000002}:",
		"Total IO:5819",
		"For latency buckets of 256us, 1ms, 4ms, 16ms, 64ms, 128ms, 256ms, 2000ms, 6000ms, 10000ms, 20000ms, 20000+ms,",
		"The IO success counts are " + plainList(realStorPortSuccess) + ".",
		"The IO failed counts are " + plainList(failed) + ".",
		"The IO total latency (in 100ns) are " + plainList(realStorPortLatency) + ".",
		"Total Bytes Read:110592",
		"Total Bytes Written:88438784",
	}
	for i := 0; i < len(lines)-1; i++ {
		lines[i] += strings.Repeat(" ", 17)
	}
	return receiverEvent{
		channel:   "Microsoft-Windows-Storage-Storport/Operational",
		provider:  "Microsoft-Windows-StorPort",
		computer:  "dc01.example.org",
		id:        505,
		recordID:  58211,
		level:     "Information",
		task:      "Port",
		opcode:    "Info",
		keywords:  []any{"Read request", "Write request"},
		processID: 4,
		threadID:  912,
		message:   strings.Join(lines, "\n"),
		eventData: data,
	}
}

func storPortBody(t *testing.T, event receiverEvent) plog.LogRecord {
	t.Helper()
	return shapeCfg(t, &Config{}, event)
}

func TestRealStorPort505SummaryNamesTheDeviceIOBytesAndLatency(t *testing.T) {
	event := realStorPort505([12]int{})
	record := storPortBody(t, event)
	want := "StorPort 2:0:0:0 QEMU HARDDISK: 5819 IO, read 108.0 KiB, written 84.3 MiB, max latency 1915.2 ms"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if strings.Contains(record.Body().Str(), "SN00000001") {
		t.Error("the serial number must never be in the summary")
	}
	if got := attrString(t, record, "vstan.winlog.message"); got != event.message {
		t.Errorf("vstan.winlog.message = %q, want the rendered message", got)
	}
	if got := attrString(t, record, "winlog.event_data.BucketIoSuccess1"); got != "4392" {
		t.Errorf("the bucket keys must stay attributes, got %q", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
}

// Invented fixture: the failed counts below are made up.
func TestInventedStorPort505FailedIOIsShownAndRaisesWarn(t *testing.T) {
	record := storPortBody(t, realStorPort505([12]int{2, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0}))
	want := "StorPort 2:0:0:0 QEMU HARDDISK: 5819 IO, read 108.0 KiB, written 84.3 MiB, max latency 1915.2 ms, 3 failed IO"
	if got := record.Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if record.SeverityNumber() != plog.SeverityNumberWarn || record.SeverityText() != "WARN" {
		t.Errorf("severity = %v %q, want WARN", record.SeverityNumber(), record.SeverityText())
	}
}

func TestRealStorPort505WithoutFailedIOStaysInfo(t *testing.T) {
	record := storPortBody(t, realStorPort505([12]int{}))
	if record.SeverityNumber() != plog.SeverityNumberInfo {
		t.Errorf("severity = %v, want INFO", record.SeverityNumber())
	}
	if strings.Contains(record.Body().Str(), "failed") {
		t.Errorf("body %q mentions failed IO although there is none", record.Body().Str())
	}
}

// Invented fixture: the failed counts below are made up.
func TestInventedStorPort505WarnComesWithoutASummaryToo(t *testing.T) {
	event := realStorPort505([12]int{0, 0, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	delete(event.eventData, "TotalIoCount")
	record := storPortBody(t, event)
	if got := record.Body().Str(); got != event.message {
		t.Errorf("body = %q, want the message kept", got)
	}
	if record.SeverityNumber() != plog.SeverityNumberWarn {
		t.Errorf("severity = %v, want WARN from the failed IO count", record.SeverityNumber())
	}
}

func TestRealStorPort505KeepsTheBodyWhenAFieldIsMissingOrUnparsable(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"no total":                     func(d map[string]any) { delete(d, "TotalIoCount") },
		"a total that is text":         func(d map[string]any) { d["TotalIoCount"] = "lots" },
		"a negative total":             func(d map[string]any) { d["TotalIoCount"] = "-5" },
		"no port number":               func(d map[string]any) { delete(d, "PortNumber") },
		"a path that is text":          func(d map[string]any) { d["PathID"] = "x" },
		"no target":                    func(d map[string]any) { delete(d, "TargetID") },
		"no lun":                       func(d map[string]any) { delete(d, "LUN") },
		"read bytes that is text":      func(d map[string]any) { d["TotalReadBytes"] = "many" },
		"written bytes negative":       func(d map[string]any) { d["TotalWriteBytes"] = "-1" },
		"latency that is text":         func(d map[string]any) { d["MaxReadWriteLatency_100ns"] = "slow" },
		"a failed bucket that is text": func(d map[string]any) { d["BucketIoFailed3"] = "some" },
	}
	for name, mutate := range mutations {
		event := realStorPort505([12]int{})
		mutate(event.eventData)
		record := storPortBody(t, event)
		if got := record.Body().Str(); got != event.message {
			t.Errorf("%s: body = %q, want the message kept", name, got)
		}
		if _, ok := record.Attributes().Get("vstan.winlog.message"); ok {
			t.Errorf("%s: vstan.winlog.message is set although no summary was made", name)
		}
		if record.SeverityNumber() != plog.SeverityNumberInfo {
			t.Errorf("%s: severity = %v, want INFO", name, record.SeverityNumber())
		}
	}
}

func TestRealStorPort505TrimsAndOmitsTheDeviceName(t *testing.T) {
	cases := map[string]struct {
		vendor, product, want string
	}{
		"both":         {"QEMU     ", "HARDDISK", "StorPort 2:0:0:0 QEMU HARDDISK: 5819 IO"},
		"vendor only":  {"ATA       ", "", "StorPort 2:0:0:0 ATA: 5819 IO"},
		"product only": {"", "HARDDISK  ", "StorPort 2:0:0:0 HARDDISK: 5819 IO"},
		"neither":      {"        ", "", "StorPort 2:0:0:0: 5819 IO"},
	}
	for name, tc := range cases {
		event := realStorPort505([12]int{})
		event.eventData["VendorId"], event.eventData["ProductId"] = tc.vendor, tc.product
		got := storPortBody(t, event).Body().Str()
		if !strings.HasPrefix(got, tc.want+",") {
			t.Errorf("%s: body = %q, want it to start with %q", name, got, tc.want+",")
		}
	}
}

func TestRealStorPort505OptionalTotalsAreLeftOutWhenAbsent(t *testing.T) {
	event := realStorPort505([12]int{})
	for _, key := range []string{"TotalReadBytes", "TotalWriteBytes", "MaxReadWriteLatency_100ns"} {
		delete(event.eventData, key)
	}
	want := "StorPort 2:0:0:0 QEMU HARDDISK: 5819 IO"
	if got := storPortBody(t, event).Body().Str(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestFormatBinaryBytes(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 512: "512 B", 1023: "1023 B", 1024: "1.0 KiB", 110592: "108.0 KiB",
		1048576: "1.0 MiB", 88438784: "84.3 MiB", 3 << 30: "3.0 GiB", 5 << 40: "5.0 TiB",
	}
	for n, want := range cases {
		if got := formatBinaryBytes(n); got != want {
			t.Errorf("formatBinaryBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
