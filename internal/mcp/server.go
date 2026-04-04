// Package mcp wires up the MCP STDIO server and registers all memory tools.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ramborogers/cyber-memory/internal/scorer"
	"github.com/ramborogers/cyber-memory/internal/store"
)

// Embedder is the interface the MCP server needs from the embedding engine.
type Embedder interface {
	Embed(text string) ([]float32, error)
	EmbedDocument(text string) ([]float32, error)
}

// Server is the MCP server.
type Server struct {
	mcp   *server.MCPServer
	st    *store.Store
	emb   Embedder
	log   *slog.Logger
	stdio *server.StdioServer
}

// New creates and configures the MCP server with all tools registered.
func New(st *store.Store, emb Embedder, log *slog.Logger) *Server {
	s := &Server{
		mcp: server.NewMCPServer(
			"cyber-memory",
			"1.0.0",
			server.WithToolCapabilities(true),
		),
		st:  st,
		emb: emb,
		log: log,
	}
	s.registerTools()
	s.stdio = server.NewStdioServer(s.mcp)
	return s
}

// Serve blocks and serves MCP over STDIO until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	return s.stdio.Listen(ctx, os.Stdin, os.Stdout)
}

// ---- tool registration ----

func (s *Server) registerTools() {
	s.mcp.AddTool(mcpgo.NewTool("memory_store",
		mcpgo.WithDescription("Store a new memory. Embedding is generated server-side."),
		mcpgo.WithString("content", mcpgo.Required(), mcpgo.Description("The text content to remember.")),
		mcpgo.WithString("summary", mcpgo.Description("Optional one-line summary.")),
		mcpgo.WithString("kind", mcpgo.Description("episodic | semantic | procedural (default: episodic)")),
		mcpgo.WithString("source", mcpgo.Description("Origin label, e.g. 'user', 'tool:bash' (default: agent)")),
		mcpgo.WithNumber("importance", mcpgo.Description("0.0–5.0 weight (default: 1.0)")),
		mcpgo.WithArray("tags", mcpgo.Description("Optional string labels."), mcpgo.WithStringItems()),
	), s.handleStore)

	s.mcp.AddTool(mcpgo.NewTool("memory_recall",
		mcpgo.WithDescription("Semantic + temporal search. Returns top-k scored memories."),
		mcpgo.WithString("query", mcpgo.Required(), mcpgo.Description("Natural language query.")),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default: 10)")),
		mcpgo.WithString("kind", mcpgo.Description("episodic | semantic | procedural | any (default: any)")),
		mcpgo.WithArray("tags", mcpgo.Description("Filter to memories with at least one of these tags."), mcpgo.WithStringItems()),
		mcpgo.WithNumber("min_score", mcpgo.Description("Minimum composite score threshold (default: 0)")),
	), s.handleRecall)

	s.mcp.AddTool(mcpgo.NewTool("memory_search",
		mcpgo.WithDescription("Full-text keyword search (FTS5). Faster than vector recall, useful for exact terms."),
		mcpgo.WithString("query", mcpgo.Required(), mcpgo.Description("FTS5 match expression.")),
		mcpgo.WithNumber("limit", mcpgo.Description("Max results (default: 10)")),
		mcpgo.WithArray("tags", mcpgo.Description("Filter to memories with at least one of these tags."), mcpgo.WithStringItems()),
	), s.handleSearch)

	s.mcp.AddTool(mcpgo.NewTool("memory_relate",
		mcpgo.WithDescription("Create a directed edge between two memories in the knowledge graph."),
		mcpgo.WithNumber("src_id", mcpgo.Required(), mcpgo.Description("Source memory ID.")),
		mcpgo.WithNumber("dst_id", mcpgo.Required(), mcpgo.Description("Destination memory ID.")),
		mcpgo.WithString("kind", mcpgo.Description("supports | contradicts | precedes | relates_to (default: relates_to)")),
		mcpgo.WithNumber("weight", mcpgo.Description("Edge weight 0.0–1.0 (default: 1.0)")),
	), s.handleRelate)

	s.mcp.AddTool(mcpgo.NewTool("memory_graph",
		mcpgo.WithDescription("Traverse the knowledge graph from a root memory."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Root memory ID.")),
		mcpgo.WithNumber("depth", mcpgo.Description("Max hops (default: 2)")),
	), s.handleGraph)

	s.mcp.AddTool(mcpgo.NewTool("memory_update",
		mcpgo.WithDescription("Update an existing memory. Content triggers re-embedding."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Memory ID.")),
		mcpgo.WithString("content", mcpgo.Description("Replacement content (triggers re-embed).")),
		mcpgo.WithString("summary", mcpgo.Description("Replacement summary.")),
		mcpgo.WithNumber("importance", mcpgo.Description("New importance value.")),
		mcpgo.WithArray("tags", mcpgo.Description("Replaces all existing tags."), mcpgo.WithStringItems()),
	), s.handleUpdate)

	s.mcp.AddTool(mcpgo.NewTool("memory_forget",
		mcpgo.WithDescription("Hard-delete a memory and all its graph edges."),
		mcpgo.WithNumber("id", mcpgo.Required(), mcpgo.Description("Memory ID to delete.")),
	), s.handleForget)

	s.mcp.AddTool(mcpgo.NewTool("memory_stats",
		mcpgo.WithDescription("Return aggregate statistics about the memory store."),
	), s.handleStats)
}

// ---- handlers ----

func (s *Server) handleStore(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	content := req.GetString("content", "")
	if content == "" {
		return errResult("content is required"), nil
	}
	summary := req.GetString("summary", "")
	kind := req.GetString("kind", "episodic")
	src := req.GetString("source", "agent")
	importance := req.GetFloat("importance", 1.0)
	tags := stringSlice(req.GetArguments()["tags"])

	emb, err := s.emb.EmbedDocument(content)
	if err != nil {
		return errResult(fmt.Sprintf("embed: %v", err)), nil
	}

	id, err := s.st.Insert(store.StoreMemoryInput{
		Content:    content,
		Summary:    summary,
		Embedding:  emb,
		Kind:       kind,
		Source:     src,
		Importance: importance,
		Tags:       tags,
	})
	if err != nil {
		return errResult(fmt.Sprintf("store: %v", err)), nil
	}
	return jsonResult(map[string]any{"id": id}), nil
}

func (s *Server) handleRecall(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	query := req.GetString("query", "")
	if query == "" {
		return errResult("query is required"), nil
	}
	limit := req.GetInt("limit", 10)
	kind := req.GetString("kind", "any")
	tags := stringSlice(req.GetArguments()["tags"])
	minScore := req.GetFloat("min_score", 0)

	queryVec, err := s.emb.Embed(query)
	if err != nil {
		return errResult(fmt.Sprintf("embed query: %v", err)), nil
	}

	candidates, err := s.st.AllWithEmbeddings(kind)
	if err != nil {
		return errResult(fmt.Sprintf("load candidates: %v", err)), nil
	}

	results := scorer.Rank(candidates, queryVec, limit, minScore)

	// Touch access counts.
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.Memory.ID
	}
	_ = s.st.TouchAccess(ids)

	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		r.Memory.Tags, _ = s.st.TagsFor(r.Memory.ID)
		// Apply tag filter after scoring.
		if len(tags) > 0 && !hasAnyTag(r.Memory.Tags, tags) {
			continue
		}
		out = append(out, memoryToMap(r.Memory, r.Score))
	}
	return jsonResult(out), nil
}

