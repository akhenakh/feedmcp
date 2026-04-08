package main

import (
	"bufio"
	"database/sql"
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

	// Clear existing dataset data
	_, _ = db.Exec("DELETE FROM search_idx WHERE dataset = ?", dataset)
	_, _ = db.Exec("DELETE FROM document_chunks WHERE dataset = ?", dataset)

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(decoder)
	// 10MB buffer for extremely long lines in minified code
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	var currentPath string
	var contentBuilder strings.Builder
	inBlock := false

	// Text splitter for smart RAG chunking
	splitter := textsplitter.NewMarkdownTextSplitter(
		textsplitter.WithChunkSize(2000),
		textsplitter.WithChunkOverlap(200),
		textsplitter.WithHeadingHierarchy(true),
	)

	filesProcessed := 0

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "```") {
			if !inBlock {
				// We found the start of a fcopy code block.
				// Format is typically: ```language path/to/file.md
				parts := strings.Fields(strings.TrimPrefix(line, "```"))

				if len(parts) >= 2 {
					currentPath = parts[1] // Extract path after language
				} else if len(parts) == 1 {
					currentPath = parts[0]
				} else {
					currentPath = "unknown.md"
				}

				inBlock = true
				contentBuilder.Reset()
			} else {
				// End of code block, process the file
				inBlock = false
				content := contentBuilder.String()

				// Apply segmentation
				chunks, err := splitter.SplitText(content)
				if err != nil || len(chunks) == 0 {
					chunks = []string{content} // fallback
				}

				for i, chunk := range chunks {
					saveChunk(tx, dataset, currentPath, i, chunk)
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
