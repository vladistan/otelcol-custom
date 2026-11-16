// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const bytesPerMiB = 1 << 20

// emit adds points to a builder; collect gathers them first so a read that
// fails halfway sends nothing.
type emit func(*hwschema.Builder)

// info is what the BMC says about itself, for the resource attributes.
type info struct{ model, serial, firmware string }

type status struct {
	Health *string `json:"Health"`
	State  string  `json:"State"`
}

func (s status) absent() bool { return s.State == "Absent" }

// stateOf maps a Status.Health to an hw.state; a null or unknown health has none.
func (s status) stateOf() (string, bool) {
	if s.Health == nil {
		return "", false
	}
	return hwschema.StateFromHealth(*s.Health)
}

func addStatus(out *[]emit, hwType, id, name string, s status) {
	if state, ok := s.stateOf(); ok {
		*out = append(*out, func(b *hwschema.Builder) { b.Status(hwType, id, name, state) })
	}
}

// collect reads the selected resources. A path the BMC does not serve is
// skipped; any other failure ends the read.
func (t *target) collect(ctx context.Context) ([]emit, info, error) {
	var (
		out []emit
		inf info
	)
	has := func(r string) bool { return slices.Contains(t.cfg.Resources, r) }
	steps := []struct {
		resource string
		run      func(context.Context, *[]emit, *info) error
	}{
		{"bmc", t.readManager},
		{"system", t.readSystem},
		{"thermal", t.readThermal},
		{"power", t.readPower},
		{"storage", t.readStorage},
		{"memory", func(ctx context.Context, o *[]emit, _ *info) error {
			return t.readMembers(ctx, o, t.profile.Memory(), "memory", hwschema.TypeMemory, memoryPoints)
		}},
		{"processors", func(ctx context.Context, o *[]emit, _ *info) error {
			return t.readMembers(ctx, o, t.profile.Processors(), "cpu", hwschema.TypeCPU, nil)
		}},
	}
	for _, s := range steps {
		if !has(s.resource) {
			continue
		}
		if err := s.run(ctx, &out, &inf); err != nil && !errors.Is(err, errAbsent) {
			return nil, info{}, err
		}
	}
	return out, inf, nil
}

func (t *target) readManager(ctx context.Context, out *[]emit, inf *info) error {
	var m struct {
		FirmwareVersion string `json:"FirmwareVersion"`
		Status          status `json:"Status"`
	}
	if err := t.client.get(ctx, t.profile.Manager, &m); err != nil {
		return err
	}
	inf.firmware = m.FirmwareVersion
	addStatus(out, hwschema.TypeBMC, "bmc", "BMC", m.Status)
	return nil
}

func (t *target) readSystem(ctx context.Context, out *[]emit, inf *info) error {
	var s struct {
		Model        string `json:"Model"`
		SerialNumber string `json:"SerialNumber"`
		Status       status `json:"Status"`
	}
	if err := t.client.get(ctx, t.profile.System, &s); err != nil {
		return err
	}
	inf.model, inf.serial = s.Model, s.SerialNumber
	addStatus(out, hwschema.TypeHost, "system", "System", s.Status)

	var c struct {
		Status status `json:"Status"`
	}
	if err := t.client.get(ctx, t.profile.Chassis, &c); err != nil {
		if errors.Is(err, errAbsent) {
			return nil
		}
		return err
	}
	addStatus(out, hwschema.TypeEnclosure, "chassis", "Chassis", c.Status)
	return nil
}

// Thermal.

type temperature struct {
	MemberID      string   `json:"MemberId"`
	Name          string   `json:"Name"`
	Reading       *float64 `json:"ReadingCelsius"`
	UpperCritical *float64 `json:"UpperThresholdCritical"`
	UpperNonCrit  *float64 `json:"UpperThresholdNonCritical"`
}

type fan struct {
	MemberID string   `json:"MemberId"`
	ID       string   `json:"Id"`
	Name     string   `json:"Name"`
	FanName  string   `json:"FanName"`
	Reading  *float64 `json:"Reading"`
	Units    string   `json:"ReadingUnits"`
	Percent  *struct {
		Reading *float64 `json:"Reading"`
		RPM     *float64 `json:"SpeedRPM"`
	} `json:"SpeedPercent"`
	Status status `json:"Status"`
}

