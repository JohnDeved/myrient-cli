package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database for the local index.
type DB struct {
	db *sql.DB
}

// OpenDB opens or creates the SQLite database at the given path.
func OpenDB(dbPath string) (*DB, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS collections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		path TEXT NOT NULL,
		description TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS directories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL UNIQUE,
		collection_id INTEGER REFERENCES collections(id),
		last_crawled DATETIME
	);

	CREATE INDEX IF NOT EXISTS idx_directories_path ON directories(path);
	CREATE INDEX IF NOT EXISTS idx_directories_collection ON directories(collection_id);

	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		path TEXT NOT NULL,
		url TEXT NOT NULL,
		size TEXT DEFAULT '',
		date TEXT DEFAULT '',
		directory_id INTEGER REFERENCES directories(id),
		collection_id INTEGER REFERENCES collections(id)
	);

	CREATE INDEX IF NOT EXISTS idx_files_directory ON files(directory_id);
	CREATE INDEX IF NOT EXISTS idx_files_collection ON files(collection_id);
	CREATE INDEX IF NOT EXISTS idx_files_name ON files(name);

	CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts5(
		name,
		path,
		content=files,
		content_rowid=id,
		tokenize='unicode61 remove_diacritics 2'
	);

	CREATE TRIGGER IF NOT EXISTS files_ai AFTER INSERT ON files BEGIN
		INSERT INTO files_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
	END;

	CREATE TRIGGER IF NOT EXISTS files_ad AFTER DELETE ON files BEGIN
		INSERT INTO files_fts(files_fts, rowid, name, path) VALUES('delete', old.id, old.name, old.path);
	END;

	CREATE TRIGGER IF NOT EXISTS files_au AFTER UPDATE ON files BEGIN
		INSERT INTO files_fts(files_fts, rowid, name, path) VALUES('delete', old.id, old.name, old.path);
		INSERT INTO files_fts(files_fts, rowid, name, path) VALUES (new.id, new.name, new.path);
	END;
	`
	_, err := db.Exec(schema)
	return err
}

// Collection represents a top-level Myrient collection.
type Collection struct {
	ID          int64
	Name        string
	Path        string
	Description string
}

// FileRecord represents an indexed file.
type FileRecord struct {
	ID           int64
	Name         string
	Path         string
	URL          string
	Size         string
	Date         string
	DirectoryID  int64
	CollectionID int64
}

// SearchResult is a file with its collection info.
type SearchResult struct {
	FileRecord
	CollectionName string
}

// sanitizeFTS5Query escapes FTS5 special characters so user input
// does not cause syntax errors. Each word is wrapped in double quotes,
// and embedded double quotes are doubled (FTS5 escaping).
func sanitizeFTS5Query(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// Split into words and quote each individually.
	// This turns `mario (usa)` into `"mario" "usa"` which FTS5 treats as AND.
	words := strings.Fields(query)
	var quoted []string
	for _, w := range words {
		// Remove any characters that have no business being in a search term.
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		// Escape embedded double quotes by doubling them.
		w = strings.ReplaceAll(w, `"`, `""`)
		// Strip FTS5 operators that would confuse the parser.
		w = strings.NewReplacer(
			"(", "",
			")", "",
			"[", "",
			"]", "",
			"{", "",
			"}", "",
			"^", "",
		).Replace(w)
		if w == "" {
			continue
		}
		quoted = append(quoted, `"`+w+`"`)
	}
	if len(quoted) == 0 {
		return ""
	}
	return strings.Join(quoted, " ")
}

