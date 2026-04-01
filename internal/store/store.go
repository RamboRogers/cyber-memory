// Package store implements the SQLite-backed memory store.
// Uses modernc.org/sqlite (pure Go, no CGO) with FTS5 for keyword search
// and plain BLOB columns for embeddings (cosine similarity done in Go).
package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS memories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    content      TEXT    NOT NULL,
    summary      TEXT    NOT NULL DEFAULT '',
    embedding    BLOB,                              -- float32[] little-endian, dim=768
    kind         TEXT    NOT NULL DEFAULT 'episodic',
    source       TEXT    NOT NULL DEFAULT 'agent',
    importance   REAL    NOT NULL DEFAULT 1.0,
    access_count INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    accessed_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX IF NOT EXISTS idx_memories_kind        ON memories(kind);
CREATE INDEX IF NOT EXISTS idx_memories_created_at  ON memories(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_memories_accessed_at ON memories(accessed_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    content,
    summary,
    content='memories',
    content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS memories_fts_insert AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, content, summary) VALUES (new.id, new.content, new.summary);
END;

CREATE TRIGGER IF NOT EXISTS memories_fts_delete AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, summary) VALUES ('delete', old.id, old.content, old.summary);
END;

CREATE TRIGGER IF NOT EXISTS memories_fts_update AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, content, summary) VALUES ('delete', old.id, old.content, old.summary);
    INSERT INTO memories_fts(rowid, content, summary) VALUES (new.id, new.content, new.summary);
END;

