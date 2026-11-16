#!/usr/bin/env bash
# Tests check-k8s-discovery-config.sh on the committed sample configs and on scratch configs.
# Usage: test-check-k8s-discovery-config.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-k8s-discovery-config.sh"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

# expect_exit_code NAME WANT_CODE CMD... : runs CMD, compares its exit code; the output stays in $SCRATCH/out.
expect_exit_code() {
	local name="$1" want="$2" got=0
	shift 2
	"$@" >"$SCRATCH/out" 2>&1 || got=$?
	if [ "$got" -eq "$want" ]; then
		echo "PASS: $name"
	else
		echo "FAIL: $name (want exit code $want, got $got)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

expect_output() {
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

write() {
	cat >"$SCRATCH/$1"
}

expect_exit_code "the committed sample configs and manifests pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"
expect_output "success output counts a creator config" "[1-9][0-9]* receiver_creator"

write good-inline.yaml <<'EOF'
extensions:
  k8s_observer:
    node: ${env:K8S_NODE_NAME}
    observe_pods: true
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    receivers: {}
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "an inline watch_observers and extensions list pass" 0 "$CHECK" "$SCRATCH/good-inline.yaml"

write good-block.yaml <<'EOF'
extensions:
  health_check: {}
  k8s_observer:
    observe_pods: true
receivers:
  receiver_creator:
    watch_observers:
      - k8s_observer
    receivers: {}
service:
  extensions:
    - health_check
    - k8s_observer
EOF
expect_exit_code "block lists pass" 0 "$CHECK" "$SCRATCH/good-block.yaml"

write no-watch.yaml <<'EOF'
receivers:
  receiver_creator/pods:
    receivers: {}
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "a creator without watch_observers fails" 1 "$CHECK" "$SCRATCH/no-watch.yaml"
expect_output "the missing watch_observers is named" "receiver_creator/pods has no watch_observers"

write not-in-extensions.yaml <<'EOF'
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    receivers: {}
service:
  extensions: [health_check]
EOF
expect_exit_code "an observer missing from service.extensions fails" 1 "$CHECK" "$SCRATCH/not-in-extensions.yaml"
expect_output "the missing observer is named" "k8s_observer is not in service.extensions"

write no-extensions-key.yaml <<'EOF'
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    receivers: {}
service:
  pipelines: {}
EOF
expect_exit_code "a config with no service.extensions fails" 1 "$CHECK" "$SCRATCH/no-extensions-key.yaml"

write discovery-on.yaml <<'EOF'
extensions:
  k8s_observer:
    observe_pods: true
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    discovery:
      enabled: true
    receivers: {}
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "discovery.enabled true fails" 1 "$CHECK" "$SCRATCH/discovery-on.yaml"
expect_output "the discovery setting is named" "discovery.enabled is on"

write discovery-off.yaml <<'EOF'
extensions:
  k8s_observer:
    observe_pods: true
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    discovery:
      enabled: false
    receivers: {}
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "discovery.enabled false passes" 0 "$CHECK" "$SCRATCH/discovery-off.yaml"

write unrelated-enabled.yaml <<'EOF'
receivers:
  psi:
    cgroup:
      enabled: true
EOF
expect_exit_code "an enabled true outside discovery passes" 0 "$CHECK" "$SCRATCH/unrelated-enabled.yaml"

write two-creators.yaml <<'EOF'
receivers:
  receiver_creator/a:
    watch_observers: [k8s_observer]
  receiver_creator/b:
    receivers: {}
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "the second creator without watch_observers fails" 1 "$CHECK" "$SCRATCH/two-creators.yaml"
expect_output "only the second creator is named" "receiver_creator/b has no watch_observers"

write manifest-ok.yaml <<'EOF'
receivers:
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheusreceiver v0.164.1
EOF
expect_exit_code "a manifest without prometheus_simple passes" 0 "$CHECK" --manifest "$SCRATCH/manifest-ok.yaml" "$SCRATCH/unrelated-enabled.yaml"

write manifest-simple.yaml <<'EOF'
receivers:
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/prometheussimplereceiver v0.164.1
EOF
expect_exit_code "a manifest that compiles prometheus_simple fails" 1 "$CHECK" --manifest "$SCRATCH/manifest-simple.yaml" "$SCRATCH/unrelated-enabled.yaml"
expect_output "the manifest is named" "manifest-simple.yaml"

# Spellings of discovery.enabled: true that YAML reads as true.
for spelling in 'true' '"true"' "'true'" 'True' 'TRUE'; do
	write discovery-spelling.yaml <<EOF
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    discovery:
      enabled: $spelling
service:
  extensions: [k8s_observer]
EOF
	expect_exit_code "discovery.enabled $spelling fails" 1 "$CHECK" "$SCRATCH/discovery-spelling.yaml"
done

for flow in '{enabled: true}' '{ enabled: "true", other: 1 }' "{other: 1, enabled: 'True'}"; do
	write discovery-flow.yaml <<EOF
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    discovery: $flow
service:
  extensions: [k8s_observer]
EOF
	expect_exit_code "flow style discovery $flow fails" 1 "$CHECK" "$SCRATCH/discovery-flow.yaml"
done

write discovery-flow-off.yaml <<'EOF'
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    discovery: {enabled: false}
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "flow style discovery enabled false passes" 0 "$CHECK" "$SCRATCH/discovery-flow-off.yaml"

write discovery-quoted-false.yaml <<'EOF'
receivers:
  receiver_creator/pods:
    watch_observers: [k8s_observer]
    discovery:
      enabled: "false"
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "discovery.enabled quoted false passes" 0 "$CHECK" "$SCRATCH/discovery-quoted-false.yaml"

for quote in '"' "'"; do
	write quoted-no-watch.yaml <<EOF
receivers:
  ${quote}receiver_creator/pods${quote}:
    receivers: {}
service:
  extensions: [k8s_observer]
EOF
	expect_exit_code "a creator key quoted with $quote and no watch_observers fails" 1 "$CHECK" "$SCRATCH/quoted-no-watch.yaml"
	expect_output "the quoted creator is named without its quotes" "receiver_creator/pods has no watch_observers"
done

write quoted-ok.yaml <<'EOF'
receivers:
  "receiver_creator/pods":
    watch_observers: [k8s_observer]
service:
  extensions: [k8s_observer]
EOF
expect_exit_code "a quoted creator key with watch_observers passes" 0 "$CHECK" "$SCRATCH/quoted-ok.yaml"

# Manifests: the six application receivers belong to the edge manifests only; memcached and kafka_metrics to none.
mkdir "$SCRATCH/m"
for app in redis postgresql mysql mongodb rabbitmq nginx; do
	cat >"$SCRATCH/m/builder-edge.yaml" <<EOF
receivers:
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/${app}receiver v0.164.1
EOF
	expect_exit_code "the edge manifest may compile ${app}receiver" 0 "$CHECK" --manifest "$SCRATCH/m/builder-edge.yaml" "$SCRATCH/unrelated-enabled.yaml"
	cp "$SCRATCH/m/builder-edge.yaml" "$SCRATCH/m/builder-edge-static.yaml"
	expect_exit_code "the static edge manifest may compile ${app}receiver" 0 "$CHECK" --manifest "$SCRATCH/m/builder-edge-static.yaml" "$SCRATCH/unrelated-enabled.yaml"
	for other in builder-gateway.yaml builder-scrape.yaml builder-config.yaml builder-edge-windows.yaml; do
		cp "$SCRATCH/m/builder-edge.yaml" "$SCRATCH/m/$other"
		expect_exit_code "$other with ${app}receiver fails" 1 "$CHECK" --manifest "$SCRATCH/m/$other" "$SCRATCH/unrelated-enabled.yaml"
		expect_output "the failure names $other" "$other"
	done
done

write m/builder-gateway.yaml <<'EOF'
receivers:
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/mongodbatlasreceiver v0.164.1
EOF
expect_exit_code "mongodbatlasreceiver is not mongodbreceiver" 0 "$CHECK" --manifest "$SCRATCH/m/builder-gateway.yaml" "$SCRATCH/unrelated-enabled.yaml"

for banned in memcachedreceiver kafkametricsreceiver; do
	for manifest in builder-edge.yaml builder-edge-static.yaml builder-gateway.yaml; do
		cat >"$SCRATCH/m/$manifest" <<EOF
receivers:
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/${banned} v0.164.1
EOF
		expect_exit_code "$manifest with $banned fails" 1 "$CHECK" --manifest "$SCRATCH/m/$manifest" "$SCRATCH/unrelated-enabled.yaml"
		expect_output "the failure names $banned" "$banned"
	done
done

expect_exit_code "a missing config is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"
expect_output "usage error names the file" "absent.yaml"
expect_exit_code "an unknown option is a usage error" 2 "$CHECK" --nope

[ "$failures" -eq 0 ]
