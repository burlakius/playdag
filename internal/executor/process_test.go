package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"playdag/internal/loader"
	"playdag/internal/protocol"
)

// writeAgent drops an executable shell agent into a temp dir and returns the dir.
func writeAgent(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	return dir
}

func node(id, script string, requires, provides []string) *loader.TestNode {
	return &loader.TestNode{
		TestID:   id,
		Script:   script,
		Contract: loader.DataContract{Requires: requires, Provides: provides},
	}
}

func TestProcess_Passed(t *testing.T) {
	dir := writeAgent(t, "agent.sh", `echo '{"schema_version":"1.0","status":"PASSED","provides":{"card.id":42}}'`)
	e := NewProcess(dir, "http://localhost:8000")

	res, err := e.Run(context.Background(), node("card.create", "agent.sh", nil, []string{"card.id"}), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed {
		t.Fatalf("status: got %q", res.Status)
	}
	if res.Provides["card.id"] != "42" {
		t.Fatalf("provides: got %q", res.Provides["card.id"])
	}
}

// A non-Python agent proves the executor is not tied to one language.
func TestProcess_ReceivesRequestOnStdin(t *testing.T) {
	dir := writeAgent(t, "echo.sh", `
req=$(cat)
case "$req" in
  *'"session.token":"tok123"'*) echo '{"status":"PASSED"}' ;;
  *) echo '{"status":"FAILED","error":"context missing"}' ;;
esac`)
	e := NewProcess(dir, "")

	res, err := e.Run(context.Background(), node("t", "echo.sh", []string{"session.token"}, nil),
		Input{Context: map[string]string{"session.token": "tok123"}, Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed {
		t.Fatalf("agent did not see the context on stdin: %q", res.Error)
	}
}

func TestProcess_ReceivesRequestInEnv(t *testing.T) {
	dir := writeAgent(t, "env.sh", `
case "$PLAYDAG_CONTEXT" in
  *'"tok123"'*) echo '{"status":"PASSED"}' ;;
  *) echo '{"status":"FAILED","error":"env missing"}' ;;
esac`)
	e := NewProcess(dir, "")

	res, err := e.Run(context.Background(), node("t", "env.sh", []string{"session.token"}, nil),
		Input{Context: map[string]string{"session.token": "tok123"}, Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed {
		t.Fatalf("agent did not see %s: %q", protocol.ContextEnv, res.Error)
	}
}

func TestProcess_BaseURLInEnv(t *testing.T) {
	dir := writeAgent(t, "url.sh", `
if [ "$BASE_URL" = "http://sut:9000" ]; then echo '{"status":"PASSED"}'
else echo '{"status":"FAILED","error":"BASE_URL='"$BASE_URL"'"}'; fi`)
	e := NewProcess(dir, "http://sut:9000")

	res, err := e.Run(context.Background(), node("t", "url.sh", nil, nil), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed {
		t.Fatalf("BASE_URL not passed: %q", res.Error)
	}
}

func TestProcess_AgentReportedFailureIsNotAnError(t *testing.T) {
	dir := writeAgent(t, "fail.sh", `echo '{"status":"FAILED","error":"login rejected"}'`)
	e := NewProcess(dir, "")

	res, err := e.Run(context.Background(), node("t", "fail.sh", nil, nil), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("a reported failure must not surface as an error: %v", err)
	}
	if res.Status != protocol.StatusFailed || res.Error != "login rejected" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestProcess_NonZeroExitIncludesStderr(t *testing.T) {
	dir := writeAgent(t, "crash.sh", `echo "boom: connection refused" >&2; exit 3`)
	e := NewProcess(dir, "")

	_, err := e.Run(context.Background(), node("t", "crash.sh", nil, nil), Input{Attempt: 1})
	if err == nil {
		t.Fatal("expected an error for a crashing agent")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected stderr in the error, got: %v", err)
	}
}

func TestProcess_ProtocolViolationIsAnError(t *testing.T) {
	dir := writeAgent(t, "junk.sh", `echo 'not json'`)
	e := NewProcess(dir, "")

	if _, err := e.Run(context.Background(), node("t", "junk.sh", nil, nil), Input{Attempt: 1}); err == nil {
		t.Fatal("expected an error for a protocol violation")
	}
}

func TestProcess_MissingScriptIsAnError(t *testing.T) {
	e := NewProcess(t.TempDir(), "")
	if _, err := e.Run(context.Background(), node("t", "nope.sh", nil, nil), Input{Attempt: 1}); err == nil {
		t.Fatal("expected an error for a missing script")
	}
}

func TestProcess_CancelledContext(t *testing.T) {
	dir := writeAgent(t, "slow.sh", `sleep 5; echo '{"status":"PASSED"}'`)
	e := NewProcess(dir, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := e.Run(ctx, node("t", "slow.sh", nil, nil), Input{Attempt: 1}); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestCommand_PythonScriptsGoThroughPython3(t *testing.T) {
	got := command("/agents/login.py")
	want := []string{"python3", "/agents/login.py"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestCommand_OtherScriptsRunDirectly(t *testing.T) {
	if got := command("/agents/login.sh"); len(got) != 1 || got[0] != "/agents/login.sh" {
		t.Fatalf("want [/agents/login.sh], got %v", got)
	}
}
