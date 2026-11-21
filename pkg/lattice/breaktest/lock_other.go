//go:build !unix

package breaktest

// @module-feature breaktest

// processAlive cannot tell on this platform, so a lock is always taken as held:
// the user removes a stale lock by hand, as the error says.
func processAlive(pid int) bool { return true }
