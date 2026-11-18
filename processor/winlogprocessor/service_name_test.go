// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package winlogprocessor

import (
	"context"
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

const fallbackServiceName = "otelcol-edge"

// providerBatch is one resource that carries the fallback service.name and one
// record per provider, in order.
func providerBatch(providers ...string) plog.Logs {
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", fallbackServiceName)
	rl.Resource().Attributes().PutStr("host.name", "WIN-1")
	records := rl.ScopeLogs().AppendEmpty().LogRecords()
	for i, p := range providers {
		testEvent{channel: "Application", id: int64(100 + i), provider: p}.toLogRecord().CopyTo(records.AppendEmpty())
	}
	return logs
}

func serviceNameOn() *Config {
	return &Config{ServiceNameFromProvider: true}
}

// resourceNames returns service.name of every resource and the provider names
// of its records, in output order.
func resourceNames(logs []plog.Logs) (names []string, providers [][]string) {
	for _, batch := range logs {
		for i := 0; i < batch.ResourceLogs().Len(); i++ {
			rl := batch.ResourceLogs().At(i)
			name := ""
			if v, ok := rl.Resource().Attributes().Get("service.name"); ok {
				name = v.Str()
			}
			names = append(names, name)
			var group []string
			for j := 0; j < rl.ScopeLogs().Len(); j++ {
				records := rl.ScopeLogs().At(j).LogRecords()
				for k := 0; k < records.Len(); k++ {
					v, _ := records.At(k).Attributes().Get("winlog.provider_name")
					group = append(group, v.Str())
				}
			}
			providers = append(providers, group)
		}
	}
	return names, providers
}

func consume(t *testing.T, cfg *Config, in plog.Logs) []plog.Logs {
	t.Helper()
	h := newHarnessCfg(t, cfg)
	if err := h.proc.ConsumeLogs(context.Background(), in); err != nil {
		t.Fatalf("consume: %v", err)
	}
	return h.sink.AllLogs()
}

func TestServiceNameOptionDefaultsToFalse(t *testing.T) {
	if NewFactory().CreateDefaultConfig().(*Config).ServiceNameFromProvider {
		t.Fatal("service_name_from_provider must default to false")
	}
}

func TestServiceNameOffLeavesTheResourceAlone(t *testing.T) {
	out := consume(t, &Config{}, providerBatch("Microsoft-Windows-GroupPolicy", "NETLOGON"))
	names, _ := resourceNames(out)
	if len(names) != 1 || names[0] != fallbackServiceName {
		t.Fatalf("resources = %v, want one %q", names, fallbackServiceName)
	}
}

func TestServiceNameOnSplitsProvidersAndStripsThePrefix(t *testing.T) {
	out := consume(t, serviceNameOn(), providerBatch(
		"Microsoft-Windows-Security-Auditing", "Service Control Manager", "Microsoft-Windows-GroupPolicy"))
	names, providers := resourceNames(out)
	want := []string{"Security-Auditing", "Service Control Manager", "GroupPolicy"}
	if len(names) != len(want) {
		t.Fatalf("service.name values = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("resource %d service.name = %q, want %q", i, names[i], want[i])
		}
		if len(providers[i]) != 1 {
			t.Errorf("resource %d holds %d records, want 1", i, len(providers[i]))
		}
	}
}

func TestServiceNameOnGroupsRecordsOfOneProviderInOrder(t *testing.T) {
	logs := providerBatch("NETLOGON", "Microsoft-Windows-PowerShell", "NETLOGON", "Microsoft-Windows-PowerShell", "NETLOGON")
	out := consume(t, serviceNameOn(), logs)
	names, providers := resourceNames(out)
	if len(names) != 2 || names[0] != "NETLOGON" || names[1] != "PowerShell" {
		t.Fatalf("service.name values = %v, want [NETLOGON PowerShell]", names)
	}
	if len(providers[0]) != 3 || len(providers[1]) != 2 {
		t.Fatalf("records per resource = %d and %d, want 3 and 2", len(providers[0]), len(providers[1]))
	}
	wantIDs := [][]int64{{100, 102, 104}, {101, 103}}
	for i := 0; i < out[0].ResourceLogs().Len(); i++ {
		var got []int64
		records := out[0].ResourceLogs().At(i).ScopeLogs().At(0).LogRecords()
		for k := 0; k < records.Len(); k++ {
			v, _ := records.At(k).Attributes().Get("winlog.event_id")
			got = append(got, v.Int())
		}
		if len(got) != len(wantIDs[i]) {
			t.Fatalf("resource %d event ids = %v, want %v", i, got, wantIDs[i])
		}
		for k := range got {
			if got[k] != wantIDs[i][k] {
				t.Errorf("resource %d event ids = %v, want %v", i, got, wantIDs[i])
				break
			}
		}
	}
}

func TestServiceNameOnKeepsTheFallbackForAnEmptyProvider(t *testing.T) {
	out := consume(t, serviceNameOn(), providerBatch("", "NETLOGON"))
	names, _ := resourceNames(out)
	if len(names) != 2 || names[0] != fallbackServiceName || names[1] != "NETLOGON" {
		t.Fatalf("service.name values = %v, want [%s NETLOGON]", names, fallbackServiceName)
	}
}

func TestServiceNameOnKeepsTheFallbackForAnAbsentProvider(t *testing.T) {
	logs := providerBatch("x")
	record := logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0)
	record.Body().Map().Remove("provider")
	names, _ := resourceNames(consume(t, serviceNameOn(), logs))
	if len(names) != 1 || names[0] != fallbackServiceName {
		t.Fatalf("service.name values = %v, want one %q", names, fallbackServiceName)
	}
}

