#!/usr/bin/env bash
# Tests check-edge-windows-manifest.sh on the committed manifests and on
# scratch copies with one component added or removed. No network.
# Usage: test-check-edge-windows-manifest.sh
# Exit: 0 all pass, 1 a case failed.
set -euo pipefail
unset CDPATH

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
readonly ROOT
readonly CHECK="$ROOT/scripts/check-edge-windows-manifest.sh"
readonly WINDOWS="$ROOT/builder-edge-windows.yaml"
readonly STATIC="$ROOT/builder-edge-static.yaml"
SCRATCH="$(mktemp -d)"
readonly SCRATCH
trap 'rm -rf "$SCRATCH"' EXIT

failures=0

# expect_exit_code NAME WANT_CODE CMD... : runs CMD, compares its exit code.
# The command output stays in $SCRATCH/out for expect_output.
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

# expect_output NAME PATTERN : the last command output contains PATTERN.
expect_output() {
	if grep -q -- "$2" "$SCRATCH/out"; then
		echo "PASS: $1"
	else
		echo "FAIL: $1 (no match for $2)"
		sed 's/^/    /' "$SCRATCH/out"
		failures=$((failures + 1))
	fi
}

expect_exit_code "the committed manifests pass" 0 "$CHECK"
expect_output "success output says PASS" "^PASS"

cp "$WINDOWS" "$SCRATCH/extra.yaml"
sed -i.bak '/^exporters:/a\
  - gomod: go.opentelemetry.io/collector/exporter/nopexporter v0.164.0' "$SCRATCH/extra.yaml"
expect_exit_code "an unlisted added component fails" 1 "$CHECK" "$SCRATCH/extra.yaml" "$STATIC"
expect_output "the added component is named" "nopexporter"

grep -v "windowseventlogreceiver" "$WINDOWS" >"$SCRATCH/missing.yaml"
expect_exit_code "a missing expected component fails" 1 "$CHECK" "$SCRATCH/missing.yaml" "$STATIC"
expect_output "the missing component is named" "windowseventlogreceiver"

grep -v "gpureceiver" "$WINDOWS" >"$SCRATCH/no-gpu.yaml"
expect_exit_code "a Windows manifest without gpureceiver fails" 1 "$CHECK" "$SCRATCH/no-gpu.yaml" "$STATIC"
expect_output "the missing gpureceiver is named" "expected addition is missing: .*gpureceiver"

grep -v "fswatchreceiver" "$WINDOWS" >"$SCRATCH/no-fswatch.yaml"
expect_exit_code "a Windows manifest without fswatchreceiver fails" 1 "$CHECK" "$SCRATCH/no-fswatch.yaml" "$STATIC"
expect_output "the missing fswatchreceiver is named" "expected addition is missing: .*fswatchreceiver"

grep -v "windiskhealthreceiver" "$WINDOWS" >"$SCRATCH/no-windiskhealth.yaml"
expect_exit_code "a Windows manifest without windiskhealthreceiver fails" 1 "$CHECK" "$SCRATCH/no-windiskhealth.yaml" "$STATIC"
expect_output "the missing windiskhealthreceiver is named" "expected addition is missing: .*windiskhealthreceiver"

# windiskhealthreceiver reads through the decoder and schema modules, so the Windows edge keeps both.
for name in smartdecode hwschema; do
	grep -v "$name" "$WINDOWS" >"$SCRATCH/no-win-$name.yaml"
	expect_exit_code "a Windows manifest without $name fails" 1 "$CHECK" "$SCRATCH/no-win-$name.yaml" "$STATIC"
	expect_output "the missing Windows $name is named" "unexpected removal: .*$name"
done

grep -v "sqlserverreceiver" "$WINDOWS" >"$SCRATCH/no-sqlserver.yaml"
expect_exit_code "a Windows manifest without sqlserverreceiver fails" 1 "$CHECK" "$SCRATCH/no-sqlserver.yaml" "$STATIC"
expect_output "the missing sqlserverreceiver is named" "expected addition is missing: .*sqlserverreceiver"

# sqlserver is at the contrib version of the other contrib components, in the Windows manifest only.
if grep -q "receiver/sqlserverreceiver v0.164.1" "$ROOT/builder-edge-windows.yaml"; then
	echo "PASS: builder-edge-windows.yaml lists sqlserverreceiver v0.164.1"
