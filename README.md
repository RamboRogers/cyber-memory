<img src="https://raw.githubusercontent.com/RamboRogers/cyber-memory/master/media/banner.jpeg" alt="cyber-memory" width="100%">

<div align="center">

# cyber-memory

**Persistent, intelligent memory for AI agents — in a single binary.**

[![GitHub release](https://img.shields.io/github/v/release/RamboRogers/cyber-memory?style=flat-square&color=00ff41)](https://github.com/RamboRogers/cyber-memory/releases/latest)
[![License](https://img.shields.io/github/license/RamboRogers/cyber-memory?style=flat-square&color=00ff41)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square)](https://golang.org)
[![MCP](https://img.shields.io/badge/MCP-STDIO-00ff41?style=flat-square)](https://modelcontextprotocol.io)
[![Platform](https://img.shields.io/badge/platform-darwin%20arm64%20%7C%20linux%20amd64-00ff41?style=flat-square)](#installation)

*Drop-in MCP memory server. No configuration. No dependencies. No cloud. Just copy and run.*

</div>

---

## What it does

cyber-memory gives any AI agent — Claude, GPT, a custom LLM — **persistent, searchable, graph-connected memory** stored entirely on your machine.

It speaks [Model Context Protocol](https://modelcontextprotocol.io) over STDIO. Add it to your MCP config and your agent gains 8 memory tools immediately. Everything — the embedding model, the vector store, the graph engine, the ORT runtime — ships inside one binary.

```
Your Agent ──STDIO/JSON-RPC──► cyber-memory ──► SQLite DB
                                   │
                  EmbeddingGemma-300m (embedded)
                  Vector search (cosine + temporal decay)
                  Knowledge graph (recursive CTE)
                  Full-text search (FTS5)
```

---

## Installation

### One-liner (macOS Apple Silicon / Linux x86-64)

```sh
curl -fsSL https://raw.githubusercontent.com/RamboRogers/cyber-memory/master/install.sh | sh
```

The installer detects your OS and architecture, downloads the correct binary to `/usr/local/bin`, and prints your MCP config snippet.

### Supported platforms

| Platform | Architecture | Binary |
|---|---|---|
| macOS | Apple Silicon (arm64) | [cyber-memory-darwin-arm64](https://github.com/RamboRogers/cyber-memory/releases/latest/download/cyber-memory-darwin-arm64) |
| Linux | x86-64 | [cyber-memory-linux-amd64](https://github.com/RamboRogers/cyber-memory/releases/latest/download/cyber-memory-linux-amd64) |

> **macOS Intel, Linux arm64, and Windows** are not yet supported. The embedding dependency (`ortgenai`) has POSIX-only C headers that block Windows cross-compilation, Intel Mac ORT builds were dropped by Microsoft after v1.23.2, and Linux arm64 cross-compilation needs further work. PRs welcome.

```sh
# macOS Apple Silicon
curl -fsSL https://github.com/RamboRogers/cyber-memory/releases/latest/download/cyber-memory-darwin-arm64 -o cyber-memory
chmod +x cyber-memory && sudo mv cyber-memory /usr/local/bin/

# Linux x86-64
curl -fsSL https://github.com/RamboRogers/cyber-memory/releases/latest/download/cyber-memory-linux-amd64 -o cyber-memory
chmod +x cyber-memory && sudo mv cyber-memory /usr/local/bin/
```

---

## MCP Configuration

Add to your `claude_desktop_config.json` (or equivalent MCP host config):

```json
{
  "mcpServers": {
    "memory": {
      "command": "cyber-memory"
    }
  }
}
```

That's it. No API keys. No ports. No environment variables required.

On first use the agent will trigger a ~300 MB model download (EmbeddingGemma-300m) to `~/.local/share/cyber-memory/`. Every subsequent start is instant.

---

## Why cyber-memory is different

Most agent memory systems give you one thing: vector similarity search. cyber-memory gives you three, fused into a single ranked result.

### 1. Vector search — meaning, not keywords

Every memory is embedded with **Google's EmbeddingGemma-300m**, the highest-ranked sub-500M multilingual embedding model on MTEB. When your agent asks *"what does the user prefer about code style?"*, the right memory surfaces even if it says *"terse, no comments, idiomatic Go"* — because semantic meaning matches, not string overlap.

768-dimensional vectors. 2048-token context. Float32 precision on CPU. No GPU required.

### 2. Knowledge graph — connections, not just documents

Memories can be explicitly linked with typed, weighted edges:

```
"user prefers dark mode"  ──supports──►  "UI settings were changed"
"deploy failed last week" ──precedes──►  "hotfix was applied"
"old auth approach"       ──contradicts──► "new OAuth flow"
```

Recall a root memory, then traverse the graph — `memory_graph` returns the full connected subgraph up to N hops via recursive SQL CTEs. No graph database needed. No separate service. Just SQLite.

### 3. The scoring algorithm — relevance that ages gracefully

Raw cosine similarity treats a match from 3 years ago the same as one from yesterday. cyber-memory doesn't.

Every recalled memory is scored by:

```
score = cosine_sim × recency(t) × importance × access_boost(n)

recency(t)      = exp(−0.01 × days_since_created)   ← ~100-day half-life
access_boost(n) = 1 + log₁(n) × 0.1                ← mild bump for frequently-used memories
```

**What this means in practice:**
- A highly relevant but stale memory scores lower than a slightly less relevant recent one
- Memories your agent returns to repeatedly bubble up naturally
- High-importance memories (flagged by the agent) resist decay
- Fresh memories don't need to "earn" their rank — recency is a first-class signal

No tuning required. The defaults work.

### 4. Full-text search — speed when you need exact terms

For cases where the agent knows the exact phrase — a function name, an error code, a username — `memory_search` runs FTS5 BM25 search directly in SQLite. Sub-millisecond. No embedding call.

---

## The 8 memory tools

| Tool | What it does |
|---|---|
| `memory_store` | Store content. Embedding generated server-side. |
| `memory_recall` | Semantic + temporal ranked search. Returns scored results. |
| `memory_search` | FTS5 full-text keyword search. |
| `memory_relate` | Create a typed graph edge between two memories. |
| `memory_graph` | Traverse the knowledge graph from a root node. |
| `memory_update` | Update content (auto re-embeds), tags, or importance. |
| `memory_forget` | Hard-delete a memory and all its graph edges. |
| `memory_stats` | Agent self-awareness: total memories, oldest/newest timestamps. |

### memory_store

```json
{
  "content":    "The user prefers concise Go with no redundant comments",
  "kind":       "semantic",
  "importance": 2.0,
  "tags":       ["preferences", "go", "style"],
  "source":     "user"
}
```

`kind` options: `episodic` (events), `semantic` (facts), `procedural` (how-to)

### memory_recall

```json
{
  "query":     "what are the user's coding preferences?",
  "limit":     5,
  "kind":      "semantic",
  "min_score": 0.3
}
```

Returns memories ranked by `cosine × recency × importance × access_boost`.

### memory_relate

```json
{
  "src_id": 42,
  "dst_id": 17,
  "kind":   "supports",
  "weight": 1.0
}
```

`kind` options: `supports`, `contradicts`, `precedes`, `relates_to`

### memory_graph

```json
{ "id": 42, "depth": 2 }
```

Returns `{ "nodes": [...], "edges": [...] }` — the full connected subgraph within 2 hops.

---

## CLI usage

The same binary doubles as a maintenance tool:

```sh
# Show what's stored
cyber-memory --list 20
cyber-memory --search "OAuth"
cyber-memory --stats

# Maintenance
cyber-memory --purge-days 90         # delete unaccessed memories older than 90 days
cyber-memory --wipe --confirm         # drop everything

# Override database location
cyber-memory --db /path/to/memory.db
# or
CYBER_MEMORY_DB=/path/to/memory.db cyber-memory
```

---

## Data storage

Everything lives in one SQLite file:

```
~/.local/share/cyber-memory/
├── db.sqlite3               ← all memories, tags, graph edges, embeddings
├── libonnxruntime.dylib     ← extracted from binary on first run
└── models/
    └── onnx-community_embeddinggemma-300m-ONNX/
        └── onnx/
            ├── model_quantized.onnx       ← embedding model graph
            └── model_quantized.onnx_data  ← model weights (~295 MB, downloaded once)
```

Override the DB location with `$CYBER_MEMORY_DB` or `--db`. The rest of the data follows the DB directory automatically.

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     cyber-memory                        │
│                                                         │
│  STDIO (JSON-RPC 2.0)                                   │
│       │                                                 │
│  ┌────▼─────────┐   ┌──────────────────────────────┐   │
│  │  MCP Server  │   │       Embed Engine            │   │
│  │  (mcp-go)    │──►│  EmbeddingGemma-300m (ONNX)   │   │
│  └────┬─────────┘   │  768-dim · 2048 tokens · CPU  │   │
│       │             └──────────────────────────────┘   │
│  ┌────▼─────────────────────────────────────────────┐   │
│  │                 SQLite Store                      │   │
│  │  ┌──────────┐  ┌──────┐  ┌────────────────────┐  │   │
│  │  │ memories │  │ tags │  │     relations       │  │   │
│  │  │ +FTS5    │  │      │  │  (graph edges)      │  │   │
│  │  └──────────┘  └──────┘  └────────────────────┘  │   │
│  └───────────────────────────────────────────────────┘   │
│                                                         │
│  ┌──────────────────────────────────────────────────┐   │
│  │                    Scorer                         │   │
│  │   cosine_sim × recency × importance × access     │   │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
```

**No external processes. No network calls after first run. No root required.**

---

## Building from source

Requires: Go 1.22+, Rust/cargo (for `libtokenizers.a`), CGO enabled.

```sh
git clone https://github.com/RamboRogers/cyber-memory
cd cyber-memory

# Build libtokenizers.a once (requires cargo)
make tokenizers

# Build the binary
make build

# Install to /usr/local/bin
make install
```

To embed ORT libraries for additional platforms, add the `.dylib`/`.so`/`.dll` to `assets/<os>_<arch>/` with a matching `ort_<os>_<arch>.go` build-tagged file, then rebuild.

---

## Security & Privacy

- **No telemetry.** No analytics. No phone-home.
- **Fully local.** All embeddings and inference run on your CPU. Nothing leaves your machine after the one-time model download.
- **Your data.** The SQLite file is yours — inspect it with any SQLite browser, back it up, copy it.
- **Minimal attack surface.** STDIO only. No listening ports. No HTTP server.

---

## Roadmap

- [ ] Pre-built binaries for `linux/amd64`, `linux/arm64`, `windows/amd64`
- [ ] Embedded ORT for all platforms (currently darwin/arm64 only)
- [ ] Automatic memory consolidation (agent-controlled summarization)
- [ ] Memory export/import (JSON)
- [ ] Optional importance inference from content

---

## Author

**Matthew Rogers** — [@RamboRogers](https://github.com/RamboRogers)

Built because every agent memory solution I tried either required a cloud service, a running database, or asked me seventeen questions before storing a single fact.

---

<div align="center">

[Releases](https://github.com/RamboRogers/cyber-memory/releases) · [Issues](https://github.com/RamboRogers/cyber-memory/issues) · [RamboRogers](https://github.com/RamboRogers)

*If this is useful to you, a star goes a long way.*

</div>
