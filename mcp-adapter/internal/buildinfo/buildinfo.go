package buildinfo

import (
	"runtime"
	"runtime/debug"
)

const (
	GatewayVersion     = "1.6.0"
	CoreAPIVersion     = 1
	BridgeAPIVersion   = 1
	ToolCatalogVersion = 4
	MCPProtocol        = "2026-07-28"
)

var (
	Commit    = ""
	BuildDate = ""
)

type Info struct {
	GatewayVersion     string `json:"gateway_version"`
	CoreAPIVersion     int    `json:"core_api_version"`
	BridgeAPIVersion   int    `json:"bridge_api_version"`
	ToolCatalogVersion int    `json:"tool_catalog_version"`
	MCPProtocol        string `json:"mcp_protocol"`
	GoVersion          string `json:"go_version"`
	Commit             string `json:"commit,omitempty"`
	BuildDate          string `json:"build_date,omitempty"`
	Modified           bool   `json:"modified,omitempty"`
	MCPSDKVersion      string `json:"mcp_sdk_version,omitempty"`
}

func Current() Info {
	info := Info{
		GatewayVersion:     GatewayVersion,
		CoreAPIVersion:     CoreAPIVersion,
		BridgeAPIVersion:   BridgeAPIVersion,
		ToolCatalogVersion: ToolCatalogVersion,
		MCPProtocol:        MCPProtocol,
		GoVersion:          runtime.Version(),
		Commit:             Commit,
		BuildDate:          BuildDate,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range bi.Deps {
			if dep.Path == "github.com/modelcontextprotocol/go-sdk" {
				info.MCPSDKVersion = dep.Version
				break
			}
		}
		for _, setting := range bi.Settings {
			switch setting.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = setting.Value
				}
			case "vcs.time":
				if info.BuildDate == "" {
					info.BuildDate = setting.Value
				}
			case "vcs.modified":
				info.Modified = setting.Value == "true"
			}
		}
	}
	return info
}
