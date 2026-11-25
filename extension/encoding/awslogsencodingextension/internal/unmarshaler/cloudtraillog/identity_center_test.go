// Copyright 2026 V-Lad
// SPDX-License-Identifier: Apache-2.0

package cloudtraillog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

// Generic values only: the GUID, account ids and addresses are made up (documentation ranges).
const (
	identityCenterGUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	identityStoreARN   = "arn:aws:identitystore::111122223333:identitystore/d-0000000000"

	getRoleCredentials = `{"Records":[{"eventVersion":"1.08","eventTime":"2026-11-22T10:00:00Z","eventSource":"sso.amazonaws.com",` +
		`"eventName":"GetRoleCredentials","awsRegion":"eu-west-1","sourceIPAddress":"203.0.113.9","userAgent":"agent-test/1.0",` +
		`"requestParameters":null,"responseElements":null,"requestID":"11111111-2222-3333-4444-555555555555",` +
		`"eventID":"66666666-7777-8888-9999-000000000000","eventType":"AwsApiCall","recipientAccountId":"111122223333",` +
		`"userIdentity":{"type":"IdentityCenterUser","accountId":"111122223333","onBehalfOf":{"userId":"` + identityCenterGUID +
		`","identityStoreArn":"` + identityStoreARN + `"}},` +
		`"serviceEventDetails":{"role_name":"ReadOnly","account_id":"444455556666"}}]}`
)

func decodeOneRecord(t *testing.T, payload string, userIdentityPrefix bool) pcommon.Map {
	t.Helper()
	unmarshaler := NewCloudTrailLogUnmarshaler(component.BuildInfo{Version: "test-version"}, userIdentityPrefix)

	logs, err := unmarshaler.UnmarshalAWSLogs(strings.NewReader(payload))

	require.NoError(t, err)
	require.Equal(t, 1, logs.LogRecordCount())
	return logs.ResourceLogs().At(0).ScopeLogs().At(0).LogRecords().At(0).Attributes()
}

func requireStr(t *testing.T, attrs pcommon.Map, key, want string) {
	t.Helper()
	got, ok := attrs.Get(key)
	require.True(t, ok, "%s is missing; have %v", key, attrs.AsRaw())
	require.Equal(t, want, got.Str(), key)
}

func requireAbsent(t *testing.T, attrs pcommon.Map, key string) {
	t.Helper()
	if v, ok := attrs.Get(key); ok {
		require.Failf(t, "attribute present", "%s = %v, want it absent", key, v.AsRaw())
	}
}

var userIdentityPrefixModes = map[string]bool{"user identity prefix off": false, "user identity prefix on": true}

func TestIdentityCenterUser_OnBehalfOf_GivesTheUserIDAndTheStoreArn(t *testing.T) {
	for name, userIdentityPrefix := range userIdentityPrefixModes {
		t.Run(name, func(t *testing.T) {
			attrs := decodeOneRecord(t, getRoleCredentials, userIdentityPrefix)

			requireStr(t, attrs, "user.id", identityCenterGUID)
			requireStr(t, attrs, "aws.user_identity.on_behalf_of.identity_store_arn", identityStoreARN)
		})
	}
}

func TestIdentityCenterUser_ServiceEventDetails_GiveTheRoleAndTheTargetAccount(t *testing.T) {
	for name, userIdentityPrefix := range userIdentityPrefixModes {
		t.Run(name, func(t *testing.T) {
			attrs := decodeOneRecord(t, getRoleCredentials, userIdentityPrefix)

			requireStr(t, attrs, "aws.service_event.role_name", "ReadOnly")
			requireStr(t, attrs, "aws.service_event.account_id", "444455556666")
		})
	}
}

func TestIdentityCenterUser_OnBehalfOfUserID_OverridesTheUserIdentityUserID(t *testing.T) {
	payload := strings.Replace(getRoleCredentials, `"accountId":"111122223333","onBehalfOf"`,
		`"accountId":"111122223333","userId":"other-id","onBehalfOf"`, 1)

	attrs := decodeOneRecord(t, payload, false)

	requireStr(t, attrs, "user.id", identityCenterGUID)
}

