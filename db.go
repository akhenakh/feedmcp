package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"sort"
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
	StartSeq int
	EndSeq   int
	Content  string
}

func searchDocs(db *sql.DB, query string, dataset string, limit int) ([]SearchResult, error) {
	// Escape single quotes
	query = strings.ReplaceAll(query, "'", "''")

	datasetFilter := ""
	if dataset != "" {
		datasetFilter = fmt.Sprintf(" AND c.dataset = '%s'", dataset)
	}

	// 1. Initial High-Precision BM25 Retrieval
	// (Weighted query: filepath 10.0, headers 5.0, content 1.0)
	q := fmt.Sprintf(`
		SELECT c.dataset, c.filepath, c.seq
		FROM search_idx s
		JOIN document_chunks c ON s.rowid = c.id
		WHERE search_idx MATCH '%s'%s
		ORDER BY bm25(search_idx, 10.0, 5.0, 1.0) ASC
		LIMIT %d;
	`, query, datasetFilter, limit)

	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 2. Group matches by dataset+filepath to preserve context ordering
	type fileKey struct{ dataset, filepath string }
	var orderedKeys []fileKey
	hits := make(map[fileKey][]int)

	for rows.Next() {
		var ds, fp string
		var seq int
		if err := rows.Scan(&ds, &fp, &seq); err == nil {
			k := fileKey{ds, fp}
			if _, exists := hits[k]; !exists {
				orderedKeys = append(orderedKeys, k) // Persist relevance order
			}
			hits[k] = append(hits[k], seq)
		}
	}

	// 3. Relevant Segment Extraction (RSE)
	var results []SearchResult

	for _, key := range orderedKeys {
		seqs := hits[key]
		sort.Ints(seqs) // Ensure sequential order

		type span struct{ start, end int }
		var spans []span

		for _, s := range seqs {
			// REMOVED aggressive context enrichment (s-1, s+1) to keep payload sizes lean
			start := s
			end := s

			// Merge contiguous/overlapping chunks logically
			if len(spans) == 0 {
				spans = append(spans, span{start, end})
			} else {
				last := &spans[len(spans)-1]
				if start <= last.end+1 {
					if end > last.end {
						last.end = end
					}
				} else {
					spans = append(spans, span{start, end})
				}
			}
		}

		// 4. Fetch the continuous text segments from the DB
		for _, sp := range spans {
			fetchQ := `
				SELECT seq, content 
				FROM document_chunks 
				WHERE dataset = ? AND filepath = ? AND seq >= ? AND seq <= ?
				ORDER BY seq ASC
			`
			spanRows, err := db.Query(fetchQ, key.dataset, key.filepath, sp.start, sp.end)
			if err != nil {
				continue
			}

			var sb strings.Builder
			actualStart, actualEnd := -1, -1

			for spanRows.Next() {
				var sq int
				var cnt string
				if err := spanRows.Scan(&sq, &cnt); err == nil {
					if actualStart == -1 {
						actualStart = sq
					}
					actualEnd = sq
					sb.WriteString(cnt)
					sb.WriteString("\n")
				}
			}
			spanRows.Close()

			if actualStart != -1 {
				results = append(results, SearchResult{
					Dataset:  key.dataset,
					Filepath: key.filepath,
					StartSeq: actualStart,
					EndSeq:   actualEnd,
					Content:  sb.String(),
				})
			}
		}
	}
	return results, nil
}
