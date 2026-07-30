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

// fakePython3 puts a stub python3 on PATH for the duration of the test. body
// is a shell script that stands in for a pytest run: it sees the same argv and
// environment the real one would.
func fakePython3(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake python3: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPytest_ResultIsRead(t *testing.T) {
	fakePython3(t, `echo '{"schema_version":"1.0","status":"PASSED","provides":{"card.id":"42"}}' > "$PLAYDAG_RESULT"`)
	e := NewPytest(t.TempDir(), "")

	res, err := e.Run(context.Background(), node("card.create", "tests/test_card.py::test_create", nil, []string{"card.id"}), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed || res.Provides["card.id"] != "42" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// pytest exits non-zero for a failing test; the plugin still wrote a response,
// so that must surface as a FAILED node and not a transport error.
func TestPytest_FailingTestIsNotATransportError(t *testing.T) {
	fakePython3(t, `
echo '{"schema_version":"1.0","status":"FAILED","error":"AssertionError: boom"}' > "$PLAYDAG_RESULT"
exit 1`)
	e := NewPytest(t.TempDir(), "")

	res, err := e.Run(context.Background(), node("card.create", "", nil, nil), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("a reported failure must not surface as an error: %v", err)
	}
	if res.Status != protocol.StatusFailed || !strings.Contains(res.Error, "boom") {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestPytest_NoResponseIsAnError(t *testing.T) {
	fakePython3(t, `echo "ERROR: file not found: test_card.py" >&2; exit 4`)
	e := NewPytest(t.TempDir(), "")

	_, err := e.Run(context.Background(), node("card.create", "", nil, nil), Input{Attempt: 1})
	if err == nil {
		t.Fatal("expected an error when the plugin wrote nothing")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("expected the pytest output in the error, got: %v", err)
	}
}

func TestPytest_ContextInEnv(t *testing.T) {
	fakePython3(t, `
case "$PLAYDAG_CONTEXT" in
  *'"session.token":"tok123"'*) echo '{"status":"PASSED"}' > "$PLAYDAG_RESULT" ;;
  *) echo '{"status":"FAILED","error":"no context"}' > "$PLAYDAG_RESULT" ;;
esac`)
	e := NewPytest(t.TempDir(), "")

	res, err := e.Run(context.Background(), node("card.create", "", []string{"session.token"}, nil),
		Input{Context: map[string]string{"session.token": "tok123"}, Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed {
		t.Fatalf("context did not reach pytest: %q", res.Error)
	}
}

func TestPytest_ResultFileIsRemoved(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "path.txt")
	fakePython3(t, `printf '%s' "$PLAYDAG_RESULT" > `+trace+`
echo '{"status":"PASSED"}' > "$PLAYDAG_RESULT"`)
	e := NewPytest(dir, "")

	if _, err := e.Run(context.Background(), node("t", "", nil, nil), Input{Attempt: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	used, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("fake python3 did not record the result path: %v", err)
	}
	if _, err := os.Stat(string(used)); !os.IsNotExist(err) {
		t.Fatalf("result file %s outlived the run", used)
	}
}

func TestPytest_InvokesWithNodeIDsPositionally(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "argv.txt")
	fakePython3(t, `printf '%s\n' "$@" > `+trace+`
echo '{"status":"PASSED"}' > "$PLAYDAG_RESULT"`)
	e := NewPytest(dir, "")

	n := node("auth.login", "tests/test_auth.py::test_login", nil, nil)
	if _, err := e.Run(context.Background(), n, Input{Attempt: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("fake python3 did not record argv: %v", err)
	}
	want := []string{"-m", "pytest", "-q", "tests/test_auth.py::test_login"}
	if strings.Join(strings.Fields(string(got)), " ") != strings.Join(want, " ") {
		t.Fatalf("args:\n want %v\n got  %v", want, strings.Fields(string(got)))
	}
}

func TestPytest_RunBatchSplitsResultsByNode(t *testing.T) {
	fakePython3(t, `echo '{"schema_version":"1.1","results":{
		"card.rename":{"status":"PASSED","provides":{"card.title":"new"}},
		"card.label":{"status":"FAILED","error":"label not applied"}}}' > "$PLAYDAG_RESULT"`)
	e := NewPytest(t.TempDir(), "")

	nodes := []*loader.TestNode{
		node("card.rename", "tests/test_card.py::test_rename", nil, nil),
		node("card.label", "tests/test_card.py::test_label", nil, nil),
	}
	res, err := e.RunBatch(context.Background(), nodes, []Input{{Attempt: 1}, {Attempt: 1}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Status != protocol.StatusPassed || res[0].Provides["card.title"] != "new" {
		t.Errorf("first node: %+v", res[0])
	}
	if res[1].Status != protocol.StatusFailed || !strings.Contains(res[1].Error, "label not applied") {
		t.Errorf("second node: %+v", res[1])
	}
}

// Each node in a batch must see its own context and nothing else, or declaring
// what a test requires would stop meaning anything.
func TestPytest_RunBatchKeepsContextsApart(t *testing.T) {
	fakePython3(t, `
case "$PLAYDAG_CONTEXT" in
  *'"a.key":"a"'*'"b.key":"b"'*) echo '{"results":{"a":{"status":"PASSED"},"b":{"status":"PASSED"}}}' > "$PLAYDAG_RESULT" ;;
  *) echo '{"results":{"a":{"status":"FAILED","error":"contexts merged"},"b":{"status":"FAILED"}}}' > "$PLAYDAG_RESULT" ;;
esac`)
	e := NewPytest(t.TempDir(), "")

	nodes := []*loader.TestNode{node("a", "", []string{"a.key"}, nil), node("b", "", []string{"b.key"}, nil)}
	res, err := e.RunBatch(context.Background(), nodes, []Input{
		{Context: map[string]string{"a.key": "a"}, Attempt: 1},
		{Context: map[string]string{"b.key": "b"}, Attempt: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res[0].Status != protocol.StatusPassed {
		t.Fatalf("each node must get its own context: %q", res[0].Error)
	}
}

func TestPytest_RunBatchNoResponseIsAnError(t *testing.T) {
	fakePython3(t, `echo "ERROR: collection failed" >&2; exit 2`)
	e := NewPytest(t.TempDir(), "")

	_, err := e.RunBatch(context.Background(), []*loader.TestNode{node("a", "", nil, nil)}, []Input{{Attempt: 1}})
	if err == nil || !strings.Contains(err.Error(), "collection failed") {
		t.Fatalf("expected the pytest output in the error, got: %v", err)
	}
}

// A node the agent never reached inside a batch must fail, not vanish: the
// runner is the one to decide whether that fails the whole batch or just that
// node, but it needs a result to decide on.
func TestPytest_RunBatchMissingNodeIsAnError(t *testing.T) {
	fakePython3(t, `echo '{"results":{"a":{"status":"PASSED"}}}' > "$PLAYDAG_RESULT"`)
	e := NewPytest(t.TempDir(), "")

	nodes := []*loader.TestNode{node("a", "", nil, nil), node("b", "", nil, nil)}
	_, err := e.RunBatch(context.Background(), nodes, []Input{{Attempt: 1}, {Attempt: 1}})
	if err == nil || !strings.Contains(err.Error(), "b") {
		t.Fatalf("expected an error naming the missing node, got: %v", err)
	}
}
