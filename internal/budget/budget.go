package budget

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

const HostMemoryReserve = 2 << 30

var ReadMemAvailable = func() (uint64, error) {
	body, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	return ParseMemAvailable(body)
}

var NumCPU = runtime.NumCPU

func ParseMemAvailable(body []byte) (uint64, error) {
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("meminfo: %s", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("meminfo: %w", err)
		}
		return kb * 1024, nil
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("meminfo: MemAvailable missing")
}

func MemoryBudget(available uint64) uint64 {
	if available <= HostMemoryReserve {
		return 0
	}
	return available - HostMemoryReserve
}
