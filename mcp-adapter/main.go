package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"mcp-gateway-adapter/internal/policy"
)

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) > 1 {
		subcmd := os.Args[1]
		switch subcmd {
		case "status":
			os.Exit(cmdStatus(os.Args[2:]))
		case "migrate":
			os.Exit(cmdMigrate(os.Args[2:]))
		case "doctor":
			os.Exit(cmdDoctor(os.Args[2:]))
		case "maintenance":
			os.Exit(cmdMaintenance(os.Args[2:]))
		case "backup":
			os.Exit(cmdBackup(os.Args[2:]))
		case "restore":
			os.Exit(cmdRestore(os.Args[2:]))
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
			os.Exit(cmdVersion(os.Args[2:]))
		case "help", "-help", "--help":
			printHelp()
			return
		default:
			// Preserve the original flag-only stdio invocation for existing clients.
			if strings.HasPrefix(subcmd, "-") {
				runMCPServer(os.Args[1:])
				return
			}
			fmt.Fprintf(os.Stderr, "Unknown command: %s\nRun 'mcp-gateway help' for usage.\n", subcmd)
			os.Exit(2)
		}
	}

	// No arguments: default to MCP server (stdio mode).
	runMCPServer(nil)
}

func runMCPServer(args []string) {
	fs := flag.NewFlagSet("serve-mcp", flag.ContinueOnError)
	transportFlag := fs.String("transport", "stdio", "Transport mode: stdio or http")
	bindFlag := fs.String("bind", "127.0.0.1:8090", "Bind address for Streamable HTTP mode (default: 127.0.0.1:8090)")
	dbFlag := fs.String("db", "", "Path to SQLite database file")
	clientIDFlag := fs.String("client-id", "", "Authenticated AI client identifier")
	authTokenFlag := fs.String("auth-token", "", "Shared secret Bearer auth token for authenticating HTTP clients")
	authTokenFileFlag := fs.String("auth-token-file", "", "Path to file containing shared Bearer auth token")
	allowedHostsFlag := fs.String("allowed-hosts", "", "Comma-separated additional Host headers allowed by the HTTP transport")
	versionFlag := fs.Bool("version", false, "Print version information")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *versionFlag {
		_ = cmdVersion(nil)
		return
	}

	bridgeConfig := DefaultBridgeConfig()
	dbPath := resolveDBPath(*dbFlag)
	bridgeConfig.DBPath = dbPath

	_, coreInstance, coreErr := initStoreAndCore(dbPath)
	if coreErr != nil {
		log.Printf("[ERROR] Gateway Core initialization failed: %v", coreErr)
	} else {
		bridgeConfig.Core = coreInstance
	}

	// Resolve shared auth token from file, flag, or environment.
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

	for _, host := range strings.Split(*allowedHostsFlag, ",") {
		host = strings.TrimSpace(host)
		if host != "" {
			bridgeConfig.AllowedHosts = append(bridgeConfig.AllowedHosts, host)
		}
	}

	if *clientIDFlag != "" {
		bridgeConfig.ClientID = strings.TrimSpace(*clientIDFlag)
	} else if envClient := os.Getenv("MCP_CLIENT_ID"); envClient != "" {
		bridgeConfig.ClientID = strings.TrimSpace(envClient)
	} else if bridgeConfig.AuthToken != "" {
		bridgeConfig.ClientID = "chatgpt-main"
	}
	if policy.IsReservedInternalClientID(bridgeConfig.ClientID) {
		log.Fatalf("Client ID %q is reserved for internal gateway principals", bridgeConfig.ClientID)
	}

	if coreErr != nil && *transportFlag == "stdio" {
		log.Fatalf("Gateway Core is required for stdio transport: %v", coreErr)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	state := NewAdapterState()
	info, err := bridgeConfig.CheckBridgeCompatibility(ctx)
	if err != nil {
		log.Printf("[WARN] Go Core compatibility/readiness check failed: %v", err)
		state.SetReady(false, fmt.Sprintf("ADAPTER_NOT_READY: %v", err), info)
	} else {
		log.Printf("[INFO] Go Core contract verified (Core API v%d, Bridge API v%d, Gateway v%s)", info.CoreAPIVersion, info.BridgeAPIVersion, info.GatewayVersion)
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
