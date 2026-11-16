// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/bougou/go-ipmi/pkg/bmc"
	"github.com/bougou/go-ipmi/pkg/hal/mock"
	"github.com/bougou/go-ipmi/pkg/server"
	"github.com/bougou/go-ipmi/pkg/transport/udp"
	"github.com/bougou/go-ipmi/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The adapter implements the four-method interface and the receiver compiles
// against the interface alone.
var _ bmcSession = (*ipmiSession)(nil)

func TestSplitAddress(t *testing.T) {
	tests := map[string]struct {
		host string
		port int
	}{
		"192.0.2.12":         {"192.0.2.12", 623},
		"192.0.2.12:6230":    {"192.0.2.12", 6230},
		"bmc.example.com":    {"bmc.example.com", 623},
		"[2001:db8::1]:6230": {"2001:db8::1", 6230},
		"192.0.2.12:x":       {"192.0.2.12", 623},
	}
	for in, want := range tests {
		host, port := splitAddress(in)
		assert.Equal(t, want.host, host, in)
		assert.Equal(t, want.port, port, in)
	}
}

func TestPrivilegeLevels(t *testing.T) {
	assert.Equal(t, types.PrivilegeLevelUser, privilege("user"))
	assert.Equal(t, types.PrivilegeLevelOperator, privilege("operator"))
	assert.Equal(t, types.PrivilegeLevelAdministrator, privilege("administrator"))
	assert.Equal(t, types.PrivilegeLevelUser, privilege("anything else"))
}

func TestKindOf(t *testing.T) {
	celsius := types.SensorUnit{BaseUnit: types.SensorUnitType_DegreesC}
	rpm := types.SensorUnit{BaseUnit: types.SensorUnitType_RPM}
	percentFan := types.SensorUnit{BaseUnit: types.SensorUnitType_RPM, Percentage: true}
	volts := types.SensorUnit{BaseUnit: types.SensorUnitType_Volts}

	assert.Equal(t, kindTemperature, kindOf(types.SensorTypeTemperature, celsius, true))
	assert.Equal(t, kindFan, kindOf(types.SensorTypeFan, rpm, true))
	assert.Equal(t, kindOther, kindOf(types.SensorTypeFan, percentFan, true))
	assert.Equal(t, kindVoltage, kindOf(types.SensorTypeVoltage, volts, true))
	assert.Equal(t, kindPowerSupply, kindOf(types.SensorTypePowerSupply, types.SensorUnit{}, false))
	assert.Equal(t, kindIntrusion, kindOf(types.SensorTypePhysicalSecurity, types.SensorUnit{}, false))
	assert.Equal(t, kindOther, kindOf(types.SensorTypeTemperature, volts, true), "a temperature sensor with another unit is not read")
	assert.Equal(t, kindOther, kindOf(types.SensorTypeTemperature, celsius, false), "a discrete temperature sensor is not a reading")
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "read udp: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	tests := map[string]struct {
		in   error
		want error
	}{
		"unauthorized name":   {types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedName), errAuth},
		"bad integrity check": {types.NewRmcpStatusError(types.RmcpStatusCodeInvalidIntegrityCheckValue), errAuth},
		"role not allowed":    {types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedRoleOfPriLevel), errAuth},
		"wrapped auth":        {errors.Join(errors.New("connect"), types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedName)), errAuth},
		"data not present":    {types.NewResponseError(types.CompletionCode(0xCB), "not present"), errNotPresent},
		"deadline":            {context.DeadlineExceeded, errTimeout},
		"network timeout":     {timeoutError{}, errTimeout},
		"timeout in the text": {errors.New("exchange: timeout waiting for response"), errTimeout},
		"refused":             {&net.OpError{Err: syscall.ECONNREFUSED}, errRefused},
		"refused in the text": {errors.New("dial: connection refused"), errRefused},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, classify(tt.in), tt.want)
		})
	}
}

func TestClassifyKeepsACompletionCodeAndPassesOtherErrorsThrough(t *testing.T) {
	var ce completionError
	require.ErrorAs(t, classify(types.NewResponseError(types.CompletionCode(0xD4), "denied")), &ce)
	assert.Equal(t, uint8(ccInsufficientPrivilege), ce.code)

	plain := errors.New("something else")
	assert.Same(t, plain, classify(plain))
	assert.NoError(t, classify(nil))
}

func TestClassifiedErrorKeepsTheOriginalTextForRedaction(t *testing.T) {
	err := classify(types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedName))

	assert.Contains(t, err.Error(), "RMCP+ status")
}

