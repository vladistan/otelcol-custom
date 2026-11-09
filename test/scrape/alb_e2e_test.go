// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build durability

package scrape

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
	"gopkg.in/yaml.v3"
)

const (
	albQueuePath = "/123456789012/alb"
	albBucket    = "e2e-alb"
	albPrefix    = "elb/"
	albKeyDir    = albPrefix + "AWSLogs/111122223333/elasticloadbalancing/us-east-1/2026/11/05/"
	albLogKey    = albKeyDir + "111122223333_elasticloadbalancing_us-east-1_app.example-alb.50dc6c495c0c9188_20261105T1000Z_192.0.2.50_abcd1234.log.gz"
	albOtherKey  = albKeyDir + "not-an-access-log.txt"
	albLines     = 3
)

const albSeedScript = `
import base64, boto3, gzip, json, os
kw = dict(endpoint_url='http://localhost:5000', region_name='us-east-1', aws_access_key_id='x', aws_secret_access_key='y')
s3 = boto3.client('s3', **kw)
sqs = boto3.client('sqs', **kw)
s3.create_bucket(Bucket='ALB_BUCKET')
url = sqs.create_queue(QueueName='alb')['QueueUrl']
text = base64.b64decode(os.environ['ALB_LOG_B64'])
for obj in json.loads(os.environ['ALB_OBJECTS']):
    body = gzip.compress(text) if obj['gzip'] else text
    s3.put_object(Bucket='ALB_BUCKET', Key=obj['key'], Body=body)
    sqs.send_message(QueueUrl=url, MessageBody=json.dumps({'Records': [
        {'eventSource': 'aws:s3', 'eventName': 'ObjectCreated:Put',
         's3': {'bucket': {'name': 'ALB_BUCKET'}, 'object': {'key': obj['key']}}}]}))
`

type albObject struct {
	Key  string `json:"key"`
	Gzip bool   `json:"gzip"`
}

type albRecord struct {
	attrs map[string]any
}

// albSink is an OTLP/HTTP log endpoint that keeps every record it receives.
type albSink struct {
	mu      sync.Mutex
	records []albRecord
}

func (s *albSink) snapshot() []albRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]albRecord(nil), s.records...)
}

