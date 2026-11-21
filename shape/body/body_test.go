// Copyright 2024 V-Lad
// SPDX-License-Identifier: Apache-2.0

package body

import "testing"

func TestCompose_ComponentTextAndPairs(t *testing.T) {
	for _, c := range []struct {
		name      string
		component string
		text      string
		pairs     []Pair
		want      string
	}{
		{"component and text", "agent.datapath", "endpoint regenerated", nil, "[agent.datapath] endpoint regenerated"},
		{"no component", "", "Connect to server", nil, "Connect to server"},
		{"pairs after text", "", "Connection ended", []Pair{{"remote", "198.51.100.190:53714"}, {"connectionCount", "23"}}, "Connection ended remote=198.51.100.190:53714 connectionCount=23"},
		{"empty pair value dropped", "", "msg", []Pair{{"a", ""}, {"b", "1"}}, "msg b=1"},
		{"pair value with space is quoted", "", "msg", []Pair{{"error", "reading from a closed fifo"}}, `msg error="reading from a closed fifo"`},
		{"component with pairs", "NETWORK", "Authentication succeeded", []Pair{{"principal", "__system"}}, "[NETWORK] Authentication succeeded principal=__system"},
		{"empty text keeps the pairs", "", "", []Pair{{"a", "1"}}, "a=1"},
		{"all empty", "", "", nil, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Compose(c.component, c.text, c.pairs...); got != c.want {
				t.Fatalf("Compose = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCompose_PairWithAQuoteInTheValue_IsEscaped(t *testing.T) {
	got := Compose("", "msg", Pair{"error", `said "no" twice`})
	if want := `msg error="said \"no\" twice"`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestVerb_IsUppercase(t *testing.T) {
	for in, want := range map[string]string{
		"get": "GET", "Post": "POST", "WATCH": "WATCH", "update": "UPDATE", "": "",
	} {
		if got := Verb(in); got != want {
			t.Errorf("Verb(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTTP_BuildsMethodPathStatusAndSize(t *testing.T) {
	for _, c := range []struct {
		name string
		in   HTTPLine
		want string
	}{
		{"uvicorn", HTTPLine{Method: "GET", Path: "/healthz", Status: 200}, "GET /healthz 200"},
		{"nginx with size", HTTPLine{Method: "POST", Path: "/api/7/envelope/", Status: 200, SizeBytes: 41}, "POST /api/7/envelope/ 200 41B"},
		{"influx query and duration", HTTPLine{Method: "POST", Path: "/write", Query: "db=zero", Status: 204, DurationMicros: 14684}, "POST /write db=zero 204 14.7ms"},
		{"lowercase verb", HTTPLine{Method: "get", Path: "/ready", Status: 200, DurationMicros: 0, HasDuration: true}, "GET /ready 200 0ms"},
		{"long duration", HTTPLine{Method: "GET", Path: "/api/live/ws", Status: 101, DurationMicros: 40_000_000}, "GET /api/live/ws 101 40s"},
		{"sub millisecond", HTTPLine{Method: "GET", Path: "/a", Status: 200, DurationMicros: 70}, "GET /a 200 70us"},
		{"host in front of the path", HTTPLine{Method: "GET", Host: "app.example.org", Path: "/api/v1/bus/status", Status: 200, DurationMicros: 11000}, "GET app.example.org/api/v1/bus/status 200 11ms"},
		{"binary units", HTTPLine{Method: "GET", Path: "/f", Status: 200, SizeBytes: 178176}, "GET /f 200 174KiB"},
		{"no status", HTTPLine{Method: "GET", Path: "/f"}, "GET /f"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := HTTP(c.in); got != c.want {
				t.Fatalf("HTTP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFormatBytes_UsesKiBMiBAndB(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0B", 41: "41B", 1023: "1023B", 1024: "1KiB", 28467: "27.8KiB", 178176: "174KiB", 1048576: "1MiB", 31 << 20: "31MiB",
	} {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDuration_PicksTheUnit(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0ms", 70: "70us", 999: "999us", 1000: "1ms", 14684: "14.7ms", 11000: "11ms", 1_500_000: "1.5s", 40_000_000: "40s", 90_000_000: "1m30s",
	} {
		if got := FormatDuration(in); got != want {
			t.Errorf("FormatDuration(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAudit_ActionTargetAndStatus(t *testing.T) {
	for _, c := range []struct{ action, target, status, want string }{
		{"backendServices.get", "", "NOT_FOUND", "backendServices.get NOT_FOUND"},
		{"GET", "leases kube-system/eks-coredns-autoscaler", "200", "GET leases kube-system/eks-coredns-autoscaler 200"},
		{"ssm:GetParameter", "/instance-access/mappings/general", "", "ssm:GetParameter /instance-access/mappings/general"},
		{"sts:AssumeRole", "AWSServiceRoleForIPAM", "", "sts:AssumeRole AWSServiceRoleForIPAM"},
		{"", "", "", ""},
	} {
		if got := Audit(c.action, c.target, c.status); got != c.want {
			t.Errorf("Audit(%q, %q, %q) = %q, want %q", c.action, c.target, c.status, got, c.want)
		}
	}
}

func TestDefaultTextWhenEmpty_UsesTheFallback(t *testing.T) {
	if got := OrOriginal("", "raw line"); got != "raw line" {
		t.Fatalf("got %q", got)
	}
	if got := OrOriginal("shaped", "raw line"); got != "shaped" {
		t.Fatalf("got %q", got)
	}
}
