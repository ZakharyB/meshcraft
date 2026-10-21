package resources

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// sysMem reads /proc/meminfo on Linux.
func sysMem() (total, free uint64, ok bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	var gotT, gotA bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, gotT = v*1024, true
		case "MemAvailable:":
			free, gotA = v*1024, true
		}
	}
	return total, free, gotT && gotA
}
