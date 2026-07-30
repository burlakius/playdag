package extractor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubPython3 puts a python3 on PATH that writes manifestJSON to whatever path
// follows --playdag-collect=, so the extractor exercises the real command path
// without pytest installed.
func stubPython3(t *testing.T, manifestJSON string) {
	t.Helper()
	bin := t.TempDir()

	fixture := filepath.Join(bin, "manifest.json")
	if err := os.WriteFile(fixture, []byte(manifestJSON), 0o644); err != nil {
		t.Fatalf("write manifest fixture: %v", err)
	}

	script := `#!/bin/sh
for arg in "$@"; do
  case "$arg" in
    --playdag-collect=*) dest="${arg#--playdag-collect=}" ;;
  esac
done
cat ` + fixture + ` > "$dest"
`
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python3: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stubFailingPython3 puts a python3 on PATH that exits non-zero without ever
// writing a manifest, standing in for a collection error.
func stubFailingPython3(t *testing.T, stderr string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit 2\n"
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python3: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestPytest_ExtractsManifest(t *testing.T) {
	base := t.TempDir()
	stubPython3(t, `{
	  "nodes": [
	    {
	      "nodeid": "tests/test_auth.py::test_login",
	      "file": "tests/test_auth.py",
	      "line": 6,
	      "id": "auth.login",
	      "provides": ["session.token"],
	      "criticality": "BLOCKER",
	      "priority": 10,
	      "tags": ["api"]
	    },
	    {
	      "nodeid": "tests/test_card.py::test_move",
	      "file": "tests/test_card.py",
	      "line": 14,
	      "id": "card.move",
	      "requires": ["session.token", "card.id"],
	      "criticality": "NON_CRITICAL"
	    }
	  ]
	}`)

	nodes, err := NewPytest(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d: %v", len(nodes), ids(nodes))
	}

	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.TestID] = n
	}

	login, ok := byID["auth.login"]
	if !ok {
		t.Fatalf("auth.login missing, got %v", ids(nodes))
	}
	if login.Script != "tests/test_auth.py::test_login" {
		t.Errorf("script: got %q", login.Script)
	}
	if login.Line != 6 {
		t.Errorf("line: got %d", login.Line)
	}
	if login.Description != "tests/test_auth.py::test_login" {
		t.Errorf("description should fall back to the nodeid, got %q", login.Description)
	}
	if len(login.Contract.Provides) != 1 || login.Contract.Provides[0] != "session.token" {
		t.Errorf("provides: got %v", login.Contract.Provides)
	}
	if login.Policy.Criticality != "BLOCKER" || login.Policy.Priority != 10 {
		t.Errorf("policy: got %+v", login.Policy)
	}
	if len(login.Resources.Tags) != 1 || login.Resources.Tags[0] != "api" {
		t.Errorf("tags: got %v", login.Resources.Tags)
	}

	move := byID["card.move"]
	want := []string{"session.token", "card.id"}
	if len(move.Contract.Requires) != 2 || move.Contract.Requires[0] != want[0] || move.Contract.Requires[1] != want[1] {
		t.Errorf("card.move requires: want %v, got %v", want, move.Contract.Requires)
	}
	if move.Policy.Criticality != "NON_CRITICAL" {
		t.Errorf("card.move criticality: got %q", move.Policy.Criticality)
	}
	if len(move.Contract.Provides) != 0 {
		t.Errorf("card.move provides should be empty, not nil-vs-empty ambiguous: %v", move.Contract.Provides)
	}
}

func TestPytest_ExtractedSuiteValidates(t *testing.T) {
	base := t.TempDir()
	stubPython3(t, `{"nodes": [
	  {"nodeid": "t/a.py::test_a", "file": "t/a.py", "line": 1, "id": "a", "provides": ["k"], "criticality": "BLOCKER"},
	  {"nodeid": "t/b.py::test_b", "file": "t/b.py", "line": 1, "id": "b", "requires": ["k"], "criticality": "BLOCKER"}
	]}`)

	nodes, err := NewPytest(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if err := Validate(nodes); err != nil {
		t.Fatalf("the extracted suite does not validate: %v", err)
	}
}

func TestPytest_CriticalityDefaultsToBlocker(t *testing.T) {
	base := t.TempDir()
	stubPython3(t, `{"nodes": [{"nodeid": "t/a.py::test_a", "file": "t/a.py", "line": 1, "id": "a"}]}`)

	nodes, err := NewPytest(base).Extract(context.Background())
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if nodes[0].Policy.Criticality != "BLOCKER" {
		t.Fatalf("want BLOCKER default, got %q", nodes[0].Policy.Criticality)
	}
}

func TestPytest_CollectionFailureIsSurfaced(t *testing.T) {
	base := t.TempDir()
	stubFailingPython3(t, "tests/test_broken.py: SyntaxError")

	_, err := NewPytest(base).Extract(context.Background())
	if err == nil {
		t.Fatal("expected an error when pytest reports a collection failure")
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("pytest's message should survive, got: %v", err)
	}
}

func TestPytest_BrokenManifestIsAnError(t *testing.T) {
	base := t.TempDir()
	stubPython3(t, "not json")

	if _, err := NewPytest(base).Extract(context.Background()); err == nil {
		t.Fatal("expected an error for an unparsable manifest")
	}
}