// readThermal reads the legacy Thermal resource, which carries the readings, the thresholds and the fans, and
// falls back to ThermalSubsystem only when the BMC no longer serves it. Dell keeps both; reading the legacy one
// first keeps the metric set the same before and after a firmware update.
func (t *target) readThermal(ctx context.Context, out *[]emit, _ *info) error {
	err := t.readLegacyThermal(ctx, out)
	if errors.Is(err, errAbsent) {
		return t.readThermalSubsystem(ctx, out)
	}
	return err
}

func (t *target) readThermalSubsystem(ctx context.Context, out *[]emit) error {
	var sub struct {
		Metrics *struct {
			ID string `json:"@odata.id"`
		} `json:"ThermalMetrics"`
		Fans *struct {
			ID string `json:"@odata.id"`
		} `json:"Fans"`
	}
	if err := t.client.get(ctx, t.profile.ThermalSubsystem(), &sub); err != nil {
		return err
	}
	if sub.Metrics != nil {
		var m struct {
			Readings []struct {
				Device  string   `json:"DeviceName"`
				Reading *float64 `json:"Reading"`
			} `json:"TemperatureReadingsCelsius"`
		}
		if err := t.client.get(ctx, sub.Metrics.ID, &m); err != nil && !errors.Is(err, errAbsent) {
			return err
		}
		seen := map[string]bool{}
		for i, r := range m.Readings {
			if r.Reading == nil {
				continue
			}
			// The device name keeps the id when the BMC reorders the list; a missing or repeated name falls back
			// to the position.
			id := r.Device
			if id == "" || seen[id] {
				id = itoa(i)
			}
			seen[id] = true
			addTemperature(out, temperature{MemberID: id, Name: r.Device, Reading: r.Reading})
		}
	}
	if sub.Fans != nil {
		members, err := t.members(ctx, sub.Fans.ID)
		if err != nil {
			return err
		}
		for _, raw := range members {
			var f fan
			if json.Unmarshal(raw, &f) == nil {
				addFan(out, f)
			}
		}
	}
	return nil
}

func (t *target) readLegacyThermal(ctx context.Context, out *[]emit) error {
	var th struct {
		Temperatures []temperature `json:"Temperatures"`
		Fans         []fan         `json:"Fans"`
	}
	if err := t.client.get(ctx, t.profile.Thermal(), &th); err != nil {
		return err
	}
	for _, tmp := range th.Temperatures {
		if tmp.Reading != nil {
			addTemperature(out, tmp)
		}
	}
	for _, f := range th.Fans {
		addFan(out, f)
	}
	return nil
}

func addTemperature(out *[]emit, tmp temperature) {
	id := "temperature/" + tmp.MemberID
	name := tmp.Name
	reading := *tmp.Reading
	*out = append(*out, func(b *hwschema.Builder) {
		b.Temperature(id, name, "", reading)
		if tmp.UpperCritical != nil {
			b.TemperatureLimit(id, name, "", hwschema.LimitCritical, *tmp.UpperCritical)
		}
		if tmp.UpperNonCrit != nil {
			b.TemperatureLimit(id, name, "", hwschema.LimitHighDegraded, *tmp.UpperNonCrit)
		}
	})
}

func addFan(out *[]emit, f fan) {
	member := firstOf(f.MemberID, f.ID)
	id, name := "fan/"+member, firstOf(f.FanName, f.Name)
	switch {
	case f.Percent != nil && (f.Percent.Reading != nil || f.Percent.RPM != nil):
		if f.Percent.Reading != nil {
			ratio := *f.Percent.Reading / 100
			*out = append(*out, func(b *hwschema.Builder) { b.FanSpeedRatio(id, name, "", ratio) })
		}
		if f.Percent.RPM != nil {
			rpm := *f.Percent.RPM
			*out = append(*out, func(b *hwschema.Builder) { b.FanSpeed(id, name, "", rpm) })
		}
	case f.Reading != nil && strings.EqualFold(f.Units, "RPM"):
		rpm := *f.Reading
		*out = append(*out, func(b *hwschema.Builder) { b.FanSpeed(id, name, "", rpm) })
	case f.Reading != nil && strings.EqualFold(f.Units, "Percent"):
		ratio := *f.Reading / 100
		*out = append(*out, func(b *hwschema.Builder) { b.FanSpeedRatio(id, name, "", ratio) })
	}
	addStatus(out, hwschema.TypeFan, id, name, f.Status)
}

