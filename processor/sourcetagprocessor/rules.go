// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package sourcetagprocessor

import (
	"fmt"
	"regexp"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	sourceAttr   = "log.source"
	categoryAttr = "event.category"

	formatAttr       = "encoding.format"
	rpcMethodAttr    = "rpc.method"
	errorCodeAttr    = "aws.error.code"
	regionAttr       = "aws.region"
	cloudProvider    = "cloud.provider"
	cloudRegion      = "cloud.region"
	cloudAccountID   = "cloud.account.id"
	cloudService     = "cloud.service.name"
	serviceName      = "service.name"
	logGroupAttr     = "cloudwatch.log.group.name"
	logStreamAttr    = "cloudwatch.log.stream"
	gcpResourceType  = "gcp.resource_type"
	gcpResourceID    = "cloud.resource_id"
	gcpProject       = "gcp.project"
	gcpLabelRegion   = "gcp.label.region"
	gcpLabelZone     = "gcp.label.zone"
	gcpLabelService  = "gcp.label.service_name"
	gcpLabelLocation = "gcp.label.location"
	gcpLabelProject  = "gcp.label.project_id"

	formatCloudTrail = "aws.cloudtrail"
	formatS3Access   = "aws.s3access"
	formatELBAccess  = "aws.elbaccess"

	sourceCloudTrail = "aws.cloudtrail"
	sourceS3Access   = "aws.s3access"
	sourceELB        = "aws.elb"
	sourceEKS        = "aws.eks"
	sourceCloudWatch = "aws.cloudwatch"
	sourceGCPAudit   = "gcp.audit"
	sourceGCPRun     = "gcp.cloudrun"
	sourceGCPLB      = "gcp.lb"
	sourceAzure      = "azure.activity"
	sourceAuth0      = "auth0"

	categoryAudit       = "audit"
	categoryAccess      = "access"
	categoryApplication = "application"
)

var (
	eksGroup       = regexp.MustCompile(`^/aws/eks/`)
	eksAuditStream = regexp.MustCompile(`^kube-apiserver-audit`)
	gcpAuditID     = regexp.MustCompile(`^cloudaudit`)
	gcpLBType      = regexp.MustCompile(`^(http_load_balancer|l7_lb_rule|https_lb_rule)$`)
)

// The resource function runs once per resource, before any record function reads its attributes.
type rules struct {
	resource func(res pcommon.Map)
	record   func(res, scope pcommon.Map, lr plog.LogRecord)
	// services maps a log.source to the cloud.service.name of its resource.
	services map[string]string
}

var (
	awsServices = map[string]string{
		sourceCloudTrail: "cloudtrail",
		sourceS3Access:   "s3",
		sourceELB:        "elasticloadbalancing",
		sourceEKS:        "eks",
		sourceCloudWatch: "logs",
		sourceAuth0:      "auth0",
	}
	gcpServices = map[string]string{
		sourceGCPAudit: "cloudaudit.googleapis.com",
		sourceGCPRun:   "run.googleapis.com",
		sourceGCPLB:    "loadbalancing.googleapis.com",
	}
	azureServices = map[string]string{sourceAzure: "monitor"}
)

func rulesFor(provider string) (rules, error) {
	switch provider {
	case providerAWS:
		return rules{resource: awsResource, record: awsRecord, services: awsServices}, nil
	case providerGCP:
		return rules{resource: gcpResource, record: gcpRecord, services: gcpServices}, nil
	case providerAzure:
		return rules{resource: azureResource, record: azureRecord, services: azureServices}, nil
	}
	return rules{}, fmt.Errorf("no rules for provider %q", provider)
}

// hasValue reports whether key holds a value; an empty value counts as absent, as nil does in OTTL.
func hasValue(m pcommon.Map, key string) bool {
	v, ok := m.Get(key)
	return ok && v.Type() != pcommon.ValueTypeEmpty
}

func attrIs(m pcommon.Map, key, want string) bool {
	v, ok := m.Get(key)
	return ok && v.Type() == pcommon.ValueTypeStr && v.Str() == want
}

func matches(m pcommon.Map, key string, re *regexp.Regexp) bool {
	v, ok := m.Get(key)
	return ok && v.Type() == pcommon.ValueTypeStr && re.MatchString(v.Str())
}

// copyIfPresent skips the copy when src is missing or empty, so dst keeps its value.
func copyIfPresent(src pcommon.Map, from string, dst pcommon.Map, to string) {
	if v, ok := src.Get(from); ok && v.Type() != pcommon.ValueTypeEmpty {
		v.CopyTo(dst.PutEmpty(to))
	}
}

func setSourceIfNil(attrs pcommon.Map, source string, cond bool) {
	if cond && !hasValue(attrs, sourceAttr) {
		attrs.PutStr(sourceAttr, source)
	}
}

func setCategoryFor(attrs pcommon.Map, source, category string) {
	if attrIs(attrs, sourceAttr, source) {
		attrs.PutStr(categoryAttr, category)
	}
}

