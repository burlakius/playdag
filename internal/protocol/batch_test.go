package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeBatchRequest(t *testing.T) {
	payload, err := EncodeBatchRequest([]Request{
		{TestID: "auth.login", Provides: []string{"session.token"}},
		{TestID: "card.create", Context: map[string]string{"session.token": "abc"}},
	})
	if err != nil {
		t.Fatalf("EncodeBatchRequest: %v", err)
	}

	var got BatchRequest
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != Version {
		t.Errorf("envelope schema_version = %q, want %q", got.SchemaVersion, Version)
	}
	if len(got.Tests) != 2 {
		t.Fatalf("got %d tests, want 2", len(got.Tests))
	}
	for _, req := range got.Tests {
		if req.SchemaVersion != Version {
			t.Errorf("%s: schema_version = %q, want %q", req.TestID, req.SchemaVersion, Version)
		}
	}
	if got.Tests[1].Context["session.token"] != "abc" {
		t.Errorf("context did not survive: %v", got.Tests[1].Context)
	}
}

func TestDecodeBatchResponse(t *testing.T) {
	data := []byte(`{"schema_version":"1.1","results":{
		"auth.login":{"status":"PASSED","provides":{"session.token":"t"}},
		"card.create":{"status":"FAILED","error":"boom"}}}`)

	got, err := DecodeBatchResponse(data, []string{"auth.login", "card.create"})
	if err != nil {
		t.Fatalf("DecodeBatchResponse: %v", err)
	}
	if got["auth.login"].Provides["session.token"] != "t" {
		t.Errorf("login provides = %v", got["auth.login"].Provides)
	}
	if got["card.create"].Error != "boom" {
		t.Errorf("create error = %q", got["card.create"].Error)
	}
}

// A batch that quietly drops a node would shrink the suite, which is the one
// thing the report may never do.
func TestDecodeBatchResponseMissingNode(t *testing.T) {
	data := []byte(`{"schema_version":"1.1","results":{"auth.login":{"status":"PASSED"}}}`)

	_, err := DecodeBatchResponse(data, []string{"auth.login", "card.create"})
	if err == nil {
		t.Fatal("want an error naming the missing node")
	}
	if !strings.Contains(err.Error(), "card.create") {
		t.Errorf("error does not name the missing node: %v", err)
	}
}

func TestDecodeBatchResponseErrors(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "no output"},
		{"not json", "{", "invalid JSON"},
		{"bad version", `{"schema_version":"9.0","results":{}}`, "unsupported schema_version"},
		{"bad status", `{"results":{"a":{"status":"MAYBE"}}}`, `results["a"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeBatchResponse([]byte(tt.data), []string{"a"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// A 1.0 agent answers a single node the same way it always did; only the batch
// envelope is new.
func TestDecodeResponseAcceptsOlderAgents(t *testing.T) {
	resp, err := DecodeResponse([]byte(`{"schema_version":"1.0","status":"PASSED"}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if resp.Status != StatusPassed {
		t.Errorf("status = %q", resp.Status)
	}
}
