package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"mcp-gateway-adapter/internal/core"
	"mcp-gateway-adapter/internal/registry"
	"mcp-gateway-adapter/internal/remote"
)

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) > 1 {
		subcmd := os.Args[1]
		switch subcmd {
		case "status":
			os.Exit(cmdStatus(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor(os.Args[2:]))
		case "maintenance":
			os.Exit(cmdMaintenance(os.Args[2:]))
		case "backup":
			os.Exit(cmdBackup(os.Args[2:]))
		case "restore":
			os.Exit(cmdRestore(os.Args[2:]))
		case "repair":
			os.Exit(cmdRepair(os.Args[2:]))
		case "setup":
			os.Exit(cmdSetup(os.Args[2:]))
		case "benchmark":
			os.Exit(cmdBenchmark(os.Args[2:]))
		case "serve-admin":
			os.Exit(cmdServeAdmin(os.Args[2:]))
		case "serve-mcp":
			runMCPServer(os.Args[2:])
			return
		case "version":
			fmt.Println("mcp-gateway v1.4.0 (Core API v1, MCP Protocol 2026-07-28, Registry Schema v5)")
			return
		case "help", "-help", "--help":
			printHelp()
			return
		default:
			// If first arg starts with '-', run default MCP server with those flags (backwards compatibility)
			if strings.HasPrefix(subcmd, "-") {
				runMCPServer(os.Args[1:])
				return
			}
			fmt.Fprintf(os.Stderr, "Unknown command: %s\nRun 'mcp-gateway help' for usage.\n", subcmd)
			os.Exit(2)
		}
	}

	// No arguments: default to MCP server (stdio mode)
	runMCPServer(os.Args[1:])
}

func runMCPServer(args []string) {
	fs := flag.NewFlagSet("serve-mcp", flag.ContinueOnError)
	transportFlag := fs.String("transport", "stdio", "Transport mode: stdio or http")
	bindFlag := fs.String("bind", "127.0.0.1:8090", "Bind address for Streamable HTTP mode (default: 127.0.0.1:8090)")
	pythonFlag := fs.String("python", "", "Path to python3 binary (legacy, unused in Go-only)")
	pythonPathFlag := fs.String("pythonpath", "", "Path to Python source directory (legacy, unused in Go-only)")
	dbFlag := fs.String("db", "", "Path to SQLite database file")
	clientIDFlag := fs.String("client-id", "", "Authenticated AI client identifier")
	authTokenFlag := fs.String("auth-token", "", "Shared secret Bearer auth token for authenticating HTTP clients")
	authTokenFileFlag := fs.String("auth-token-file", "", "Path to file containing shared Bearer auth token")
	versionFlag := fs.Bool("version", false, "Print version information")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	bridgeConfig := DefaultBridgeConfig()
	if *pythonFlag != "" {
		bridgeConfig.PythonBin = *pythonFlag
	}
	if *pythonPathFlag != "" {
		bridgeConfig.PythonPath = *pythonPathFlag
	}
	if *dbFlag != "" {
		bridgeConfig.DBPath = *dbFlag
	}

	dbPath := resolveDBPath(bridgeConfig.DBPath)
	bridgeConfig.DBPath = dbPath

	if store, err := registry.OpenStore(context.Background(), dbPath); err == nil {
		sshTransport := remote.NewSSHTransport()
		coreInstance := core.NewWithRemote(store, core.Config{
			GatewayVersion:     "1.4.0",
			CoreAPIVersion:     1,
			ToolCatalogVersion: 4,
			MCPProtocol:        "2026-07-28",
			DBPath:             dbPath,
			BackupDir:          filepath.Join(filepath.Dir(dbPath), "backups"),
		}, sshTransport)
		bridgeConfig.Core = coreInstance
	}

	if *versionFlag {
		fmt.Println("mcp-gateway-adapter v1.4.0 (MCP Protocol 2026-07-28)")
		return
	}

	// Resolve shared auth token from file, flag, or environment
	var token string
	if *authTokenFlag != "" {
		token = *authTokenFlag
	} else if *authTokenFileFlag != "" {
		data, err := os.ReadFile(*authTokenFileFlag)
		if err != nil {
			log.Fatalf("Failed to read auth token file %s: %v", *authTokenFileFlag, err)
		}
		token = strings.TrimSpace(string(data))
	} else if envFile := os.Getenv("MCP_AUTH_TOKEN_FILE"); envFile != "" {
		data, err := os.ReadFile(envFile)
		if err != nil {
			log.Fatalf("Failed to read auth token file from MCP_AUTH_TOKEN_FILE %s: %v", envFile, err)
		}
		token = strings.TrimSpace(string(data))
	} else if envToken := os.Getenv("MCP_AUTH_TOKEN"); envToken != "" {
		token = strings.TrimSpace(envToken)
	}
	bridgeConfig.AuthToken = token

	if *clientIDFlag != "" {
		bridgeConfig.ClientID = *clientIDFlag
	} else if envClient := os.Getenv("MCP_CLIENT_ID"); envClient != "" {
		bridgeConfig.ClientID = envClient
	} else if bridgeConfig.AuthToken != "" {
		bridgeConfig.ClientID = "chatgpt-main"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	state := NewAdapterState()
	info, err := bridgeConfig.CheckBridgeCompatibility(ctx)
	if err != nil {
		log.Printf("[WARN] Bridge compatibility check failed: %v", err)
		state.SetReady(false, fmt.Sprintf("ADAPTER_NOT_READY: %v", err), info)
	} else {
		log.Printf("[INFO] Bridge compatibility verified (Core API v%d, Bridge API v%d, Gateway v%s)", info.CoreAPIVersion, info.BridgeAPIVersion, info.GatewayVersion)
		state.SetReady(true, "ready", info)
	}

	server := NewGatewayServer(bridgeConfig, state)

	switch *transportFlag {
	case "stdio":
		if err := RunStdio(ctx, server); err != nil {
			log.Fatalf("Stdio server terminated with error: %v", err)
		}
	case "http":
		if err := RunHTTP(ctx, server, *bindFlag, state, bridgeConfig); err != nil {
			log.Fatalf("HTTP server terminated with error: %v", err)
		}
	default:
		log.Fatalf("Unknown transport mode: %s. Supported: stdio, http", *transportFlag)
	}
}
