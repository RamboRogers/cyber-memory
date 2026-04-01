//go:build cgo && (ORT || ALL)

// cyber-memory: zero-config STDIO MCP server for agent memory.
//
// When run without flags it starts as an MCP STDIO server.
// Maintenance flags allow listing, searching, purging, and wiping memories.
//
// Build:
//
//	CGO_ENABLED=1 go build -tags ORT -o cyber-memory .
//
// Config (all optional, resolved in order):
//
//	CYBER_MEMORY_DB   — full path to the SQLite database file
//	XDG_DATA_HOME     — base for ~/.local/share/cyber-memory/db.sqlite3
//	CYBER_MEMORY_ORT  — path to libonnxruntime shared library
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"
	"time"

	"github.com/ramborogers/cyber-memory/internal/embed"
	mcpsrv "github.com/ramborogers/cyber-memory/internal/mcp"
	"github.com/ramborogers/cyber-memory/internal/store"
)

const version = "0.1.0"

func main() {
	// ---- flags ----
	var (
		dbFlag      = flag.String("db", "", "Override database path (also: $CYBER_MEMORY_DB)")
		listFlag    = flag.Int("list", -1, "Print the N most recent memories and exit (default 20 when flag present without value)")
		searchFlag  = flag.String("search", "", "Full-text search and exit")
		purgeFlag   = flag.Int("purge-days", 0, "Delete unaccessed memories older than N days and exit")
		wipeFlag    = flag.Bool("wipe", false, "Drop all memories (requires --confirm)")
		confirmFlag = flag.Bool("confirm", false, "Required for destructive operations")
		statsFlag   = flag.Bool("stats", false, "Print database statistics and exit")
		versionFlag = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Printf("cyber-memory %s (go%s %s/%s)\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		os.Exit(0)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dbPath := resolveDBPath(*dbFlag)
	log.Debug("database path resolved", "path", dbPath)

	st, err := store.Open(dbPath, log)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// ---- CLI modes (no embedding engine needed) ----

	if *statsFlag {
		runStats(st, log)
		return
	}

	if *listFlag != -1 {
		n := *listFlag
		if n <= 0 {
			n = 20
		}
		runList(st, n, log)
		return
	}

	if *searchFlag != "" {
		runSearch(st, *searchFlag, log)
		return
	}

	if *purgeFlag > 0 {
		runPurge(st, *purgeFlag, log)
		return
	}

	if *wipeFlag {
		if !*confirmFlag {
			fmt.Fprintln(os.Stderr, "error: --wipe requires --confirm")
			os.Exit(1)
		}
		if err := st.Wipe(); err != nil {
			log.Error("wipe", "err", err)
			os.Exit(1)
		}
		fmt.Println("database wiped")
		return
	}

	// ---- MCP STDIO server mode ----

	ortLib := os.Getenv("CYBER_MEMORY_ORT")
	dataDir := filepath.Dir(dbPath)

	engine, err := embed.New(dataDir, ortLib, log)
	if err != nil {
		log.Error("init embedding engine", "err", err)
		os.Exit(1)
	}
	defer engine.Close()

	srv := mcpsrv.New(st, engine, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Info("cyber-memory MCP server started", "version", version, "db", dbPath)
	if err := srv.Serve(ctx); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

// resolveDBPath returns the database file path, following the priority chain:
//  1. --db flag / $CYBER_MEMORY_DB env var
//  2. $XDG_DATA_HOME/cyber-memory/db.sqlite3
//  3. ~/.local/share/cyber-memory/db.sqlite3
func resolveDBPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if v := os.Getenv("CYBER_MEMORY_DB"); v != "" {
		return v
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "cyber-memory", "db.sqlite3")
}

// ---- CLI helpers ----

func runStats(st *store.Store, log *slog.Logger) {
	stats, err := st.Stats()
	if err != nil {
		log.Error("stats", "err", err)
		os.Exit(1)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for k, v := range stats {
		fmt.Fprintf(w, "%s\t%v\n", k, v)
	}
	w.Flush()
}

func runList(st *store.Store, n int, log *slog.Logger) {
	mems, err := st.List(n)
	if err != nil {
		log.Error("list", "err", err)
		os.Exit(1)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tCREATED\tCONTENT")
	for _, m := range mems {
		content := m.Content
		if len(content) > 80 {
			content = content[:77] + "..."
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", m.ID, m.Kind, m.CreatedAt.Format(time.DateTime), content)
	}
	w.Flush()
}

func runSearch(st *store.Store, query string, log *slog.Logger) {
	mems, err := st.FTSSearch(query, nil, 20)
	if err != nil {
		log.Error("search", "err", err)
		os.Exit(1)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tCREATED\tCONTENT")
	for _, m := range mems {
		content := m.Content
		if len(content) > 80 {
			content = content[:77] + "..."
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", m.ID, m.Kind, m.CreatedAt.Format(time.DateTime), content)
	}
	w.Flush()
}

func runPurge(st *store.Store, days int, log *slog.Logger) {
	n, err := st.PurgeStaleDays(days)
	if err != nil {
		log.Error("purge", "err", err)
		os.Exit(1)
	}
	fmt.Printf("purged %d memories (unaccessed, older than %d days)\n", n, days)
}
