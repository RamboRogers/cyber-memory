# cyber-memory

A zero-config, single-binary STDIO MCP server for agent memory.

Uses a CPU embedding model (nomic-embed-text via ONNX) to generate 768-dim vectors, stored in a pure-Go SQLite database with cosine similarity search done in-process. No user configuration required — it just works on first run.

---

# Author

- Matthew Rogers, @ramborogers on GitHub
- https://github.com/ramborogers/cyber-memory

## Goals

- Drop-in MCP memory layer for any agent (Claude, GPT, custom)
- No CGO, no external services, no config files required
- Single binary — copy it somewhere and add it to your MCP config
- Fast semantic recall with temporal decay so stale memories don't crowd out fresh ones
- Knowledge graph edges so related concepts can be traversed, not just matched

---

## Architecture

```
Agent (MCP client)
    │  STDIO (JSON-RPC 2.0)
    ▼
cyber-memory (single binary)
    ├── MCP handler        — tool dispatch
    ├── Embed engine       — ONNX model loaded at startup (embedded in binary via go:embed)
    ├── SQLite store       — modernc.org/sqlite (pure Go, no CGO)
    │    ├── memories      — content + embeddings + temporal fields
    │    ├── tags          — many-to-many memory labels
    │    └── relations     — directed graph edges between memories
    └── Scorer             — cosine_sim × recency × importance × access_boost
```

**DB location** (resolved in order, no prompts):
1. `$CYBER_MEMORY_DB` env var
2. `$XDG_DATA_HOME/cyber-memory/db.sqlite3`
3. `~/.local/share/cyber-memory/db.sqlite3`

The directory is created automatically on first run. The model is embedded in the binary at compile time — no download step.

---

## Data Model

### memories

```sql
CREATE TABLE memories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    content      TEXT    NOT NULL,
    summary      TEXT,                              -- optional one-liner, auto-generated if blank
    embedding    BLOB    NOT NULL,                  -- float32[] LE bytes, dim=768
    kind         TEXT    NOT NULL DEFAULT 'episodic',
                                                   -- 'episodic' | 'semantic' | 'procedural'
    source       TEXT,                             -- e.g. 'user', 'tool:bash', 'agent:claude'
    importance   REAL    NOT NULL DEFAULT 1.0,     -- 0.0–5.0, set by caller or inferred
    access_count INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    accessed_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX idx_memories_kind       ON memories(kind);
CREATE INDEX idx_memories_created_at ON memories(created_at DESC);
CREATE INDEX idx_memories_accessed_at ON memories(accessed_at DESC);

-- Full-text search (no extra deps — sqlite FTS5 is compiled in)
CREATE VIRTUAL TABLE memories_fts USING fts5(
    content,
    summary,
    content='memories',
    content_rowid='id'
);
```

### tags

```sql
CREATE TABLE tags (
    memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    tag       TEXT    NOT NULL,
    PRIMARY KEY (memory_id, tag)
);

CREATE INDEX idx_tags_tag ON tags(tag);
```

### relations (knowledge graph edges)

```sql
CREATE TABLE relations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    src_id     INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    dst_id     INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL,    -- 'supports' | 'contradicts' | 'precedes' | 'relates_to'
    weight     REAL    NOT NULL DEFAULT 1.0,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX idx_relations_src ON relations(src_id);
CREATE INDEX idx_relations_dst ON relations(dst_id);
```

---

## Scoring

Every recalled memory gets a composite score that blends semantic similarity with temporal and behavioral signals:

```
score = cosine_sim(query_vec, memory_vec)
      × recency(created_at)
      × importance
      × access_boost(access_count)

recency(t)       = exp(-λ × days_since(t))      λ=0.01 by default (~100-day half-life)
access_boost(n)  = 1 + log1p(n) × 0.1           mild bump for frequently accessed memories
```

Cosine similarity is computed in Go over the full candidate set (or a recency-filtered subset for large DBs). No approximate index needed until >500k memories.

---

## MCP Tools

All tools return structured JSON. No tool requires configuration or asks the user anything.

### `memory_store`
Store a new memory. Embedding is generated server-side.

```json
{
  "content":    "string (required)",
  "tags":       ["string"],
  "kind":       "episodic|semantic|procedural (default: episodic)",
  "source":     "string (default: 'agent')",
  "importance": 1.0
}
```
Returns: `{ "id": 42 }`

### `memory_recall`
Semantic + temporal search. Returns the top-k scored memories.

