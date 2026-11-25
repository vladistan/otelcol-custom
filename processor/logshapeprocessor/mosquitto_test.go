// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// The lines follow a real broker log; the client and service names are invented.
func TestMosquitto_ConnectionLines_LoseTheIsoTimeAndKeepTheText(t *testing.T) {
	texts := []string{
		"New connection from 127.0.0.1:36468 on port 1883.",
		"New client connected from 127.0.0.1:50882 as auto-7C019B88 (p2, c1, k60).",
		"Client auto-7C019B88 disconnected.",
		"Client local.sensor-mirror-in disconnected: Broken pipe.",
		"Connecting bridge rabbitmq (rabbitmq.demo.svc:1883)",
	}
	for _, text := range texts {
		line := "2026-11-19T22:56:53: " + text
		lr, _ := runW4(t, input{body: line})

		w4Body(t, lr, text)
		w4Time(t, lr, time.Date(2026, 11, 19, 22, 56, 53, 0, time.UTC))
		w4Severity(t, lr, plog.SeverityNumberUnspecified, "")
		if attrStr(t, lr.Attributes(), "log.record.original") != line {
			t.Fatalf("attributes = %v", lr.Attributes().AsRaw())
		}
	}
}

func TestMosquitto_TextThatIsNotAMosquittoLine_IsNotClaimed(t *testing.T) {
	const line = "2026-11-19T22:56:53: the job finished"

	lr, _ := runW4(t, input{body: line})

	w4Untouched(t, lr, line, map[string]any{})
}

func TestMosquitto_ImpossibleDate_IsUnchangedAndCounted(t *testing.T) {
	h := newHarness(t, defaultConfig())
	const bad = "2026-13-45T25:61:61: New connection from 127.0.0.1:1 on port 1883."

	lr, _ := h.run(input{body: bad})

	w4Untouched(t, lr, bad, map[string]any{})
	w4Failed(t, h, "mosquitto")
}