else
	echo "FAIL: builder-edge-windows.yaml does not list sqlserverreceiver v0.164.1"
	failures=$((failures + 1))
fi
for other in builder-config.yaml builder-edge.yaml builder-edge-static.yaml builder-gateway.yaml builder-scrape.yaml builder-edge-freebsd.yaml; do
	if grep -q "sqlserver" "$ROOT/$other"; then
		echo "FAIL: $other lists sqlserver; only the Windows edge manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no sqlserver entry"
	fi
done

# The entrypoint registers its config providers from the imports of main.go. The manifest must list the same
# ones: an explicit providers section replaces the builder default, and a provider left out loses its pinned
# version in the generated go.mod.
MAIN_GO="$ROOT/cmd/otelcol-custom/main.go"
providers_in_main() {
	sed -nE 's#^[[:space:]]*([a-z]+[[:space:]]+)?"go\.opentelemetry\.io/collector/confmap/provider/([a-z]+provider)".*#\2#p' "$MAIN_GO" | LC_ALL=C sort -u
}
providers_in_manifest() {
	awk '/^providers:/ {f = 1; next} /^[a-z]/ {f = 0} f' "$1" | sed -nE 's#.*confmap/provider/([a-z]+provider)[[:space:]].*#\1#p' | LC_ALL=C sort -u
}
providers_in_main >"$SCRATCH/main-providers"
if [ "$(wc -l <"$SCRATCH/main-providers")" -ge 5 ]; then
	echo "PASS: main.go imports $(wc -l <"$SCRATCH/main-providers" | tr -d ' ') config providers"
else
	echo "FAIL: main.go imports fewer than 5 config providers: $(tr '\n' ' ' <"$SCRATCH/main-providers")"
	failures=$((failures + 1))
fi
providers_in_manifest "$WINDOWS" >"$SCRATCH/manifest-providers"
if cmp -s "$SCRATCH/main-providers" "$SCRATCH/manifest-providers"; then
	echo "PASS: builder-edge-windows.yaml lists the providers that main.go imports"
else
	echo "FAIL: builder-edge-windows.yaml providers differ from main.go: manifest $(tr '\n' ' ' <"$SCRATCH/manifest-providers") main $(tr '\n' ' ' <"$SCRATCH/main-providers")"
	failures=$((failures + 1))
fi
while IFS= read -r provider; do
	grep -v "confmap/provider/$provider" "$WINDOWS" >"$SCRATCH/no-$provider.yaml"
	expect_exit_code "a Windows manifest without $provider fails" 1 "$CHECK" "$SCRATCH/no-$provider.yaml" "$STATIC"
	expect_output "the missing $provider is named" "$provider"
done <"$SCRATCH/main-providers"

grep -v "tightvncprocessor" "$WINDOWS" >"$SCRATCH/no-tightvnc.yaml"
expect_exit_code "a Windows manifest without tightvncprocessor fails" 1 "$CHECK" "$SCRATCH/no-tightvnc.yaml" "$STATIC"
expect_output "the missing tightvncprocessor is named" "expected addition is missing: .*tightvncprocessor"

for linux in builder-config.yaml builder-edge.yaml builder-edge-static.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "fswatch" "$ROOT/$linux"; then
		echo "FAIL: $linux lists fswatch; only the Windows edge manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $linux has no fswatch entry"
	fi
	if grep -q "windiskhealth" "$ROOT/$linux"; then
		echo "FAIL: $linux lists windiskhealth; only the Windows edge manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $linux has no windiskhealth entry"
	fi
	if grep -q "tightvnc" "$ROOT/$linux"; then
		echo "FAIL: $linux lists tightvnc; only the Windows edge manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $linux has no tightvnc entry"
	fi
done

grep -v "journaldreceiver" "$STATIC" >"$SCRATCH/static-no-journald.yaml"
expect_exit_code "journaldreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-journald.yaml"
expect_output "the missing removal is named" "expected removal is missing: .*journaldreceiver"

