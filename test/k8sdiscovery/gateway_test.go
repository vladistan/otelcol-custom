// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package k8sdiscovery

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	observerpkg "github.com/open-telemetry/opentelemetry-collector-contrib/extension/observer"
	"go.opentelemetry.io/collector/consumer/consumertest"
)

const fakeRedisInfo = "# Server\r\nredis_version:7.4.2\r\nuptime_in_seconds:3600\r\n" +
	"# Clients\r\nconnected_clients:3\r\n" +
	"# Memory\r\nused_memory:1048576\r\n" +
	"# Stats\r\ntotal_commands_processed:1000\r\n" +
	"# Keyspace\r\ndb0:keys=5,expires=0,avg_ttl=0\r\n"

// fakeRedis answers the commands a scrape sends: INFO with a fixed text, an error for HELLO (the client then
// speaks the older protocol) and OK for the rest. It counts the connections it accepts.
type fakeRedis struct {
	addr     string
	accepted atomic.Int64
}

func startFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeRedis{addr: listener.Addr().String()}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.accepted.Add(1)
			go serveFakeRedis(conn)
		}
	}()
	return server
}

func serveFakeRedis(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		command, err := readCommand(reader)
		if err != nil {
			return
		}
		var reply string
		switch strings.ToUpper(command[0]) {
		case "INFO":
			reply = fmt.Sprintf("$%d\r\n%s\r\n", len(fakeRedisInfo), fakeRedisInfo)
		case "HELLO":
			reply = "-ERR unknown command 'HELLO', with args beginning with: '3'\r\n"
		default:
			reply = "+OK\r\n"
		}
		if _, err := io.WriteString(conn, reply); err != nil {
			return
		}
	}
}

// readCommand reads one command in the array form every client sends: *N, then N bulk strings.
func readCommand(reader *bufio.Reader) ([]string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "*")))
	if err != nil || count < 1 {
		return nil, fmt.Errorf("not a command array: %q", header)
	}
	command := make([]string, 0, count)
	for range count {
		sizeLine, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(sizeLine, "$")))
		if err != nil || size < 0 {
			return nil, fmt.Errorf("not a bulk string: %q", sizeLine)
		}
		data := make([]byte, size+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		command = append(command, string(data[:size]))
	}
	return command, nil
}

// The endpoint says port 6379, as the rule tests, but its target is the fake server's address.
func redisPortEndpoint(address string, labels map[string]string) observerpkg.Endpoint {
	return observerpkg.Endpoint{
		ID:     "port-1",
		Target: address,
		Details: &observerpkg.Port{
			Name: "redis", Port: 6379,
			Pod: observerpkg.Pod{Name: "redis-0", UID: "uid-1", Namespace: "shop", Labels: labels},
		},
	}
}

func redisMetricNames(sink *consumertest.MetricsSink) []string {
	var names []string
	for _, batch := range sink.AllMetrics() {
		for i := range batch.ResourceMetrics().Len() {
			scopes := batch.ResourceMetrics().At(i).ScopeMetrics()
			for j := range scopes.Len() {
				for k := range scopes.At(j).Metrics().Len() {
					if name := scopes.At(j).Metrics().At(k).Name(); strings.HasPrefix(name, "redis.") {
						names = append(names, name)
					}
				}
			}
		}
	}
	return names
}

func fastRedisBlock(t *testing.T) map[string]any {
	t.Helper()
	block := creatorBlock(t, gatewayDiscoveryConfig, "receiver_creator/redis")
	child(t, child(t, child(t, block, "receivers"), "redis"), "config")["collection_interval"] = "1s"
	return block
}

func TestGatewayTemplate_RedisRuleScrapesAFakeServerThroughTheObserverAndTheScrapeCarriesThePodAttributes(t *testing.T) {
	server := startFakeRedis(t)
	started := startCreator(t, fastRedisBlock(t))

	started.observer.notify.OnAdd([]observerpkg.Endpoint{redisPortEndpoint(server.addr, map[string]string{"app.kubernetes.io/name": "redis"})})

	waitFor(t, 30*time.Second, "a redis scrape with the pod attributes", func() bool {
		return podAttributes(started.sink) != nil && len(redisMetricNames(started.sink)) > 0
	})
	got := podAttributes(started.sink)
	want := map[string]string{"k8s.pod.uid": "uid-1", "k8s.pod.name": "redis-0", "k8s.namespace.name": "shop"}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q (attributes: %v)", key, got[key], value, got)
		}
	}
}

func TestGatewayTemplate_CollectorsOwnPortIsNotScraped(t *testing.T) {
	server := startFakeRedis(t)
	started := startCreator(t, fastRedisBlock(t))
	own := map[string]string{"app.kubernetes.io/component": "opentelemetry-collector"}

	started.observer.notify.OnAdd([]observerpkg.Endpoint{redisPortEndpoint(server.addr, own)})

	time.Sleep(2500 * time.Millisecond)
	if got := started.sink.DataPointCount(); got != 0 {
		t.Errorf("got %d data points from the collector's own pod", got)
	}
	if got := server.accepted.Load(); got != 0 {
		t.Errorf("the server accepted %d connections from the collector's own pod", got)
	}
}
