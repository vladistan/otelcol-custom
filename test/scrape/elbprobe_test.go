// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"gopkg.in/yaml.v3"

	"github.com/vladistan/otelcol-custom/processor/healthprobesampler"
	"github.com/vladistan/otelcol-custom/processor/logshapeprocessor"
)

const (
	route53Agent          = "Amazon-Route53-Health-Check-Service (ref 1234)"
	builderScrape         = "../../builder-scrape.yaml"
	probeModulePath       = "github.com/vladistan/otelcol-custom/processor/healthprobesampler"
	probeSampleModulePath = "github.com/vladistan/otelcol-custom/processor/internal/probesample"
)

// buildELBLine changes the status, path and user agent of the committed ALB access log line.
func buildELBLine(t *testing.T, status, path, agent string) []byte {
	t.Helper()
	line := string(readFile(t, "testdata/alb-access.log"))
	line = strings.Replace(line, " 200 200 ", " "+status+" "+status+" ", 1)
	line = strings.Replace(line, "http://www.example.com:80/health", "http://www.example.com:80"+path, 1)
	line = strings.Replace(line, `"curl/8.0"`, `"`+agent+`"`, 1)
	return []byte(line)
}

func newLogshape(t *testing.T, next consumer.Logs) processor.Logs {
	t.Helper()
	factory := logshapeprocessor.NewFactory()
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), factory.CreateDefaultConfig(), next)
	if err != nil {
		t.Fatalf("create logshape: %v", err)
	}
	return proc
}

// shapeELB decodes a line, tags its source and shapes it as the scrape logs/aws pipeline does.
func shapeELB(t *testing.T, line []byte) plog.Logs {
	t.Helper()
	tagged := new(consumertest.LogsSink)
	out := through(t, newALBDecoder(t), newSourceTag(t, "sourcetag/aws", tagged), tagged, line)
	shaped := new(consumertest.LogsSink)
	if err := newLogshape(t, shaped).ConsumeLogs(context.Background(), out); err != nil {
		t.Fatalf("logshape: %v", err)
	}
	if len(shaped.AllLogs()) != 1 {
		t.Fatalf("logshape forwarded %d batches, want 1", len(shaped.AllLogs()))
	}
	return shaped.AllLogs()[0]
}

func newELBSampler(t *testing.T, keepRatio float64, statusAttribute string, sink *consumertest.LogsSink) processor.Logs {
	t.Helper()
	factory := healthprobesampler.NewFactory()
	cfg := factory.CreateDefaultConfig().(*healthprobesampler.Config)
	cfg.KeepRatio = keepRatio
	if statusAttribute != "" {
		cfg.StatusAttribute = statusAttribute
	}
	proc, err := factory.CreateLogs(context.Background(), processortest.NewNopSettings(factory.Type()), cfg, sink)
	if err != nil {
		t.Fatalf("create healthprobesampler: %v", err)
	}
	return proc
}

func countSurviving(t *testing.T, sampler processor.Logs, sink *consumertest.LogsSink, ld plog.Logs) int {
	t.Helper()
	sink.Reset()
	if err := sampler.ConsumeLogs(context.Background(), ld); err != nil {
		t.Fatalf("sampler: %v", err)
	}
	return sink.LogRecordCount()
}

func TestELBRoute53ProbeIsClassedWithDefaultPatterns(t *testing.T) {
	for _, status := range []string{"200", "302"} {
		shaped := shapeELB(t, buildELBLine(t, status, "/", route53Agent))
		attrs := recordsOf(shaped)[0].log.Attributes()
		if v, ok := attrs.Get("http.response.status_code"); !ok || v.AsString() != status {
			t.Fatalf("status %s: http.response.status_code = %v (present=%v)", status, v.AsRaw(), ok)
		}

		sink := new(consumertest.LogsSink)
		if got := countSurviving(t, newELBSampler(t, 0, "", sink), sink, shaped); got != 0 {
			t.Fatalf("status %s: a Route53 probe survived keep_ratio 0 (%d records)", status, got)
		}
	}
}

func TestELBRoute53ProbeKeptIsMarkedAsProbe(t *testing.T) {
	sink := new(consumertest.LogsSink)
	shaped := shapeELB(t, buildELBLine(t, "200", "/", route53Agent))
	if got := countSurviving(t, newELBSampler(t, 1, "", sink), sink, shaped); got != 1 {
		t.Fatalf("records = %d, want 1", got)
	}
	wantStr(t, recordsOf(sink.AllLogs()[0])[0].log.Attributes(), "log.type", "health_probe")
}

func TestELBOrdinaryRowIsNotClassedAsProbe(t *testing.T) {
	sink := new(consumertest.LogsSink)
	shaped := shapeELB(t, buildELBLine(t, "200", "/orders", "curl/8.0"))
	if got := countSurviving(t, newELBSampler(t, 0, "", sink), sink, shaped); got != 1 {
		t.Fatalf("an ordinary 200 was dropped (%d records)", got)
	}
}

func TestELBStatusAttributeOverrideClassesByOriginalStatus(t *testing.T) {
	const original = "aws.elb.status.code"
	cases := map[string]int{"200": 0, "302": 0, "503": 1}
	for status, want := range cases {
		sink := new(consumertest.LogsSink)
		shaped := shapeELB(t, buildELBLine(t, status, "/", route53Agent))
		if got := countSurviving(t, newELBSampler(t, 0, original, sink), sink, shaped); got != want {
			t.Fatalf("status %s: records after keep_ratio 0 = %d, want %d", status, got, want)
		}
	}
}

func TestScrapeManifestListsHealthprobe(t *testing.T) {
	var manifest struct {
		Replaces   []string `yaml:"replaces"`
		Processors []struct {
			Gomod string `yaml:"gomod"`
		} `yaml:"processors"`
	}
	if err := yaml.Unmarshal(readFile(t, builderScrape), &manifest); err != nil {
		t.Fatalf("parse %s: %v", builderScrape, err)
	}
	isListed := false
	for _, p := range manifest.Processors {
		isListed = isListed || strings.HasPrefix(p.Gomod, probeModulePath+" ")
	}
	if !isListed {
		t.Errorf("processors of %s lack %s", builderScrape, probeModulePath)
	}
	for _, want := range []string{
		probeModulePath + " => ../../processor/healthprobesampler",
		probeSampleModulePath + " => ../../processor/internal/probesample",
	} {
		isFound := false
		for _, r := range manifest.Replaces {
			isFound = isFound || r == want
		}
		if !isFound {
			t.Errorf("replaces of %s lack %q", builderScrape, want)
		}
	}
}