```json
{
  "query":     "string (required)",
  "limit":     10,
  "kind":      "episodic|semantic|procedural|any (default: any)",
  "tags":      ["string"],
  "min_score": 0.0
}
```
Returns: `[{ "id", "content", "summary", "score", "tags", "kind", "created_at", "accessed_at" }]`

Accessing a memory auto-increments `access_count` and updates `accessed_at`.

### `memory_search`
Full-text keyword search (FTS5), ranked by BM25 + recency. Faster than vector search, useful for exact term lookups.

```json
{
  "query": "string (required)",
  "limit": 10,
  "tags":  ["string"]
}
```
Returns: same shape as `memory_recall`.

### `memory_relate`
Create a directed edge between two memories.

```json
{
  "src_id":   42,
  "dst_id":   17,
  "kind":     "supports|contradicts|precedes|relates_to",
  "weight":   1.0
}
```
Returns: `{ "relation_id": 5 }`

### `memory_graph`
Traverse the knowledge graph from a root memory via recursive CTE.

```json
{
  "id":    42,
  "depth": 2
}
```
Returns: `{ "nodes": [...], "edges": [...] }`

### `memory_update`
Replace content (re-embeds automatically) or update metadata.

```json
{
  "id":         42,
  "content":    "updated text",
  "importance": 2.0,
  "tags":       ["new-tag"]
}
```

### `memory_forget`
Hard-delete a memory and its edges.

```json
{ "id": 42 }
```

### `memory_stats`
Returns counts, DB size, oldest/newest memory timestamps. Useful for agent self-awareness.

```json
{}
```

---

## CLI Flags

The binary also functions as a CLI for maintenance. When no flags are given it starts as an MCP STDIO server.

```
cyber-memory [flags]

  --db PATH          Override DB path (also: $CYBER_MEMORY_DB)
  --list [N]         Print the N most recent memories (default 20)
  --search QUERY     Full-text search from the terminal
  --purge-days N     Delete memories older than N days with access_count=0
  --wipe             Drop and recreate the database (requires --confirm)
  --confirm          Required for destructive operations
  --stats            Print DB statistics and exit
  --version          Print version and exit
```

---

## Embedding Model

- **Model**: `google/embeddinggemma-300m` — ONNX build at `onnx-community/embeddinggemma-300m-ONNX`
- **Dimensions**: 768 (default); supports Matryoshka reduction to 512 / 256 / 128 for storage tradeoffs
- **Max tokens**: 2,048 (SentencePiece tokenizer, same as Gemma 3)
- **Precision**: float32 only — activations do NOT support float16
- **Runtime**: `github.com/yalue/onnxruntime_go` (ONNX Runtime Go bindings)
- **Batching**: batches of 32 at write time; single string embed at recall time

**Preprocessing pipeline** (must match training exactly):
1. Tokenize with SentencePiece, prepend `[BOS]`, append `[EOS]`
2. Pad batch to longest sequence
3. Run ONNX inference → per-token vectors
4. **Mean pooling** over all token positions
5. **L2 normalize** to unit length

Optional task prefix (improves retrieval quality):
```
task: search result | query: {text}
```

> **Portability note**: ONNX Runtime ships a platform-native `.so`/`.dylib`/`.dll`. The binary embeds the correct one for the target platform via `go:embed` and extracts it to a temp dir at startup. The model weights file (~300 MB) is downloaded once on first run to `$DB_DIR/model/` — no user action needed beyond having internet access the first time. Build matrix: `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`.

---

## Dependencies (Go modules)

| Package | Purpose |
|---|---|
| `modernc.org/sqlite` | Pure Go SQLite (no CGO) |
| `github.com/mark3labs/mcp-go` | MCP STDIO server framework |
| `github.com/yalue/onnxruntime_go` | ONNX Runtime Go bindings |
| `github.com/nicholasgasior/gsp` or equivalent | SentencePiece tokenizer (must match Gemma 3 vocab) |

> SentencePiece is the one hard dependency with a C core. If strict no-CGO is required, a pure Go SentencePiece port can be used but must load the same `tokenizer.model` file shipped with the ONNX weights to guarantee identical tokenization.

---

## Non-Goals

- No REST/HTTP server — STDIO only
- No user auth or multi-tenancy
- No cloud sync
- No automatic memory consolidation or summarization (the agent decides what to store)
- No configuration wizard, interactive prompts, or setup steps


