// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package idracreceiver

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// $expand.

func TestAnExpandThe400IsAnswerGetsOneRetryWithoutTheQueryAndIsRemembered(t *testing.T) {
	for _, code := range []int{400, 501} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			f := newFakeRedfish()
			defer f.close()
			f.rejectQuery = code
			f.set(sys+"/Memory", obj{"Members": []obj{ref(sys + "/Memory/DIMM.Socket.A1")}})
			f.set(sys+"/Memory/DIMM.Socket.A1", obj{"Id": "DIMM.Socket.A1", "Name": "DIMM A1", "CapacityMiB": 8192, "Status": obj{"Health": "OK", "State": "Enabled"}})
			r := newRig(t, f, nil)

			md := r.scrape(t)
			r.scrape(t)

			u, _ := up(t, md)
			assert.Equal(t, int64(1), u)
			assert.Equal(t, 1, pointCount(md, "hw.memory.size"))
			assert.Equal(t, 1, f.countQuery(sys+"/Storage"), "the query is sent once for the whole BMC, then dropped")
			assert.Equal(t, 0, f.countQuery(sys+"/Memory"), "the BMC that refused the query is not asked again")
		})
	}
}

func TestA500OnAnExpandIsNotARefusalOfTheQuery(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.forceStatus(sys+"/Memory", 500)
	r := newRig(t, f, nil)

	md := r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(0), u)
	assert.Equal(t, 1, f.count("GET", sys+"/Memory"), "no retry without the query")
}

// Newer firmware.

func TestTheLegacyThermalAndPowerAreReadFirstAndTheSubsystemsOnlyWhenTheyAreGone(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.set(chassis+"/ThermalSubsystem", obj{"Fans": ref(chassis + "/ThermalSubsystem/Fans")})
	f.set(chassis+"/PowerSubsystem", obj{"PowerSupplies": ref(chassis + "/PowerSubsystem/PowerSupplies")})
	r := newRig(t, f, nil)

	md := r.scrape(t)

	assert.Equal(t, 1, pointCount(md, "hw.host.power"), "host power comes from the legacy Power")
	assert.Equal(t, 1, pointCount(md, "hw.voltage"))
	assert.Equal(t, 3, pointCount(md, "hw.temperature.limit"))
	assert.Equal(t, 0, f.count("GET", chassis+"/ThermalSubsystem"))
	assert.Equal(t, 0, f.count("GET", chassis+"/PowerSubsystem"))
}

func subsystemOnly(f *fakeRedfish) {
	f.remove(chassis + "/Thermal")
	f.remove(chassis + "/Power")
	f.set(chassis+"/ThermalSubsystem", obj{
		"ThermalMetrics": ref(chassis + "/ThermalSubsystem/ThermalMetrics"),
		"Fans":           ref(chassis + "/ThermalSubsystem/Fans"),
	})
	f.set(chassis+"/ThermalSubsystem/ThermalMetrics", obj{"TemperatureReadingsCelsius": []obj{
		{"DeviceName": "System Board Inlet Temp", "Reading": 22}, {"DeviceName": "CPU1 Temp", "Reading": 41}, {"Reading": 30},
	}})
	f.set(chassis+"/ThermalSubsystem/Fans", obj{"Members": []obj{
		{"Id": "Fan.1", "Name": "Fan 1", "SpeedPercent": obj{"Reading": 40, "SpeedRPM": 6240}, "Status": obj{"Health": "OK"}},
		{"Id": "Fan.2", "Name": "Fan 2", "SpeedPercent": obj{"Reading": 55}, "Status": obj{"Health": "OK"}},
	}})
	f.set(chassis+"/PowerSubsystem", obj{"PowerSupplies": ref(chassis + "/PowerSubsystem/PowerSupplies")})
	f.set(chassis+"/PowerSubsystem/PowerSupplies", obj{"Members": []obj{
		{"Id": "PSU.Slot.1", "Name": "PS1", "PowerCapacityWatts": 800, "Metrics": ref(chassis + "/PowerSubsystem/PowerSupplies/PSU.Slot.1/Metrics"), "Status": obj{"Health": "OK"}},
		{"Id": "PSU.Slot.2", "Name": "PS2", "PowerCapacityWatts": 800, "LastPowerOutputWatts": 150, "Status": obj{"Health": "OK"}},
	}})
	f.set(chassis+"/PowerSubsystem/PowerSupplies/PSU.Slot.1/Metrics", obj{"OutputPowerWatts": obj{"Reading": 120}})
}

