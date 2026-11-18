// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

const (
	sys          = "/redfish/v1/Systems/System.Embedded.1"
	chassis      = "/redfish/v1/Chassis/System.Embedded.1"
	mgr          = "/redfish/v1/Managers/iDRAC.Embedded.1"
	sessionToken = "tok-0123456789" // gitleaks:allow
)

type obj = map[string]any

func ref(path string) obj { return obj{"@odata.id": path} }

// fakeRedfish is an iDRAC-like Redfish service over TLS. Routes map a path to a
// JSON body; the query string is ignored. It records what the receiver does.
type fakeRedfish struct {
	srv *httptest.Server

	mu          sync.Mutex
	routes      map[string]any
	raw         map[string][]byte        // exact body per path, sent as it is (for broken JSON)
	status      map[string]int           // forced status per path
	statusOnce  map[string]int           // forced status for the next request to the path only
	stall       map[string]time.Duration // delay before answering
	loginStatus int                      // status of the next logins, 0 for the normal 201
	logins      int
	deletes     int
	authHeader  bool // an Authorization header was ever seen
	paths       []string
	uris        []string                      // method and request URI with the query
	rejectQuery int                           // status for any GET that carries a query, 0 to accept
	onRequest   func(path string, start bool) // called when a request starts and when it ends
	current     int
	maxInFlight int
}

