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

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

// This file is the only one that speaks go-ipmi, and it sends read commands
// only; the wire allow-list in adapter_test.go enforces that.

const (
	defaultPort     = 623
	minPort         = 1
	maxPort         = 65535
	exchangeRetries = 1
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
	host, port, err := splitAddress(cfg.Address)
	if err != nil {
		return nil, err
	}
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

// splitAddress splits host or host:port. Only a missing port defaults; a port
// that does not parse is an error, or the wrong port would be polled silently.
func splitAddress(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		if address == "" {
			return "", 0, errors.New("address is empty")
		}
		return address, defaultPort, nil
	}
	if host == "" {
		return "", 0, fmt.Errorf("address %q has no host", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < minPort || port > maxPort {
		return "", 0, fmt.Errorf("address %q: port must be a number from %d to %d", address, minPort, maxPort)
	}
	return host, port, nil
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
		return 0, classifyDCMI(err)
	}
	return float64(res.CurrentPower), nil
}

// SDR FRU device locator values: the entity IDs of a processor and a memory
// device, and the device type modifiers of a DIMM and a processor FRU.
const (
	entityProcessor   = 0x03
	entityMemory      = 0x20
	modifierDIMM      = 0x01
	modifierProcessor = 0x03
)

// ListFRUDevices lists the logical FRU devices of the SDR that belong to a DIMM or a processor.
// The built-in device 0 is read apart.
func (s *ipmiSession) ListFRUDevices(ctx context.Context) ([]fruDevice, error) {
	sdrs, err := s.c.GetSDRsRaw(ctx, types.SDRRecordTypeFRUDeviceLocator)
	if err != nil {
		return nil, classify(err)
	}
	var out []fruDevice
	for _, sdr := range sdrs {
		loc := sdr.FRUDeviceLocator
		if loc == nil || !loc.IsLogicalFRUDevice || loc.AccessLUN != 0 || loc.FRUDeviceID_SlaveAddress == 0 {
			continue
		}
		var kind string
		switch {
		case loc.FRUEntityID == entityMemory || loc.DeviceTypeModifier == modifierDIMM:
			kind = hwschema.FRUDIMM
		case loc.FRUEntityID == entityProcessor || loc.DeviceTypeModifier == modifierProcessor:
			kind = hwschema.FRUCPU
		default:
			continue
		}
		// go-ipmi sets only the responder address of a message; it cannot bridge through Send Message,
		// so a device the BMC does not hold is marked and never asked (device N of the BMC is another device).
		isRemote := (loc.DeviceAccessAddress != 0 && loc.DeviceAccessAddress != types.BMC_SA) || loc.ChannelNumber != 0
		out = append(out, fruDevice{
			id: loc.FRUDeviceID_SlaveAddress, name: string(loc.DeviceIDBytes), kind: kind,
			isRemote: isRemote, isSPD: loc.DeviceTypeModifier == modifierDIMM,
		})
		if len(out) >= fruMaxDevices {
			break
		}
	}
	return out, nil
}

// ReadFRU reads the chassis, board and product areas of one FRU device.
func (s *ipmiSession) ReadFRU(ctx context.Context, deviceID uint8) ([]byte, error) {
	info, err := s.c.GetFRUInventoryAreaInfo(ctx, deviceID)
	if err != nil {
		return nil, classify(err)
	}
	if err := fruAccessError(info.DeviceAccessedByWords); err != nil {
		return nil, err
	}
	return readFRUImage(int(info.AreaSizeBytes), func(offset, count int) ([]byte, error) {
		res, err := s.c.ReadFRUData(ctx, deviceID, uint16(offset), uint8(count))
		if err != nil {
			return nil, classify(err)
		}
		data := res.Data
		if int(res.CountReturned) < len(data) {
			data = data[:res.CountReturned]
		}
		return data, nil
	})
}

// fruAccessError refuses a FRU device that is read in words; the offsets of Read FRU Data would
// then count words, and the reader counts bytes.
func fruAccessError(byWords bool) error {
	if byWords {
		return fmt.Errorf("%w: read by words", errFRUUnsupported)
	}
	return nil
}

// classifyDCMI is classify for the DCMI power read, where a reply that is too short or is not a DCMI answer
// is a fault of the BMC, marked errMalformed. The other reads keep such an error as the library gave it.
func classifyDCMI(err error) error {
	if err != nil && (errors.Is(err, types.ErrUnpackedDataTooShort) || errors.Is(err, types.ErrDCMIGroupExtensionIDMismatch) ||
		strings.Contains(strings.ToLower(err.Error()), "not enough data for")) {
		return fmt.Errorf("%w: %s", errMalformed, err)
	}
	return classify(err)
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

// isAuthText finds credential failures that go-ipmi's Connect reports as text: it joins the
// errors of every cipher suite with %v, which drops the typed status error.
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

// refusesRole reports the completion codes of that step that mean the account may not have the level.
func refusesRole(code uint8) bool {
	return code == completionRequestedLevelUnavailable || code == completionAboveChannelLimit || code == completionInsufficientPrivilege
}

func isTimeoutText(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "timeout") || strings.Contains(s, "timed out") || strings.Contains(s, "deadline exceeded")
}