CREATE TABLE IF NOT EXISTS tags (
    memory_id INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    tag       TEXT    NOT NULL,
    PRIMARY KEY (memory_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_tags_tag ON tags(tag);

CREATE TABLE IF NOT EXISTS relations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    src_id     INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    dst_id     INTEGER NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL DEFAULT 'relates_to',
    weight     REAL    NOT NULL DEFAULT 1.0,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_relations_src ON relations(src_id);
CREATE INDEX IF NOT EXISTS idx_relations_dst ON relations(dst_id);
`

// Memory is a single stored memory with all metadata.
type Memory struct {
	ID          int64
	Content     string
	Summary     string
	Embedding   []float32
	Kind        string
	Source      string
	Importance  float64
	AccessCount int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	AccessedAt  time.Time
	Tags        []string
	Score       float64 // populated during search
}

// Relation is a directed edge between two memories.
type Relation struct {
	ID        int64
	SrcID     int64
	DstID     int64
	Kind      string
	Weight    float64
	CreatedAt time.Time
}

// StoreMemoryInput is the input for storing a new memory.
type StoreMemoryInput struct {
	Content    string
	Summary    string
	Embedding  []float32
	Kind       string
	Source     string
	Importance float64
	Tags       []string
}

// UpdateMemoryInput is the input for updating an existing memory.
type UpdateMemoryInput struct {
	ID         int64
	Content    *string
	Summary    *string
	Embedding  []float32
	Importance *float64
	Tags       []string // if non-nil, replaces all tags
}

// Store wraps a SQLite database.
type Store struct {
	db  *sql.DB
	log *slog.Logger
}

// Open opens (or creates) the SQLite database at path, applying migrations.
func Open(path string, log *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // WAL mode; single writer
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	log.Info("store opened", "path", path)
	return &Store{db: db, log: log}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Insert stores a new memory and returns its ID.
func (s *Store) Insert(in StoreMemoryInput) (int64, error) {
	if in.Kind == "" {
		in.Kind = "episodic"
	}
	if in.Source == "" {
		in.Source = "agent"
	}
	if in.Importance == 0 {
		in.Importance = 1.0
	}

	embBlob := encodeEmbedding(in.Embedding)

	res, err := s.db.Exec(
		`INSERT INTO memories (content, summary, embedding, kind, source, importance)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		in.Content, in.Summary, embBlob, in.Kind, in.Source, in.Importance,
	)
	if err != nil {
		return 0, fmt.Errorf("insert memory: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.setTags(id, in.Tags); err != nil {
		return 0, err
	}
	return id, nil
}

// Update updates an existing memory. Only non-nil fields are changed.
func (s *Store) Update(in UpdateMemoryInput) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if in.Content != nil {
		embBlob := encodeEmbedding(in.Embedding)
		summary := ""
		if in.Summary != nil {
			summary = *in.Summary
		}
		_, err = tx.Exec(
			`UPDATE memories SET content=?, summary=?, embedding=?, updated_at=unixepoch() WHERE id=?`,
			*in.Content, summary, embBlob, in.ID,
		)
		if err != nil {
			return fmt.Errorf("update content: %w", err)
		}
	} else if in.Summary != nil {
		_, err = tx.Exec(`UPDATE memories SET summary=?, updated_at=unixepoch() WHERE id=?`, *in.Summary, in.ID)
		if err != nil {
			return err
		}
	}
	if in.Importance != nil {
		_, err = tx.Exec(`UPDATE memories SET importance=?, updated_at=unixepoch() WHERE id=?`, *in.Importance, in.ID)
		if err != nil {
			return err
		}
	}
	if in.Tags != nil {
		if _, err := tx.Exec(`DELETE FROM tags WHERE memory_id=?`, in.ID); err != nil {
			return err
		}
		for _, tag := range in.Tags {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO tags (memory_id, tag) VALUES (?,?)`, in.ID, tag); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Delete hard-deletes a memory and cascades to tags + relations.
func (s *Store) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM memories WHERE id=?`, id)
	return err
}

// GetByID retrieves a single memory by ID and increments its access count.
func (s *Store) GetByID(id int64) (*Memory, error) {
	m, err := s.scanMemory(s.db.QueryRow(
		`SELECT id,content,summary,embedding,kind,source,importance,access_count,created_at,updated_at,accessed_at
		 FROM memories WHERE id=?`, id,
	))
	if err != nil {
		return nil, err
	}
	m.Tags, _ = s.tagsFor(id)
	_, _ = s.db.Exec(
		`UPDATE memories SET access_count=access_count+1, accessed_at=unixepoch() WHERE id=?`, id,
	)
	return m, nil
}

// AllWithEmbeddings returns all memories that have embeddings, with minimal fields for scoring.
func (s *Store) AllWithEmbeddings(kind string) ([]*Memory, error) {
	q := `SELECT id,content,summary,embedding,kind,source,importance,access_count,created_at,updated_at,accessed_at
	      FROM memories WHERE embedding IS NOT NULL`
	args := []any{}
	if kind != "" && kind != "any" {
		q += " AND kind=?"
		args = append(args, kind)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanMemories(rows)
}

// FTSSearch performs full-text search ranked by BM25.
func (s *Store) FTSSearch(query string, tags []string, limit int) ([]*Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(
		`SELECT m.id,m.content,m.summary,m.embedding,m.kind,m.source,m.importance,m.access_count,
		        m.created_at,m.updated_at,m.accessed_at
		 FROM memories m
		 JOIN memories_fts f ON f.rowid = m.id
		 WHERE memories_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()
	mems, err := s.scanMemories(rows)
	if err != nil {
		return nil, err
	}
	filtered := s.filterByTags(mems, tags)
	return s.attachTags(filtered, nil)
}

// TouchAccess bumps access_count and accessed_at for a list of IDs.
func (s *Store) TouchAccess(ids []int64) error {
	for _, id := range ids {
		if _, err := s.db.Exec(
			`UPDATE memories SET access_count=access_count+1, accessed_at=unixepoch() WHERE id=?`, id,
		); err != nil {
			return err
		}
	}
	return nil
}

// Relate creates a directed edge between src and dst.
func (s *Store) Relate(srcID, dstID int64, kind string, weight float64) (int64, error) {
	if kind == "" {
		kind = "relates_to"
	}
	if weight == 0 {
		weight = 1.0
	}
	res, err := s.db.Exec(
		`INSERT INTO relations (src_id, dst_id, kind, weight) VALUES (?,?,?,?)`,
		srcID, dstID, kind, weight,
	)
	if err != nil {
		return 0, fmt.Errorf("insert relation: %w", err)
	}
	return res.LastInsertId()
}

// Graph traverses the knowledge graph up to depth hops from rootID using a recursive CTE.
func (s *Store) Graph(rootID int64, depth int) ([]*Memory, []Relation, error) {
	if depth <= 0 {
		depth = 2
	}
	rows, err := s.db.Query(`
		WITH RECURSIVE graph(id, depth) AS (
			SELECT dst_id, 1 FROM relations WHERE src_id = ?
			UNION ALL
			SELECT r.dst_id, g.depth+1 FROM relations r JOIN graph g ON r.src_id = g.id WHERE g.depth < ?
		)
		SELECT DISTINCT m.id,m.content,m.summary,m.embedding,m.kind,m.source,m.importance,m.access_count,
		       m.created_at,m.updated_at,m.accessed_at
		FROM memories m JOIN graph g ON m.id = g.id`,
		rootID, depth,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("graph traverse: %w", err)
	}
	defer rows.Close()
	nodes, err := s.scanMemories(rows)
	if err != nil {
		return nil, nil, err
	}

	// Collect all IDs (root + nodes)
	ids := []int64{rootID}
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	rels, err := s.relationsAmong(ids)
	return nodes, rels, err
}

// Stats returns aggregate database statistics.
func (s *Store) Stats() (map[string]any, error) {
	stats := map[string]any{}
	row := s.db.QueryRow(`SELECT COUNT(*), MIN(created_at), MAX(created_at) FROM memories`)
	var count int64
	var minT, maxT sql.NullInt64
	if err := row.Scan(&count, &minT, &maxT); err != nil {
		return nil, err
	}
	stats["total_memories"] = count
	if minT.Valid {
		stats["oldest_memory"] = time.Unix(minT.Int64, 0).UTC().Format(time.RFC3339)
	}
	if maxT.Valid {
		stats["newest_memory"] = time.Unix(maxT.Int64, 0).UTC().Format(time.RFC3339)
	}

	var relCount int64
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM relations`).Scan(&relCount)
	stats["total_relations"] = relCount

	return stats, nil
}

// Wipe drops all data (tables remain).
func (s *Store) Wipe() error {
	_, err := s.db.Exec(`DELETE FROM memories; DELETE FROM relations; DELETE FROM tags;`)
	return err
}

// PurgeStaleDays deletes memories older than days that have never been accessed.
func (s *Store) PurgeStaleDays(days int) (int64, error) {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	res, err := s.db.Exec(
		`DELETE FROM memories WHERE created_at < ? AND access_count = 0`, cutoff,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// List returns the N most recently created memories.
func (s *Store) List(n int) ([]*Memory, error) {
	if n <= 0 {
		n = 20
	}
	rows, err := s.db.Query(
		`SELECT id,content,summary,embedding,kind,source,importance,access_count,created_at,updated_at,accessed_at
		 FROM memories ORDER BY created_at DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.attachTags(s.scanMemories(rows))
}

// ---- helpers ----

func (s *Store) setTags(id int64, tags []string) error {
	for _, tag := range tags {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO tags (memory_id, tag) VALUES (?,?)`, id, tag); err != nil {
			return err
		}
	}
	return nil
}

// TagsFor returns the tags for a given memory ID.
func (s *Store) TagsFor(id int64) ([]string, error) {
	return s.tagsFor(id)
}

func (s *Store) tagsFor(id int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT tag FROM tags WHERE memory_id=? ORDER BY tag`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

func (s *Store) attachTags(mems []*Memory, err error) ([]*Memory, error) {
	if err != nil {
		return nil, err
	}
	for _, m := range mems {
		m.Tags, _ = s.tagsFor(m.ID)
	}
	return mems, nil
}

func (s *Store) filterByTags(mems []*Memory, filter []string) []*Memory {
	if len(filter) == 0 {
		return mems
	}
	set := make(map[string]struct{}, len(filter))
	for _, t := range filter {
		set[t] = struct{}{}
	}
	out := mems[:0]
	for _, m := range mems {
		m.Tags, _ = s.tagsFor(m.ID)
		for _, t := range m.Tags {
			if _, ok := set[t]; ok {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

func (s *Store) relationsAmong(ids []int64) ([]Relation, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	rows, err := s.db.Query(
		`SELECT id,src_id,dst_id,kind,weight,created_at FROM relations WHERE src_id IN (`+placeholders(len(ids))+`)`,
		int64sToAny(ids)...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rels []Relation
	for rows.Next() {
		var r Relation
		var ts int64
		if err := rows.Scan(&r.ID, &r.SrcID, &r.DstID, &r.Kind, &r.Weight, &ts); err != nil {
			return nil, err
		}
		if _, ok := set[r.DstID]; ok {
			r.CreatedAt = time.Unix(ts, 0).UTC()
			rels = append(rels, r)
		}
	}
	return rels, rows.Err()
}

func (s *Store) scanMemory(row *sql.Row) (*Memory, error) {
	var m Memory
	var embBlob []byte
	var createdAt, updatedAt, accessedAt int64
	err := row.Scan(
		&m.ID, &m.Content, &m.Summary, &embBlob,
		&m.Kind, &m.Source, &m.Importance, &m.AccessCount,
		&createdAt, &updatedAt, &accessedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memory not found")
	}
	if err != nil {
		return nil, err
	}
	m.Embedding = decodeEmbedding(embBlob)
	m.CreatedAt = time.Unix(createdAt, 0).UTC()
	m.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	m.AccessedAt = time.Unix(accessedAt, 0).UTC()
	return &m, nil
}

func (s *Store) scanMemories(rows *sql.Rows) ([]*Memory, error) {
	var mems []*Memory
	for rows.Next() {
		var m Memory
		var embBlob []byte
		var createdAt, updatedAt, accessedAt int64
		if err := rows.Scan(
			&m.ID, &m.Content, &m.Summary, &embBlob,
			&m.Kind, &m.Source, &m.Importance, &m.AccessCount,
			&createdAt, &updatedAt, &accessedAt,
		); err != nil {
			return nil, err
		}
		m.Embedding = decodeEmbedding(embBlob)
		m.CreatedAt = time.Unix(createdAt, 0).UTC()
		m.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		m.AccessedAt = time.Unix(accessedAt, 0).UTC()
		mems = append(mems, &m)
	}
	return mems, rows.Err()
}

// encodeEmbedding converts []float32 → little-endian BLOB.
func encodeEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// decodeEmbedding converts little-endian BLOB → []float32.
func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func placeholders(n int) string {
	if n == 0 {
		return ""
	}
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}

func int64sToAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
