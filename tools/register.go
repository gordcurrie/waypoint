package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// RegisterAll registers every tool group with the MCP server.
func RegisterAll(s *mcp.Server, client influxClient, dataDir string) {
	registerActivityTools(s, client)
	registerHealthTools(s, client)
	registerTrainingTools(s, client)
	registerSplitTools(s, client)
	registerWorkoutTools(s, client, dataDir)
}
