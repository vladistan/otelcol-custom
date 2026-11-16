// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8sdiscovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	observerpkg "github.com/open-telemetry/opentelemetry-collector-contrib/extension/observer"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mongodbreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mysqlreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/nginxreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/postgresqlreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/rabbitmqreceiver"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/receivercreator"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/redisreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// observableObserver stands in for k8s_observer: it hands the endpoints a test gives it to the creator.
type observableObserver struct {
	notify observerpkg.Notify
}

func (*observableObserver) Start(context.Context, component.Host) error { return nil }
func (*observableObserver) Shutdown(context.Context) error              { return nil }

func (o *observableObserver) ListAndWatch(notify observerpkg.Notify) { o.notify = notify }
func (o *observableObserver) Unsubscribe(observerpkg.Notify)         { o.notify = nil }

// testHost lists the fake observer and the receiver factories the edge compiles in, the two things
// receiver_creator asks its host for.
type testHost struct {
	component.Host
	extensions map[component.ID]component.Component
	factories  map[component.Type]receiver.Factory
}

func (h testHost) GetExtensions() map[component.ID]component.Component { return h.extensions }

func (h testHost) GetFactory(kind component.Kind, typ component.Type) component.Factory {
	if kind != component.KindReceiver {
		return nil
	}
	if f, ok := h.factories[typ]; ok {
		return f
	}
	return nil
}

func receiverFactories() map[component.Type]receiver.Factory {
	all := []receiver.Factory{
		prometheusreceiver.NewFactory(), redisreceiver.NewFactory(), postgresqlreceiver.NewFactory(),
		mysqlreceiver.NewFactory(), mongodbreceiver.NewFactory(), rabbitmqreceiver.NewFactory(), nginxreceiver.NewFactory(),
	}
	out := map[component.Type]receiver.Factory{}
	for _, f := range all {
		out[f.Type()] = f
	}
	return out
}

func creatorBlock(t *testing.T, path, name string) map[string]any {
	t.Helper()
	return child(t, child(t, loadDoc(t, path), "receivers"), name)
}

// setScrapeInterval shortens the interval in the reference template so a test scrapes at once.
func setScrapeInterval(t *testing.T, block map[string]any, interval string) {
	t.Helper()
	prom := child(t, child(t, block, "receivers"), "prometheus")
	scrapes, _ := child(t, child(t, prom, "config"), "config")["scrape_configs"].([]any)
	if len(scrapes) != 1 {
		t.Fatalf("got %d scrape_configs, want one", len(scrapes))
	}
	scrapes[0].(map[string]any)["scrape_interval"] = interval
}

type startedCreator struct {
	receiver receiver.Metrics
	observer *observableObserver
	logs     *observer.ObservedLogs
	sink     *consumertest.MetricsSink
}

func startCreator(t *testing.T, block map[string]any) startedCreator {
	t.Helper()
	factory := receivercreator.NewFactory()
	cfg := factory.CreateDefaultConfig()
	if err := confmap.NewFromStringMap(block).Unmarshal(cfg); err != nil {
		t.Fatalf("load the creator block: %v", err)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	set := receivertest.NewNopSettings(factory.Type())
	set.Logger = zap.New(core)
	sink := new(consumertest.MetricsSink)
	rcvr, err := factory.CreateMetrics(context.Background(), set, cfg, sink)
	if err != nil {
		t.Fatalf("create the creator: %v", err)
	}
	obs := &observableObserver{}
	host := testHost{
		Host:       componenttest.NewNopHost(),
		extensions: map[component.ID]component.Component{component.MustNewID("k8s_observer"): obs},
		factories:  receiverFactories(),
	}
	if err := rcvr.Start(context.Background(), host); err != nil {
		t.Fatalf("start the creator: %v", err)
	}
	t.Cleanup(func() { _ = rcvr.Shutdown(context.Background()) })
	if obs.notify == nil {
		t.Fatal("the creator did not subscribe to the observer")
	}
	return startedCreator{receiver: rcvr, observer: obs, logs: logs, sink: sink}
}

func podEndpoint(ip string, annotations map[string]string) observerpkg.Endpoint {
	return observerpkg.Endpoint{
		ID:     "pod-1",
		Target: ip,
		Details: &observerpkg.Pod{
			Name: "web-0", UID: "uid-1", Namespace: "shop",
			Labels:      map[string]string{"app.kubernetes.io/name": "web"},
			Annotations: annotations,
		},
	}
}

func waitFor(t *testing.T, within time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", within, what)
}

func podAttributes(sink *consumertest.MetricsSink) map[string]string {
	for _, batch := range sink.AllMetrics() {
		for i := 0; i < batch.ResourceMetrics().Len(); i++ {
			attrs := batch.ResourceMetrics().At(i).Resource().Attributes()
			if _, ok := attrs.Get("k8s.pod.uid"); ok {
				return rawStrings(attrs)
			}
		}
	}
	return nil
}

func rawStrings(attrs pcommon.Map) map[string]string {
	out := map[string]string{}
	attrs.Range(func(k string, v pcommon.Value) bool {
		out[k] = v.AsString()
		return true
	})
	return out
}

func TestTemplate_PrometheusScrapesAnAnnotatedPodAndTheScrapeCarriesThePodAttributes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "# TYPE test_gauge gauge\ntest_gauge 7\n")
	}))
	defer server.Close()
	addr, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	block := creatorBlock(t, discoveryConfig, "receiver_creator/pods")
	setScrapeInterval(t, block, "1s")
	started := startCreator(t, block)

	started.observer.notify.OnAdd([]observerpkg.Endpoint{podEndpoint(addr.Hostname(), map[string]string{
		"prometheus.io/scrape": "true", "prometheus.io/port": addr.Port(),
	})})

	waitFor(t, 20*time.Second, "a scrape with the pod attributes", func() bool { return podAttributes(started.sink) != nil })
	got := podAttributes(started.sink)
	want := map[string]string{"k8s.pod.uid": "uid-1", "k8s.pod.name": "web-0", "k8s.namespace.name": "shop"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q (attributes: %v)", key, got[key], value, got)
		}
	}
}

