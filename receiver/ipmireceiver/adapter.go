// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bougou/go-ipmi/pkg/client"
	"github.com/bougou/go-ipmi/pkg/types"
)

// This file is the only one that speaks go-ipmi. It reaches the library through
// GetDeviceID, GetSDRsMap, GetSensorReading and GetDCMIPowerReading, plus the
// repository info call that tells the SDR timestamp, and the connect and close
// calls of the session. Nothing here sends a chassis, power, user or
// configuration command.

const (
	defaultPort      = 623
	exchangeRetries  = 1
	completionNoData = 0xCB // requested data not present
)

// newDialer returns the dialFunc that opens a go-ipmi session; exchange is the
// timeout of one request, with one retry.
func newDialer(exchange time.Duration) dialFunc {
	return func(ctx context.Context, cfg BMCConfig, level string) (bmcSession, error) {
		c, err := newClient(cfg, level, exchange)
		if err != nil {
			return nil, err
		}
		connect := c.Connect
		if cfg.IPMIVersion == "auto" {
			connect = c.ConnectAuto
		}
		if err := connect(ctx); err != nil {
			closeQuietly(c)
			return nil, classify(err)
		}
		return &ipmiSession{c: c}, nil
	}
}

// newClient builds the client without connecting: address, credentials,
// interface and cipher suite, privilege level, exchange timeout and retry.
func newClient(cfg BMCConfig, level string, exchange time.Duration) (*client.Client, error) {
	host, port := splitAddress(cfg.Address)
	c, err := client.NewClient(host, port, cfg.Username, string(cfg.Password))
	if err != nil {
		return nil, fmt.Errorf("new client: %w", err)
	}
	c.WithTimeout(exchange).WithRetry(exchangeRetries).WithMaxPrivilegeLevel(privilege(level))
	if cfg.IPMIVersion == "1.5" {
		c.WithInterface(client.InterfaceLan)
	} else {
		c.WithInterface(client.InterfaceLanplus)
		if cfg.CipherSuite != nil {
			c.WithCipherSuiteID(types.CipherSuiteID(*cfg.CipherSuite))
		}
	}
	return c, nil
}

func splitAddress(address string) (string, int) {
	if h, p, err := net.SplitHostPort(address); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			return h, n
		}
		return h, defaultPort
	}
	return address, defaultPort
}

func privilege(level string) types.PrivilegeLevel {
	switch level {
	case "administrator":
		return types.PrivilegeLevelAdministrator
	case "operator":
		return types.PrivilegeLevelOperator
	}
	return types.PrivilegeLevelUser
}

func closeQuietly(c *client.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	_ = c.Close(ctx)
}

type ipmiSession struct{ c *client.Client }

var _ bmcSession = (*ipmiSession)(nil)

func (s *ipmiSession) Close(ctx context.Context) error { return s.c.Close(ctx) }

func (s *ipmiSession) GetDeviceID(ctx context.Context) (deviceInfo, error) {
	res, err := s.c.GetDeviceID(ctx)
	if err != nil {
		return deviceInfo{}, classify(err)
	}
	return deviceInfo{firmware: fmt.Sprintf("%d.%02x", res.MajorFirmwareRevision, res.MinorFirmwareRevision)}, nil
}

// sdrHandle is what GetSensorReading needs of a record: the SDR itself for the
// conversion factors and the generator, which addresses the sensor's owner.
type sdrHandle struct {
	sdr    *types.SDR
	analog bool
	unit   types.SensorUnit
	owner  uint8
	lun    uint8
}

func (s *ipmiSession) GetSDRsMap(ctx context.Context, known time.Time) (sdrSet, bool, error) {
	info, err := s.c.GetSDRRepoInfo(ctx)
	if err != nil {
		return sdrSet{}, false, classify(err)
	}
	stamp := info.MostRecentAdditionTime
	if info.MostRecentEraseTime.After(stamp) {
		stamp = info.MostRecentEraseTime
	}
	if !known.IsZero() && stamp.Equal(known) {
		return sdrSet{}, true, nil
	}
	byGenerator, err := s.c.GetSDRsMap(ctx)
	if err != nil {
		return sdrSet{}, false, classify(err)
	}
	set := sdrSet{stamp: stamp}
	for _, byNumber := range byGenerator {
		for _, sdr := range byNumber {
			if rec, ok := toRecord(sdr); ok {
				set.records = append(set.records, rec)
			}
		}
	}
	sort.Slice(set.records, func(i, j int) bool { return set.records[i].number < set.records[j].number })
	return set, false, nil
}

