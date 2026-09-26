package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const frozenToolCatalogHash = "41e2a308522d6abd42c645f95a64f358ac3f99d1dca5fd314ba19e05ebde54e2"

type frozenToolContract struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	InputSchema any                  `json:"input_schema"`
	Annotations *mcp.ToolAnnotations `json:"annotations,omitempty"`
}

type frozenPythonCatalog struct {
	OK                 bool     `json:"ok"`
	Tools              []string `json:"tools"`
	ToolCount          int      `json:"tool_count"`
	CatalogHash        string   `json:"catalog_hash"`
	ToolCatalogVersion int      `json:"tool_catalog_version"`
}

func listFrozenToolContracts(t *testing.T) []frozenToolContract {
	t.Helper()

	ctx := context.Background()
	state := NewAdapterState()
	state.SetReady(true, "contract-freeze", nil)
	server := NewGatewayServer(nil, state)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "contract-freeze", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect contract client: %v", err)
	}
	defer session.Close()

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools for contract freeze: %v", err)
	}

	out := make([]frozenToolContract, 0, len(list.Tools))
	for _, tool := range list.Tools {
		out = append(out, frozenToolContract{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			Annotations: tool.Annotations,
		})
	}
	return out
}

func TestFrozenPythonCatalogMatchesGoAdapter(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "contracts", "python_catalog.json"))
	if err != nil {
		t.Fatal(err)
	}

	var oracle frozenPythonCatalog
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatalf("decode Python catalog fixture: %v", err)
	}
	if !oracle.OK {
		t.Fatal("Python catalog fixture is not an OK response")
	}
	if oracle.ToolCount != 21 || len(oracle.Tools) != 21 {
		t.Fatalf("Python catalog count=%d tools=%d want 21", oracle.ToolCount, len(oracle.Tools))
	}
	if oracle.ToolCatalogVersion != 4 {
		t.Fatalf("Python tool_catalog_version=%d want frozen version 4", oracle.ToolCatalogVersion)
	}
	if oracle.CatalogHash != frozenToolCatalogHash {
		t.Fatalf("Python catalog hash=%q want=%q", oracle.CatalogHash, frozenToolCatalogHash)
	}
	if !reflect.DeepEqual(oracle.Tools, allKnownTools) {
		t.Fatalf("Go/Python tool catalog mismatch\nGo:     %v\nPython: %v", allKnownTools, oracle.Tools)
	}

	encoded, err := json.Marshal(allKnownTools)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	if got := hex.EncodeToString(sum[:]); got != frozenToolCatalogHash {
		t.Fatalf("Go catalog hash=%s want=%s", got, frozenToolCatalogHash)
	}
}

func TestToolContractsGolden(t *testing.T) {
	got := listFrozenToolContracts(t)
	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal tool contracts: %v", err)
	}
	encoded = append(encoded, '\n')

	path := filepath.Join("testdata", "contracts", "tool_contracts.json")
	if os.Getenv("UPDATE_CONTRACTS") == "1" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("update tool contracts: %v", err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read frozen tool contracts: %v", err)
	}
	if gotText := string(encoded); gotText != string(want) {
		t.Fatalf("tool contracts changed; inspect the semantic change before explicitly regenerating %s", path)
	}
}