// Power.

type powerSupply struct {
	MemberID string   `json:"MemberId"`
	ID       string   `json:"Id"`
	Name     string   `json:"Name"`
	Capacity *float64 `json:"PowerCapacityWatts"`
	Output   *float64 `json:"LastPowerOutputWatts"`
	Metrics  *struct {
		ID string `json:"@odata.id"`
	} `json:"Metrics"`
	Status status `json:"Status"`
}

// readPower reads the legacy Power resource first (host power, supplies, voltages) and falls back to
// PowerSubsystem only when the BMC no longer serves it. That resource has no host power or voltages.
func (t *target) readPower(ctx context.Context, out *[]emit, _ *info) error {
	err := t.readLegacyPower(ctx, out)
	if errors.Is(err, errAbsent) {
		return t.readPowerSubsystem(ctx, out)
	}
	return err
}

func (t *target) readPowerSubsystem(ctx context.Context, out *[]emit) error {
	var sub struct {
		Supplies *struct {
			ID string `json:"@odata.id"`
		} `json:"PowerSupplies"`
	}
	if err := t.client.get(ctx, t.profile.PowerSubsystem(), &sub); err != nil {
		return err
	}
	if sub.Supplies == nil {
		return nil
	}
	members, err := t.members(ctx, sub.Supplies.ID)
	if err != nil {
		return err
	}
	for _, raw := range members {
		var p powerSupply
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		if p.Output == nil && p.Metrics != nil {
			p.Output = t.supplyOutput(ctx, p.Metrics.ID)
		}
		addSupply(out, p)
	}
	return nil
}

func (t *target) readLegacyPower(ctx context.Context, out *[]emit) error {
	var p struct {
		Control []struct {
			Consumed *float64 `json:"PowerConsumedWatts"`
		} `json:"PowerControl"`
		Supplies []powerSupply `json:"PowerSupplies"`
		Voltages []struct {
			MemberID string   `json:"MemberId"`
			Name     string   `json:"Name"`
			Reading  *float64 `json:"ReadingVolts"`
		} `json:"Voltages"`
	}
	if err := t.client.get(ctx, t.profile.Power(), &p); err != nil {
		return err
	}
	if len(p.Control) > 0 && p.Control[0].Consumed != nil {
		watts := *p.Control[0].Consumed
		*out = append(*out, func(b *hwschema.Builder) { b.HostPower(watts) })
	}
	for _, s := range p.Supplies {
		addSupply(out, s)
	}
	for _, v := range p.Voltages {
		if v.Reading == nil {
			continue
		}
		id, name, volts := "voltage/"+v.MemberID, v.Name, *v.Reading
		*out = append(*out, func(b *hwschema.Builder) { b.Voltage(id, name, "", volts) })
	}
	return nil
}

// supplyOutput reads the output power of a supply from its Metrics resource, where newer Redfish
// schemas keep it. A Metrics resource that does not answer gives no value.
func (t *target) supplyOutput(ctx context.Context, path string) *float64 {
	var m struct {
		Output *struct {
			Reading *float64 `json:"Reading"`
		} `json:"OutputPowerWatts"`
	}
	if err := t.client.get(ctx, path, &m); err != nil || m.Output == nil {
		return nil
	}
	return m.Output.Reading
}

func addSupply(out *[]emit, p powerSupply) {
	id, name := "power_supply/"+firstOf(p.MemberID, p.ID), p.Name
	if p.Output != nil {
		usage := *p.Output
		*out = append(*out, func(b *hwschema.Builder) { b.PowerSupplyUsage(id, name, "", usage) })
	}
	if p.Capacity != nil {
		limit := *p.Capacity
		*out = append(*out, func(b *hwschema.Builder) { b.PowerSupplyLimit(id, name, "", limit) })
		if p.Output != nil && limit > 0 {
			ratio := *p.Output / limit
			*out = append(*out, func(b *hwschema.Builder) { b.PowerSupplyUtilization(id, name, "", ratio) })
		}
	}
	addStatus(out, hwschema.TypePowerSupply, id, name, p.Status)
}

// Storage, memory and processors.

