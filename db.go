package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func initDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Optimize for concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}

	// Create document chunks table
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS document_chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		dataset TEXT,
		filepath TEXT,
		seq INTEGER,
		content TEXT
	);`)
	if err != nil {
		return nil, err
	}

	// Create FTS5 Virtual Table
	_, err = db.Exec(`
	CREATE VIRTUAL TABLE IF NOT EXISTS search_idx USING fts5(
		dataset UNINDEXED, 
		filepath, 
		headers, 
		content,
		content_rowid='id'
	);`)

	return db, err
}

func extractHeaders(content string) string {
	var headers []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			headers = append(headers, strings.TrimLeft(line, "# "))
		}
	}
	return strings.Join(headers, " ")
}

func saveChunk(tx *sql.Tx, dataset, filepath string, seq int, content string) error {
	headers := extractHeaders(content)

	res, err := tx.Exec(`INSERT INTO document_chunks (dataset, filepath, seq, content) VALUES (?, ?, ?, ?)`,
		dataset, filepath, seq, content)
	if err != nil {
		return err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return err
	}

	// Insert into FTS index (Filename and Headers get higher weight in MATCH queries)
	_, err = tx.Exec(`INSERT INTO search_idx (rowid, dataset, filepath, headers, content) VALUES (?, ?, ?, ?, ?)`,
		id, dataset, filepath, headers, content)

	return err
}

type SearchResult struct {
	Dataset  string
	Filepath string
	Seq      int
	Snippet  string
}

func searchDocs(db *sql.DB, query string, dataset string, limit int) ([]SearchResult, error) {
	// Escape single quotes
	query = strings.ReplaceAll(query, "'", "''")

	// If a dataset is provided, filter using the document_chunks (aliased as 'c') dataset column
	datasetFilter := ""
	if dataset != "" {
		// FIX: Use c.dataset to resolve the ambiguity
		datasetFilter = fmt.Sprintf(" AND c.dataset = '%s'", dataset)
	}

	// Weighted query: filepath (10), headers (5), content (1)
	q := fmt.Sprintf(`
		SELECT 
			c.dataset, c.filepath, c.seq, 
			snippet(search_idx, 3, '>>', '<<', '...', 30) 
		FROM search_idx s
		JOIN document_chunks c ON s.rowid = c.id
		WHERE search_idx MATCH '%s'%s
		ORDER BY bm25(search_idx, 10.0, 5.0, 1.0) 
		LIMIT %d;
	`, query, datasetFilter, limit)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Dataset, &r.Filepath, &r.Seq, &r.Snippet); err == nil {
			// Clean snippet newlines for cleaner output
			r.Snippet = strings.ReplaceAll(r.Snippet, "\n", " ")
			results = append(results, r)
		}
	}
	return results, nil
}