// A loopback BMC from the go-ipmi simulator, with one user and cipher suite 3.
func startSimulator(t *testing.T, username, password string) BMCConfig {
	t.Helper()
	return startSimulatorMax(t, username, password, bmc.PrivilegeLevelAdministrator)
}

// startSimulatorMax is startSimulator with the highest privilege the user may get.
func startSimulatorMax(t *testing.T, username, password string, max bmc.PrivilegeLevel) BMCConfig {
	t.Helper()
	info := bmc.DeviceInfo{
		DeviceID: 32, DeviceRevision: 1, FirmwareMajor: 3, FirmwareMinor: 0x26, IPMIVersion: 0x20,
		ManufacturerID: 0x000157, ProductID: 0x0001, AdditionalDeviceSupport: 0x39,
	}
	var guid [16]byte
	copy(guid[:], "ipmireceiver-test")
	b := bmc.New(info, guid, mock.New(), bmc.WithCipherSuites([]types.CipherSuiteID{3}))
	user, err := b.Users.Add(2, username)
	require.NoError(t, err)
	user.SetPassword([]byte(password))
	user.Enabled = true
	user.ChannelAccess[1] = bmc.UserChannelAccess{MaxPrivilege: max, Enabled: true}

	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })
	srv := server.NewServer(b, udp.Wrap(pc))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()

	suite := 3
	return BMCConfig{
		Name: "sim", Address: pc.LocalAddr().String(), Username: username, Password: "secret",
		Vendor: "supermicro", CipherSuite: &suite, IPMIVersion: "2.0", PrivilegeLevel: "user",
	}
}