func (s *Server) handleSearch(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	query := req.GetString("query", "")
	if query == "" {
		return errResult("query is required"), nil
	}
	limit := req.GetInt("limit", 10)
	tags := stringSlice(req.GetArguments()["tags"])

	mems, err := s.st.FTSSearch(query, tags, limit)
	if err != nil {
		return errResult(fmt.Sprintf("fts search: %v", err)), nil
	}
	out := make([]map[string]any, len(mems))
	for i, m := range mems {
		out[i] = memoryToMap(m, 0)
	}
	return jsonResult(out), nil
}

func (s *Server) handleRelate(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	srcID := int64(req.GetFloat("src_id", 0))
	dstID := int64(req.GetFloat("dst_id", 0))
	if srcID == 0 || dstID == 0 {
		return errResult("src_id and dst_id are required"), nil
	}
	kind := req.GetString("kind", "relates_to")
	weight := req.GetFloat("weight", 1.0)

	relID, err := s.st.Relate(srcID, dstID, kind, weight)
	if err != nil {
		return errResult(fmt.Sprintf("relate: %v", err)), nil
	}
	return jsonResult(map[string]any{"relation_id": relID}), nil
}

func (s *Server) handleGraph(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	id := int64(req.GetFloat("id", 0))
	if id == 0 {
		return errResult("id is required"), nil
	}
	depth := req.GetInt("depth", 2)

	nodes, rels, err := s.st.Graph(id, depth)
	if err != nil {
		return errResult(fmt.Sprintf("graph: %v", err)), nil
	}
	nodeOut := make([]map[string]any, len(nodes))
	for i, n := range nodes {
		n.Tags, _ = s.st.TagsFor(n.ID)
		nodeOut[i] = memoryToMap(n, 0)
	}
	edgeOut := make([]map[string]any, len(rels))
	for i, r := range rels {
		edgeOut[i] = map[string]any{
			"id":     r.ID,
			"src_id": r.SrcID,
			"dst_id": r.DstID,
			"kind":   r.Kind,
			"weight": r.Weight,
		}
	}
	return jsonResult(map[string]any{"nodes": nodeOut, "edges": edgeOut}), nil
}

