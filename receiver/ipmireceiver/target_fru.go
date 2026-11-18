// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package ipmireceiver

import (
	"context"
	"errors"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/vladistan/otelcol-custom/receiver/hwschema"
)

const (
	// fruMaxDevices caps the DIMM and processor FRU devices read in one pass.
	fruMaxDevices = 64
	// fruTimeout is the budget of one FRU pass, apart from the sensor scrape.
	fruTimeout = 60 * time.Second
	// fruRetry is the wait after a failed pass.
	fruRetry = time.Hour
)

// readFRU reads the BMC FRU inventory when it is due, on the open session. It
// returns the points to add and never fails the scrape: a read that goes wrong
// gives status 0, one warning per run of failures and a retry in an hour.
func (t *target) readFRU(ctx context.Context, conn bmcSession, now time.Time) []func(*hwschema.Builder) {
	if now.Before(t.nextFRU) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, fruTimeout)
	defer cancel()

	var points []func(*hwschema.Builder)
	add := func(fruType string, fields map[string]string) {
		if len(fields) > 0 {
			points = append(points, func(b *hwschema.Builder) { b.FRUInfo(fruType, fields) })
		}
	}
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	// unsupported counts devices that are skipped for their kind or format; they are not failures.
	unsupported := 0
	rec, err := t.readOneFRU(ctx, conn, 0)
	switch {
	case err == nil:
		id := map[string]string{hwschema.AttrID: "fru/0"}
		add(hwschema.FRUBoard, withFields(rec.board, id))
		add(hwschema.FRUProduct, withFields(rec.product, id))
		add(hwschema.FRUChassis, withFields(rec.chassis, id))
	case errors.Is(err, errFRUUnsupported):
		unsupported++
	case !errors.Is(err, errNotPresent):
		fail(err)
	}

	devices, err := conn.ListFRUDevices(ctx)
	if err != nil && !errors.Is(err, errNotPresent) {
		fail(err)
	}
	for i, d := range devices {
		if i >= fruMaxDevices {
			break
		}
		if d.isRemote {
			unsupported++
			continue
		}
		rec, err := t.readOneFRU(ctx, conn, d.id)
		if errors.Is(err, errNotPresent) {
			continue
		}
		if errors.Is(err, errFRUUnsupported) || (d.isSPD && errors.Is(err, errFRUCorrupt)) {
			unsupported++
			continue
		}
		if err != nil {
			fail(err)
			continue
		}
		fields := rec.product
		if fields == nil {
			fields = rec.board
		}
		add(d.kind, withFields(fields, map[string]string{
			hwschema.AttrID: "fru/" + strconv.Itoa(int(d.id)), hwschema.AttrName: cleanFRUText(d.name),
		}))
	}

	if unsupported > 0 && !t.hasNotedFRU {
		t.hasNotedFRU = true
		t.env.logger.Info("BMC FRU devices that are on another controller or not in the IPMI FRU format are not read",
			zap.String("bmc", t.cfg.Name), zap.Int("devices", unsupported))
	}
	points = append(points, func(b *hwschema.Builder) { b.InventoryStatus(hwschema.SourceFRU, firstErr == nil) })
	if firstErr == nil {
		t.nextFRU = now.Add(t.cfg.FRUInterval)
		t.hasWarnedFRU = false
		return points
	}
	t.nextFRU = now.Add(min(t.cfg.FRUInterval, fruRetry))
	if !t.hasWarnedFRU {
		t.hasWarnedFRU = true
		t.env.logger.Warn("BMC FRU read failed; it is tried again within an hour",
			zap.String("bmc", t.cfg.Name), zap.String("error", t.redact(firstErr.Error())))
	}
	return points
}

func (t *target) readOneFRU(ctx context.Context, conn bmcSession, id uint8) (fruRecord, error) {
	img, err := conn.ReadFRU(ctx, id)
	if err != nil {
		return fruRecord{}, err
	}
	return decodeFRU(img)
}

// withFields returns a copy of fields with extra added; nil fields stay nil.
func withFields(fields, extra map[string]string) map[string]string {
	if fields == nil {
		return nil
	}
	out := make(map[string]string, len(fields)+len(extra))
	for k, v := range fields {
		out[k] = v
	}
	for k, v := range extra {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
