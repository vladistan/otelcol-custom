// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

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

// charonlogAndCharon names the pipelines of a config that list a charonlog processor and a logshape
// processor with the charon branch on, which shape the same lines twice. A logshape block with no
// branches list runs every branch.
func charonlogAndCharon(cfg refConfig) []string {
	isProc := func(name, kind string) bool { return name == kind || strings.HasPrefix(name, kind+"/") }
	var out []string
	for pipe, p := range cfg.Service.Pipelines {
		hasCharonlog, hasCharon := false, false
		for _, proc := range p.Processors {
			if isProc(proc, "charonlog") {
				hasCharonlog = true
			}
			if isProc(proc, "logshape") {
				branches, _ := cfg.Processors[proc]["branches"].([]any)
				if len(branches) == 0 || indexOf(anyStrings(branches), "charon") >= 0 {
					hasCharon = true
				}
			}
		}
		if hasCharonlog && hasCharon {
			out = append(out, pipe)
		}
	}
	return out
}

func anyStrings(list []any) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func TestReferenceConfigs_NoPipelineListsCharonlogAndLogshapeWithCharon(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(configsDir, "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no reference configs: %v", err)
	}
	drafts, _ := filepath.Glob(filepath.Join(configsDir+"-drafts", "*", "*.yaml"))
	for _, f := range append(files, drafts...) {
		var cfg refConfig
		if err := yaml.Unmarshal(readFile(t, f), &cfg); err != nil {
			continue // a draft that is not a collector config
		}
		for _, pipe := range charonlogAndCharon(cfg) {
			t.Errorf("%s: pipeline %s lists charonlog and logshape with the charon branch; list one", f, pipe)
		}
	}
}