grep -v "containernameprocessor" "$STATIC" >"$SCRATCH/static-no-containername.yaml"
expect_exit_code "containernameprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-containername.yaml"
expect_output "the missing containername removal is named" "expected removal is missing: .*containernameprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-containername.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/vladistan/otelcol-custom/processor/containernameprocessor v0.0.0' "$SCRATCH/windows-with-containername.yaml"
expect_exit_code "containernameprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-containername.yaml" "$STATIC"
expect_output "the Windows containername is named" "containernameprocessor"

grep -v "kthreadprocessor" "$STATIC" >"$SCRATCH/static-no-kthread.yaml"
expect_exit_code "kthreadprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-kthread.yaml"
expect_output "the missing kthread removal is named" "expected removal is missing: .*kthreadprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-kthread.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/vladistan/otelcol-custom/processor/kthreadprocessor v0.0.0' "$SCRATCH/windows-with-kthread.yaml"
expect_exit_code "kthreadprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-kthread.yaml" "$STATIC"
expect_output "the Windows kthread is named" "kthreadprocessor"

grep -v "processlifetimeprocessor" "$STATIC" >"$SCRATCH/static-no-processlifetime.yaml"
expect_exit_code "processlifetimeprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-processlifetime.yaml"
expect_output "the missing processlifetime removal is named" "expected removal is missing: .*processlifetimeprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-processlifetime.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/vladistan/otelcol-custom/processor/processlifetimeprocessor v0.0.0' "$SCRATCH/windows-with-processlifetime.yaml"
expect_exit_code "processlifetimeprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-processlifetime.yaml" "$STATIC"
expect_output "the Windows processlifetime is named" "processlifetimeprocessor"
grep -v "dockerstatsreceiver" "$STATIC" >"$SCRATCH/static-no-docker.yaml"
expect_exit_code "dockerstatsreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-docker.yaml"
expect_output "the missing docker_stats removal is named" "expected removal is missing: .*dockerstatsreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-docker.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/dockerstatsreceiver v0.164.1' "$SCRATCH/windows-with-docker.yaml"
expect_exit_code "dockerstatsreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-docker.yaml" "$STATIC"
expect_output "the Windows docker_stats is named" "dockerstatsreceiver"

for linux in builder-edge.yaml builder-edge-static.yaml; do
	if grep -q "dockerstatsreceiver" "$ROOT/$linux"; then
		echo "PASS: $linux lists dockerstatsreceiver"
	else
		echo "FAIL: $linux does not list dockerstatsreceiver"
		failures=$((failures + 1))
	fi
done
for other in builder-config.yaml builder-edge-windows.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "dockerstatsreceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists dockerstatsreceiver; only the Linux edge manifests may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no dockerstatsreceiver entry"
	fi
done

grep -v "k8sattributesprocessor" "$STATIC" >"$SCRATCH/static-no-k8sattributes.yaml"
expect_exit_code "k8sattributesprocessor no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-k8sattributes.yaml"
expect_output "the missing k8sattributes removal is named" "expected removal is missing: .*k8sattributesprocessor"

cp "$WINDOWS" "$SCRATCH/windows-with-k8sattributes.yaml"
sed -i.bak '/^processors:/a\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8sattributesprocessor v0.164.1' "$SCRATCH/windows-with-k8sattributes.yaml"
expect_exit_code "k8sattributesprocessor on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-k8sattributes.yaml" "$STATIC"
expect_output "the Windows k8sattributes is named" "k8sattributesprocessor"

for linux in builder-edge.yaml builder-edge-static.yaml; do
	if grep -q "k8sattributesprocessor" "$ROOT/$linux"; then
		echo "PASS: $linux lists k8sattributesprocessor"
	else
		echo "FAIL: $linux does not list k8sattributesprocessor"
		failures=$((failures + 1))
	fi
done
# The gateway and the default manifest carry k8sattributes already; the Windows edge and the scrape build must not.
for other in builder-edge-windows.yaml builder-scrape.yaml; do
	if grep -q "k8sattributesprocessor" "$ROOT/$other"; then
		echo "FAIL: $other lists k8sattributesprocessor; the edge builds on Linux are the only edge builds that may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no k8sattributesprocessor entry"
	fi
