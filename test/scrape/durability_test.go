// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

//go:build durability

package scrape

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
	"gopkg.in/yaml.v3"
)

const (
	motoName      = "scrape-moto"
	collectorName = "scrape-collector"
	networkName   = "scrape-net"
	volumeName    = "scrape-storage"
	motoHostPort  = "5055"
	queuePath     = "/123456789012/cloudtrail"
	objectCount   = 3
	recordsPerObj = 3
	eventIDKey    = "aws.cloudtrail.event_id"

	// Fake credential values: the scan looks for them in logs and zpages.
	fakeAccessID = "durability-access-id-0001"
	fakeSigning  = "durability-signing-value-7c1e"
	fakeSession  = "durability-session-3f9a"
)

const seedScript = `
import boto3, json
kw = dict(endpoint_url='http://localhost:5000', region_name='us-east-1', aws_access_key_id='x', aws_secret_access_key='y')
s3 = boto3.client('s3', **kw)
sqs = boto3.client('sqs', **kw)
s3.create_bucket(Bucket='example-cloudtrail-bucket')
url = sqs.create_queue(QueueName='cloudtrail')['QueueUrl']
for i in range(3):
    recs = [{'awsRegion': 'us-east-1', 'eventID': 'dur-%d-%d' % (i, j), 'eventName': 'GetObject',
             'eventSource': 's3.amazonaws.com', 'eventTime': '2026-10-25T10:0%d:0%dZ' % (i, j),
             'eventVersion': '1.08', 'recipientAccountId': '111122223333',
             'userIdentity': {'accountId': '111122223333', 'type': 'IAMUser', 'userName': 'alice'}} for j in range(3)]
    key = 'AWSLogs/111122223333/CloudTrail/us-east-1/2026/10/25/obj%d.json' % i
    s3.put_object(Bucket='example-cloudtrail-bucket', Key=key, Body=json.dumps({'Records': recs}))
    sqs.send_message(QueueUrl=url, MessageBody=json.dumps({'Records': [
        {'eventSource': 'aws:s3', 'eventName': 'ObjectCreated:Put',
         's3': {'bucket': {'name': 'example-cloudtrail-bucket'}, 'object': {'key': key}}}]}))
`

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// quiet runs a command and ignores failure; it is for cleanup.
func quiet(name string, args ...string) { _ = exec.Command(name, args...).Run() }

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\ncollector log tail:\n%s", what, collectorLogs("60"))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func collectorLogs(tail string) string {
	out, _ := exec.Command("docker", "logs", "--tail", tail, collectorName).CombinedOutput()
	return string(out)
}

// otlpSink is an OTLP/HTTP log endpoint that can refuse requests.
type otlpSink struct {
	mu        sync.Mutex
	blocked   bool
	attempted map[string]bool
	delivered map[string]bool
	badSource int
}

func newSink() *otlpSink {
	return &otlpSink{blocked: true, attempted: map[string]bool{}, delivered: map[string]bool{}}
}

func (s *otlpSink) setBlocked(b bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blocked = b
}

func (s *otlpSink) count(m map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(m)
}

func (s *otlpSink) badSourceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.badSource
}

