// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package logshapeprocessor

import (
	"strings"
	"testing"
)

// Generic values only: the GUID, the accounts and the address are made up (documentation range).
const (
	identityCenterGUID      = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	identityCenterAccountID = "444455556666"
	identityCenterAddress   = "203.0.113.9"
)

func identityCenterConfig() *Config {
	cfg := cloudTrailConfig()
	cfg.UserAliases = map[string]string{identityCenterGUID: "test-user"}
	cfg.AccountAliases[identityCenterAccountID] = "test-prod"
	return cfg
}

func identityCenterAttrs(overrides map[string]any) map[string]any {
	attrs := map[string]any{
		"rpc.method":                   "sso.amazonaws.com/GetRoleCredentials",
		"aws.principal.type":           "IdentityCenterUser",
		"user.id":                      identityCenterGUID,
		"aws.service_event.role_name":  "ReadOnly",
		"aws.service_event.account_id": identityCenterAccountID,
		"source.address":               identityCenterAddress,
	}
	for k, v := range overrides {
		if v == nil {
			delete(attrs, k)
			continue
		}
		attrs[k] = v
	}
	return attrs
}

func identityCenterRun(t *testing.T, cfg *Config, overrides map[string]any) (body, service string) {
	t.Helper()
	lr, res := newHarness(t, cfg).run(cloudTrailInput(identityCenterAttrs(overrides)))
	return lr.Body().Str(), attrStr(t, res.Attributes(), "service.name")
}

func TestCloudTrail_IdentityCenterUser_CallerIsTheAliasOfTheGUID(t *testing.T) {
	_, service := identityCenterRun(t, identityCenterConfig(), nil)

	if service != "test-user" {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_IdentityCenterUser_WithoutAnAlias_CallerIsTheGUID(t *testing.T) {
	_, service := identityCenterRun(t, cloudTrailConfig(), nil)

	if service != identityCenterGUID {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_IdentityCenterUser_WithoutAGUID_CallerIsCloudtrail(t *testing.T) {
	_, service := identityCenterRun(t, identityCenterConfig(), map[string]any{"user.id": nil})

	if service != "cloudtrail" {
		t.Fatalf("service.name = %q", service)
	}
}

func TestCloudTrail_GetRoleCredentials_BodyHasRoleAccountAliasAndAddress(t *testing.T) {
	body, _ := identityCenterRun(t, identityCenterConfig(), nil)

	if want := "sso:GetRoleCredentials ReadOnly@test-prod from " + identityCenterAddress; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestCloudTrail_GetRoleCredentials_AccountWithoutAnAlias_BodyHasTheID(t *testing.T) {
	body, _ := identityCenterRun(t, cloudTrailConfig(), nil)

	if want := "sso:GetRoleCredentials ReadOnly@" + identityCenterAccountID + " from " + identityCenterAddress; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestCloudTrail_GetRoleCredentials_MissingRoleOrAccount_BodyHasNoAtPart(t *testing.T) {
	for name, drop := range map[string]string{"role": "aws.service_event.role_name", "account": "aws.service_event.account_id"} {
		t.Run(name, func(t *testing.T) {
			body, _ := identityCenterRun(t, identityCenterConfig(), map[string]any{drop: nil})

			if want := "sso:GetRoleCredentials from " + identityCenterAddress; body != want {
				t.Fatalf("body = %q, want %q", body, want)
			}
		})
	}
}

func TestCloudTrail_GetRoleCredentials_SourceThatIsNotAnAddress_IsUsedAsGiven(t *testing.T) {
	body, _ := identityCenterRun(t, identityCenterConfig(), map[string]any{"source.address": "AWS Internal"})

	if want := "sso:GetRoleCredentials ReadOnly@test-prod from AWS Internal"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestCloudTrail_GetRoleCredentials_WithoutASource_BodyHasNoFromPart(t *testing.T) {
	body, _ := identityCenterRun(t, identityCenterConfig(), map[string]any{"source.address": nil})

	if want := "sso:GetRoleCredentials ReadOnly@test-prod"; body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestCloudTrail_OtherIdentityType_BodyHoldsNoAddress(t *testing.T) {
	body, _ := identityCenterRun(t, identityCenterConfig(), map[string]any{"aws.principal.type": "IAMUser"})

	if body != "sso:GetRoleCredentials" {
		t.Fatalf("body = %q, want the plain body with no address", body)
	}
}

func TestCloudTrail_IdentityCenterUser_OtherEvent_GetsThePlainBodyAndTheCaller(t *testing.T) {
	body, service := identityCenterRun(t, identityCenterConfig(), map[string]any{"rpc.method": "sso.amazonaws.com/ListAccounts"})

	if body != "sso:ListAccounts" {
		t.Fatalf("body = %q", body)
	}
	if service != "test-user" {
		t.Fatalf("service.name = %q", service)
	}
}

func TestConfig_UserAliases_EmptyKeyIsRefused(t *testing.T) {
	cfg := defaultConfig()
	cfg.UserAliases = map[string]string{"": "test-user"}

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "user_aliases") {
		t.Fatalf("err = %v, want it to name user_aliases", err)
	}
}

func TestConfig_UserAliases_EmptyValueIsRefusedAndNamesTheEntry(t *testing.T) {
	cfg := defaultConfig()
	cfg.UserAliases = map[string]string{identityCenterGUID: ""}

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "user_aliases") || !strings.Contains(err.Error(), identityCenterGUID) {
		t.Fatalf("err = %v, want it to name the user_aliases entry", err)
	}
}

func TestConfig_UserAliases_ValidMapIsAccepted(t *testing.T) {
	cfg := defaultConfig()
	cfg.UserAliases = map[string]string{identityCenterGUID: "test-user"}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}