// UpsertCollection inserts or updates a collection and returns its ID.
func (d *DB) UpsertCollection(name, path, description string) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO collections (name, path, description) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET path=excluded.path, description=excluded.description`,
		name, path, description,
	)
	if err != nil {
		return 0, err
	}

	// If the row was updated (not inserted), we need to fetch the ID.
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		row := d.db.QueryRow("SELECT id FROM collections WHERE name = ?", name)
		if err := row.Scan(&id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// GetCollections returns all collections.
func (d *DB) GetCollections() ([]Collection, error) {
	rows, err := d.db.Query("SELECT id, name, path, description FROM collections ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Path, &c.Description); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// UpsertDirectory inserts or updates a directory and returns its ID.
func (d *DB) UpsertDirectory(path string, collectionID int64) (int64, error) {
	res, err := d.db.Exec(
		`INSERT INTO directories (path, collection_id) VALUES (?, ?)
		 ON CONFLICT(path) DO UPDATE SET collection_id=excluded.collection_id`,
		path, collectionID,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		row := d.db.QueryRow("SELECT id FROM directories WHERE path = ?", path)
		if err := row.Scan(&id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// MarkDirectoryCrawled updates the last_crawled timestamp.
func (d *DB) MarkDirectoryCrawled(dirID int64) error {
	_, err := d.db.Exec(
		"UPDATE directories SET last_crawled = ? WHERE id = ?",
		time.Now().UTC(), dirID,
	)
	return err
}

// IsDirectoryStale checks whether a directory needs re-crawling.
func (d *DB) IsDirectoryStale(path string, staleDays int) (bool, error) {
	var lastCrawled sql.NullTime
	err := d.db.QueryRow("SELECT last_crawled FROM directories WHERE path = ?", path).Scan(&lastCrawled)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if !lastCrawled.Valid {
		return true, nil
	}
	return time.Since(lastCrawled.Time) > time.Duration(staleDays)*24*time.Hour, nil
}

// ClearDirectoryFiles deletes all files for a directory (before re-indexing).
func (d *DB) ClearDirectoryFiles(dirID int64) error {
	_, err := d.db.Exec("DELETE FROM files WHERE directory_id = ?", dirID)
	return err
}

// InsertFile adds a file to the index.
func (d *DB) InsertFile(name, path, fileURL, size, date string, dirID, colID int64) error {
	_, err := d.db.Exec(
		`INSERT INTO files (name, path, url, size, date, directory_id, collection_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, path, fileURL, size, date, dirID, colID,
	)
	return err
}

// InsertFileBatch inserts multiple files in a single transaction.
func (d *DB) InsertFileBatch(files []FileRecord) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO files (name, path, url, size, date, directory_id, collection_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range files {
		if _, err := stmt.Exec(f.Name, f.Path, f.URL, f.Size, f.Date, f.DirectoryID, f.CollectionID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Search performs a full-text search across all indexed files.
func (d *DB) Search(query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 50
	}

	sanitized := sanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}

	rows, err := d.db.Query(`
		SELECT f.id, f.name, f.path, f.url, f.size, f.date, f.directory_id, f.collection_id,
		       COALESCE(c.name, '') as collection_name
		FROM files_fts fts
		JOIN files f ON f.id = fts.rowid
		LEFT JOIN collections c ON c.id = f.collection_id
		WHERE files_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, sanitized, limit)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(
			&r.ID, &r.Name, &r.Path, &r.URL, &r.Size, &r.Date,
			&r.DirectoryID, &r.CollectionID, &r.CollectionName,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// SearchInCollection performs FTS search filtered by collection.
func (d *DB) SearchInCollection(query string, collectionName string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 50
	}

	sanitized := sanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}

	rows, err := d.db.Query(`
		SELECT f.id, f.name, f.path, f.url, f.size, f.date, f.directory_id, f.collection_id,
		       COALESCE(c.name, '') as collection_name
		FROM files_fts fts
		JOIN files f ON f.id = fts.rowid
		LEFT JOIN collections c ON c.id = f.collection_id
		WHERE files_fts MATCH ?
		  AND c.name LIKE ?
		ORDER BY rank
		LIMIT ?
	`, sanitized, "%"+collectionName+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(
			&r.ID, &r.Name, &r.Path, &r.URL, &r.Size, &r.Date,
			&r.DirectoryID, &r.CollectionID, &r.CollectionName,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Stats returns index statistics.
type Stats struct {
	Collections int
	Directories int
	Files       int
}

// GetStats returns statistics about the index.
func (d *DB) GetStats() (Stats, error) {
	var s Stats
	if err := d.db.QueryRow("SELECT COUNT(*) FROM collections").Scan(&s.Collections); err != nil {
		return s, err
	}
	if err := d.db.QueryRow("SELECT COUNT(*) FROM directories").Scan(&s.Directories); err != nil {
		return s, err
	}
	if err := d.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&s.Files); err != nil {
		return s, err
	}
	return s, nil
}