func (t *target) readStorage(ctx context.Context, out *[]emit, _ *info) error {
	controllers, err := t.members(ctx, t.profile.Storage())
	if err != nil {
		return err
	}
	for _, raw := range controllers {
		var c struct {
			Drives []struct {
				ID string `json:"@odata.id"`
			} `json:"Drives"`
		}
		if json.Unmarshal(raw, &c) != nil {
			continue
		}
		for _, d := range c.Drives {
			var drive struct {
				ID       string   `json:"Id"`
				Name     string   `json:"Name"`
				Capacity *float64 `json:"CapacityBytes"`
				LifeLeft *float64 `json:"PredictedMediaLifeLeftPercent"`
				Status   status   `json:"Status"`
			}
			if err := t.client.get(ctx, d.ID, &drive); err != nil {
				if t.soft("drive", d.ID, err) {
					continue
				}
				return err
			}
			addDrive(out, drive.ID, drive.Name, drive.Capacity, drive.LifeLeft, drive.Status)
		}
	}
	return nil
}

func addDrive(out *[]emit, idPart, name string, capacity, lifeLeft *float64, s status) {
	id := "physical_disk/" + idPart
	if capacity != nil {
		size := *capacity
		*out = append(*out, func(b *hwschema.Builder) { b.PhysicalDiskSize(id, name, "", size) })
	}
	if lifeLeft != nil {
		used := 1 - *lifeLeft/100
		*out = append(*out, func(b *hwschema.Builder) { b.PhysicalDiskEndurance(id, name, "", used) })
	}
	addStatus(out, hwschema.TypePhysicalDisk, id, name, s)
}

type member struct {
	ID          string   `json:"Id"`
	Name        string   `json:"Name"`
	CapacityMiB *float64 `json:"CapacityMiB"`
	Status      status   `json:"Status"`
}

// memoryPoints adds the size of a DIMM.
func memoryPoints(out *[]emit, id, name string, m member) {
	if m.CapacityMiB != nil {
		size := *m.CapacityMiB * bytesPerMiB
		*out = append(*out, func(b *hwschema.Builder) { b.MemorySize(id, name, "", size) })
	}
}

// readMembers reads a collection of DIMMs or processors: one status each, and
// extra points through the callback. A member whose state is Absent is skipped.
func (t *target) readMembers(ctx context.Context, out *[]emit, path, prefix, hwType string, extra func(*[]emit, string, string, member)) error {
	members, err := t.members(ctx, path)
	if err != nil {
		return err
	}
	for _, raw := range members {
		var m member
		if json.Unmarshal(raw, &m) != nil || m.Status.absent() {
			continue
		}
		id := prefix + "/" + m.ID
		if extra != nil {
			extra(out, id, m.Name, m)
		}
		addStatus(out, hwType, id, m.Name, m.Status)
	}
	return nil
}

// members reads a collection with $expand. A BMC that answers 400 or 501 to the query is read once more
// without it and is not sent the query again. A member the BMC left as a bare link is read on its own.
func (t *target) members(ctx context.Context, path string) ([]json.RawMessage, error) {
	var coll struct {
		Members []json.RawMessage `json:"Members"`
	}
	query := expandQuery
	if t.env.now().Before(t.noExpandUntil) {
		query = ""
	}
	err := t.client.get(ctx, path+query, &coll)
	if query != "" && isStatus(err, http.StatusBadRequest, http.StatusNotImplemented) {
		t.noExpandUntil = t.env.now().Add(reprobeAfter)
		t.env.logger.Info("The BMC does not accept $expand; collections are read member by member",
			zap.String("target", t.cfg.Name))
		err = t.client.get(ctx, path, &coll)
	}
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, 0, len(coll.Members))
	for _, raw := range coll.Members {
		var probe map[string]json.RawMessage
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		if len(probe) > 1 {
			out = append(out, raw)
			continue
		}
		var link struct {
			ID string `json:"@odata.id"`
		}
		if json.Unmarshal(raw, &link) != nil || link.ID == "" {
			continue
		}
		var full json.RawMessage
		if err := t.client.get(ctx, link.ID, &full); err != nil {
			if t.soft("member", link.ID, err) {
				continue
			}
			return nil, err
		}
		out = append(out, full)
	}
	return out, nil
}

// isStatus reports whether err is an unexpected HTTP status with one of the codes.
func isStatus(err error, codes ...int) bool {
	var se *statusError
	return errors.As(err, &se) && slices.Contains(codes, se.code)
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func itoa(i int) string { return strconv.Itoa(i) }