// awsRecord keeps the statement order of the rules it replaces: later ones read what earlier ones set.
func awsRecord(res, scope pcommon.Map, lr plog.LogRecord) {
	attrs := lr.Attributes()
	if attrIs(scope, formatAttr, formatCloudTrail) {
		attrs.PutStr(sourceAttr, sourceCloudTrail)
	}
	if attrIs(scope, formatAttr, formatS3Access) {
		attrs.PutStr(sourceAttr, sourceS3Access)
	}
	setCategoryFor(attrs, sourceCloudTrail, categoryAudit)
	setCategoryFor(attrs, sourceS3Access, categoryAccess)
	if attrIs(scope, formatAttr, formatELBAccess) {
		attrs.PutStr(sourceAttr, sourceELB)
	}
	setCategoryFor(attrs, sourceELB, categoryAccess)

	setSourceIfNil(attrs, sourceEKS, matches(res, logGroupAttr, eksGroup))
	if attrIs(attrs, sourceAttr, sourceEKS) && matches(res, logStreamAttr, eksAuditStream) {
		attrs.PutStr(categoryAttr, categoryAudit)
	}
	setSourceIfNil(attrs, sourceCloudWatch, hasValue(res, logGroupAttr))
	setCategoryFor(attrs, sourceCloudWatch, categoryApplication)
	if attrIs(attrs, sourceAttr, sourceEKS) && !hasValue(attrs, categoryAttr) {
		attrs.PutStr(categoryAttr, categoryApplication)
	}

	if attrIs(attrs, sourceAttr, sourceCloudTrail) {
		cloudTrailBody(attrs, lr)
	}
	awsSeverity(attrs, lr)
}

// cloudTrailBody takes the body from rpc.method, which holds "eventSource/eventName", then swaps every "/" for a space.
// A missing rpc.method empties the body: the OTTL set it replaces assigned nil.
func cloudTrailBody(attrs pcommon.Map, lr plog.LogRecord) {
	if v, ok := attrs.Get(rpcMethodAttr); ok {
		v.CopyTo(lr.Body())
	} else {
		pcommon.NewValueEmpty().CopyTo(lr.Body())
	}
	if lr.Body().Type() == pcommon.ValueTypeStr {
		lr.Body().SetStr(strings.ReplaceAll(lr.Body().Str(), "/", " "))
	}
}

// awsSeverity raises a CloudTrail record with an error code to WARN and writes the text of INFO and
// WARN. A record with no severity stays unset.
func awsSeverity(attrs pcommon.Map, lr plog.LogRecord) {
	if attrIs(attrs, sourceAttr, sourceCloudTrail) && hasValue(attrs, errorCodeAttr) {
		lr.SetSeverityNumber(plog.SeverityNumberWarn)
	}
	switch lr.SeverityNumber() {
	case plog.SeverityNumberInfo:
		lr.SetSeverityText("INFO")
	case plog.SeverityNumberWarn:
		lr.SetSeverityText("WARN")
	}
}

func awsResource(res pcommon.Map) {
	if !hasValue(res, cloudProvider) && hasValue(res, regionAttr) {
		res.PutStr(cloudProvider, providerAWS)
	}
	if !hasValue(res, cloudRegion) {
		copyIfPresent(res, regionAttr, res, cloudRegion)
	}
}

func gcpResource(res pcommon.Map) {
	res.PutStr(cloudProvider, providerGCP)
	copyIfPresent(res, gcpLabelService, res, serviceName)
	copyIfPresent(res, gcpLabelLocation, res, cloudRegion)
	// Load balancer resources carry region or zone and no location label.
	if !hasValue(res, cloudRegion) {
		copyIfPresent(res, gcpLabelRegion, res, cloudRegion)
	}
	if v, ok := res.Get(gcpLabelZone); ok && !hasValue(res, cloudRegion) && v.Type() == pcommon.ValueTypeStr && v.Str() != "" {
		res.PutStr(cloudRegion, regionOfZone(v.Str()))
	}
	copyIfPresent(res, gcpLabelProject, res, cloudAccountID)
	if !hasValue(res, cloudAccountID) {
		copyIfPresent(res, gcpProject, res, cloudAccountID)
	}
}

// regionOfZone drops the one-letter zone suffix (us-central1-b is us-central1); global and region names stay.
func regionOfZone(zone string) string {
	i := strings.LastIndexByte(zone, '-')
	if i > 0 && len(zone)-i-1 == 1 {
		return zone[:i]
	}
	return zone
}

// gcpRecord sets gcp.audit with no nil guard: an audit resource id outranks any source set earlier.
func gcpRecord(res, _ pcommon.Map, lr plog.LogRecord) {
	attrs := lr.Attributes()
	if matches(res, gcpResourceID, gcpAuditID) {
		attrs.PutStr(sourceAttr, sourceGCPAudit)
	}
	setSourceIfNil(attrs, sourceGCPRun, attrIs(res, gcpResourceType, "cloud_run_revision"))
	setSourceIfNil(attrs, sourceGCPLB, matches(res, gcpResourceType, gcpLBType))
	setCategoryFor(attrs, sourceGCPAudit, categoryAudit)
	setCategoryFor(attrs, sourceGCPLB, categoryAccess)
	setCategoryFor(attrs, sourceGCPRun, categoryApplication)
}

func azureResource(res pcommon.Map) {
	res.PutStr(cloudProvider, providerAzure)
}

func azureRecord(_, _ pcommon.Map, lr plog.LogRecord) {
	lr.Attributes().PutStr(sourceAttr, sourceAzure)
	lr.Attributes().PutStr(categoryAttr, categoryAudit)
}
