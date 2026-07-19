package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// RegisterAll registers every tool group with the MCP server.
func RegisterAll(s *mcp.Server, client influxClient) {
	registerActivityTools(s, client)
	registerHealthTools(s, client)
	registerTrainingTools(s, client)
}
