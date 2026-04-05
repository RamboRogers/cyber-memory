package mcp

import (
	"context"
	"io"
	"log/slog"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestListToolsTagsSchemaUsesStringItems(t *testing.T) {
	t.Parallel()

	ctx, client, _ := newInitializedClient(t, "v1.2.3")
	defer client.Close()

	list, err := client.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	toolsByName := make(map[string]mcpgo.Tool, len(list.Tools))
	for _, tool := range list.Tools {
		toolsByName[tool.Name] = tool
	}

	for _, name := range []string{"memory_store", "memory_recall", "memory_search", "memory_update"} {
		tool, ok := toolsByName[name]
		if !ok {
			t.Fatalf("tool %q missing from ListTools", name)
		}

		tagsSchema, ok := tool.InputSchema.Properties["tags"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q tags schema missing or wrong type: %#v", name, tool.InputSchema.Properties["tags"])
		}
		if got := tagsSchema["type"]; got != "array" {
			t.Fatalf("tool %q tags.type = %#v, want %q", name, got, "array")
		}

		itemsSchema, ok := tagsSchema["items"].(map[string]any)
		if !ok {
			t.Fatalf("tool %q tags.items missing or wrong type: %#v", name, tagsSchema["items"])
		}
		if got := itemsSchema["type"]; got != "string" {
			t.Fatalf("tool %q tags.items.type = %#v, want %q", name, got, "string")
		}
	}
}

func TestInitializeReportsConfiguredVersion(t *testing.T) {
	t.Parallel()

	_, client, result := newInitializedClient(t, "v9.9.9")
	defer client.Close()

	if got := result.ServerInfo.Version; got != "v9.9.9" {
		t.Fatalf("ServerInfo.Version = %q, want %q", got, "v9.9.9")
	}
}

func newInitializedClient(t *testing.T, serverVersion string) (context.Context, *mcpclient.Client, *mcpgo.InitializeResult) {
	t.Helper()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(nil, nil, logger, serverVersion)

	client, err := mcpclient.NewInProcessClient(srv.mcp)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}

	if err := client.Start(ctx); err != nil {
		client.Close()
		t.Fatalf("Start: %v", err)
	}

	initReq := mcpgo.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcpgo.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcpgo.Implementation{
		Name:    "cyber-memory-test",
		Version: "1.0.0",
	}

	result, err := client.Initialize(ctx, initReq)
	if err != nil {
		client.Close()
		t.Fatalf("Initialize: %v", err)
	}

	return ctx, client, result
}
