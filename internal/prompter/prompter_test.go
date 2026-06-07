package prompter

import (
	"strings"
	"testing"
)

func TestTypewriterOutput(t *testing.T) {
	var b strings.Builder
	Typewriter(&b, "faça já", 1e9) // huge cps -> no real wait
	got := b.String()
	if !strings.HasPrefix(got, "faça já") {
		t.Errorf("output missing/garbled text: %q", got)
	}
	if !strings.HasSuffix(got, caret) {
		t.Errorf("output missing trailing caret: %q", got)
	}
}

func TestTypeDurationGrows(t *testing.T) {
	short := TypeDuration("hi", 32)
	long := TypeDuration("hi there, this is a longer line\nwith a newline", 32)
	if long <= short {
		t.Errorf("longer text should take longer: short=%v long=%v", short, long)
	}
	if short <= 0 {
		t.Errorf("duration should be positive, got %v", short)
	}
}

func TestTypeDurationDefaultsInvalidCPS(t *testing.T) {
	if got, want := TypeDuration("hi", 0), TypeDuration("hi", 32); got != want {
		t.Errorf("invalid cps should default: got %v want %v", got, want)
	}
}

func TestShquote(t *testing.T) {
	if got := shquote("/a b/c"); got != "'/a b/c'" {
		t.Errorf("shquote spaces = %q", got)
	}
	if got := shquote("it's"); got != `'it'\''s'` {
		t.Errorf("shquote apostrophe = %q", got)
	}
}

var _ Prompter = (*Hypr)(nil)
