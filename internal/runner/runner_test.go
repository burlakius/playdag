// Integration tests with real subprocess are in runner_integration_test.go
// with build tag //go:build integration
package runner

import (
	"strings"
	"testing"
)

func TestParseAgentResult_Passed(t *testing.T) {
	input := `{"status":"PASSED","provides":{"auth.token":"tok123"}}`
	got := ParseAgentResult([]byte(input))
	if got.Status != "PASSED" {
		t.Fatalf("expected PASSED, got %q", got.Status)
	}
	if got.Provides["auth.token"] != "tok123" {
		t.Fatalf("expected tok123, got %q", got.Provides["auth.token"])
	}
	if got.Error != "" {
		t.Fatalf("expected no error, got %q", got.Error)
	}
}

func TestParseAgentResult_Failed(t *testing.T) {
	input := `{"status":"FAILED","error":"login rejected"}`
	got := ParseAgentResult([]byte(input))
	if got.Status != "FAILED" {
		t.Fatalf("expected FAILED, got %q", got.Status)
	}
	if got.Error != "login rejected" {
		t.Fatalf("expected 'login rejected', got %q", got.Error)
	}
}

func TestParseAgentResult_EmptyOutput(t *testing.T) {
	for _, input := range [][]byte{nil, {}, []byte("   \n")} {
		got := ParseAgentResult(input)
		if got.Status != "FAILED" {
			t.Fatalf("expected FAILED for empty output, got %q", got.Status)
		}
		if !strings.Contains(got.Error, "no output") {
			t.Fatalf("expected 'no output' in error, got %q", got.Error)
		}
	}
}

func TestParseAgentResult_InvalidJSON(t *testing.T) {
	got := ParseAgentResult([]byte("not-json"))
	if got.Status != "FAILED" {
		t.Fatalf("expected FAILED, got %q", got.Status)
	}
	if !strings.Contains(got.Error, "invalid JSON") {
		t.Fatalf("expected 'invalid JSON' in error, got %q", got.Error)
	}
}

func TestParseAgentResult_MissingStatus(t *testing.T) {
	got := ParseAgentResult([]byte(`{"provides":{"k":"v"}}`))
	if got.Status != "FAILED" {
		t.Fatalf("expected FAILED, got %q", got.Status)
	}
	if !strings.Contains(got.Error, "missing status") {
		t.Fatalf("expected 'missing status' in error, got %q", got.Error)
	}
}

func TestParseAgentResult_ProvidesIntegerCoerced(t *testing.T) {
	input := `{"status":"PASSED","provides":{"auth.user_id":5}}`
	got := ParseAgentResult([]byte(input))
	if got.Status != "PASSED" {
		t.Fatalf("expected PASSED, got %q", got.Status)
	}
	if got.Provides["auth.user_id"] != "5" {
		t.Fatalf("expected \"5\", got %q", got.Provides["auth.user_id"])
	}
}

func TestParseAgentResult_ProvidesBooleanCoerced(t *testing.T) {
	input := `{"status":"PASSED","provides":{"feature.enabled":true}}`
	got := ParseAgentResult([]byte(input))
	if got.Status != "PASSED" {
		t.Fatalf("expected PASSED, got %q", got.Status)
	}
	if got.Provides["feature.enabled"] != "true" {
		t.Fatalf("expected \"true\", got %q", got.Provides["feature.enabled"])
	}
}

func TestParseAgentResult_ProvidesMixedTypes(t *testing.T) {
	input := `{"status":"PASSED","provides":{"auth.token":"abc123","auth.user_id":3,"verified":true}}`
	got := ParseAgentResult([]byte(input))
	if got.Status != "PASSED" {
		t.Fatalf("expected PASSED, got %q", got.Status)
	}
	cases := map[string]string{
		"auth.token":   "abc123",
		"auth.user_id": "3",
		"verified":     "true",
	}
	for k, want := range cases {
		if got.Provides[k] != want {
			t.Fatalf("provides[%q]: expected %q, got %q", k, want, got.Provides[k])
		}
	}
}
