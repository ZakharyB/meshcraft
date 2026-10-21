// Package resources probes local device capabilities used by the
// scheduler and by host-election decisions. Values are measured with
// the standard library; anything not measurable reports -1/false
// rather than a fabricated number.
package resources

import (
	"runtime"
	"syscall"
)

// Snapshot is a point-in-time capability reading for one peer.
type Snapshot struct {
	PeerID     string `json:"peer_id"`
	NumCPU     int    `json:"num_cpu"`
	FreeRAMMB  int64  `json:"free_ram_mb"`  // -1 if unknown
	TotalRAMMB int64  `json:"total_ram_mb"` // -1 if unknown
	FreeDiskMB int64  `json:"free_disk_mb"` // -1 if unknown
	CanHost    bool   `json:"can_host"`
}

// Probe measures this device. diskPath is the filesystem to check.
func Probe(peerID, diskPath string) Snapshot {
	s := Snapshot{PeerID: peerID, NumCPU: runtime.NumCPU(), FreeRAMMB: -1, TotalRAMMB: -1, FreeDiskMB: -1}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	// MemStats reflects this process, not the machine; machine RAM is
	// only reported on platforms where we can read it honestly.
	if total, free, ok := sysMem(); ok {
		s.TotalRAMMB = int64(total / (1024 * 1024))
		s.FreeRAMMB = int64(free / (1024 * 1024))
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(diskPath, &st); err == nil {
		s.FreeDiskMB = int64(st.Bavail) * int64(st.Bsize) / (1024 * 1024)
	}
	// Hosting heuristic: at least 2GB free RAM (when measurable) and 1GB disk.
	s.CanHost = (s.FreeRAMMB < 0 || s.FreeRAMMB >= 2048) && (s.FreeDiskMB < 0 || s.FreeDiskMB >= 1024)
	return s
}

// Score ranks a snapshot for host election: more free RAM and CPU win;
// peers that cannot host score -1.
func Score(s Snapshot) int64 {
	if !s.CanHost {
		return -1
	}
	ram := s.FreeRAMMB
	if ram < 0 {
		ram = 2048
	}
	return ram + int64(s.NumCPU)*256
}
