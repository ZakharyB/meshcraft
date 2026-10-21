//go:build !linux

package resources

// sysMem is unimplemented off Linux: report unknown rather than guess.
func sysMem() (total, free uint64, ok bool) { return 0, 0, false }