func (s *albSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body = gz
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ld, err := (&plog.ProtoUnmarshaler{}).UnmarshalLogs(raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recordsOf(ld) {
		s.records = append(s.records, albRecord{attrs: rec.log.Attributes().AsRaw()})
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

// albLogText returns the committed fixture line repeated albLines times, each with a
// distinct client port so the records are distinguishable.
func albLogText(t *testing.T) []byte {
	t.Helper()
	line := strings.TrimSpace(string(readFile(t, "testdata/alb-access.log")))
	var out strings.Builder
	for i := range albLines {
		out.WriteString(strings.Replace(line, ":54321 ", fmt.Sprintf(":%d ", 54321+i), 1))
		out.WriteByte('\n')
	}
	return []byte(out.String())
}

// albConfig builds the collector config from the reference config: the same
// extensions, exporter and logs/aws processors, with the ALB receiver pointed at moto.
func albConfig(t *testing.T) []byte {
	t.Helper()
	ref := loadReference(t)

	receiver := section(t, ref, "receivers", "aws_s3/alb")
	for _, key := range []string{"s3downloader", "sqs"} {
		receiver[key].(map[string]any)["endpoint"] = "http://" + motoName + ":5000"
	}
	receiver["s3downloader"].(map[string]any)["s3_force_path_style"] = true

	pipeline := section(t, ref, "service", "pipelines")["logs/aws"].(map[string]any)
	processors := map[string]any{}
	order := toStrings(pipeline["processors"])
	for _, name := range order {
		processors[name] = section(t, ref, "processors", name)
	}

	cfg := map[string]any{
		"extensions": map[string]any{
			"file_storage":                section(t, ref, "extensions", "file_storage"),
			"aws_logs_encoding/elbaccess": section(t, ref, "extensions", "aws_logs_encoding/elbaccess"),
		},
		"receivers":  map[string]any{"aws_s3/alb": receiver},
		"processors": processors,
		"exporters": map[string]any{
			"otlp_http": section(t, ref, "exporters", "otlp_http"),
			"debug":     map[string]any{"verbosity": "detailed"},
		},
		"service": map[string]any{
			"extensions": []string{"file_storage", "aws_logs_encoding/elbaccess"},
			"pipelines": map[string]any{"logs/aws": map[string]any{
				"receivers":  []string{"aws_s3/alb"},
				"processors": order,
				"exporters":  []string{"otlp_http", "debug"},
			}},
		},
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// startALBStack runs moto seeded with objects and an S3 notification per object,
// then the scrape collector with the aws_s3/alb receiver.
func startALBStack(t *testing.T, objects []albObject) *albSink {
	t.Helper()
	bin := os.Getenv("SCRAPE_LINUX_BIN")
	if bin == "" {
		t.Skip("SCRAPE_LINUX_BIN is not set; run test/scrape/restart-durability.sh")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := json.Marshal(objects)
	if err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		quiet("docker", "rm", "-f", collectorName, motoName)
		quiet("docker", "volume", "rm", volumeName)
	}
	cleanup()
	t.Cleanup(func() {
		if os.Getenv("KEEP_DURABILITY_CONTAINERS") == "" {
			cleanup()
		}
	})
	quiet("docker", "network", "create", networkName)

	run(t, "docker", "run", "-d", "--name", motoName, "--network", networkName,
		"-p", "127.0.0.1:"+motoHostPort+":5000", "motoserver/moto:latest")
	seed := strings.ReplaceAll(albSeedScript, "ALB_BUCKET", albBucket)
	logB64 := base64.StdEncoding.EncodeToString(albLogText(t))
	waitFor(t, "moto", 60*time.Second, func() bool {
		return exec.Command("docker", "exec", "-e", "ALB_LOG_B64="+logB64, "-e", "ALB_OBJECTS="+string(listing),
			motoName, "python3", "-c", seed).Run() == nil
	})

	sink := new(albSink)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: sink, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	port := listener.Addr().(*net.TCPAddr).Port

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, albConfig(t), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, "docker", "create", "--name", collectorName, "--network", networkName,
		"--add-host", "host.docker.internal:host-gateway",
		"-v", volumeName+":/var/lib/otel/storage",
		"-v", bin+":/otelcol:ro", "-v", cfgPath+":/etc/otel/config.yaml:ro",
		"-e", "AWS_ACCESS_KEY_ID=x", "-e", "AWS_SECRET_ACCESS_KEY=y", "-e", "AWS_REGION=us-east-1",
		"-e", "ALB_BUCKET="+albBucket, "-e", "ALB_PREFIX="+albPrefix,
		"-e", "ALB_SQS_URL=http://"+motoName+":5000"+albQueuePath,
		"-e", fmt.Sprintf("OTEL_MAIN_OTLP_ENDPOINT=http://host.docker.internal:%d", port),
		"alpine:3.22", "/otelcol", "--config", "/etc/otel/config.yaml")
	run(t, "docker", "start", collectorName)
	return sink
}

func wantAttr(t *testing.T, rec albRecord, key string, want any) {
	t.Helper()
	if got, ok := rec.attrs[key]; !ok || got != want {
		t.Fatalf("%s = %v (present=%v), want %v", key, got, ok, want)
	}
}

// TestALBGunzipAndSQSDelete sends a gzipped access log through S3 and SQS and checks
// the decoded records at the sink and that the receiver deletes the notification.
func TestALBGunzipAndSQSDelete(t *testing.T) {
	sink := startALBStack(t, []albObject{{Key: albLogKey, Gzip: true}})
	waitFor(t, "every ALB record to reach the sink", 90*time.Second,
		func() bool { return len(sink.snapshot()) >= albLines })
	waitFor(t, "the receiver to delete the SQS message", 30*time.Second,
		func() bool { return queueDrainedAt(albQueuePath) })

	records := sink.snapshot()
	if len(records) != albLines {
		t.Fatalf("records = %d, want %d", len(records), albLines)
	}
	ports := map[any]bool{}
	for _, rec := range records {
		wantAttr(t, rec, "client.address", "192.0.2.10")
		wantAttr(t, rec, "aws.elb.status.code", int64(200))
		wantAttr(t, rec, "aws.elb.target_processing_time", 0.042)
		wantAttr(t, rec, "log.source", "aws.elb")
		wantAttr(t, rec, "event.category", "access")
		ports[rec.attrs["client.port"]] = true
	}
	if len(ports) != albLines {
		t.Fatalf("distinct client.port values = %d, want %d: %v", len(ports), albLines, ports)
	}
}

// TestALBKeyWithoutLogSuffixIsNotParsed puts a plain .txt object ahead of a real
// log object. The receiver drops the .txt object with a warning, deletes its
// notification, and produces records only for the log.
func TestALBKeyWithoutLogSuffixIsNotParsed(t *testing.T) {
	sink := startALBStack(t, []albObject{{Key: albOtherKey}, {Key: albLogKey, Gzip: true}})
	waitFor(t, "the ALB log records to reach the sink", 90*time.Second,
		func() bool { return len(sink.snapshot()) >= albLines })
	waitFor(t, "the receiver to delete both SQS messages", 30*time.Second,
		func() bool { return queueDrainedAt(albQueuePath) })
	// A parsed .txt object would add records after the log's records arrived.
	time.Sleep(5 * time.Second)
	if got := len(sink.snapshot()); got != albLines {
		t.Fatalf("records = %d, want %d: the .txt object must not be parsed", got, albLines)
	}

	var warning string
	for _, line := range strings.Split(collectorLogs("all"), "\n") {
		if strings.Contains(line, "Unsupported file format") && strings.Contains(line, albOtherKey) {
			warning = line
		}
	}
	if warning == "" {
		t.Fatalf("no \"Unsupported file format\" warning names %s\ncollector log tail:\n%s", albOtherKey, collectorLogs("60"))
	}
}
