package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeObject(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("request is not a JSON object: %v", err)
	}
	return obj
}

func TestEncodeRequest_Envelope(t *testing.T) {
	data, err := EncodeRequest(Request{
		TestID:   "card.create",
		Script:   "create.py",
		Attempt:  2,
		BaseURL:  "http://localhost:8000",
		Requires: []string{"session.token"},
		Context:  map[string]string{"session.token": "tok123"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeObject(t, data)
	if obj["schema_version"] != Version {
		t.Errorf("schema_version: want %q, got %v", Version, obj["schema_version"])
	}
	if obj["test_id"] != "card.create" {
		t.Errorf("test_id: got %v", obj["test_id"])
	}
	if obj["attempt"] != float64(2) {
		t.Errorf("attempt: got %v", obj["attempt"])
	}

	ctx, ok := obj["context"].(map[string]any)
	if !ok || ctx["session.token"] != "tok123" {
		t.Errorf("context: got %v", obj["context"])
	}
}

// A pre-envelope agent reads the context map straight off the top level of the
// request, so the mirror must be there.
func TestEncodeRequest_LegacyFlatMirror(t *testing.T) {
	data, err := EncodeRequest(Request{
		TestID:  "card.create",
		Context: map[string]string{"session.token": "tok123", "user.id": "7"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeObject(t, data)
	if obj["session.token"] != "tok123" {
		t.Errorf("flat session.token: got %v", obj["session.token"])
	}
	if obj["user.id"] != "7" {
		t.Errorf("flat user.id: got %v", obj["user.id"])
	}
}

func TestEncodeRequest_ContextKeyCollision(t *testing.T) {
	_, err := EncodeRequest(Request{
		TestID:  "t",
		Context: map[string]string{"test_id": "hijacked"},
	})
	if err == nil {
		t.Fatal("expected a collision error for a context key named test_id")
	}
	if !strings.Contains(err.Error(), "test_id") {
		t.Fatalf("expected the key in the error, got: %v", err)
	}
}

func TestEncodeRequest_NoContext(t *testing.T) {
	data, err := EncodeRequest(Request{TestID: "root"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obj := decodeObject(t, data); obj["schema_version"] != Version {
		t.Fatalf("schema_version missing: %v", obj)
	}
}

func TestDecodeResponse_Passed(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"schema_version":"1.0","status":"PASSED","provides":{"auth.token":"tok123"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != StatusPassed {
		t.Errorf("status: got %q", resp.Status)
	}
	if resp.Provides["auth.token"] != "tok123" {
		t.Errorf("provides: got %q", resp.Provides["auth.token"])
	}
	if resp.Error != "" {
		t.Errorf("error: got %q", resp.Error)
	}
}

// Agents written before the envelope omit schema_version entirely.
func TestDecodeResponse_LegacyNoVersion(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"status":"PASSED","provides":{"auth.token":"tok123"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != StatusPassed || resp.Provides["auth.token"] != "tok123" {
		t.Fatalf("legacy response not accepted: %+v", resp)
	}
}

func TestDecodeResponse_Failed(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"status":"FAILED","error":"login rejected"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != StatusFailed || resp.Error != "login rejected" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestDecodeResponse_Artifacts(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"status":"PASSED","artifacts":[{"type":"trace","path":"trace.zip"}]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Artifacts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(resp.Artifacts))
	}
	if resp.Artifacts[0].Type != "trace" || resp.Artifacts[0].Path != "trace.zip" {
		t.Fatalf("unexpected artifact: %+v", resp.Artifacts[0])
	}
}

func TestDecodeResponse_EmptyOutput(t *testing.T) {
	for _, in := range [][]byte{nil, {}, []byte("   \n")} {
		_, err := DecodeResponse(in)
		if err == nil {
			t.Fatalf("expected an error for empty output %q", in)
		}
		if !strings.Contains(err.Error(), "no output") {
			t.Fatalf("expected 'no output' in error, got: %v", err)
		}
	}
}

func TestDecodeResponse_InvalidJSON(t *testing.T) {
	_, err := DecodeResponse([]byte("not-json"))
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected an 'invalid JSON' error, got: %v", err)
	}
}

func TestDecodeResponse_MissingStatus(t *testing.T) {
	_, err := DecodeResponse([]byte(`{"provides":{"k":"v"}}`))
	if err == nil || !strings.Contains(err.Error(), "missing status") {
		t.Fatalf("expected a 'missing status' error, got: %v", err)
	}
}

func TestDecodeResponse_UnknownStatus(t *testing.T) {
	_, err := DecodeResponse([]byte(`{"status":"passed"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("expected an 'unknown status' error, got: %v", err)
	}
}

func TestDecodeResponse_UnsupportedVersion(t *testing.T) {
	_, err := DecodeResponse([]byte(`{"schema_version":"2.0","status":"PASSED"}`))
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("expected a schema_version error, got: %v", err)
	}
}

func TestDecodeResponse_ProvidesCoercedToString(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"status":"PASSED","provides":{"auth.token":"abc123","auth.user_id":3,"verified":true}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"auth.token": "abc123", "auth.user_id": "3", "verified": "true"}
	for k, w := range want {
		if resp.Provides[k] != w {
			t.Errorf("provides[%q]: want %q, got %q", k, w, resp.Provides[k])
		}
	}
}

// The orchestrator must be able to read back what it wrote, which is the loop an
// adapter in any other language has to close.
func TestRoundTrip_RequestContextReachesResponse(t *testing.T) {
	data, err := EncodeRequest(Request{TestID: "t", Context: map[string]string{"a.b": "v"}})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if req.SchemaVersion != Version || req.Context["a.b"] != "v" {
		t.Fatalf("round trip lost data: %+v", req)
	}
}
