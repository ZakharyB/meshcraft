
package resources

import "runtime"

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
	s := Snapshot{
		PeerID:     peerID,
		NumCPU:     runtime.NumCPU(),
		FreeRAMMB:  -1,
		TotalRAMMB: -1,
		FreeDiskMB: -1,
	}

	// Report machine RAM only when the platform can measure it.
	if total, free, ok := sysMem(); ok {
		s.TotalRAMMB = int64(total / (1024 * 1024))
		s.FreeRAMMB = int64(free / (1024 * 1024))
	}

	// Measure available disk space using the platform implementation.
	if freeMB, ok := diskFreeMB(diskPath); ok {
		s.FreeDiskMB = freeMB
	}

	// Hosting heuristic: at least 2GB free RAM and 1GB free disk,
	// whenever those values can be measured.
	s.CanHost = (s.FreeRAMMB < 0 || s.FreeRAMMB >= 2048) &&
		(s.FreeDiskMB < 0 || s.FreeDiskMB >= 1024)

	return s
}

// Score ranks a snapshot for host election.
// More free RAM and CPU increase the score.
// Peers that cannot host score -1.
func Score(s *Snapshot) int64 {
	if !s.CanHost {
		return -1
	}

	ram := s.FreeRAMMB
	if ram < 0 {
		ram = 2048
	}

	return ram + int64(s.NumCPU)*256
}
