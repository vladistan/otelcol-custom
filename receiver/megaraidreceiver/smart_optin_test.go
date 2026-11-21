// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package megaraidreceiver

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
	"github.com/vladistan/otelcol-custom/receiver/smartdecode"
)

func optInConfig(on bool) *Config {
	cfg := smartConfig()
	cfg.SmartWhenPowerUnknown = on
	return cfg
}

func noRegisters() (response, error) { return response{status: statusOK}, nil }

func standbyAnswer() (response, error) {
	return response{status: statusSCSIError, sense: ataSense(smartdecode.ATARegisters{SectorCount: 0x00})}, nil
}

func smartCommands(dev *fakeDevice) int {
	var n int
	for _, p := range dev.passes {
		if p.cdb[14] != 0xE5 {
			n++
		}
	}
	return n
}

// ataCounts counts the ATA pass-through frames sent to the drives: CHECK POWER MODE and the rest.
// A scrape also reads the SAS drive of the lab fake, so LOG SENSE frames are left out.
func ataCounts(dev *fakeDevice) (powerChecks, smart int) {
	for _, p := range dev.passes {
		switch {
		case p.cdb[0] != ataPassThrough16:
		case p.cdb[14] == 0xE5:
			powerChecks++
		default:
			smart++
		}
	}
	return powerChecks, smart
}

func sataSmartState(t *testing.T, s *scraper) string {
	t.Helper()
	state, ok := find(s.scrape(context.Background()).metrics, metricSmartState, hwschema.AttrID, "ZC1AAAA1")
	require.True(t, ok)
	return state.attrs[attrSmartState]
}

func TestConfig_SmartWhenPowerUnknownKeyIsAbsent_IsFalse(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	require.NoError(t, confmap.NewFromStringMap(map[string]any{"smart": true}).Unmarshal(cfg))

	assert.False(t, cfg.SmartWhenPowerUnknown)
}

func TestConfig_SmartWhenPowerUnknownIsTrue_Loads(t *testing.T) {
	cfg := createDefaultConfig().(*Config)

	require.NoError(t, confmap.NewFromStringMap(map[string]any{"smart": true, "smart_when_power_unknown": true}).Unmarshal(cfg))

	assert.True(t, cfg.SmartWhenPowerUnknown)
	require.NoError(t, cfg.Validate())
}

func TestConfig_SmartWhenPowerUnknownIsNotABoolean_FailsNamingTheKey(t *testing.T) {
	for _, bad := range []any{"maybe", []any{true}, map[string]any{"on": true}} {
		cfg := createDefaultConfig().(*Config)

		err := confmap.NewFromStringMap(map[string]any{"smart_when_power_unknown": bad}).Unmarshal(cfg)

		require.Error(t, err, "%v", bad)
		assert.Contains(t, err.Error(), "smart_when_power_unknown", "%v", bad)
	}
}

func TestScrape_OptionOffAndNoRegisters_SendsNoSmartCommandAndWarnsOncePerDrive(t *testing.T) {
	dev, _ := powerDrive(t, noRegisters)
	s, logs, clock := newTestScraper(t, optInConfig(false), dev)

	first := sataSmartState(t, s)
	drives, _ := ataCounts(dev)
	require.Positive(t, drives)
	clock.advance(2 * s.cfg.SmartInterval)
	second := sataSmartState(t, s)

	powerChecks, smart := ataCounts(dev)
	assert.Equal(t, smartUnavailable, first)
	assert.Equal(t, smartUnavailable, second)
	assert.Equal(t, 2*drives, powerChecks, "every drive was read again after smart_interval")
	assert.Zero(t, smart, "only CHECK POWER MODE was sent")
	assert.Equal(t, drives, logs.FilterMessage(msgNoPowerState).Len(), "one warning for each drive, not for each read")
}

func TestReadOneSmart_OptionOnAndAnActiveDrive_ReadsSmartAfterOnePowerCheck(t *testing.T) {
	dev, _ := powerDrive(t, func() (response, error) {
		return response{status: statusSCSIError, sense: ataSense(smartdecode.ATARegisters{SectorCount: 0xFF})}, nil
	})
	s, logs, _ := newTestScraper(t, optInConfig(true), dev)

	res, stop := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	assert.False(t, stop)
	assert.Equal(t, smartOK, res.state)
	require.NotNil(t, res.data)
	powerChecks, smart := ataCounts(dev)
	assert.Equal(t, 1, powerChecks)
	assert.Positive(t, smart, "SMART commands were sent")
	assert.Equal(t, byte(0xE5), dev.passes[0].cdb[14], "the power check goes first")
	assert.Zero(t, logs.FilterMessage(msgNoPowerState).Len())
}

func TestScrape_OptionOnAndNoRegisters_ReadsOncePerSmartIntervalAndKeepsTheResult(t *testing.T) {
	dev, _ := powerDrive(t, noRegisters)
	s, _, clock := newTestScraper(t, optInConfig(true), dev)

	assert.Equal(t, smartOK, sataSmartState(t, s))
	powerChecks, smart := ataCounts(dev)
	require.Positive(t, powerChecks)
	require.Positive(t, smart)

	clock.advance(s.cfg.SmartInterval / 2)
	cached := sataSmartState(t, s)

	gotPower, gotSmart := ataCounts(dev)
	assert.Equal(t, smartOK, cached, "the cached result is still reported")
	assert.Equal(t, powerChecks, gotPower, "no frame inside smart_interval")
	assert.Equal(t, smart, gotSmart, "no frame inside smart_interval")

	clock.advance(s.cfg.SmartInterval)
	assert.Equal(t, smartOK, sataSmartState(t, s))
	again, _ := ataCounts(dev)
	assert.Equal(t, 2*powerChecks, again, "every drive was read again after smart_interval")
}