done
grep -v "ethtoolreceiver" "$STATIC" >"$SCRATCH/static-no-ethtool.yaml"
expect_exit_code "ethtoolreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-ethtool.yaml"
expect_output "the missing ethtool removal is named" "expected removal is missing: .*ethtoolreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-ethtool.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/vladistan/otelcol-custom/receiver/ethtoolreceiver v0.0.0' "$SCRATCH/windows-with-ethtool.yaml"
expect_exit_code "ethtoolreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-ethtool.yaml" "$STATIC"
expect_output "the Windows ethtool is named" "ethtoolreceiver"

# The smart receiver is Linux only; the two modules it imports are shared with windiskhealthreceiver.
for name in smartreceiver; do
	grep -v "$name" "$STATIC" >"$SCRATCH/static-no-$name.yaml"
	expect_exit_code "$name no longer removed from static fails" 1 \
		"$CHECK" "$WINDOWS" "$SCRATCH/static-no-$name.yaml"
	expect_output "the missing $name removal is named" "expected removal is missing: .*$name"
done

cp "$WINDOWS" "$SCRATCH/windows-with-smart.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/vladistan/otelcol-custom/receiver/smartreceiver v0.0.0' "$SCRATCH/windows-with-smart.yaml"
expect_exit_code "smartreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-smart.yaml" "$STATIC"
expect_output "the Windows smart receiver is named" "smartreceiver"

grep -v "pvereceiver" "$STATIC" >"$SCRATCH/static-no-pve.yaml"
expect_exit_code "pvereceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-pve.yaml"
expect_output "the missing pve removal is named" "expected removal is missing: .*pvereceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-pve.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/vladistan/otelcol-custom/receiver/pvereceiver v0.0.0' "$SCRATCH/windows-with-pve.yaml"
expect_exit_code "pvereceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-pve.yaml" "$STATIC"
expect_output "the Windows pve is named" "pvereceiver"

grep -v "snmpreceiver" "$STATIC" >"$SCRATCH/static-no-snmp.yaml"
expect_exit_code "snmpreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-snmp.yaml"
expect_output "the missing snmp removal is named" "expected removal is missing: .*snmpreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-snmp.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/snmpreceiver v0.164.1' "$SCRATCH/windows-with-snmp.yaml"
expect_exit_code "snmpreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-snmp.yaml" "$STATIC"
expect_output "the Windows snmp is named" "snmpreceiver"

grep -v "netflowreceiver" "$STATIC" >"$SCRATCH/static-no-netflow.yaml"
expect_exit_code "netflowreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-netflow.yaml"
expect_output "the missing netflow removal is named" "expected removal is missing: .*netflowreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-netflow.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/receiver/netflowreceiver v0.164.1' "$SCRATCH/windows-with-netflow.yaml"
expect_exit_code "netflowreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-netflow.yaml" "$STATIC"
expect_output "the Windows netflow is named" "netflowreceiver"

# The patched netflow receiver is a local copy: its replace line leaves with the receiver.
grep -v "netflowreceiver =>" "$STATIC" >"$SCRATCH/static-no-netflow-replace.yaml"
expect_exit_code "the netflow replace no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-netflow-replace.yaml"
expect_output "the missing netflow replace removal is named" "expected removal is missing: replace:.*netflowreceiver"

grep -v "zfskstatreceiver" "$STATIC" >"$SCRATCH/static-no-zfskstat.yaml"
expect_exit_code "zfskstatreceiver no longer removed from static fails" 1 \
	"$CHECK" "$WINDOWS" "$SCRATCH/static-no-zfskstat.yaml"
expect_output "the missing zfskstat removal is named" "expected removal is missing: .*zfskstatreceiver"

cp "$WINDOWS" "$SCRATCH/windows-with-zfskstat.yaml"
sed -i.bak '/^receivers:/a\
  - gomod: github.com/vladistan/otelcol-custom/receiver/zfskstatreceiver v0.0.0' "$SCRATCH/windows-with-zfskstat.yaml"
expect_exit_code "zfskstatreceiver on the Windows edge fails" 1 \
	"$CHECK" "$SCRATCH/windows-with-zfskstat.yaml" "$STATIC"
expect_output "the Windows zfskstat is named" "zfskstatreceiver"

