package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"playdag/internal/protocol"
)

// fakeNpx puts a stub npx on PATH for the duration of the test. body is a shell
// script that stands in for a Playwright run: it sees the same argv and
// environment the real one would.
func fakeNpx(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npx"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake npx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPlaywright_ReporterResponseIsRead(t *testing.T) {
	fakeNpx(t, `echo '{"schema_version":"1.0","status":"PASSED","provides":{"card.id":"42"}}' > "$PLAYDAG_RESULT"`)
	e := NewPlaywright(t.TempDir(), "")

	res, err := e.Run(context.Background(), node("card.create", "card.spec.ts", nil, []string{"card.id"}), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed || res.Provides["card.id"] != "42" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// Playwright exits non-zero for a failing test; the reporter still wrote a
// response, so that must surface as a FAILED node and not a transport error.
func TestPlaywright_FailingTestIsNotATransportError(t *testing.T) {
	fakeNpx(t, `
echo '{"schema_version":"1.0","status":"FAILED","error":"expect(received).toBe(expected)"}' > "$PLAYDAG_RESULT"
exit 1`)
	e := NewPlaywright(t.TempDir(), "")

	res, err := e.Run(context.Background(), node("card.create", "", nil, nil), Input{Attempt: 1})
	if err != nil {
		t.Fatalf("a reported failure must not surface as an error: %v", err)
	}
	if res.Status != protocol.StatusFailed || !strings.Contains(res.Error, "toBe") {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestPlaywright_NoResponseIsAnError(t *testing.T) {
	fakeNpx(t, `echo "Error: No tests found" >&2; exit 1`)
	e := NewPlaywright(t.TempDir(), "")

	_, err := e.Run(context.Background(), node("card.create", "", nil, nil), Input{Attempt: 1})
	if err == nil {
		t.Fatal("expected an error when the reporter wrote nothing")
	}
	if !strings.Contains(err.Error(), "No tests found") {
		t.Fatalf("expected the playwright output in the error, got: %v", err)
	}
}

func TestPlaywright_ContextInEnv(t *testing.T) {
	fakeNpx(t, `
case "$PLAYDAG_CONTEXT" in
  *'"session.token":"tok123"'*) echo '{"status":"PASSED"}' > "$PLAYDAG_RESULT" ;;
  *) echo '{"status":"FAILED","error":"no context"}' > "$PLAYDAG_RESULT" ;;
esac`)
	e := NewPlaywright(t.TempDir(), "")

	res, err := e.Run(context.Background(), node("card.create", "", []string{"session.token"}, nil),
		Input{Context: map[string]string{"session.token": "tok123"}, Attempt: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != protocol.StatusPassed {
		t.Fatalf("context did not reach playwright: %q", res.Error)
	}
}

func TestPlaywright_ResultFileIsRemoved(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "path.txt")
	fakeNpx(t, `printf '%s' "$PLAYDAG_RESULT" > `+trace+`
echo '{"status":"PASSED"}' > "$PLAYDAG_RESULT"`)
	e := NewPlaywright(dir, "")

	if _, err := e.Run(context.Background(), node("t", "", nil, nil), Input{Attempt: 1}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	used, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("fake npx did not record the result path: %v", err)
	}
	if _, err := os.Stat(string(used)); !os.IsNotExist(err) {
		t.Fatalf("result file %s outlived the run", used)
	}
}

func TestPlaywrightArgs_GrepsTheNodeTag(t *testing.T) {
	args := playwrightArgs(node("card.create", "tests/card.spec.ts", nil, nil))
	want := []string{"playwright", "test", "tests/card.spec.ts", "--grep", `@id:card\.create`, "--retries=0", "--reporter=" + reporterModule}

	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args:\n want %v\n got  %v", want, args)
	}
}

func TestPlaywrightArgs_NoScript(t *testing.T) {
	args := playwrightArgs(node("card.create", "", nil, nil))
	if args[2] != "--grep" {
		t.Fatalf("an empty script must not become an argument: %v", args)
	}
}

// A dotted id must not act as a regexp wildcard, or one node could select another.
func TestGrepFor_QuotesTheID(t *testing.T) {
	if got := grepFor("card.create"); got != `@id:card\.create` {
		t.Fatalf(`want @id:card\.create, got %s`, got)
	}
}
