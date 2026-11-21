// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

// cspell:ignore logshape gcplb healthprobe identityfill sourcetag winlog

package scrape

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const configsDir = "../configs"

type refConfig struct {
	Receivers  map[string]any            `yaml:"receivers"`
	Processors map[string]map[string]any `yaml:"processors"`
	Exporters  map[string]any            `yaml:"exporters"`
	Service    struct {
		Pipelines map[string]struct {
			Receivers  []string `yaml:"receivers"`
			Processors []string `yaml:"processors"`
			Exporters  []string `yaml:"exporters"`
		} `yaml:"pipelines"`
	} `yaml:"service"`
}

func loadConfig(t *testing.T, name string) refConfig {
	t.Helper()
	var cfg refConfig
	if err := yaml.Unmarshal(readFile(t, filepath.Join(configsDir, name)), &cfg); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return cfg
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

// shapedConfigs are the reference configs that run the logshape processor or were wired for it.
var shapedConfigs = []string{"scrape.yaml", "local-gateway.yaml", "gateway-logshape.yaml", "local-edge.yaml", "truenas-core-edge.yaml", "windows-edge.yaml"}

func TestReferenceConfigs_EveryPipelineComponentIsDefined(t *testing.T) {
	for _, name := range shapedConfigs {
		cfg := loadConfig(t, name)
		for pipe, p := range cfg.Service.Pipelines {
			for _, r := range p.Receivers {
				if _, ok := cfg.Receivers[r]; !ok {
					t.Errorf("%s: pipeline %s uses the receiver %s, which is not defined", name, pipe, r)
				}
			}
			for _, proc := range p.Processors {
				if _, ok := cfg.Processors[proc]; !ok {
					t.Errorf("%s: pipeline %s uses the processor %s, which is not defined", name, pipe, proc)
				}
			}
			for _, e := range p.Exporters {
				if _, ok := cfg.Exporters[e]; !ok {
					t.Errorf("%s: pipeline %s uses the exporter %s, which is not defined", name, pipe, e)
				}
			}
		}
	}
}

// branchNames reads the branch names of the logshape processor from its source.
func branchNames(t *testing.T) []string {
	t.Helper()
	src := string(readFile(t, "../../processor/logshapeprocessor/branch.go"))
	start := strings.Index(src, "var branchNames")
	end := strings.Index(src[start:], "}")
	if start < 0 || end < 0 {
		t.Fatal("branchNames not found in branch.go")
	}
	var names []string
	for _, m := range regexp.MustCompile(`"([a-z0-9]+)"`).FindAllStringSubmatch(src[start:start+end], -1) {
		names = append(names, m[1])
	}
	if len(names) < 30 {
		t.Fatalf("read only %d branch names", len(names))
	}
	return names
}

func TestReferenceConfigs_EveryLogshapeBranchIsKnown(t *testing.T) {
	known := branchNames(t)
	for _, name := range shapedConfigs {
		for proc, block := range loadConfig(t, name).Processors {
			if proc != "logshape" && !strings.HasPrefix(proc, "logshape/") {
				continue
			}
			branches, _ := block["branches"].([]any)
			for _, b := range branches {
				if indexOf(known, b.(string)) < 0 {
					t.Errorf("%s: %s enables the unknown branch %v", name, proc, b)
				}
			}
		}
	}
}

func TestScrapeConfig_SecondGCPLoadBalancerPathSetsTheLogSourceAndTheCloudAttributes(t *testing.T) {
	cfg := loadConfig(t, "scrape.yaml")
	pipe, ok := cfg.Service.Pipelines["logs/gcp-lb"]
	if !ok {
		t.Fatal("no pipeline logs/gcp-lb")
	}

	attrs := func(proc string) map[string]string {
		out := map[string]string{}
		list, _ := cfg.Processors[proc]["attributes"].([]any)
		if proc == "attributes/gcplb" {
			list, _ = cfg.Processors[proc]["actions"].([]any)
		}
		for _, item := range list {
			m := item.(map[string]any)
			out[m["key"].(string)], _ = m["value"].(string)
		}
		return out
	}
	if indexOf(pipe.Processors, "attributes/gcplb") < 0 || attrs("attributes/gcplb")["log.source"] != "gcp.lb" {
		t.Errorf("the path does not set log.source=gcp.lb: %v", pipe.Processors)
	}
	if indexOf(pipe.Processors, "transform/gcplb") < 0 {
		t.Fatalf("the path does not set the cloud resource attributes: %v", pipe.Processors)
	}
	statements := transformStatements(t, cfg.Processors["transform/gcplb"])
	for _, key := range []string{"cloud.provider", "cloud.account.id", "cloud.region"} {
		if !strings.Contains(statements, `attributes["`+key+`"]`) {
			t.Errorf("transform/gcplb does not set %s", key)
		}
	}
	if indexOf(pipe.Processors, "attributes/gcplb") > indexOf(pipe.Processors, "sourcetag/gcp") {
		t.Error("log.source must be set before sourcetag/gcp, which keeps a source that is set")
	}
}

// transformStatements joins the OTTL statements of a transform processor block.
func transformStatements(t *testing.T, block map[string]any) string {
	t.Helper()
	var all []string
	groups, _ := block["log_statements"].([]any)
	for _, g := range groups {
		list, _ := g.(map[string]any)["statements"].([]any)
		for _, s := range list {
			all = append(all, s.(string))
		}
	}
	if len(all) == 0 {
		t.Fatal("the transform block has no statements")
	}
	return strings.Join(all, "\n")
}

func TestLogshape_HasNoRuleThatSetsLogSourceToGCPLoadBalancer(t *testing.T) {
	files, err := filepath.Glob("../../processor/logshapeprocessor/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	write := regexp.MustCompile(`Put\w*\([^)]*"log\.source"[^)]*"gcp\.lb"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		if write.Match(readFile(t, f)) {
			t.Errorf("%s sets log.source to gcp.lb; the scrape config does", f)
		}
	}
}