grep -v "zfskstatreceiver" "$ROOT/builder-edge.yaml" >"$SCRATCH/edge-no-zfskstat.yaml"
if cmp -s "$ROOT/builder-edge.yaml" "$SCRATCH/edge-no-zfskstat.yaml"; then
	echo "FAIL: builder-edge.yaml does not list zfskstatreceiver"
	failures=$((failures + 1))
else
	echo "PASS: builder-edge.yaml lists zfskstatreceiver"
fi

for other in builder-config.yaml builder-gateway.yaml; do
	if grep -q "pvereceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists pvereceiver; only the Linux edge and scrape manifests may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no pvereceiver entry"
	fi
done
if grep -q "otelcol-custom/receiver/pvereceiver v0.0.0" "$ROOT/builder-scrape.yaml"; then
	echo "PASS: builder-scrape.yaml lists pvereceiver"
else
	echo "FAIL: builder-scrape.yaml does not list pvereceiver"
	failures=$((failures + 1))
fi

for other in builder-config.yaml builder-gateway.yaml builder-scrape.yaml; do
	if grep -q "zfskstat" "$ROOT/$other"; then
		echo "FAIL: $other lists zfskstat; only the Linux edge manifests may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no zfskstat entry"
	fi
done

if grep -q "otelcol-custom/receiver/cloudwatchmetricsreceiver" "$ROOT/builder-scrape.yaml"; then
	echo "PASS: builder-scrape.yaml lists cloudwatchmetricsreceiver"
else
	echo "FAIL: builder-scrape.yaml does not list cloudwatchmetricsreceiver"
	failures=$((failures + 1))
fi
for other in builder-config.yaml builder-edge.yaml builder-edge-static.yaml builder-edge-windows.yaml builder-gateway.yaml; do
	if grep -q "otelcol-custom/receiver/cloudwatchmetricsreceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists cloudwatchmetrics; only the scrape manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no cloudwatchmetrics entry"
	fi
done

# The pod discovery components and the six application receivers are contrib modules for the Linux edge only.
for entry in extensions:extension/observer/k8sobserver receivers:receiver/receivercreator receivers:receiver/redisreceiver \
	receivers:receiver/postgresqlreceiver receivers:receiver/mysqlreceiver receivers:receiver/mongodbreceiver \
	receivers:receiver/rabbitmqreceiver receivers:receiver/nginxreceiver; do
	section="${entry%%:*}"
	module="${entry#*:}"
	name="${module##*/}"
	grep -v -E "^[[:space:]]*-[[:space:]]*gomod:.*/${name}[[:space:]]" "$STATIC" >"$SCRATCH/static-no-$name.yaml"
	expect_exit_code "$name no longer removed from static fails" 1 \
		"$CHECK" "$WINDOWS" "$SCRATCH/static-no-$name.yaml"
	expect_output "the missing $name removal is named" "expected removal is missing: .*$name"

	cp "$WINDOWS" "$SCRATCH/windows-with-$name.yaml"
	sed -i.bak "/^$section:/a\\
  - gomod: github.com/open-telemetry/opentelemetry-collector-contrib/$module v0.164.1" "$SCRATCH/windows-with-$name.yaml"
	expect_exit_code "$name on the Windows edge fails" 1 \
		"$CHECK" "$SCRATCH/windows-with-$name.yaml" "$STATIC"
	expect_output "the Windows $name is named" "$name"
done

# check_linux_edge_only NAME: a receiver of the Linux edge builds that the Windows edge, the
# gateway, the scrape build and the default manifest must not list.
check_linux_edge_only() {
	local name="$1"
	grep -v "${name}receiver" "$STATIC" >"$SCRATCH/static-no-$name.yaml"
	expect_exit_code "${name}receiver no longer removed from static fails" 1 \
		"$CHECK" "$WINDOWS" "$SCRATCH/static-no-$name.yaml"
	expect_output "the missing $name removal is named" "expected removal is missing: .*${name}receiver"

	cp "$WINDOWS" "$SCRATCH/windows-with-$name.yaml"
	sed -i.bak "/^receivers:/a\\
  - gomod: github.com/vladistan/otelcol-custom/receiver/${name}receiver v0.0.0" "$SCRATCH/windows-with-$name.yaml"
	expect_exit_code "${name}receiver on the Windows edge fails" 1 \
		"$CHECK" "$SCRATCH/windows-with-$name.yaml" "$STATIC"
	expect_output "the Windows $name is named" "${name}receiver"

	local linux
	for linux in builder-edge.yaml builder-edge-static.yaml; do
		if grep -q "otelcol-custom/receiver/${name}receiver v0.0.0" "$ROOT/$linux"; then
			echo "PASS: $linux lists ${name}receiver"
		else
			echo "FAIL: $linux does not list ${name}receiver"
			failures=$((failures + 1))
		fi
	done

	local other
	for other in builder-config.yaml builder-gateway.yaml builder-scrape.yaml builder-edge-windows.yaml; do
		if grep -q "${name}receiver" "$ROOT/$other"; then
			echo "FAIL: $other lists ${name}receiver; only the Linux edge manifests may"
			failures=$((failures + 1))
		else
			echo "PASS: $other has no ${name}receiver entry"
		fi
	done
}

