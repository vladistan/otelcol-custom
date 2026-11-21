// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

// cspell:ignore awselb

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

// branchNames lists every branch in detector order: the first branch that claims a record wins.
// cspell:ignore mosquitto kaudit
var branchNames = []string{
	"flow", "klog", "uvicorn", "collector", "mongod", "gcpjson", "tracing", "pino", "logrus", "postgres", "redis", "nginx", "sentry", "cisco", "syslog",
	"influx", "oauth2proxy", "coredns", "gcplb", "awselb", "s3access", "gcpaudit", "eksaudit", "cloudtrail",
	"rabbitmq", "mosquitto", "stallcheck", "kaudit", "smartd", "gocd", "cron", "openvpn", "pbs", "pve", "serialconsole",
	"logfmt", "clickhouse", "calico", "ssmagent", "litellm", "stderrhint",
	"lldpd", "k8s",
}

// rec is the record a branch reads and writes. parse works on a copy, so a
// failed parse leaves the real record untouched.
type rec struct {
	lr    plog.LogRecord
	res   pcommon.Resource
	scope pcommon.InstrumentationScope
	body  string
	// memo is what claims worked out from the text and parse needs again. Only the branch that wrote it
	// reads it, by its type, and parse works without it.
	memo any
}

// memoOf returns what claims left in r.memo when it is a *T, else nil.
func memoOf[T any](r *rec) *T {
	v, _ := r.memo.(*T)
	return v
}

// branch is one parser of the table.
type branch interface {
	name() string
	// claims reads log.source, the resource and scope attributes and the body shape.
	claims(r *rec) bool
	// parse shapes the record. An error means nothing is kept.
	parse(r *rec) error
}

func newBranches(cfg *Config) ([]branch, error) {
	var out []branch
	for _, name := range branchNames {
		if !cfg.enabled(name) {
			continue
		}
		switch name {
		case "flow":
			out = append(out, newFlowBranch())
		case "klog":
			out = append(out, newKlogBranch(cfg.Klog))
		case "uvicorn":
			out = append(out, newUvicornBranch(cfg.Uvicorn))
		case "logrus":
			out = append(out, newLogrusBranch(cfg.Logrus))
		case "postgres":
			out = append(out, newPostgresBranch(cfg.Postgres))
		case "redis":
			out = append(out, newRedisBranch(cfg.Redis))
		case "nginx":
			out = append(out, newNginxBranch(cfg.Nginx))
		case "sentry":
			out = append(out, newSentryBranch(cfg.Sentry))
		case "cisco":
			b, err := newCiscoBranch(cfg.Cisco)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
		case "syslog":
			out = append(out, newSyslogBranch(cfg.Syslog))
		case "collector":
			out = append(out, newCollectorBranch())
		case "mongod":
			out = append(out, newMongodBranch())
		case "gcpjson":
			out = append(out, newGCPJSONBranch())
		case "tracing":
			out = append(out, newTracingBranch())
		case "pino":
			out = append(out, newPinoBranch())
		case "influx":
			out = append(out, newInfluxBranch())
		case "oauth2proxy":
			out = append(out, newOAuth2ProxyBranch())
		case "coredns":
			out = append(out, newCoreDNSBranch())
		case "gcplb":
			out = append(out, newGCPLBBranch(cfg.GCPLBErrorValues, cfg.AccountAliases))
		case "awselb":
			out = append(out, newAWSELBBranch(cfg.AccountAliases))
		case "s3access":
			out = append(out, newS3AccessBranch(cfg.AccountAliases))
		case "gcpaudit":
			out = append(out, newGCPAuditBranch(cfg.AccountAliases))
		case "eksaudit":
			out = append(out, newEKSAuditBranch(cfg.AccountAliases))
		case "cloudtrail":
			out = append(out, newCloudTrailBranch(cfg.AccountAliases))
		case "rabbitmq":
			out = append(out, newRabbitMQBranch())
		case "mosquitto":
			out = append(out, newMosquittoBranch())
		case "stallcheck":
			out = append(out, newStallCheckBranch())
		case "kaudit":
			out = append(out, newKernelAuditBranch())
		case "smartd":
			out = append(out, newSmartdBranch())
		case "gocd":
			out = append(out, newGoCDBranch())
		case "cron":
			out = append(out, newCronBranch())
		case "openvpn":
			out = append(out, newOpenVPNBranch())
		case "pbs":
			out = append(out, newPBSBranch())
		case "pve":
			out = append(out, newPVEBranch())
		case "serialconsole":
			out = append(out, newSerialConsoleBranch())
		case "logfmt":
			out = append(out, newLogfmtBranch())
		case "clickhouse":
			out = append(out, newClickHouseBranch())
		case "calico":
			out = append(out, newCalicoBranch())
		case "ssmagent":
			out = append(out, newSSMAgentBranch())
		case "litellm":
			out = append(out, newLitellmBranch())
		case "lldpd":
			out = append(out, newLldpdBranch())
		case "stderrhint":
			out = append(out, newStderrHintBranch())
		case "k8s":
			out = append(out, newK8sBranch(cfg.K8s))
		}
	}
	for i, b := range out {
		out[i] = withServiceOverrides(b, cfg.ServiceNameOverrides)
	}
	return out, nil
}

func bodyText(lr plog.LogRecord) string {
	switch lr.Body().Type() {
	case pcommon.ValueTypeStr:
		return lr.Body().Str()
	case pcommon.ValueTypeMap:
		if v, ok := lr.Body().Map().Get("text"); ok && v.Type() == pcommon.ValueTypeStr {
			return v.Str()
		}
	}
	return ""
}

func sourceOf(lr plog.LogRecord, res pcommon.Resource) string {
	if v, ok := lr.Attributes().Get("log.source"); ok && v.Str() != "" {
		return v.Str()
	}
	if v, ok := res.Attributes().Get("log.source"); ok && v.Str() != "" {
		return v.Str()
	}
	return "none"
}