func TestTheSubsystemsGiveTemperaturesFansAndPowerSuppliesWhenTheLegacyResourcesAreGone(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	subsystemOnly(f)
	r := newRig(t, f, nil)

	md := r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(1), u)
	assert.Equal(t, 3, pointCount(md, "hw.temperature"))
	assert.InDelta(t, 22, byID(t, md, "hw.temperature", "System Board Inlet Temp").DoubleValue(), 1e-9)
	assert.Equal(t, 0, pointCount(md, "hw.host.power"), "the subsystems have no host power and no limits")
	assert.Equal(t, 2, pointCount(md, "hw.power_supply.usage"))
	assert.InDelta(t, 120, byID(t, md, "hw.power_supply.usage", "PSU.Slot.1").DoubleValue(), 1e-9, "from the Metrics link")
	assert.InDelta(t, 150, byID(t, md, "hw.power_supply.usage", "PSU.Slot.2").DoubleValue(), 1e-9)
	assert.InDelta(t, 6240, byID(t, md, "hw.fan.speed", "Fan.1").DoubleValue(), 1e-9, "a fan that gives RPM keeps it")
	assert.InDelta(t, 0.55, byID(t, md, "hw.fan.speed_ratio", "Fan.2").DoubleValue(), 1e-9)
}

func TestSubsystemTemperaturesAreIdentifiedByDeviceNameNotByPosition(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	subsystemOnly(f)
	r := newRig(t, f, nil)
	first := r.scrape(t)
	f.set(chassis+"/ThermalSubsystem/ThermalMetrics", obj{"TemperatureReadingsCelsius": []obj{
		{"Reading": 30}, {"DeviceName": "CPU1 Temp", "Reading": 41}, {"DeviceName": "System Board Inlet Temp", "Reading": 22},
	}})

	second := r.scrape(t)

	assert.InDelta(t, byID(t, first, "hw.temperature", "CPU1 Temp").DoubleValue(), byID(t, second, "hw.temperature", "CPU1 Temp").DoubleValue(), 1e-9)
	assert.Equal(t, attr(byID(t, first, "hw.temperature", "CPU1 Temp"), "hw.id"), attr(byID(t, second, "hw.temperature", "CPU1 Temp"), "hw.id"))
}

// Slow chassis.

func TestOneDriveThatAnswers500IsSkippedAndTheRestOfTheScrapeIsKept(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	ctrl := sys + "/Storage/RAID.Integrated.1-1"
	f.set(ctrl, obj{"Id": "RAID.Integrated.1-1", "Drives": []obj{ref(ctrl + "/Drives/Disk.Bay.0"), ref(ctrl + "/Drives/Disk.Bay.1")}})
	f.set(ctrl+"/Drives/Disk.Bay.1", obj{"Id": "Disk.Bay.1", "Name": "Disk 1", "CapacityBytes": 960000000000, "Status": obj{"Health": "OK"}})
	f.forceStatus(ctrl+"/Drives/Disk.Bay.0", 500)
	r := newRig(t, f, nil)

	md := r.scrape(t)
	r.scrape(t)

	u, _ := up(t, md)
	assert.Equal(t, int64(1), u)
	assert.Equal(t, 1, pointCount(md, "hw.physical_disk.size"), "the good drive is kept")
	assert.Equal(t, 1, pointCount(md, "hw.fan.speed"), "so is everything else")
	warns := r.logs.FilterLevelExact(zapcore.WarnLevel).FilterField(zap.String("part", "drive"))
	assert.Equal(t, 1, warns.Len(), "one warning for the episode, not one per scrape")
	assert.Equal(t, []string{"db-1/drive", "db-1/drive"}, r.skips, "every skipped read is counted, one per scrape")
}