check_linux_edge_only hwmon
check_linux_edge_only iscsi
check_linux_edge_only idrac

# hwschema is the library idrac and windiskhealth build on: its replace line is in the Linux and Windows edge manifests.
for edge in builder-edge.yaml builder-edge-static.yaml builder-edge-windows.yaml; do
	if grep -q "receiver/hwschema => ../../receiver/hwschema" "$ROOT/$edge"; then
		echo "PASS: $edge replaces hwschema"
	else
		echo "FAIL: $edge does not replace hwschema"
		failures=$((failures + 1))
	fi
done

# The ipmi receiver is in the scrape manifest only, with go-ipmi pinned.
if grep -q "otelcol-custom/receiver/ipmireceiver v0.0.0" "$ROOT/builder-scrape.yaml"; then
	echo "PASS: builder-scrape.yaml lists ipmireceiver"
else
	echo "FAIL: builder-scrape.yaml does not list ipmireceiver"
	failures=$((failures + 1))
fi
if grep -q "github.com/bougou/go-ipmi => github.com/bougou/go-ipmi v0.9.2" "$ROOT/builder-scrape.yaml"; then
	echo "PASS: builder-scrape.yaml pins go-ipmi v0.9.2"
else
	echo "FAIL: builder-scrape.yaml does not pin go-ipmi v0.9.2"
	failures=$((failures + 1))
fi
for other in builder-config.yaml builder-gateway.yaml builder-edge.yaml builder-edge-static.yaml builder-edge-windows.yaml; do
	if grep -q "ipmireceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists ipmireceiver; only the scrape manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no ipmireceiver entry"
	fi
done

# The ipp receiver is in the scrape manifest only.
if grep -q "otelcol-custom/receiver/ippreceiver v0.0.0" "$ROOT/builder-scrape.yaml"; then
	echo "PASS: builder-scrape.yaml lists ippreceiver"
else
	echo "FAIL: builder-scrape.yaml does not list ippreceiver"
	failures=$((failures + 1))
fi
for other in builder-config.yaml builder-gateway.yaml builder-edge.yaml builder-edge-static.yaml builder-edge-windows.yaml; do
	if grep -q "ippreceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists ippreceiver; only the scrape manifest may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no ippreceiver entry"
	fi
done

check_linux_edge_only megaraid

# The inventory receiver is in the Linux, static Linux and Windows edge manifests and in no other.
for listed in builder-edge.yaml builder-edge-static.yaml builder-edge-windows.yaml; do
	if grep -q "otelcol-custom/receiver/inventoryreceiver v0.0.0" "$ROOT/$listed"; then
		echo "PASS: $listed lists inventoryreceiver"
	else
		echo "FAIL: $listed does not list inventoryreceiver"
		failures=$((failures + 1))
	fi
done
for other in builder-config.yaml builder-gateway.yaml builder-scrape.yaml builder-edge-freebsd.yaml; do
	if grep -q "inventoryreceiver" "$ROOT/$other"; then
		echo "FAIL: $other lists inventoryreceiver; the Linux and Windows edge manifests only may"
		failures=$((failures + 1))
	else
		echo "PASS: $other has no inventoryreceiver entry"
	fi
done

expect_exit_code "a missing manifest is a usage error" 2 "$CHECK" "$SCRATCH/absent.yaml"
expect_output "usage error names the file" "absent.yaml"

[ "$failures" -eq 0 ]