func (s *Server) handleUpdate(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	id := int64(req.GetFloat("id", 0))
	if id == 0 {
		return errResult("id is required"), nil
	}

	in := store.UpdateMemoryInput{ID: id}

	if c := req.GetString("content", ""); c != "" {
		emb, err := s.emb.EmbedDocument(c)
		if err != nil {
			return errResult(fmt.Sprintf("embed: %v", err)), nil
		}
		in.Content = &c
		in.Embedding = emb
		if sum := req.GetString("summary", ""); sum != "" {
			in.Summary = &sum
		}
	} else if sum := req.GetString("summary", ""); sum != "" {
		in.Summary = &sum
	}

	args := req.GetArguments()
	if _, ok := args["importance"]; ok {
		imp := req.GetFloat("importance", 1.0)
		in.Importance = &imp
	}
	if _, ok := args["tags"]; ok {
		tags := stringSlice(args["tags"])
		in.Tags = tags
	}

	if err := s.st.Update(in); err != nil {
		return errResult(fmt.Sprintf("update: %v", err)), nil
	}
	return jsonResult(map[string]any{"ok": true}), nil
}

func (s *Server) handleForget(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	id := int64(req.GetFloat("id", 0))
	if id == 0 {
		return errResult("id is required"), nil
	}
	if err := s.st.Delete(id); err != nil {
		return errResult(fmt.Sprintf("delete: %v", err)), nil
	}
	return jsonResult(map[string]any{"ok": true}), nil
}

func (s *Server) handleStats(_ context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	stats, err := s.st.Stats()
	if err != nil {
		return errResult(fmt.Sprintf("stats: %v", err)), nil
	}
	return jsonResult(stats), nil
}

// ---- helpers ----

func jsonResult(v any) *mcpgo.CallToolResult {
	b, _ := json.Marshal(v)
	return mcpgo.NewToolResultText(string(b))
}

func errResult(msg string) *mcpgo.CallToolResult {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{mcpgo.NewTextContent(string(b))},
		IsError: true,
	}
}

func stringSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasAnyTag(memTags []string, filter []string) bool {
	set := make(map[string]struct{}, len(filter))
	for _, t := range filter {
		set[t] = struct{}{}
	}
	for _, t := range memTags {
		if _, ok := set[t]; ok {
			return true
		}
	}
	return false
}

func memoryToMap(m *store.Memory, score float64) map[string]any {
	return map[string]any{
		"id":           m.ID,
		"content":      m.Content,
		"summary":      m.Summary,
		"kind":         m.Kind,
		"source":       m.Source,
		"importance":   m.Importance,
		"access_count": m.AccessCount,
		"tags":         m.Tags,
		"score":        score,
		"created_at":   m.CreatedAt.Format("2006-01-02T15:04:05Z"),
		"accessed_at":  m.AccessedAt.Format("2006-01-02T15:04:05Z"),
	}
}
