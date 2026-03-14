// Package schemas embeds JSON Schema files for pack validation.
package schemas

import _ "embed"

//go:embed pack.schema.json
var PackSchemaJSON []byte

//go:embed mcp-server.schema.json
var MCPServerSchemaJSON []byte
