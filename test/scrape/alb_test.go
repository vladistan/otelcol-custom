// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package scrape

import (
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/encoding/awslogsencodingextension"
	"go.opentelemetry.io/collector/consumer/consumertest"
)

func newALBDecoder(t *testing.T) logsDecoder {
	block := section(t, loadReference(t), "extensions", "aws_logs_encoding/elbaccess")
	return newDecoder(t, awslogsencodingextension.NewFactory(), block)
}

func TestALBAccessLogFields(t *testing.T) {
	sink := new(consumertest.LogsSink)
	proc := newSourceTag(t, "sourcetag/aws", sink)
	out := through(t, newALBDecoder(t), proc, sink, readFile(t, "testdata/alb-access.log"))

	recs := recordsOf(out)
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	attrs := recs[0].log.Attributes()
	wantStr(t, attrs, "client.address", "192.0.2.10")
	wantStr(t, attrs, "log.source", "aws.elb")
	wantStr(t, attrs, "event.category", "access")
	if v, ok := attrs.Get("aws.elb.status.code"); !ok || v.Int() != 200 {
		t.Fatalf("aws.elb.status.code = %v (present=%v), want 200", v.AsRaw(), ok)
	}
	if v, ok := attrs.Get("aws.elb.target_processing_time"); !ok || v.Double() != 0.042 {
		t.Fatalf("aws.elb.target_processing_time = %v (present=%v), want 0.042", v.AsRaw(), ok)
	}
}

// The receiver strips .gz before it matches the suffix, so an ALB object key
// ending in .log.gz must match the configured suffix as .log.
func TestALBSuffixMatchesObjectKey(t *testing.T) {
	block := section(t, loadReference(t), "receivers", "aws_s3/alb")
	encodings := block["encodings"].([]any)
	enc := encodings[0].(map[string]any)
	if enc["extension"] != "aws_logs_encoding/elbaccess" {
		t.Fatalf("encoding extension = %v, want aws_logs_encoding/elbaccess", enc["extension"])
	}
	key := "elb/AWSLogs/111122223333/elasticloadbalancing/us-east-1/2026/11/05/" +
		"111122223333_elasticloadbalancing_us-east-1_app.example-alb.50dc6c495c0c9188_20261105T1000Z_192.0.2.50_abcd1234.log.gz"
	stripped := strings.TrimSuffix(key, ".gz")
	if suffix := enc["suffix"].(string); !strings.HasSuffix(stripped, suffix) {
		t.Fatalf("suffix %q does not match %q", suffix, stripped)
	}
}
