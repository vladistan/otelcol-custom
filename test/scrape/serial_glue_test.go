// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/vladistan/otelcol-custom/processor/logrecombineprocessor"
	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor"
)

const serialFixture = "../../processor/logrecombineprocessor/testdata/serial-fragments.jsonl"

type serialRow struct {
	Node string `json:"node"`
	UID  string `json:"uid"`
	Text string `json:"text"`
}

func loadSerialRows(t *testing.T) []serialRow {
	t.Helper()
	f, err := os.Open(serialFixture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []serialRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "#") {
			continue
		}
		var r serialRow
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, r)
	}
	return rows
}

// glueFromReferenceConfig builds the processor from the logrecombine/serial block of scrape.yaml, read
// the way the collector reads it, so a wrong key or value fails here.
func glueFromReferenceConfig(t *testing.T, next consumer.Logs) processor.Logs {
	t.Helper()
	retrieved, err := confmap.NewRetrievedFromYAML(readFile(t, configsDir+"/scrape.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	conf, err := retrieved.AsConf()
	if err != nil {
		t.Fatal(err)
	}
	block, err := conf.Sub("processors::logrecombine/serial")
	if err != nil {
		t.Fatal(err)
	}
	factory := logrecombineprocessor.NewFactory()
	cfg := factory.CreateDefaultConfig()
	if err := block.Unmarshal(cfg); err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg.(interface{ Validate() error }); !ok {
		t.Fatal("the processor config has no Validate")
	} else if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, next)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func serialLogs(r serialRow) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("gcp.label.instance_id", r.Node)
	rec := rl.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	rec.Body().SetStr(r.Text)
	a := rec.Attributes()
	a.PutStr("cloud.resource_id", "serialconsole.googleapis.com/serial_port_1_output")
	a.PutStr("gcp.label.is_text_escaped", "true")
	a.PutStr("log.record.uid", r.UID)
	return ld
}

var shaLabels = regexp.MustCompile(`\[(\d+) sha256 labels\]`)

func TestSerialGlue_WrappedLinesReachLogshapeAsWholeLines(t *testing.T) {
	rows := loadSerialRows(t)
	if len(rows) == 0 {
		t.Fatal("no fixture rows")
	}
	shapeSink := &consumertest.LogsSink{}
	shapeFactory := logshapeprocessor.NewFactory()
	shape, err := shapeFactory.CreateLogs(context.Background(), processortest.NewNopSettings(shapeFactory.Type()), shapeFactory.CreateDefaultConfig(), shapeSink)
	if err != nil {
		t.Fatal(err)
	}
	// The glue feeds logshape, as the pipeline of scrape.yaml does.
	glue := glueFromReferenceConfig(t, shape)

	ctx := context.Background()
	if err := shape.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := glue.Start(ctx, nil); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := glue.ConsumeLogs(ctx, serialLogs(r)); err != nil {
			t.Fatal(err)
		}
	}
	if err := glue.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := shape.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	var records []plog.LogRecord
	for _, ld := range shapeSink.AllLogs() {
		for i := range ld.ResourceLogs().Len() {
			scopes := ld.ResourceLogs().At(i).ScopeLogs()
			for j := range scopes.Len() {
				for k := range scopes.At(j).LogRecords().Len() {
					records = append(records, scopes.At(j).LogRecords().At(k))
				}
			}
		}
	}
	// 75 rows: 7 bare lines are dropped and the wrapped lines join, which leaves 48 records.
	if len(records) != 48 {
		t.Fatalf("got %d records, want 48", len(records))
	}
	var wholeLabelRuns, tagged int
	for _, rec := range records {
		body := rec.Body().Str()
		if strings.Contains(body, `sha256:`) && !strings.Contains(body, `\r\n`) {
			// a hash list that stays in a body must hold whole digests only
			for _, m := range regexp.MustCompile(`sha256:[0-9a-f]+`).FindAllString(body, -1) {
				if len(m) != len("sha256:")+64 {
					t.Errorf("a cut digest reached the body: %q", m)
				}
			}
		}
		if m := shaLabels.FindStringSubmatch(body); m != nil && m[1] != "1" {
			wholeLabelRuns++
		}
		if _, ok := rec.Attributes().Get("process.pid"); ok {
			tagged++
		}
		if v, ok := rec.Attributes().Get("log.record.original"); ok && strings.Count(v.Str(), `\r\n`) > 1 {
			t.Errorf("log.record.original keeps an interior fragment end")
		}
	}
	if wholeLabelRuns == 0 {
		t.Error("no record summarises its hash list: the joined line did not reach the serialconsole branch whole")
	}
	if tagged == 0 {
		t.Error("no tagged line was shaped with its process id")
	}
}
