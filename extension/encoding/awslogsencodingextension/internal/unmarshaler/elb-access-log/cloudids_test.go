// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package elbaccesslogs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
)

const (
	docTargetGroupARN = "arn:aws:elasticloadbalancing:us-east-2:123456789012:targetgroup/my-targets/73e2d6bc24d8a067"
	docCertARN        = "arn:aws:acm:us-east-2:123456789012:certificate/12345678-1234-1234-1234-123456789012"
)

func resourceOf(t *testing.T, ld plog.Logs) map[string]any {
	t.Helper()
	require.Equal(t, 1, ld.ResourceLogs().Len())
	return ld.ResourceLogs().At(0).Resource().Attributes().AsRaw()
}

func TestARNRegionAccount(t *testing.T) {
	valid := []struct{ arn, region, account string }{
		{docTargetGroupARN, "us-east-2", "123456789012"},
		{docCertARN, "us-east-2", "123456789012"},
		{"arn:aws-us-gov:acm:us-gov-west-1:123456789012:certificate/x", "us-gov-west-1", "123456789012"},
	}
	for _, c := range valid {
		region, account := arnRegionAccount(c.arn)
		assert.Equal(t, c.region, region, c.arn)
		assert.Equal(t, c.account, account, c.arn)
	}

	for _, arn := range []string{"", "-", "not-an-arn", "arn:aws:acm:::certificate/x", "arn:aws:acm:us-east-2::certificate/x",
		"arn:aws:acm::123456789012:certificate/x", "arn:aws:acm:us-east-2:12345:certificate/x", "arn:aws:acm:us-east-2:abcdefghijkl:certificate/x"} {
		region, account := arnRegionAccount(arn)
		assert.Empty(t, region, arn)
		assert.Empty(t, account, arn)
	}
}

func TestALB_TheTargetGroupARNGivesRegionAndAccount(t *testing.T) {
	ld, _, err := decodeALB(t, albLine("192.0.2.10:2817", "198.51.100.7:80"))
	require.NoError(t, err)

	res := resourceOf(t, ld)
	assert.Equal(t, "us-east-2", res["cloud.region"])
	assert.Equal(t, "123456789012", res["cloud.account.id"])
	assert.Equal(t, "aws", res["cloud.provider"])
}

func TestALB_TheCertificateARNIsUsedWhenThereIsNoTargetGroup(t *testing.T) {
	line := strings.Replace(albLine("192.0.2.10:2817", "-"), docTargetGroupARN, "-", 1)
	ld, _, err := decodeALB(t, line)
	require.NoError(t, err)

	res := resourceOf(t, ld)
	assert.Equal(t, "us-east-2", res["cloud.region"])
	assert.Equal(t, "123456789012", res["cloud.account.id"])
}

func TestALB_NoARNLeavesRegionAndAccountOut(t *testing.T) {
	line := strings.Replace(strings.Replace(albLine("192.0.2.10:2817", "-"), docTargetGroupARN, "-", 1), docCertARN, "-", 1)
	ld, _, err := decodeALB(t, line)
	require.NoError(t, err)

	res := resourceOf(t, ld)
	assert.NotContains(t, res, "cloud.region")
	assert.NotContains(t, res, "cloud.account.id")
	assert.Equal(t, "aws", res["cloud.provider"])
}

func TestALB_TheFirstLineWithAnARNDecidesTheResource(t *testing.T) {
	first := strings.Replace(albLine("192.0.2.10:1", "-"), docTargetGroupARN, "-", 1)
	first = strings.Replace(first, docCertARN, "-", 1)
	other := strings.ReplaceAll(albLine("192.0.2.10:2", "-"), "us-east-2", "eu-west-1")
	later := strings.ReplaceAll(albLine("192.0.2.10:3", "-"), "us-east-2", "ap-south-1")
	ld, _, err := decodeALB(t, first, other, later)
	require.NoError(t, err)

	assert.Equal(t, "eu-west-1", resourceOf(t, ld)["cloud.region"])
	assert.Equal(t, 3, ld.LogRecordCount())
}

func TestNLB_TheCertificateARNGivesRegionAndAccount(t *testing.T) {
	line := strings.Replace(nlbLine("192.0.2.10:2817", "198.51.100.7:443"),
		"arn:aws:acm:us-east-2:123456789012:certificate/2a108f19-aded-46b0-8493-c63eb1ef4a99", docCertARN, 1)
	ld, _, err := decodeALB(t, line)
	require.NoError(t, err)

	res := resourceOf(t, ld)
	assert.Equal(t, "us-east-2", res["cloud.region"])
	assert.Equal(t, "123456789012", res["cloud.account.id"])
}

func TestNLB_ADashCertificateLeavesRegionAndAccountOut(t *testing.T) {
	line := strings.Replace(nlbLine("192.0.2.10:2817", "198.51.100.7:443"),
		"arn:aws:acm:us-east-2:123456789012:certificate/2a108f19-aded-46b0-8493-c63eb1ef4a99", "-", 1)
	ld, _, err := decodeALB(t, line)
	require.NoError(t, err)

	res := resourceOf(t, ld)
	assert.NotContains(t, res, "cloud.region")
	assert.NotContains(t, res, "cloud.account.id")
}

// A classic load balancer line holds no ARN, so its region and account are not in the record.
func TestCLB_HasNoARNSoRegionAndAccountStayOut(t *testing.T) {
	ld, _, err := decodeALB(t, clbLine("192.0.2.10:2817", "198.51.100.7:80"))
	require.NoError(t, err)

	res := resourceOf(t, ld)
	assert.NotContains(t, res, "cloud.region")
	assert.NotContains(t, res, "cloud.account.id")
	assert.Equal(t, "aws", res["cloud.provider"])
}