// toRecord keeps the Full and Compact sensor records and maps their type to a kind.
func toRecord(sdr *types.SDR) (sdrRecord, bool) {
	var (
		sensorType types.SensorType
		unit       types.SensorUnit
		gen        types.GeneratorID
		number     types.SensorNumber
		name       []byte
	)
	rec := sdrRecord{}
	h := &sdrHandle{sdr: sdr}
	switch sdr.RecordHeader.RecordType {
	case types.SDRRecordTypeFullSensor:
		f := sdr.Full
		sensorType, unit, gen, number, name = f.SensorType, f.SensorUnit, f.GeneratorID, f.SensorNumber, f.IDStringBytes
		rec.upperCritical = threshold(f.Mask.Threshold.UCR.Readable, f.UCR_Raw, f)
		rec.upperNonCritical = threshold(f.Mask.Threshold.UNC.Readable, f.UNC_Raw, f)
	case types.SDRRecordTypeCompactSensor:
		c := sdr.Compact
		sensorType, unit, gen, number, name = c.SensorType, c.SensorUnit, c.GeneratorID, c.SensorNumber, c.IDStringBytes
	default:
		return sdrRecord{}, false
	}
	h.analog, h.unit, h.owner, h.lun = sdr.HasAnalogReading(), unit, uint8(gen.OwnerID()), uint8(gen.LUN())
	rec.number = uint8(number)
	rec.name = strings.TrimSpace(string(name))
	rec.kind = kindOf(sensorType, unit, h.analog)
	rec.handle = h
	if rec.kind != kindTemperature {
		rec.upperCritical, rec.upperNonCritical = nil, nil
	}
	return rec, true
}

// threshold converts an SDR threshold byte, when the record says it is readable.
func threshold(readable bool, raw uint8, f *types.SDRFull) *float64 {
	if !readable {
		return nil
	}
	v := types.ConvertReading(raw, f.SensorUnit.AnalogDataFormat, f.ReadingFactors, f.LinearizationFunc)
	return &v
}

func kindOf(t types.SensorType, unit types.SensorUnit, analog bool) sensorKind {
	switch {
	case t == types.SensorTypeTemperature && analog && unit.BaseUnit == types.SensorUnitType_DegreesC:
		return kindTemperature
	case t == types.SensorTypeFan && analog && unit.BaseUnit == types.SensorUnitType_RPM && !unit.Percentage:
		return kindFan
	case t == types.SensorTypeVoltage && analog && unit.BaseUnit == types.SensorUnitType_Volts:
		return kindVoltage
	case t == types.SensorTypePowerSupply && !analog:
		return kindPowerSupply
	case t == types.SensorTypePhysicalSecurity && !analog:
		return kindIntrusion
	}
	return kindOther
}

func (s *ipmiSession) GetSensorReading(ctx context.Context, rec sdrRecord) (sensorReading, error) {
	h, ok := rec.handle.(*sdrHandle)
	if !ok {
		return sensorReading{}, errors.New("sensor record has no handle")
	}
	// The sensor is addressed to its owner, as the SDR says.
	ctx = client.WithCommandContext(ctx, (&client.CommandContext{}).WithResponderAddr(h.owner).WithResponderLUN(h.lun))
	res, err := s.c.GetSensorReading(ctx, rec.number)
	if err != nil {
		return sensorReading{}, classify(err)
	}
	if res.SensorScanningDisabled || res.ReadingUnavailable {
		return sensorReading{}, errNotPresent
	}
	if h.analog {
		raw := res.Reading
		return sensorReading{value: convert(h, raw)}, nil
	}
	return sensorReading{active: res.ActiveStates.TrueEvents()}, nil
}

