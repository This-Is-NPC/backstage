package stage

import "testing"

func TestExportPrefix(t *testing.T) {
	if got := exportPrefix(nil); got != "" {
		t.Errorf("empty env should give empty prefix, got %q", got)
	}
	// keys sorted -> deterministic
	got := exportPrefix(map[string]string{"B": "2", "A": "1"})
	want := "export A='1'; export B='2'; "
	if got != want {
		t.Errorf("exportPrefix = %q, want %q", got, want)
	}
	// value with a space and quote is shell-safe
	got = exportPrefix(map[string]string{"P": "/a b/c"})
	if got != "export P='/a b/c'; " {
		t.Errorf("exportPrefix quoting = %q", got)
	}
}

func TestOrHelpers(t *testing.T) {
	if orDot("") != "." || orDot("x") != "x" {
		t.Error("orDot")
	}
	if orBash("") != "bash" || orBash("okt tui") != "okt tui" {
		t.Error("orBash")
	}
}

var _ Stager = (*Hypr)(nil)
