package memory

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Memory represents a single stored memory entry.
type Memory struct {
	ID        int64
	Content   string
	Category  string
	Source    string
	Metadata  map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MemoryStats holds aggregate counts for the memory store.
type MemoryStats struct {
	Total      int
	ByCategory map[string]int
}

// MemoryStore provides semantic memory storage backed by SQLite with FTS5,
// with markdown files as the source of truth.
type MemoryStore struct {
	db        *sql.DB
	workspace string
}

const schemaVersion = 1

// NewMemoryStore opens or creates a SQLite memory database at dbPath.
// workspace is the picoclaw workspace root (parent of memory/).
func NewMemoryStore(dbPath string, workspace string) (*MemoryStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create memory directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open memory database: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	s := &MemoryStore{db: db, workspace: workspace}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate schema: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *MemoryStore) Close() error {
	return s.db.Close()
}

func (s *MemoryStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS memories (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			content TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'general',
			source TEXT NOT NULL DEFAULT 'manual',
			metadata TEXT,
			content_hash TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_memories_category ON memories(category);
		CREATE INDEX IF NOT EXISTS idx_memories_content_hash ON memories(content_hash);
	`)
	if err != nil {
		return err
	}

	// Create FTS5 table if it doesn't exist.
	// FTS5 virtual tables don't support IF NOT EXISTS, so check first.
	var ftsExists int
	err = s.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='memories_fts'
	`).Scan(&ftsExists)
	if err != nil {
		return err
	}

	if ftsExists == 0 {
		_, err = s.db.Exec(`
			CREATE VIRTUAL TABLE memories_fts USING fts5(
				content,
				category,
				content='memories',
				content_rowid='id'
			);

			-- Triggers to keep FTS in sync
			CREATE TRIGGER memories_ai AFTER INSERT ON memories BEGIN
				INSERT INTO memories_fts(rowid, content, category)
				VALUES (new.id, new.content, new.category);
			END;

			CREATE TRIGGER memories_ad AFTER DELETE ON memories BEGIN
				INSERT INTO memories_fts(memories_fts, rowid, content, category)
				VALUES ('delete', old.id, old.content, old.category);
			END;

			CREATE TRIGGER memories_au AFTER UPDATE ON memories BEGIN
				INSERT INTO memories_fts(memories_fts, rowid, content, category)
				VALUES ('delete', old.id, old.content, old.category);
				INSERT INTO memories_fts(rowid, content, category)
				VALUES (new.id, new.content, new.category);
			END;
		`)
		if err != nil {
			return err
		}
	}

	// Set schema version if not present
	var count int
	err = s.db.QueryRow("SELECT COUNT(*) FROM schema_version").Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		_, err = s.db.Exec("INSERT INTO schema_version (version) VALUES (?)", schemaVersion)
		if err != nil {
			return err
		}
	}

	return nil
}

// SchemaVersion returns the current schema version.
func (s *MemoryStore) SchemaVersion() (int, error) {
	var version int
	err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	return version, err
}

// Store saves a new memory to the database and writes through to markdown.
// Category determines which markdown file is written:
//   - "preference", "note" → MEMORY.md
//   - "fact", "event" → today's daily log
func (s *MemoryStore) Store(content, category, source string, metadata map[string]string) (int64, error) {
	var metaJSON *string
	if metadata != nil {
		data, err := json.Marshal(metadata)
		if err != nil {
			return 0, fmt.Errorf("failed to marshal metadata: %w", err)
		}
		str := string(data)
		metaJSON = &str
	}

	hash := contentHash(content)

	result, err := s.db.Exec(
		`INSERT INTO memories (content, category, source, metadata, content_hash)
		 VALUES (?, ?, ?, ?, ?)`,
		content, category, source, metaJSON, hash,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to insert memory: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	// Write through to markdown (best-effort — DB is the index, markdown is truth)
	s.writeToMarkdown(content, category)

	return id, nil
}

// Search performs an FTS5 full-text search, ranked by BM25 relevance.
// If category is non-empty, results are filtered by category.
func (s *MemoryStore) Search(query string, limit int, category string) ([]Memory, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 5
	}

	// Tokenize query for FTS5 prefix matching
	ftsQuery := buildFTSQuery(query)

	var rows *sql.Rows
	var err error

	if category != "" {
		rows, err = s.db.Query(`
			SELECT m.id, m.content, m.category, m.source, m.metadata, m.created_at, m.updated_at
			FROM memories_fts fts
			JOIN memories m ON m.id = fts.rowid
			WHERE memories_fts MATCH ?
			  AND m.category = ?
			ORDER BY bm25(memories_fts)
			LIMIT ?
		`, ftsQuery, category, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT m.id, m.content, m.category, m.source, m.metadata, m.created_at, m.updated_at
			FROM memories_fts fts
			JOIN memories m ON m.id = fts.rowid
			WHERE memories_fts MATCH ?
			ORDER BY bm25(memories_fts)
			LIMIT ?
		`, ftsQuery, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	return scanMemories(rows)
}

// Get retrieves a single memory by ID.
func (s *MemoryStore) Get(id int64) (*Memory, error) {
	row := s.db.QueryRow(`
		SELECT id, content, category, source, metadata, created_at, updated_at
		FROM memories WHERE id = ?
	`, id)

	mem, err := scanMemory(row)
	if err != nil {
		return nil, fmt.Errorf("memory not found: %w", err)
	}
	return mem, nil
}

// Delete removes a memory by ID.
func (s *MemoryStore) Delete(id int64) error {
	_, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
	return err
}

// List returns memories, optionally filtered by category.
func (s *MemoryStore) List(category string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if category != "" {
		rows, err = s.db.Query(`
			SELECT id, content, category, source, metadata, created_at, updated_at
			FROM memories WHERE category = ?
			ORDER BY created_at DESC LIMIT ?
		`, category, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT id, content, category, source, metadata, created_at, updated_at
			FROM memories ORDER BY created_at DESC LIMIT ?
		`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanMemories(rows)
}

// Stats returns aggregate counts for the memory store.
func (s *MemoryStore) Stats() (*MemoryStats, error) {
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&total)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query("SELECT category, COUNT(*) FROM memories GROUP BY category")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCategory := make(map[string]int)
	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err != nil {
			return nil, err
		}
		byCategory[cat] = count
	}

	return &MemoryStats{Total: total, ByCategory: byCategory}, nil
}

