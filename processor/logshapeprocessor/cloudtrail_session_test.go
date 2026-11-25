// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"fmt"
	"testing"

	"go.opentelemetry.io/collector/pdata/plog"
)

// Generic names only: the role, the sessions and the accounts are made up.
const (
	machineSession  = "1700000000000000000" // 19 digits: a nanosecond timestamp
	sessionIssuer   = "aws-sdk-test-role"
	describeCall    = "secretsmanager.amazonaws.com/DescribeSecret"
	assumeRoleEvent = "sts.amazonaws.com/AssumeRole"
)

func assumedRoleAttrs(method, session, issuer string) map[string]any {
	attrs := map[string]any{
		"rpc.method":         method,
		"aws.principal.type": "AssumedRole",
		"aws.principal.arn":  "arn:aws:sts::111122223333:assumed-role/TestRole/" + session,
	}
	if issuer != "" {
		attrs["aws.user_identity.session_context.issuer.user_name"] = issuer
	}
	return attrs
}

func cloudTrailRun(t *testing.T, attrs map[string]any) (plog.LogRecord, string) {
	t.Helper()
	lr, res := newHarness(t, cloudTrailConfig()).run(cloudTrailInput(attrs))
	return lr, attrStr(t, res.Attributes(), "service.name")
}

func TestCloudTrail_EveryEvent_GivesTheCallerOfAnAssumeRoleEvent(t *testing.T) {
	for name, c := range map[string]struct {
		identity map[string]any
		want     string
	}{
		"AWSService":         {map[string]any{"aws.principal.type": "AWSService", "aws.user_identity.invoked_by": "ssm.amazonaws.com"}, "ssm"},
		"AssumedRole":        {assumedRoleAttrs("", "test-session", "TestRole"), "TestRole/test-session"},
		"AWSAccount":         {map[string]any{"aws.principal.type": "AWSAccount", "aws.user_identity.account_id": cloudTrailTestAccount}, "test-ops"},
		"SAMLUser":           {map[string]any{"aws.principal.type": "SAMLUser", "user.name": "test-user"}, "test-user"},
		"WebIdentityUser":    {map[string]any{"aws.principal.type": "WebIdentityUser", "user.name": "test-user"}, "test-user"},
		"IAMUser":            {map[string]any{"aws.principal.type": "IAMUser", "user.name": "test-user"}, "test-user"},
		"IdentityCenterUser": {map[string]any{"aws.principal.type": "IdentityCenterUser", "user.id": identityCenterGUID}, identityCenterGUID},
		"no type":            {map[string]any{}, "cloudtrail"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{describeCall, assumeRoleEvent} {
				attrs := map[string]any{}
				for k, v := range c.identity {
					attrs[k] = v
				}
				attrs["rpc.method"] = method

				_, service := cloudTrailRun(t, attrs)

				if service != c.want {
					t.Fatalf("%s: service.name = %q, want %q", method, service, c.want)
				}
			}
		})
	}
}

func TestCloudTrail_AssumedRole_MachineSession_CallerIsTheIssuerRoleName(t *testing.T) {
	_, service := cloudTrailRun(t, assumedRoleAttrs(describeCall, machineSession, sessionIssuer))

	if service != sessionIssuer {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_AssumedRole_ReadableSession_CallerIsRoleAndSession(t *testing.T) {
	_, service := cloudTrailRun(t, assumedRoleAttrs(describeCall, "test-session", sessionIssuer))

	if service != sessionIssuer+"/test-session" {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_AssumedRole_MachineSessionAndNoIssuerName_CallerIsTheRoleOfThePrincipalARN(t *testing.T) {
	_, service := cloudTrailRun(t, assumedRoleAttrs(describeCall, machineSession, ""))

	if service != "TestRole" {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_AssumedRole_NeitherIssuerNameNorPrincipalARN_CallerIsCloudtrail(t *testing.T) {
	_, service := cloudTrailRun(t, map[string]any{"rpc.method": describeCall, "aws.principal.type": "AssumedRole"})

	if service != "cloudtrail" {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_SessionName_IsMachineGeneratedWhenItIsTenOrMoreDigits(t *testing.T) {
	for session, want := range map[string]string{
		machineSession:        "TestRole",
		"1234567890":          "TestRole",
		"123456789":           "TestRole/123456789",
		"abc1234567890":       "TestRole/abc1234567890",       // pragma: allowlist secret
		"i-0abc1234def567890": "TestRole/i-0abc1234def567890", // pragma: allowlist secret
		"1700000000000000a":   "TestRole/1700000000000000a",
	} {
		t.Run(session, func(t *testing.T) {
			_, service := cloudTrailRun(t, assumedRoleAttrs(describeCall, session, "TestRole"))

			if service != want {
				t.Fatalf("service.name = %q, want %q", service, want)
			}
		})
	}
}

func TestCloudTrail_AssumeRoleBody_LeavesOutAMachineGeneratedTargetSession(t *testing.T) {
	for name, c := range map[string]struct {
		params map[string]any
		want   string
	}{
		"requested, machine-generated": {map[string]any{"roleSessionName": machineSession}, "sts:AssumeRole TargetRole"},
		"requested, readable":          {map[string]any{"roleSessionName": "x"}, "sts:AssumeRole TargetRole x"},
	} {
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"roleArn": "arn:aws:iam::111122223333:role/TargetRole"}
			for k, v := range c.params {
				params[k] = v
			}

			lr, _ := cloudTrailRun(t, map[string]any{"rpc.method": assumeRoleEvent, "aws.request.parameters": params})

			w4Body(t, lr, c.want)
		})
	}
}

func TestCloudTrail_AssumeRoleBody_LeavesOutAMachineGeneratedSessionOfTheResponse(t *testing.T) {
	attrs := map[string]any{
		"rpc.method":             assumeRoleEvent,
		"aws.request.parameters": map[string]any{"roleArn": "arn:aws:iam::111122223333:role/TargetRole"},
		"aws.response.elements": map[string]any{
			"assumedRoleUser": map[string]any{"arn": "arn:aws:sts::111122223333:assumed-role/TargetRole/" + machineSession},
		},
	}

	lr, _ := cloudTrailRun(t, attrs)

	w4Body(t, lr, "sts:AssumeRole TargetRole")
}

func TestCloudTrail_MachineGeneratedSession_IsKeptInTheSessionNameAttribute(t *testing.T) {
	lr, _ := cloudTrailRun(t, assumedRoleAttrs(describeCall, machineSession, sessionIssuer))

	if got := attrStr(t, lr.Attributes(), "aws.user_identity.session_name"); got != machineSession {
		t.Fatalf("aws.user_identity.session_name = %q", got)
	}
}

func TestCloudTrail_ReadableSession_GivesNoSessionNameAttribute(t *testing.T) {
	lr, _ := cloudTrailRun(t, assumedRoleAttrs(describeCall, "test-session", sessionIssuer))

	if v, ok := lr.Attributes().Get("aws.user_identity.session_name"); ok {
		t.Fatalf("aws.user_identity.session_name = %v, want it absent", v.AsRaw())
	}
}

func TestCloudTrail_OneHundredNumericSessionsOfOneRole_GiveOneServiceName(t *testing.T) {
	services := map[string]bool{}
	for i := range 100 {
		session := fmt.Sprintf("%d", 1700000000000000000+int64(i)*7919)

		_, service := cloudTrailRun(t, assumedRoleAttrs(describeCall, session, sessionIssuer))

		services[service] = true
	}

	if len(services) != 1 || !services[sessionIssuer] {
		t.Fatalf("service names = %v, want only %q", services, sessionIssuer)
	}
}