func TestReferenceConfigs_TheCharonlogCheckFindsBothInOnePipeline(t *testing.T) {
	const both = `
processors:
  charonlog: {}
  logshape:
    branches: [cisco, charon]
service:
  pipelines:
    logs: {processors: [charonlog, logshape]}
`
	const without = `
processors:
  charonlog: {}
  logshape:
    branches: [cisco]
service:
  pipelines:
    logs: {processors: [charonlog, logshape]}
`
	const allBranches = `
processors:
  charonlog: {}
  logshape/main: {}
service:
  pipelines:
    logs: {processors: [charonlog, logshape/main]}
`
	const split = `
processors:
  charonlog: {}
  logshape:
    branches: [charon]
service:
  pipelines:
    logs/a: {processors: [charonlog]}
    logs/b: {processors: [logshape]}
`
	for name, tt := range map[string]struct {
		cfg  string
		want int
	}{"both with charon on": {both, 1}, "logshape without charon": {without, 0}, "logshape with every branch": {allBranches, 1}, "in separate pipelines": {split, 0}} {
		var cfg refConfig
		if err := yaml.Unmarshal([]byte(tt.cfg), &cfg); err != nil {
			t.Fatal(err)
		}
		if got := charonlogAndCharon(cfg); len(got) != tt.want {
			t.Errorf("%s: %v, want %d pipelines", name, got, tt.want)
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
	if indexOf(pipe.Processors, "attributes/gcplb") < 0 || attrs("attributes/gcplb")["log.source"] != "lb" {
		t.Errorf("the path does not set log.source=lb: %v", pipe.Processors)
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
			t.Errorf("%s sets log.source to lb; the scrape config does", f)
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

// serialGlue is the block of the processor that glues the wrapped fragments of a serial console line.
func serialGlue(t *testing.T) map[string]any {
	t.Helper()
	block, ok := loadConfig(t, "scrape.yaml").Processors["logrecombine/serial"]
	if !ok {
		t.Fatal("scrape.yaml defines no logrecombine/serial processor")
	}
	return block
}

func patternList(t *testing.T, block map[string]any, key string) []*regexp.Regexp {
	t.Helper()
	raw, ok := block[key].([]any)
	if !ok || len(raw) == 0 {
		t.Fatalf("logrecombine/serial has no %s", key)
	}
	var out []*regexp.Regexp
	for _, v := range raw {
		re, err := regexp.Compile(v.(string))
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		out = append(out, re)
	}
	return out
}

func matchesAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func TestScrapeConfig_SerialGlueRunsBeforeLogshapeInTheGCPPipeline(t *testing.T) {
	procs := loadConfig(t, "scrape.yaml").Service.Pipelines["logs/gcp"].Processors
	glue, shape := indexOf(procs, "logrecombine/serial"), indexOf(procs, "logshape/gcp")
	if glue < 0 || shape < 0 || glue > shape {
		t.Errorf("logrecombine/serial must run before logshape/gcp: %v", procs)
	}
	if limiter := indexOf(procs, "memory_limiter"); limiter > glue {
		t.Errorf("memory_limiter must stay first: %v", procs)
	}
}

func TestScrapeConfig_SerialGlueRulesMatchTheLinesTheyAreMeantFor(t *testing.T) {
	block := serialGlue(t)
	if block["require_attribute"] != "gcp.label.is_text_escaped" {
		t.Errorf("require_attribute = %v", block["require_attribute"])
	}
	if block["fragment_suffix"] != `\r\n` {
		t.Errorf("fragment_suffix = %q, want the four characters backslash r backslash n", block["fragment_suffix"])
	}
	if block["min_previous_length"] != 515 {
		t.Errorf("min_previous_length = %v", block["min_previous_length"])
	}
	first := regexp.MustCompile(block["is_first_entry_pattern"].(string))
	empty := patternList(t, block, "empty_patterns")
	never := patternList(t, block, "never_glue_patterns")

	for line, want := range map[string]bool{
		`2026-01-01T00:00:00+0000 node-1 gcfsd-v2[3009]: time=\"x\"` + `\r\n`: true,
		`2026-01-01T00:00:00+0000 node-1 kubelet[1]: I0101 x` + `\r\n`:        true,
		`sha256:00,sha256:11` + `\r\n`:                                        false,
		`[  OK  ] Started thing` + `\r\n`:                                     false,
	} {
		if got := first.MatchString(line); got != want {
			t.Errorf("first entry %q = %v, want %v", line, got, want)
		}
	}
	if !matchesAny(empty, `\r\n`) || matchesAny(empty, `\r\nx`) || matchesAny(empty, ` \r\n`) {
		t.Error("empty_patterns must match the bare line end and nothing longer")
	}
	for _, line := range []string{
		`[    1.234567] kernel line` + `\r\n`,
		`[\x1b[0;32m  OK  \x1b[0m] Started thing` + `\r\n`,
		`Stopping thing...` + `\r\n`,
		`ssh-ed25519 AAAA root@node-1` + `\r\n`,
		`-----BEGIN SSH HOST KEY KEYS-----` + `\r\n`,
		`<14>Jan 01 00:00:00 cloud-init: x` + `\r\n`,
		`0010: 00 01 02 03` + `\r\n`,
	} {
		if !matchesAny(never, line) {
			t.Errorf("never_glue_patterns must match %q", line)
		}
	}
	for _, line := range []string{
		`sha256:00,sha256:11` + `\r\n`,
		`rd/io.containerd.snapshotter.v1.gcfs/snapshotter/snapshots/129/fs` + `\r\n`,
		`\",\"Percentage\":0}` + `\r\n`,
	} {
		if matchesAny(never, line) {
			t.Errorf("never_glue_patterns must not match the fragment %q", line)
		}
	}
}

func TestScrapeBuilder_ListsLogrecombineWithItsLocalModules(t *testing.T) {
	text := string(readFile(t, "../../builder-scrape.yaml"))
	for _, want := range []string{
		"processor/logrecombineprocessor => ../../processor/logrecombineprocessor",
		"- gomod: github.com/vladistan/otelcol-custom/processor/logrecombineprocessor v0.0.0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("builder-scrape.yaml lacks %q", want)
		}
	}
	for _, mod := range []string{"shape/multiline", "shape/severity", "shape/strip"} {
		if !strings.Contains(text, "otelcol-custom/"+mod+" => ../../"+mod) {
			t.Errorf("builder-scrape.yaml lacks the replace of %s, which logrecombine needs", mod)
		}
	}
}