func TestReadOneSmart_OptionOnAndNoRegisters_ReadsSmartWithoutTheSkipWarning(t *testing.T) {
	dev, _ := powerDrive(t, noRegisters)
	s, logs, _ := newTestScraper(t, optInConfig(true), dev)

	res, stop := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	assert.False(t, stop)
	assert.Equal(t, smartOK, res.state)
	require.NotNil(t, res.data)
	assert.Positive(t, smartCommands(dev), "SMART commands were sent")
	assert.Zero(t, logs.FilterMessage(msgNoPowerState).Len())
}

func TestReadOneSmart_StandbyAnswer_SkipsTheDriveWhateverTheOptionSays(t *testing.T) {
	for _, on := range []bool{false, true} {
		dev, _ := powerDrive(t, standbyAnswer)
		s, _, _ := newTestScraper(t, optInConfig(on), dev)

		res, _ := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

		assert.Equal(t, smartStandby, res.state, "option %v", on)
		assert.Zero(t, smartCommands(dev), "option %v", on)
	}
}

func TestReadOneSmart_OptionOnAndAPowerCheckThatFails_StillEndsThePass(t *testing.T) {
	dev, _ := powerDrive(t, func() (response, error) { return response{}, errControllerBusy })
	s, _, _ := newTestScraper(t, optInConfig(true), dev)

	_, stop := s.readOneSmart(dev, testCtrl, newCtrlState(), 4)

	assert.True(t, stop)
	assert.Zero(t, smartCommands(dev))
}

// hostName matches the lab host names, which no published text may carry.
var hostName = regexp.MustCompile(`\bpve\d+\b`)

func TestDocs_MegaRAIDSection_StatesTheWakeRiskAndTheCondition(t *testing.T) {
	section := megaraidSection(t)

	for _, want := range []string{"smart_when_power_unknown", "wake", "smart_interval", "unverified", "no power state"} {
		assert.Contains(t, section, want)
	}
	assert.NotContains(t, section, "never wakes a drive", "with the option on, a read can wake a drive")
	assert.Regexp(t, `(?s)Drives that sleep.*smart_when_power_unknown`, section, "the sleeping-drive paragraph names the option")
}

// recentChangelog returns the Unreleased section followed by the first version section after it.
func recentChangelog(text string) (string, error) {
	_, rest, ok := strings.Cut(text, "## [Unreleased]")
	if !ok {
		return "", errors.New("the Unreleased heading is missing")
	}
	unreleased, afterUnreleased, found := strings.Cut(rest, "\n## [")
	if !found {
		return unreleased, nil
	}
	newest, _, _ := strings.Cut(afterUnreleased, "\n## [")
	return unreleased + "\n## [" + newest, nil
}

func changelogRecent(t *testing.T) string {
	t.Helper()
	recent, err := recentChangelog(readDoc(t, "CHANGELOG.md"))
	require.NoError(t, err)
	return recent
}

func TestDocs_Changelog_StatesTheOptionAndItsRisk(t *testing.T) {
	recent := changelogRecent(t)

	for _, want := range []string{"smart_when_power_unknown", "wake", "no power state", "unverified"} {
		assert.Contains(t, recent, want)
	}
}

func TestRecentChangelog_PicksUnreleasedAndTheNewestVersionOnly(t *testing.T) {
	const marker = "needle"
	body := func(unreleased, newest, older string) string {
		return "# Changelog\n\n## [Unreleased]\n" + unreleased + "\n## [0.2.0] - 2026-01-02\n" + newest +
			"\n## [0.1.0] - 2026-01-01\n" + older + "\n"
	}
	cases := []struct {
		name    string
		text    string
		present bool
		wantErr bool
	}{
		{"OnlyInUnreleased", body(marker, "x", "y"), true, false},
		{"OnlyInNewestVersion", body("x", marker, "y"), true, false},
		{"OnlyInOlderVersion", body("x", "y", marker), false, false},
		{"AbsentEverywhere", body("x", "y", "z"), false, false},
		{"NoUnreleasedHeading", "# Changelog\n\n## [0.2.0] - 2026-01-02\n" + marker + "\n", false, true},
		{"UnreleasedWithoutVersion", "# Changelog\n\n## [Unreleased]\n" + marker + "\n", true, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := recentChangelog(c.text)

			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.present, strings.Contains(got, marker))
		})
	}
}

func TestDocs_PublishedText_CarriesNoHostName(t *testing.T) {
	unreleased := changelogRecent(t)
	_, readmeRest, ok := strings.Cut(readDoc(t, "README.md"), "### MegaRAID controllers")
	require.True(t, ok, "the README section is missing")
	readme, _, _ := strings.Cut(readmeRest, "\n### ")
	published := map[string]string{
		"CHANGELOG.md recent":  unreleased,
		"docs/otel-metrics.md": megaraidSection(t),
		"README.md MegaRAID":   readme,
		"edge-megaraid.yaml":   readDoc(t, "test/configs/edge-megaraid.yaml"),
	}

	for name, text := range published {
		assert.Empty(t, hostName.FindAllString(text, -1), "%s names a host", name)
	}
}

func TestSample_SmartWhenPowerUnknownIsFalseWithARiskComment(t *testing.T) {
	var line string
	for _, l := range strings.Split(readDoc(t, "test/configs/edge-megaraid.yaml"), "\n") {
		if strings.Contains(l, "smart_when_power_unknown:") {
			line = l
		}
	}

	require.NotEmpty(t, line, "the key is missing from the sample")
	assert.Contains(t, line, "smart_when_power_unknown: false")
	assert.Contains(t, line, "wake")
}