func TestTemplate_PodWithoutTheScrapeAnnotationIsNotScraped(t *testing.T) {
	block := creatorBlock(t, discoveryConfig, "receiver_creator/pods")
	setScrapeInterval(t, block, "1s")
	started := startCreator(t, block)

	started.observer.notify.OnAdd([]observerpkg.Endpoint{podEndpoint("127.0.0.1", nil)})

	time.Sleep(2 * time.Second)
	if got := started.sink.DataPointCount(); got != 0 {
		t.Errorf("got %d data points from a pod with no annotation", got)
	}
	if got := started.logs.FilterLevelExact(zapcore.ErrorLevel).Len(); got != 0 {
		t.Errorf("got %d Error logs, want none", got)
	}
}

func TestTemplate_CollectorsOwnPodIsNotScrapedEvenWhenAnnotated(t *testing.T) {
	block := creatorBlock(t, discoveryConfig, "receiver_creator/pods")
	setScrapeInterval(t, block, "1s")
	started := startCreator(t, block)
	own := podEndpoint("127.0.0.1", map[string]string{"prometheus.io/scrape": "true"})
	own.Details.(*observerpkg.Pod).Labels = map[string]string{"app.kubernetes.io/component": "opentelemetry-collector"}

	started.observer.notify.OnAdd([]observerpkg.Endpoint{own})

	time.Sleep(2 * time.Second)
	if got := started.sink.DataPointCount(); got != 0 {
		t.Errorf("got %d data points from the collector's own pod", got)
	}
}

func TestTemplate_InvalidReceiverConfigLogsOneErrorAndTheCollectorKeepsRunning(t *testing.T) {
	block := creatorBlock(t, discoveryConfig, "receiver_creator/pods")
	prom := child(t, child(t, block, "receivers"), "prometheus")
	child(t, prom, "config")["config"] = "not a scrape config"
	started := startCreator(t, block)

	started.observer.notify.OnAdd([]observerpkg.Endpoint{podEndpoint("127.0.0.1", map[string]string{"prometheus.io/scrape": "true"})})

	waitFor(t, 10*time.Second, "an Error log", func() bool { return started.logs.FilterLevelExact(zapcore.ErrorLevel).Len() > 0 })
	time.Sleep(500 * time.Millisecond)
	if got := started.logs.FilterLevelExact(zapcore.ErrorLevel).Len(); got != 1 {
		t.Errorf("got %d Error logs, want exactly one: %v", got, started.logs.FilterLevelExact(zapcore.ErrorLevel).All())
	}
	if err := started.receiver.Shutdown(context.Background()); err != nil {
		t.Errorf("shutdown after the bad template: %v", err)
	}
}

func TestTemplate_ReceiverTypeThatIsNotCompiledInLogsOneError(t *testing.T) {
	block := creatorBlock(t, discoveryConfig, "receiver_creator/pods")
	block["receivers"] = map[string]any{
		"memcached": map[string]any{
			"rule":   `type == "pod"`,
			"config": map[string]any{"endpoint": "`endpoint`"},
		},
	}
	started := startCreator(t, block)

	started.observer.notify.OnAdd([]observerpkg.Endpoint{podEndpoint("127.0.0.1", nil)})

	waitFor(t, 10*time.Second, "an Error log", func() bool { return started.logs.FilterLevelExact(zapcore.ErrorLevel).Len() > 0 })
	entries := started.logs.FilterLevelExact(zapcore.ErrorLevel).All()
	if !strings.Contains(fmt.Sprint(entries[0].ContextMap()), "unable to lookup factory") {
		t.Errorf("Error = %v, want one that says the factory is missing", entries[0])
	}
}