func TestARefusalOfExpandIsForgottenAfterSixHoursAndTheQueryIsSentAgain(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.rejectQuery = 400
	r := newRig(t, f, nil)

	r.scrape(t)
	r.scrape(t)
	assert.Equal(t, 1, f.countQuery(sys+"/Storage"), "dropped after the refusal")
	r.clock.Advance(7 * time.Hour)
	r.scrape(t)

	assert.Equal(t, 2, f.countQuery(sys+"/Storage"), "asked again once, then dropped again")
}

func TestADriveThatTimesOutStillFailsTheScrapeBecauseTheBMCIsSlow(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	f.stall[sys+"/Storage/RAID.Integrated.1-1/Drives/Disk.Bay.0"] = 5 * time.Second
	r := newRig(t, f, func(c *TargetConfig) { c.RequestTimeout = 100 * time.Millisecond; c.ScrapeTimeout = 2 * time.Second })

	md := r.scrape(t)

	_, reason := up(t, md)
	assert.Equal(t, "timeout", reason)
}

func TestAtMostMaxConcurrentTargetsAreReadAtOnce(t *testing.T) {
	var fakes []*fakeRedfish
	cfg := createDefaultConfig().(*Config)
	cfg.CollectionInterval = time.Hour
	cfg.MaxConcurrent = 2
	for i := range 6 {
		f := newFakeRedfish()
		defer f.close()
		f.stall[sys] = 80 * time.Millisecond
		fakes = append(fakes, f)
		tc := validTarget()
		tc.Name = fmt.Sprintf("db-%d", i)
		tc.Address = f.srv.URL
		tc.InsecureSkipVerify = true
		cfg.Targets = append(cfg.Targets, tc)
	}
	var running, peak atomic.Int32
	for _, f := range fakes {
		f.onRequest = func(path string, start bool) {
			if path != sys {
				return
			}
			if !start {
				running.Add(-1)
				return
			}
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
		}
	}
	set, _ := settings()
	sink := new(consumertest.MetricsSink)
	r, err := newReceiver(set, cfg, sink)
	require.NoError(t, err)
	r.offset = func(string, time.Duration) time.Duration { return 0 }

	require.NoError(t, r.Start(context.Background(), componenttest.NewNopHost()))
	assert.Eventually(t, func() bool { return len(sink.AllMetrics()) >= 6 }, 20*time.Second, 20*time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	assert.LessOrEqual(t, peak.Load(), int32(2))
	assert.Greater(t, peak.Load(), int32(0))
}

// Episodes and re-probing.

func TestTheCertificateWarningComesOncePerEpisode(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	r := newRig(t, f, func(c *TargetConfig) { c.CAFile = "" })

	r.scrape(t)
	r.scrape(t)
	r.scrape(t)

	assert.Equal(t, 1, r.logs.FilterLevelExact(zapcore.WarnLevel).FilterMessageSnippet("certificate").Len())
}

func TestAnAbsentTopLevelPathIsAskedAgainAfterSixHours(t *testing.T) {
	f := newFakeRedfish()
	defer f.close()
	memory := f.routes[sys+"/Memory"]
	f.remove(sys + "/Memory")
	r := newRig(t, f, nil)

	first := r.scrape(t)
	f.set(sys+"/Memory", memory)
	r.clock.Advance(time.Hour)
	during := r.scrape(t)
	r.clock.Advance(6 * time.Hour)
	after := r.scrape(t)

	assert.Equal(t, 0, pointCount(first, "hw.memory.size"))
	assert.Equal(t, 0, pointCount(during, "hw.memory.size"), "not asked inside the six hours")
	assert.Equal(t, 1, pointCount(after, "hw.memory.size"), "asked again after six hours")
}

func TestMaxConcurrentMustBeAtLeastOne(t *testing.T) {
	cfg := validConfig()
	cfg.MaxConcurrent = 0

	assert.ErrorContains(t, cfg.Validate(), "max_concurrent must be at least 1")
	assert.Equal(t, 8, createDefaultConfig().(*Config).MaxConcurrent)
}
