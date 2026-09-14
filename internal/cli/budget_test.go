package cli

import (
	"strings"
	"testing"
)

func TestParseMemAvailable(t *testing.T) {
	got, err := parseMemAvailable([]byte("MemTotal: 16000000 kB\nMemAvailable: 8192 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 8192*1024 {
		t.Fatalf("got %d", got)
	}
	if _, err := parseMemAvailable([]byte("MemTotal: 1 kB\n")); err == nil {
		t.Fatal("missing MemAvailable")
	}
}

func TestMemoryBudgetSubtractsReserveOnce(t *testing.T) {
	if memoryBudget(10<<30) != 8<<30 {
		t.Fatalf("10-2: %d", memoryBudget(10<<30))
	}
	if memoryBudget(hostMemoryReserve) != 0 {
		t.Fatal("reserve only")
	}
	if !strings.Contains("MemAvailable", "MemAvailable") {
		t.Fatal("anchor")
	}
}