func TestDialOpensASessionOnTheSimulatorAndReadsTheDeviceID(t *testing.T) {
	cfg := startSimulator(t, "ro", "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := newDialer(2*time.Second)(ctx, cfg, "user")

	require.NoError(t, err)
	dev, err := conn.GetDeviceID(ctx)
	require.NoError(t, err)
	assert.Regexp(t, `^3\.[0-9a-f]{2}$`, dev.firmware)
	require.NoError(t, conn.Close(ctx))
}

func TestDialWithAWrongPasswordIsAnAuthFailure(t *testing.T) {
	cfg := startSimulator(t, "ro", "secret")
	cfg.Password = "wrong" // pragma: allowlist secret
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := newDialer(2*time.Second)(ctx, cfg, "user")

	require.Error(t, err)
	assert.ErrorIs(t, err, errAuth)
}

func TestDialWithAnUnknownUserIsAnAuthFailure(t *testing.T) {
	cfg := startSimulator(t, "ro", "secret")
	cfg.Username = "nobody"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := newDialer(2*time.Second)(ctx, cfg, "user")

	assert.ErrorIs(t, err, errAuth)
}

func TestDialAtACipherSuiteTheBMCDoesNotOfferFails(t *testing.T) {
	cfg := startSimulator(t, "ro", "secret")
	suite := 17
	cfg.CipherSuite = &suite
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := newDialer(time.Second)(ctx, cfg, "user")

	require.Error(t, err)
}

func TestDialToAClosedPortFailsWithinTheExchangeTimeout(t *testing.T) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	addr := pc.LocalAddr().String()
	require.NoError(t, pc.Close())
	suite := 3
	cfg := BMCConfig{Name: "gone", Address: addr, Username: "ro", Password: "secret", Vendor: "supermicro", CipherSuite: &suite, IPMIVersion: "2.0"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err = newDialer(300*time.Millisecond)(ctx, cfg, "user")

	require.Error(t, err)
	assert.Less(t, time.Since(start), 8*time.Second)
	assert.True(t, errors.Is(err, errRefused) || errors.Is(err, errTimeout), err.Error())
}

func TestToRecordMapsAFullTemperatureRecordWithItsUpperCriticalThreshold(t *testing.T) {
	full := &types.SDRFull{
		SensorType:   types.SensorTypeTemperature,
		SensorUnit:   types.SensorUnit{AnalogDataFormat: types.SensorAnalogUnitFormat_Unsigned, BaseUnit: types.SensorUnitType_DegreesC},
		SensorNumber: 17, IDStringBytes: []byte("System Temp "),
		ReadingFactors: types.ReadingFactors{M: 1},
		UCR_Raw:        95, UNC_Raw: 85,
	}
	full.Mask.Threshold.UCR.Readable = true
	sdr := &types.SDR{RecordHeader: &types.SDRHeader{RecordType: types.SDRRecordTypeFullSensor}, Full: full}

	rec, ok := toRecord(sdr)

	require.True(t, ok)
	assert.Equal(t, uint8(17), rec.number)
	assert.Equal(t, "System Temp", rec.name)
	assert.Equal(t, kindTemperature, rec.kind)
	require.NotNil(t, rec.upperCritical)
	assert.InDelta(t, 95, *rec.upperCritical, 1e-9)
	assert.Nil(t, rec.upperNonCritical, "a threshold the record marks unreadable is not reported")
	assert.IsType(t, &sdrHandle{}, rec.handle)
}

func TestToRecordMapsACompactDiscreteRecordAndIgnoresOtherRecordTypes(t *testing.T) {
	compact := &types.SDRCompact{
		SensorType: types.SensorTypePowerSupply, SensorNumber: 5, IDStringBytes: []byte("PS1 Status"),
		SensorUnit: types.SensorUnit{AnalogDataFormat: types.SensorAnalogUnitFormat_NotAnalog},
	}
	rec, ok := toRecord(&types.SDR{RecordHeader: &types.SDRHeader{RecordType: types.SDRRecordTypeCompactSensor}, Compact: compact})

	require.True(t, ok)
	assert.Equal(t, kindPowerSupply, rec.kind)
	assert.Equal(t, "PS1 Status", rec.name)

	_, ok = toRecord(&types.SDR{RecordHeader: &types.SDRHeader{RecordType: types.SDRRecordTypeFRUDeviceLocator}})
	assert.False(t, ok)
}

func TestConvertAppliesTheReadingFactorsOfAFullRecord(t *testing.T) {
	full := &types.SDRFull{
		SensorUnit:     types.SensorUnit{AnalogDataFormat: types.SensorAnalogUnitFormat_Unsigned},
		ReadingFactors: types.ReadingFactors{M: 2},
	}
	h := &sdrHandle{sdr: &types.SDR{RecordHeader: &types.SDRHeader{RecordType: types.SDRRecordTypeFullSensor}, Full: full}}

	assert.InDelta(t, 100, convert(h, 50), 1e-9)

	compact := &sdrHandle{sdr: &types.SDR{RecordHeader: &types.SDRHeader{RecordType: types.SDRRecordTypeCompactSensor}}}
	assert.InDelta(t, 7, convert(compact, 7), 1e-9)
}

func TestNewClientAsksForTheConfiguredPrivilegeAndInterface(t *testing.T) {
	suite := 3
	cfg := BMCConfig{Name: "x", Address: "192.0.2.1", Username: "u", Password: "p", Vendor: "dell", CipherSuite: &suite, IPMIVersion: "2.0"}

	c, err := newClient(cfg, "operator", 3*time.Second)
	require.NoError(t, err)
	assert.Equal(t, types.PrivilegeLevelOperator, c.SessionPrivilegeLevel())

	cfg.IPMIVersion = "1.5"
	cfg.CipherSuite = nil
	_, err = newClient(cfg, "user", 3*time.Second)
	require.NoError(t, err)
}

func TestGetSensorReadingNeedsTheRecordHandle(t *testing.T) {
	s := &ipmiSession{}

	_, err := s.GetSensorReading(context.Background(), sdrRecord{number: 1})

	require.Error(t, err)
}

// connect20Error builds the error go-ipmi's Connect returns when every cipher suite fails: the inner
// errors go in with %v, so the typed status error is gone.
func connect20Error(step string, inner error) error {
	suites := []types.CipherSuiteID{3}
	return fmt.Errorf("connect20 failed after try all cipher suite ids (%v), errs: \n%v", suites,
		errors.Join(fmt.Errorf("cmd: %s failed with cipher suite id (%v), err: %w", step, suites[0], inner)))
}

func TestClassifyFindsACredentialFailureInTheTextOfTheConnectError(t *testing.T) {
	tests := map[string]struct {
		in   error
		want error
	}{
		"unauthorized name":     {connect20Error("rakp1", types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedName)), errAuth},
		"invalid name length":   {connect20Error("rakp1", types.NewRmcpStatusError(types.RmcpStatusCodeInvalidNameLength)), errAuth},
		"invalid role":          {connect20Error("RMCP+ Open Session", types.NewRmcpStatusError(types.RmcpStatusCodeInvalidRole)), errRole},
		"role not allowed":      {connect20Error("rakp1", types.NewRmcpStatusError(types.RmcpStatusCodeUnauthorizedRoleOfPriLevel)), errRole},
		"integrity check":       {connect20Error("rakp3", types.NewRmcpStatusError(types.RmcpStatusCodeInvalidIntegrityCheckValue)), errAuth},
		"wrong password":        {connect20Error("rakp3", errors.New("rakp authentication failed")), errAuth},
		"privilege level":       {fmt.Errorf("SetSessionPrivilegeLevel to (Administrator) failed, err: %w", types.NewResponseError(types.CompletionCode(0x81), "level not available")), errRole},
		"two suites, one fails": {errors.New("connect20 failed after try all cipher suite ids ([3 17]), errs: \ncmd: rakp1 failed with cipher suite id (3), err: RMCP+ status 0xd: Unauthorized name"), errAuth},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := classify(tt.in)

			assert.ErrorIs(t, err, tt.want)
			assert.ErrorIs(t, err, errAuth, "a role refusal is also an authentication failure")
		})
	}
}

