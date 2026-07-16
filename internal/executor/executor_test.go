package executor

import "testing"

func TestNew_Process(t *testing.T) {
	e, err := New("process", "/base", "http://localhost")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := e.(*ProcessExecutor); !ok {
		t.Fatalf("want *ProcessExecutor, got %T", e)
	}
}

func TestNew_Unknown(t *testing.T) {
	if _, err := New("cypress", "", ""); err == nil {
		t.Fatal("expected an error for an unknown executor")
	}
}
