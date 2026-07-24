package tools

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestRegisterAll_NoPanic ensures all tool registrations succeed without panicking.
// mcp.AddTool validates jsonschema tags at call time, so this catches malformed tags
// that the compiler and linter cannot see.
func TestRegisterAll_NoPanic(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: "test"}, nil)
	RegisterAll(s, &mockClient{}, t.TempDir())
}