func (s *otlpSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	var ids []string
	for _, rec := range recordsOf(ld) {
		if v, ok := rec.log.Attributes().Get(eventIDKey); ok {
			ids = append(ids, v.Str())
		}
		if v, ok := rec.log.Attributes().Get("log.source"); !ok || v.Str() != "aws.cloudtrail" {
			s.badSource++
		}
	}
	for _, id := range ids {
		s.attempted[id] = true
	}
	if s.blocked {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	for _, id := range ids {
		s.delivered[id] = true
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

// durabilityConfig builds the collector config from the reference config: the same
// extensions, exporter and logs/aws processors, with the CloudTrail receiver pointed
// at moto. A non-empty batchTimeout adds a batch processor before the exporter,
// which the reference config deliberately lacks (negative control).
func durabilityConfig(t *testing.T, batchTimeout string) []byte {
	t.Helper()
	ref := loadReference(t)
	pick := func(kind, name string) any { return section(t, ref, kind, name) }

	receiver := section(t, ref, "receivers", "awss3/cloudtrail")
	for _, key := range []string{"s3downloader", "sqs"} {
		receiver[key].(map[string]any)["endpoint"] = "http://" + motoName + ":5000"
	}
	receiver["s3downloader"].(map[string]any)["s3_force_path_style"] = true

	pipeline := section(t, ref, "service", "pipelines")["logs/aws"].(map[string]any)
	processors := map[string]any{}
	var order []string
	for _, name := range pipeline["processors"].([]any) {
		processors[name.(string)] = pick("processors", name.(string))
		order = append(order, name.(string))
	}
	if batchTimeout != "" {
		processors["batch"] = map[string]any{"timeout": batchTimeout}
		order = append(order, "batch")
	}

	cfg := map[string]any{
		"extensions": map[string]any{
			"file_storage":                 pick("extensions", "file_storage"),
			"zpages":                       map[string]any{"endpoint": "0.0.0.0:55679"},
			"aws_logs_encoding/cloudtrail": pick("extensions", "aws_logs_encoding/cloudtrail"),
		},
		"receivers":  map[string]any{"awss3/cloudtrail": receiver},
		"processors": processors,
		"exporters": map[string]any{
			"otlphttp": pick("exporters", "otlphttp"),
			"debug":    map[string]any{"verbosity": "detailed"},
		},
		"service": map[string]any{
			"extensions": []string{"zpages", "file_storage", "aws_logs_encoding/cloudtrail"},
			"pipelines": map[string]any{"logs/aws": map[string]any{
				"receivers":  []string{"awss3/cloudtrail"},
				"processors": order,
				"exporters":  []string{"otlphttp", "debug"},
			}},
		},
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type stack struct {
	sink *otlpSink
}

// startStack runs moto with seeded CloudTrail objects and the collector, with the sink blocked.
func startStack(t *testing.T, batchTimeout string) *stack {
	t.Helper()
	bin := os.Getenv("SCRAPE_LINUX_BIN")
	if bin == "" {
		t.Skip("SCRAPE_LINUX_BIN is not set; run test/scrape/restart-durability.sh")
	}
	bin, err := filepath.Abs(bin)
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
	waitFor(t, "moto", 60*time.Second, func() bool {
		return exec.Command("docker", "exec", motoName, "python3", "-c", seedScript).Run() == nil
	})

	sink := newSink()
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: sink, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	port := listener.Addr().(*net.TCPAddr).Port

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, durabilityConfig(t, batchTimeout), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, "docker", "create", "--name", collectorName, "--network", networkName,
		"--add-host", "host.docker.internal:host-gateway",
		"-v", volumeName+":/var/lib/otel/storage",
		"-v", bin+":/otelcol:ro", "-v", cfgPath+":/etc/otel/config.yaml:ro",
		"-p", "127.0.0.1:55679:55679",
		"-e", "AWS_ACCESS_KEY_ID="+fakeAccessID, "-e", "AWS_SECRET_ACCESS_KEY="+fakeSigning,
		"-e", "AWS_SESSION_TOKEN="+fakeSession, "-e", "AWS_REGION=us-east-1",
		"-e", "CLOUDTRAIL_SQS_URL=http://"+motoName+":5000"+queuePath,
		"-e", fmt.Sprintf("OTEL_MAIN_OTLP_ENDPOINT=http://host.docker.internal:%d", port),
		"alpine:3.22", "/otelcol", "--config", "/etc/otel/config.yaml")
	run(t, "docker", "start", collectorName)
	return &stack{sink: sink}
}

var queueCount = regexp.MustCompile(`<Name>(ApproximateNumberOfMessages(?:NotVisible)?)</Name>\s*<Value>(\d+)</Value>`)

// queueDrained reports whether every SQS message was received and deleted.
func queueDrained() bool {
	// Moto picks the service from the credential scope of the request, so the header names sqs.
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+motoHostPort+queuePath,
		strings.NewReader("Action=GetQueueAttributes&AttributeName.1=All&Version=2012-11-05"))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=x/20260101/us-east-1/sqs/aws4_request, SignedHeaders=host, Signature=x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	found := queueCount.FindAllStringSubmatch(string(body), -1)
	if len(found) != 2 {
		return false
	}
	for _, m := range found {
		if m[2] != "0" {
			return false
		}
	}
	return true
}

func TestRestartDurability(t *testing.T) {
	s := startStack(t, "")
	total := objectCount * recordsPerObj
	waitFor(t, "every CloudTrail record to reach the blocked sink", 90*time.Second,
		func() bool { return s.sink.count(s.sink.attempted) == total })
	if got := s.sink.count(s.sink.delivered); got != 0 {
		t.Fatalf("%d records delivered while the sink was blocked", got)
	}

	logsBefore := collectorLogs("all")
	run(t, "docker", "kill", collectorName)
	if out := run(t, "docker", "run", "--rm", "-v", volumeName+":/s", "alpine:3.22", "ls", "-A", "/s"); strings.TrimSpace(out) == "" {
		t.Fatal("the persistent queue directory is empty after the kill")
	}
	run(t, "docker", "start", collectorName)
	time.Sleep(5 * time.Second)
	if got := s.sink.count(s.sink.delivered); got != 0 {
		t.Fatalf("%d records delivered while the sink was still blocked", got)
	}

	s.sink.setBlocked(false)
	waitFor(t, "all records after the sink returned", 120*time.Second,
		func() bool { return s.sink.count(s.sink.delivered) == total })
	if n := s.sink.badSourceCount(); n != 0 {
		t.Fatalf("%d records lacked log.source=aws.cloudtrail", n)
	}

	var scan strings.Builder
	zpagesBytes := 0
	scan.WriteString(logsBefore)
	scan.WriteString(collectorLogs("all"))
	for _, page := range []string{"pipelinez", "servicez", "extensionz", "featurez"} {
		resp, err := http.Get("http://127.0.0.1:55679/debug/" + page)
		if err != nil {
			t.Fatalf("zpages %s: %v", page, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		zpagesBytes += len(body)
		scan.Write(body)
	}
	text := scan.String()
	if !strings.Contains(text, eventIDKey) {
		t.Fatal("credential scan input lacks the debug exporter output")
	}
	if zpagesBytes == 0 {
		t.Fatal("credential scan input lacks the zpages pages")
	}
	for _, needle := range []string{fakeAccessID, fakeSigning, fakeSession} {
		if strings.Contains(text, needle) {
			t.Fatalf("credential value %q found in logs, zpages or debug output", needle)
		}
	}
}

// killAtIntake kills the collector the moment the receiver has deleted every SQS
// message, then restarts it and releases the sink. It returns the records delivered
// after waiting up to wait.
func killAtIntake(t *testing.T, batchTimeout string, wait time.Duration) int {
	t.Helper()
	s := startStack(t, batchTimeout)
	waitFor(t, "the receiver to delete every SQS message", 90*time.Second, queueDrained)
	run(t, "docker", "kill", collectorName)
	run(t, "docker", "start", collectorName)
	s.sink.setBlocked(false)

	deadline := time.Now().Add(wait)
	total := objectCount * recordsPerObj
	for time.Now().Before(deadline) && s.sink.count(s.sink.delivered) < total {
		time.Sleep(500 * time.Millisecond)
	}
	return s.sink.count(s.sink.delivered)
}

// TestKillAtIntakeLosesNothing kills the collector right after the SQS messages are
// deleted: with the persistent queue as the first buffer every record must survive.
func TestKillAtIntakeLosesNothing(t *testing.T) {
	total := objectCount * recordsPerObj
	if got := killAtIntake(t, "", 120*time.Second); got != total {
		t.Fatalf("%d of %d records arrived after a kill at intake", got, total)
	}
}

// TestKillAtIntakeWithBatchLosesRecords is the negative control: a slow batch
// processor ahead of the queue holds records that SQS no longer has, so the same
// kill must lose some. If it does not, the test above proves nothing.
func TestKillAtIntakeWithBatchLosesRecords(t *testing.T) {
	total := objectCount * recordsPerObj
	got := killAtIntake(t, "10s", 45*time.Second)
	t.Logf("control: %d of %d records arrived", got, total)
	if got >= total {
		t.Fatalf("all %d records arrived although a batch processor held them at the kill; the kill test cannot detect loss", got)
	}
}