func newFakeRedfish() *fakeRedfish {
	f := &fakeRedfish{routes: dellFixture(), raw: map[string][]byte{}, status: map[string]int{}, statusOnce: map[string]int{}, stall: map[string]time.Duration{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeRedfish) close() { f.srv.Close() }

func (f *fakeRedfish) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.current++
	if f.current > f.maxInFlight {
		f.maxInFlight = f.current
	}
	if r.Header.Get("Authorization") != "" {
		f.authHeader = true
	}
	path := r.URL.Path
	f.paths = append(f.paths, r.Method+" "+path)
	f.uris = append(f.uris, r.Method+" "+r.URL.RequestURI())
	delay := f.stall[path]
	hook := f.onRequest
	f.mu.Unlock()
	if hook != nil {
		hook(path, true)
		defer hook(path, false)
	}
	defer func() {
		f.mu.Lock()
		f.current--
		f.mu.Unlock()
	}()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	switch {
	case r.Method == http.MethodPost && path == "/redfish/v1/SessionService/Sessions":
		f.login(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/redfish/v1/SessionService/Sessions/"):
		f.mu.Lock()
		f.deletes++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		f.get(w, r, path)
	}
}

func (f *fakeRedfish) login(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.logins++
	status := f.loginStatus
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("X-Auth-Token", sessionToken)
	w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/7")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{}`))
}

func (f *fakeRedfish) get(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("X-Auth-Token") != sessionToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	forced, hasForced := f.statusOnce[path]
	if hasForced {
		delete(f.statusOnce, path)
	}
	if code, ok := f.status[path]; ok {
		forced, hasForced = code, true
	}
	body, found := f.routes[path]
	rawBody, isRaw := f.raw[path]
	reject := f.rejectQuery
	f.mu.Unlock()
	if reject != 0 && r.URL.RawQuery != "" {
		w.WriteHeader(reject)
		return
	}
	if hasForced {
		w.WriteHeader(forced)
		return
	}
	if isRaw {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rawBody)
		return
	}
	if !found {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeRedfish) count(method, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.paths {
		if p == method+" "+path {
			n++
		}
	}
	return n
}

// countQuery counts the GETs of a path that carried a query.
func (f *fakeRedfish) countQuery(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, u := range f.uris {
		if strings.HasPrefix(u, "GET "+path+"?") {
			n++
		}
	}
	return n
}

func (f *fakeRedfish) set(path string, body any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[path] = body
}

func (f *fakeRedfish) setRaw(path string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw[path] = body
}

func (f *fakeRedfish) forceStatus(path string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[path] = code
}

func (f *fakeRedfish) forceOnce(path string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusOnce[path] = code
}

func (f *fakeRedfish) remove(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.routes, path)
}

// dellFixture is a PowerEdge R640 with iDRAC 9: the legacy Thermal and Power.
func dellFixture() map[string]any {
	return map[string]any{
		sys: obj{
			"Id": "System.Embedded.1", "Model": "PowerEdge R640", "SerialNumber": "ABC1234", "Manufacturer": "Dell Inc.",
			"PowerState": "On", "Status": obj{"Health": "OK", "State": "Enabled"},
		},
		chassis: obj{
			"Id": "System.Embedded.1", "Status": obj{"Health": "OK"},
			"Thermal": ref(chassis + "/Thermal"), "Power": ref(chassis + "/Power"),
		},
		chassis + "/Thermal": obj{
			"Temperatures": []obj{
				{"MemberId": "iDRAC.Embedded.1#SystemBoardInletTemp", "Name": "System Board Inlet Temp", "ReadingCelsius": 22,
					"UpperThresholdCritical": 47, "UpperThresholdNonCritical": 42, "Status": obj{"Health": "OK"}},
				{"MemberId": "iDRAC.Embedded.1#CPU1Temp", "Name": "CPU1 Temp", "ReadingCelsius": 41,
					"UpperThresholdCritical": 95, "Status": obj{"Health": "OK"}},
			},
			"Fans": []obj{
				{"MemberId": "0", "FanName": "System Board Fan1A", "Reading": 6240, "ReadingUnits": "RPM", "Status": obj{"Health": "OK"}},
				{"MemberId": "1", "FanName": "System Board Fan2A", "Reading": 40, "ReadingUnits": "Percent", "Status": obj{"Health": "Warning"}},
			},
		},
		chassis + "/Power": obj{
			"PowerControl": []obj{{"PowerConsumedWatts": 250}},
			"PowerSupplies": []obj{
				{"MemberId": "PSU.Slot.1", "Name": "PS1 Status", "PowerCapacityWatts": 750, "LastPowerOutputWatts": 120, "Status": obj{"Health": "OK"}},
				{"MemberId": "PSU.Slot.2", "Name": "PS2 Status", "PowerCapacityWatts": 750, "LastPowerOutputWatts": 130, "Status": obj{"Health": "Critical"}},
			},
			"Voltages": []obj{{"MemberId": "iDRAC.Embedded.1#PS1Voltage", "Name": "PS1 Voltage 1", "ReadingVolts": 12.1}},
		},
		sys + "/Storage": obj{"Members": []obj{ref(sys + "/Storage/RAID.Integrated.1-1")}},
		sys + "/Storage/RAID.Integrated.1-1": obj{
			"Id": "RAID.Integrated.1-1", "Drives": []obj{ref(sys + "/Storage/RAID.Integrated.1-1/Drives/Disk.Bay.0")},
		},
		sys + "/Storage/RAID.Integrated.1-1/Drives/Disk.Bay.0": obj{
			"Id": "Disk.Bay.0", "Name": "Solid State Disk 0", "CapacityBytes": 480103981056, "PredictedMediaLifeLeftPercent": 80,
			"Status": obj{"Health": "OK"},
		},
		sys + "/Memory": obj{"Members": []obj{
			{"Id": "DIMM.Socket.A1", "Name": "DIMM A1", "CapacityMiB": 16384, "Status": obj{"Health": "OK", "State": "Enabled"}},
			{"Id": "DIMM.Socket.A2", "Name": "DIMM A2", "CapacityMiB": 0, "Status": obj{"State": "Absent"}},
		}},
		sys + "/Processors": obj{"Members": []obj{
			{"Id": "CPU.Socket.1", "Name": "CPU 1", "Status": obj{"Health": "OK", "State": "Enabled"}},
			{"Id": "CPU.Socket.2", "Name": "CPU 2", "Status": obj{"Health": "Warning", "State": "Enabled"}},
		}},
		mgr: obj{"Id": "iDRAC.Embedded.1", "FirmwareVersion": "7.00.00.173", "Status": obj{"Health": "OK"}},
	}
}
