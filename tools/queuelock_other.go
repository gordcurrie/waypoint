//go:build !unix

package tools

// lockQueueFile is a no-op where flock isn't available. The sync sidecar only runs
// in a Linux container, so a non-unix MCP server can't share its queue volume anyway.
func lockQueueFile(string) (unlock func(), err error) {
	return func() {}, nil
}