func TestIdentityCenterUser_OnBehalfOfWithEmptyFields_GivesNoAttributes(t *testing.T) {
	payload := strings.Replace(getRoleCredentials,
		`"onBehalfOf":{"userId":"`+identityCenterGUID+`","identityStoreArn":"`+identityStoreARN+`"}`,
		`"onBehalfOf":{"userId":"","identityStoreArn":""}`, 1)

	attrs := decodeOneRecord(t, payload, false)

	requireAbsent(t, attrs, "user.id")
	requireAbsent(t, attrs, "aws.user_identity.on_behalf_of.identity_store_arn")
}

func TestIdentityCenterUser_RecordWithSourceAndAgent_KeepsBoth(t *testing.T) {
	attrs := decodeOneRecord(t, getRoleCredentials, false)

	requireStr(t, attrs, "source.address", "203.0.113.9")
	requireStr(t, attrs, "user_agent.original", "agent-test/1.0")
}

func TestIdentityCenterUser_NullRequestAndResponse_GiveNoRequestOrResponseAttributes(t *testing.T) {
	attrs := decodeOneRecord(t, getRoleCredentials, false)

	requireAbsent(t, attrs, "aws.request.parameters")
	requireAbsent(t, attrs, "aws.response.elements")
}

func TestIdentityCenterUser_WithoutOnBehalfOf_GivesNoUserIDOrStoreArn(t *testing.T) {
	payload := strings.Replace(getRoleCredentials,
		`,"onBehalfOf":{"userId":"`+identityCenterGUID+`","identityStoreArn":"`+identityStoreARN+`"}`, "", 1)

	attrs := decodeOneRecord(t, payload, false)

	requireAbsent(t, attrs, "user.id")
	requireAbsent(t, attrs, "aws.user_identity.on_behalf_of.identity_store_arn")
}

func TestOtherIdentityType_WithOnBehalfOf_GivesNoOnBehalfOfAttributes(t *testing.T) {
	payload := strings.Replace(getRoleCredentials, `"type":"IdentityCenterUser"`, `"type":"IAMUser"`, 1)

	attrs := decodeOneRecord(t, payload, false)

	requireAbsent(t, attrs, "user.id")
	requireAbsent(t, attrs, "aws.user_identity.on_behalf_of.identity_store_arn")
}

const iamUserGetObject = `{"Records":[{"eventVersion":"1.08","eventTime":"2026-11-22T10:00:00Z","eventSource":"s3.amazonaws.com",` +
	`"eventName":"GetObject","awsRegion":"eu-west-1","sourceIPAddress":"203.0.113.9","eventType":"AwsApiCall",` +
	`"recipientAccountId":"111122223333","userIdentity":{"type":"IAMUser","userName":"test-user","accountId":"111122223333"}}]}`

func TestRecordWithoutIdentityCenterObjects_GivesNoIdentityCenterAttributes(t *testing.T) {
	attrs := decodeOneRecord(t, iamUserGetObject, false)

	requireAbsent(t, attrs, "aws.service_event.role_name")
	requireAbsent(t, attrs, "aws.service_event.account_id")
	requireAbsent(t, attrs, "aws.user_identity.on_behalf_of.identity_store_arn")
}

func TestRecordWithoutIdentityCenterObjects_KeepsTheUserName(t *testing.T) {
	attrs := decodeOneRecord(t, iamUserGetObject, false)

	requireStr(t, attrs, "user.name", "test-user")
}

func TestServiceEventDetails_WithOneKey_GivesThatKeyOnly(t *testing.T) {
	payload := strings.Replace(getRoleCredentials, `,"account_id":"444455556666"`, "", 1)

	attrs := decodeOneRecord(t, payload, false)

	requireStr(t, attrs, "aws.service_event.role_name", "ReadOnly")
	requireAbsent(t, attrs, "aws.service_event.account_id")
}
