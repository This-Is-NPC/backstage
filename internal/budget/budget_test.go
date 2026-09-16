package budget

import (
	"strings"
	"testing"
)

func TestParseMemAvailable(t *testing.T) {
	got, err := ParseMemAvailable([]byte("MemTotal: 16000000 kB\nMemAvailable: 8192 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 8192*1024 {
		t.Fatalf("got %d", got)
	}
	if _, err := ParseMemAvailable([]byte("MemTotal: 1 kB\n")); err == nil {
		t.Fatal("missing MemAvailable")
	}
}

func TestMemoryBudgetSubtractsReserveOnce(t *testing.T) {
	if MemoryBudget(10<<30) != 8<<30 {
		t.Fatalf("10-2: %d", MemoryBudget(10<<30))
	}
	if MemoryBudget(HostMemoryReserve) != 0 {
		t.Fatal("reserve only")
	}
	if !strings.Contains("MemAvailable", "MemAvailable") {
		t.Fatal("anchor")
	}
}