// Reindex rebuilds the database from markdown files (MEMORY.md + daily logs).
// Existing DB entries from a prior import are skipped by content hash.
func (s *MemoryStore) Reindex() error {
	memoryDir := filepath.Join(s.workspace, "memory")

	// Index MEMORY.md
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")
	if data, err := os.ReadFile(memoryFile); err == nil {
		lines := extractMemoryLines(string(data))
		for _, line := range lines {
			s.storeIfNew(line, "note", "import")
		}
	}

	// Index daily logs
	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Look for YYYYMM directories
		if len(entry.Name()) != 6 {
			continue
		}

		monthDir := filepath.Join(memoryDir, entry.Name())
		files, err := os.ReadDir(monthDir)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(monthDir, f.Name()))
			if err != nil {
				continue
			}
			lines := extractMemoryLines(string(data))
			for _, line := range lines {
				s.storeIfNew(line, "event", "import")
			}
		}
	}

	return nil
}

// storeIfNew stores a memory only if its content hash doesn't already exist.
func (s *MemoryStore) storeIfNew(content, category, source string) {
	hash := contentHash(content)
	var exists int
	err := s.db.QueryRow("SELECT COUNT(*) FROM memories WHERE content_hash = ?", hash).Scan(&exists)
	if err != nil || exists > 0 {
		return
	}

	s.db.Exec(
		`INSERT INTO memories (content, category, source, content_hash) VALUES (?, ?, ?, ?)`,
		content, category, source, hash,
	)
}

// writeToMarkdown appends a memory to the appropriate markdown file.
func (s *MemoryStore) writeToMarkdown(content, category string) {
	memoryDir := filepath.Join(s.workspace, "memory")
	entry := fmt.Sprintf("- %s\n", content)

	switch category {
	case "preference", "note":
		// Append to MEMORY.md
		memoryFile := filepath.Join(memoryDir, "MEMORY.md")
		s.appendToFile(memoryFile, entry)
	default:
		// fact, event, general → daily log
		today := time.Now().Format("20060102")
		monthDir := today[:6]
		dailyDir := filepath.Join(memoryDir, monthDir)
		os.MkdirAll(dailyDir, 0755)

		dailyFile := filepath.Join(dailyDir, today+".md")
		if _, err := os.Stat(dailyFile); os.IsNotExist(err) {
			header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
			os.WriteFile(dailyFile, []byte(header+entry), 0644)
		} else {
			s.appendToFile(dailyFile, entry)
		}
	}
}

func (s *MemoryStore) appendToFile(path, content string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(content)
}

// extractMemoryLines parses markdown content into individual memory entries.
// It extracts list items (- ...) and non-empty, non-header lines.
func extractMemoryLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "---" {
			continue
		}
		// Strip leading "- " from list items
		if strings.HasPrefix(line, "- ") {
			line = strings.TrimPrefix(line, "- ")
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// buildFTSQuery converts a natural language query into an FTS5 query.
// Each word becomes a prefix token for partial matching.
func buildFTSQuery(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return query
	}
	// Use prefix matching: each word gets a * suffix
	var parts []string
	for _, w := range words {
		// Escape FTS5 special characters
		w = strings.ReplaceAll(w, `"`, `""`)
		parts = append(parts, `"`+w+`"*`)
	}
	return strings.Join(parts, " ")
}

func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h[:16]) // 32-char hex, enough for dedup
}

var timeFormats = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	time.RFC3339,
}

func parseTime(s string) time.Time {
	for _, fmt := range timeFormats {
		if t, err := time.Parse(fmt, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// scanMemory reads a single memory from a *sql.Row.
func scanMemory(row *sql.Row) (*Memory, error) {
	var m Memory
	var metaJSON sql.NullString
	var createdAt, updatedAt string

	err := row.Scan(&m.ID, &m.Content, &m.Category, &m.Source, &metaJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	if metaJSON.Valid && metaJSON.String != "" {
		m.Metadata = make(map[string]string)
		json.Unmarshal([]byte(metaJSON.String), &m.Metadata)
	}

	m.CreatedAt = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)

	return &m, nil
}

// scanMemories reads multiple memories from *sql.Rows.
func scanMemories(rows *sql.Rows) ([]Memory, error) {
	var memories []Memory
	for rows.Next() {
		var m Memory
		var metaJSON sql.NullString
		var createdAt, updatedAt string

		err := rows.Scan(&m.ID, &m.Content, &m.Category, &m.Source, &metaJSON, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}

		if metaJSON.Valid && metaJSON.String != "" {
			m.Metadata = make(map[string]string)
			json.Unmarshal([]byte(metaJSON.String), &m.Metadata)
		}

		m.CreatedAt = parseTime(createdAt)
		m.UpdatedAt = parseTime(updatedAt)

		memories = append(memories, m)
	}
	return memories, nil
}