func convert(h *sdrHandle, raw uint8) float64 {
	switch h.sdr.RecordHeader.RecordType {
	case types.SDRRecordTypeFullSensor:
		f := h.sdr.Full
		return types.ConvertReading(raw, f.SensorUnit.AnalogDataFormat, f.ReadingFactors, f.LinearizationFunc)
	}
	return float64(raw)
}

func (s *ipmiSession) GetDCMIPowerReading(ctx context.Context) (float64, error) {
	res, err := s.c.GetDCMIPowerReading(ctx)
	if err != nil {
		return 0, classify(err)
	}
	return float64(res.CurrentPower), nil
}

// classify maps a go-ipmi or transport error to the receiver's errors. The text
// of the original is kept, so the receiver can redact it.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var status *types.RmcpStatusError
	if errors.As(err, &status) {
		switch status.StatusCode() {
		case types.RmcpStatusCodeInvalidRole, types.RmcpStatusCodeUnauthorizedRoleOfPriLevel:
			return fmt.Errorf("%w: %w: %s", errAuth, errRole, err)
		case types.RmcpStatusCodeUnauthorizedName, types.RmcpStatusCodeInvalidNameLength,
			types.RmcpStatusCodeInvalidIntegrityCheckValue:
			return fmt.Errorf("%w: %s", errAuth, err)
		}
	}
	// A transport failure is never a credential failure, whatever call it came through: go-ipmi wraps
	// the errors of every step, so the typed network error is looked at before any text.
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return fmt.Errorf("%w: %s", errTimeout, err)
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("%w: %s", errRefused, err)
	}
	if resp, ok := types.IsResponseError(err); ok {
		code := uint8(resp.CompletionCode())
		if isPrivilegeLevelCall(err) && refusesRole(code) {
			return fmt.Errorf("%w: %w: %s", errAuth, errRole, err)
		}
		if code == completionNoData {
			return fmt.Errorf("%w: %s", errNotPresent, err)
		}
		return completionError{code: code}
	}
	switch {
	case isRoleText(err):
		return fmt.Errorf("%w: %w: %s", errAuth, errRole, err)
	case isAuthText(err):
		return fmt.Errorf("%w: %s", errAuth, err)
	case isTimeoutText(err):
		return fmt.Errorf("%w: %s", errTimeout, err)
	case strings.Contains(strings.ToLower(err.Error()), "connection refused"):
		return fmt.Errorf("%w: %s", errRefused, err)
	}
	return err
}

// isAuthText finds the credential failures go-ipmi reports without a typed error. Its Connect joins
// the errors of every cipher suite with %v, so the status error arrives as text: "RMCP+ status 0xd:
// Unauthorized name". A RAKP message whose authentication code does not match is a wrong password.
func isAuthText(err error) bool {
	s := strings.ToLower(err.Error())
	for _, m := range authTexts {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

var authTexts = []string{
	"rakp authentication failed", "authentication code mismatch",
	"unauthorized name", "invalid name length", "invalid integrity check value",
}

// isRoleText finds a role refusal in text: the Open Session or RAKP status for a role the account may
// not have, or a refused Set Session Privilege Level after a good login.
func isRoleText(err error) bool {
	s := strings.ToLower(err.Error())
	for _, m := range roleTexts {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

var roleTexts = []string{
	"invalid role", "unauthorized role of privilege level",
}

// isPrivilegeLevelCall tells the error of go-ipmi's Set Session Privilege Level step, which it wraps
// whatever the inner error is.
func isPrivilegeLevelCall(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "setsessionprivilegelevel to")
}

// refusesRole reports the completion codes of that step that mean the level is not available to the
// account: request level not available (0x81), above the channel limit (0x82) and insufficient
// privilege (0xd4).
func refusesRole(code uint8) bool {
	return code == 0x81 || code == 0x82 || code == ccInsufficientPrivilege
}

func isTimeoutText(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "timeout") || strings.Contains(s, "timed out") || strings.Contains(s, "deadline exceeded")
}
