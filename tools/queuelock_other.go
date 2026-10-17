//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package tools

// lockQueueFile is a no-op where syscall.Flock isn't available (Windows, and unix-likes
// such as solaris/illumos/aix whose syscall package lacks it). The sync sidecar only runs
// in a Linux container, so a non-unix MCP server can't share its queue volume anyway.
func lockQueueFile(string) (unlock func(), err error) {
	return func() {}, nil
}
