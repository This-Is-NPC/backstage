package take

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	d, err := ParseDuration("72h")
	if err != nil || d != 72*time.Hour {
		t.Fatalf("72h: %v %v", d, err)
	}
	d, err = ParseDuration("7d")
	if err != nil || d != 7*24*time.Hour {
		t.Fatalf("7d: %v %v", d, err)
	}
	if _, err := ParseDuration("-1h"); err == nil {
		t.Fatal("accepted negative")
	}
}

func TestParseSize(t *testing.T) {
	n, err := ParseSize("500K")
	if err != nil || n != 500*1024 {
		t.Fatalf("500K: %d %v", n, err)
	}
	n, err = ParseSize("2G")
	if err != nil || n != 2*1024*1024*1024 {
		t.Fatalf("2G: %d %v", n, err)
	}
	n, err = ParseSize("12")
	if err != nil || n != 12 {
		t.Fatalf("12: %d %v", n, err)
	}
}