func TestScrapeConfig_S3AccessHasItsAccountIDAndTheCloudRecordsAreShapedAfterTagging(t *testing.T) {
	cfg := loadConfig(t, "scrape.yaml")

	s3 := cfg.Service.Pipelines["logs/aws-s3access"]
	if indexOf(s3.Processors, "transform/s3access") < 0 || indexOf(s3.Processors, "transform/s3access") > indexOf(s3.Processors, "sourcetag/aws") {
		t.Errorf("the S3 access pipeline must add the account id first: %v", s3.Processors)
	}
	if !strings.Contains(transformStatements(t, cfg.Processors["transform/s3access"]), `attributes["cloud.account.id"]`) {
		t.Error("transform/s3access does not set cloud.account.id")
	}
	for _, pipe := range []string{"logs/aws", "logs/aws-s3access", "logs/gcp", "logs/gcp-lb"} {
		procs := cfg.Service.Pipelines[pipe].Processors
		tag, shape := -1, -1
		for i, p := range procs {
			if strings.HasPrefix(p, "sourcetag/") {
				tag = i
			}
			if strings.HasPrefix(p, "logshape/") {
				shape = i
			}
		}
		if tag < 0 || shape < tag {
			t.Errorf("%s: logshape must follow sourcetag: %v", pipe, procs)
		}
	}
	gcp := cfg.Service.Pipelines["logs/gcp"].Processors
	if indexOf(gcp, "identityfill") < indexOf(gcp, "logshape/gcp") {
		t.Errorf("identityfill must run after logshape, because it fills only empty values: %v", gcp)
	}
}

func TestGatewayConfigs_HealthProbeSamplingRunsAfterLogshape(t *testing.T) {
	for name, pipe := range map[string]string{"local-gateway.yaml": "logs/otlp", "gateway-logshape.yaml": "logs/shaped"} {
		procs := loadConfig(t, name).Service.Pipelines[pipe].Processors
		shape, probe := indexOf(procs, "logshape"), indexOf(procs, "healthprobe")
		if shape < 0 || probe < shape {
			t.Errorf("%s %s: healthprobe must follow logshape: %v", name, pipe, procs)
		}
	}
}

func TestGatewayConfigs_KeepTheOldCiscoProcessorDefinedAndSayThatTheImageTagIsShared(t *testing.T) {
	local := loadConfig(t, "local-gateway.yaml")
	if _, ok := local.Processors["ciscolog"]; !ok {
		t.Error("local-gateway.yaml must keep the ciscolog block for a rollback")
	}
	for _, name := range []string{"local-gateway.yaml", "gateway-logshape.yaml"} {
		if !strings.Contains(string(readFile(t, filepath.Join(configsDir, name))), "same image tag") {
			t.Errorf("%s does not say that the two gateways run the same image tag", name)
		}
	}
}

func TestWindowsEdge_TheEventLogPipelineAddsNoServiceNameFallback(t *testing.T) {
	cfg := loadConfig(t, "windows-edge.yaml")
	procs := cfg.Service.Pipelines["logs"].Processors
	if indexOf(procs, "winlog") < 0 {
		t.Fatalf("the event log pipeline lost winlog: %v", procs)
	}
	for _, p := range procs {
		if !strings.HasPrefix(p, "resource/") {
			continue
		}
		for _, item := range cfg.Processors[p]["attributes"].([]any) {
			if item.(map[string]any)["key"] == "service.name" {
				t.Errorf("%s in the event log pipeline inserts service.name, which undoes the winlog rule", p)
			}
		}
	}
	if _, ok := os.Stat(filepath.Join(configsDir, "windows-edge.yaml")); ok != nil {
		t.Fatal("windows-edge.yaml missing")
	}
}