func TestServiceNameOnReplacesAnExistingServiceName(t *testing.T) {
	logs := providerBatch("Microsoft-Windows-Kernel-General")
	logs.ResourceLogs().At(0).Resource().Attributes().PutStr("service.name", "custom-app")
	names, _ := resourceNames(consume(t, serviceNameOn(), logs))
	if len(names) != 1 || names[0] != "Kernel-General" {
		t.Fatalf("service.name values = %v, want [Kernel-General]", names)
	}
}

func TestServiceNameOnKeepsOtherResourceAttributes(t *testing.T) {
	out := consume(t, serviceNameOn(), providerBatch("NETLOGON", "Microsoft-Windows-GroupPolicy"))
	for i := 0; i < out[0].ResourceLogs().Len(); i++ {
		v, ok := out[0].ResourceLogs().At(i).Resource().Attributes().Get("host.name")
		if !ok || v.Str() != "WIN-1" {
			t.Errorf("resource %d host.name = %v, want WIN-1", i, v)
		}
	}
}

func TestServiceNameOnKeepsRecordCountAndOtherAttributes(t *testing.T) {
	providers := []string{"A", "Microsoft-Windows-B", "A", "C", "Microsoft-Windows-B"}
	off := consume(t, &Config{}, providerBatch(providers...))
	on := consume(t, serviceNameOn(), providerBatch(providers...))
	if off[0].LogRecordCount() != on[0].LogRecordCount() {
		t.Fatalf("record count %d with the option on, %d off", on[0].LogRecordCount(), off[0].LogRecordCount())
	}
	// Every record on the "on" side must equal the "off" record with the same event id.
	offByID := map[int64]plog.LogRecord{}
	offRecords := off[0].ResourceLogs().At(0).ScopeLogs().At(0).LogRecords()
	for k := 0; k < offRecords.Len(); k++ {
		id, _ := offRecords.At(k).Attributes().Get("winlog.event_id")
		offByID[id.Int()] = offRecords.At(k)
	}
	for i := 0; i < on[0].ResourceLogs().Len(); i++ {
		records := on[0].ResourceLogs().At(i).ScopeLogs().At(0).LogRecords()
		for k := 0; k < records.Len(); k++ {
			got := records.At(k)
			id, _ := got.Attributes().Get("winlog.event_id")
			want, ok := offByID[id.Int()]
			if !ok {
				t.Errorf("event %d is only on the option-on side", id.Int())
				continue
			}
			if !reflect.DeepEqual(got.Attributes().AsRaw(), want.Attributes().AsRaw()) {
				t.Errorf("event %d attributes = %v, want %v", id.Int(), got.Attributes().AsRaw(), want.Attributes().AsRaw())
			}
			if !reflect.DeepEqual(got.Body().AsRaw(), want.Body().AsRaw()) {
				t.Errorf("event %d body = %v, want %v", id.Int(), got.Body().AsRaw(), want.Body().AsRaw())
			}
		}
	}
}

func TestServiceNameOnDoesNotTouchRecordsTheProcessorSkips(t *testing.T) {
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.name", "other")
	rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStr("plain text")
	names, _ := resourceNames(consume(t, serviceNameOn(), logs))
	if len(names) != 1 || names[0] != "other" {
		t.Fatalf("service.name values = %v, want [other]", names)
	}
}

func TestProviderServiceName(t *testing.T) {
	cases := map[string]string{
		"Microsoft-Windows-Security-Auditing": "Security-Auditing",
		"Microsoft-Windows-GroupPolicy":       "GroupPolicy",
		"Microsoft-Windows-PowerShell":        "PowerShell",
		"Service Control Manager":             "Service Control Manager",
		"NETLOGON":                            "NETLOGON",
		"Microsoft-Windows-":                  "Microsoft-Windows-",
		"microsoft-windows-GroupPolicy":       "microsoft-windows-GroupPolicy",
		"":                                    "",
	}
	for in, want := range cases {
		if got := providerServiceName(in); got != want {
			t.Errorf("providerServiceName(%q) = %q, want %q", in, got, want)
		}
	}
}