var portInRule = regexp.MustCompile(`port == (\d+)`)

// expandEndpoint replaces the endpoint expression in every string of a template with a fixed address.
func expandEndpoint(value any, address string) any {
	switch v := value.(type) {
	case string:
		return strings.ReplaceAll(v, "`endpoint`", address)
	case map[string]any:
		out := map[string]any{}
		for key, inner := range v {
			out[key] = expandEndpoint(inner, address)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, inner := range v {
			out[i] = expandEndpoint(inner, address)
		}
		return out
	default:
		return value
	}
}

func TestAppsTemplates_EachConfigLoadsIntoItsReceiverAndValidates(t *testing.T) {
	rules := child(t, creatorBlock(t, appsConfig, "receiver_creator/apps"), "receivers")
	factories := receiverFactories()

	for name, raw := range rules {
		t.Run(name, func(t *testing.T) {
			rule := raw.(map[string]any)
			match := portInRule.FindStringSubmatch(rule["rule"].(string))
			if match == nil {
				t.Fatalf("rule %q names no port", rule["rule"])
			}
			factory, ok := factories[component.MustNewType(name)]
			if !ok {
				t.Fatalf("receiver %s is not among the factories", name)
			}
			cfg := factory.CreateDefaultConfig()
			template := expandEndpoint(rule["config"], "10.1.2.3:"+match[1]).(map[string]any)

			if err := confmap.NewFromStringMap(template).Unmarshal(cfg); err != nil {
				t.Fatalf("the template does not load into the %s config: %v", name, err)
			}
			if err := confmap.Validate(cfg); err != nil {
				t.Errorf("the %s config does not validate: %v", name, err)
			}
		})
	}
}

// A refused target must not stop the collector: each receiver starts, then reports a scrape error every interval.
func TestAppsTemplates_ARefusingTargetStartsTheReceiverAndShowsScrapeErrors(t *testing.T) {
	rules := child(t, creatorBlock(t, appsConfig, "receiver_creator/apps"), "receivers")
	if len(rules) != 6 {
		t.Fatalf("got %d application rules, want the six receivers", len(rules))
	}

	for name, raw := range rules {
		t.Run(name, func(t *testing.T) {
			rule := raw.(map[string]any)
			match := portInRule.FindStringSubmatch(rule["rule"].(string))
			if match == nil {
				t.Fatalf("rule %q names no port", rule["rule"])
			}
			port, err := strconv.ParseUint(match[1], 10, 16)
			if err != nil {
				t.Fatal(err)
			}
			rule["config"].(map[string]any)["collection_interval"] = "1s"
			if name == "mongodb" {
				// The driver waits for server selection up to this timeout (default 1m) before it reports a failure.
				rule["config"].(map[string]any)["timeout"] = "1s"
			}
			block := creatorBlock(t, appsConfig, "receiver_creator/apps")
			block["receivers"] = map[string]any{name: rule}
			started := startCreator(t, block)

			started.observer.notify.OnAdd([]observerpkg.Endpoint{{
				ID:     "port-1",
				Target: "127.0.0.1:1",
				Details: &observerpkg.Port{
					Name: name, Port: uint16(port),
					Pod: observerpkg.Pod{
						Name: name + "-0", UID: "uid-1", Namespace: "shop",
						Labels: map[string]string{"app.kubernetes.io/name": name},
					},
				},
			}})

			waitFor(t, 90*time.Second, "a scrape error from "+name, func() bool { return len(scrapeErrors(started.logs)) > 0 })
			for _, entry := range started.logs.FilterLevelExact(zapcore.ErrorLevel).All() {
				if strings.Contains(entry.Message, "failed to start receiver") {
					t.Errorf("the %s receiver did not start lazily: %v", name, entry.ContextMap())
				}
			}
		})
	}
}

func scrapeErrors(logs *observer.ObservedLogs) []observer.LoggedEntry {
	var out []observer.LoggedEntry
	for _, entry := range logs.FilterLevelExact(zapcore.ErrorLevel).All() {
		if strings.Contains(strings.ToLower(entry.Message), "scrap") {
			out = append(out, entry)
		}
	}
	return out
}

func TestAppsTemplates_TheCreatorLoadsEveryRule(t *testing.T) {
	for _, path := range []string{discoveryConfig, appsConfig} {
		name := "receiver_creator/pods"
		if path == appsConfig {
			name = "receiver_creator/apps"
		}
		cfg := receivercreator.NewFactory().CreateDefaultConfig()

		if err := confmap.NewFromStringMap(creatorBlock(t, path, name)).Unmarshal(cfg); err != nil {
			t.Errorf("%s: the creator does not load %s: %v", path, name, err)
		}
	}
}