// privilegeLevelError is the wrap go-ipmi puts on every error of the Set Session Privilege Level step.
func privilegeLevelError(inner error) error {
	return fmt.Errorf("SetSessionPrivilegeLevel to (Administrator) failed, err: %w", inner)
}

// authCapabilitiesError is the wrap of the step that asks the BMC for its authentication capabilities.
func authCapabilitiesError(inner error) error {
	return fmt.Errorf("GetChannelAuthenticationCapabilities failed, err: %w", inner)
}

func TestClassifyNeverTakesATransportFailureForACredentialFailure(t *testing.T) {
	inners := map[string]struct {
		err  error
		want error
	}{
		"network timeout": {timeoutError{}, errTimeout},
		"deadline":        {context.DeadlineExceeded, errTimeout},
		"refused":         {&net.OpError{Op: "read", Err: syscall.ECONNREFUSED}, errRefused},
		"timeout text":    {errors.New("read udp 192.0.2.1:623: i/o timeout"), errTimeout},
		"refused text":    {errors.New("read udp: connection refused"), errRefused},
	}
	calls := map[string]func(error) error{
		"connect20":                func(e error) error { return connect20Error("rakp1", e) },
		"connect20 open session":   func(e error) error { return connect20Error("RMCP+ Open Session", e) },
		"set session privilege":    privilegeLevelError,
		"authentication":           authCapabilitiesError,
		"set privilege after dial": func(e error) error { return fmt.Errorf("dial: %w", privilegeLevelError(e)) },
	}
	for call, wrap := range calls {
		for name, in := range inners {
			t.Run(call+"/"+name, func(t *testing.T) {
				err := classify(wrap(in.err))

				assert.ErrorIs(t, err, in.want)
				assert.NotErrorIs(t, err, errAuth)
				assert.NotErrorIs(t, err, errRole)
			})
		}
	}
}

func TestClassifyReadsTheCompletionCodeOfTheSetSessionPrivilegeLevelStep(t *testing.T) {
	for _, code := range []uint8{0x81, 0x82, 0xd4} {
		t.Run(fmt.Sprintf("%#x", code), func(t *testing.T) {
			err := classify(privilegeLevelError(types.NewResponseError(types.CompletionCode(code), "level refused")))

			assert.ErrorIs(t, err, errRole)
			assert.ErrorIs(t, err, errAuth)
		})
	}
	other := classify(privilegeLevelError(types.NewResponseError(types.CompletionCode(0xc0), "busy")))
	assert.NotErrorIs(t, other, errAuth, "a busy BMC is not a refused role")
	assert.NotErrorIs(t, other, errRole)
}

func TestInsufficientPrivilegeOutsideThePrivilegeStepStaysACompletionCode(t *testing.T) {
	var ce completionError

	require.ErrorAs(t, classify(types.NewResponseError(types.CompletionCode(0xd4), "denied")), &ce)
	assert.Equal(t, uint8(ccInsufficientPrivilege), ce.code)
}

func TestClassifyLeavesConfigurationFailuresOfTheHandshakeAlone(t *testing.T) {
	for name, in := range map[string]error{
		"no cipher suite match": connect20Error("RMCP+ Open Session", types.NewRmcpStatusError(types.RmcpStatusCodeNoCipherSuiteMatch)),
		"out of sessions":       connect20Error("RMCP+ Open Session", types.NewRmcpStatusError(types.RmcpStatusCodeNoResToCreateSess)),
		"inactive session":      connect20Error("rakp1", types.NewRmcpStatusError(types.RmcpStatusCodeInactiveSessionID)),
	} {
		t.Run(name, func(t *testing.T) {
			assert.NotErrorIs(t, classify(in), errAuth)
		})
	}
}

func TestDialWithAUserThatMayNotHaveTheRoleIsARoleFailure(t *testing.T) {
	cfg := startSimulatorMax(t, "ro", "secret", bmc.PrivilegeLevelUser)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := newDialer(2*time.Second)(ctx, cfg, "administrator")

	if err == nil {
		_ = conn.Close(ctx)
		t.Skip("the simulator grants a role above the user's maximum")
	}
	assert.ErrorIs(t, err, errRole)
	assert.ErrorIs(t, err, errAuth)
}
