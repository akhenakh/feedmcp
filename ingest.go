package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/tmc/langchaingo/textsplitter"
)

func ingestDirectory(db *sql.DB, dirPath string) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".zstd") {
			zstdPath := filepath.Join(dirPath, entry.Name())
			dataset := strings.TrimSuffix(entry.Name(), ".md.zstd")

			log.Printf("Ingesting dataset: %s from %s", dataset, zstdPath)
			if err := processZstdArchive(db, zstdPath, dataset); err != nil {
				log.Printf("Error processing %s: %v", zstdPath, err)
			}
		}
	}
	return nil
}

func processZstdArchive(db *sql.DB, path, dataset string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	decoder, err := zstd.NewReader(f)
	if err != nil {
		return err
	}
	defer decoder.Close()

	_, _ = db.Exec("DELETE FROM search_idx WHERE dataset = ?", dataset)
	_, _ = db.Exec("DELETE FROM document_chunks WHERE dataset = ?", dataset)

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(decoder)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	var currentPath string
	var contentBuilder strings.Builder
	inBlock := false

	// Text splitter optimized for Semantic Chunk boundaries
	splitter := textsplitter.NewMarkdownTextSplitter(
		textsplitter.WithChunkSize(1500),
		textsplitter.WithChunkOverlap(150),
		textsplitter.WithHeadingHierarchy(true),
	)

	filesProcessed := 0

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "```") {
			if !inBlock {
				parts := strings.Fields(strings.TrimPrefix(line, "```"))
				if len(parts) >= 2 {
					currentPath = parts[1]
				} else if len(parts) == 1 {
					currentPath = parts[0]
				} else {
					currentPath = "unknown.md"
				}
				inBlock = true
				contentBuilder.Reset()
			} else {
				inBlock = false
				content := contentBuilder.String()

				chunks, err := splitter.SplitText(content)
				if err != nil || len(chunks) == 0 {
					chunks = []string{content}
				}

				for i, chunk := range chunks {
					// Contextual Chunk Headers (CCH): Ground each proposition in the index
					enrichedChunk := fmt.Sprintf("--- Context: %s | File: %s | Seq: %d ---\n%s", dataset, currentPath, i, chunk)
					saveChunk(tx, dataset, currentPath, i, enrichedChunk)
				}
				filesProcessed++
			}
		} else if inBlock {
			contentBuilder.WriteString(line + "\n")
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	log.Printf("✅ Finished dataset '%s': indexed %d files.", dataset, filesProcessed)
	return nil
}
