package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func setupMCPServer(db *sql.DB) *server.MCPServer {
	mcpSrv := server.NewMCPServer("docs-mcp-server", "1.1.1")

	// TOOL 1: List Datasets
	mcpSrv.AddTool(
		mcp.NewTool("list_datasets",
			mcp.WithDescription("List all available documentation datasets currently indexed in the database."),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			rows, err := db.Query("SELECT DISTINCT dataset FROM document_chunks ORDER BY dataset")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer rows.Close()

			var datasets []string
			for rows.Next() {
				var d string
				if err := rows.Scan(&d); err == nil {
					datasets = append(datasets, "- "+d)
				}
			}

			if len(datasets) == 0 {
				return mcp.NewToolResultText("No datasets found. Run the ingestion command first."), nil
			}

			resp := fmt.Sprintf("Available datasets:\n%s", strings.Join(datasets, "\n"))
			return mcp.NewToolResultText(resp), nil
		},
	)

	// TOOL 2: Search Docs
	mcpSrv.AddTool(
		mcp.NewTool("search_docs",
			mcp.WithDescription("Search docs via hybrid-weighted indexing. Returns only the top relevant segments to keep context clean."+
				"\nAGENT INSTRUCTION: "+
				"\n1. If you fail to find what you need, use 'Query Reformulation': simplify terms, use synonyms, or split into multiple atomic queries. "+
				"\n2. Consider 'HyDE' methodology: mentally hypothesize the likely answer structure and search for those specific keywords."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Keywords to search for (e.g. 'caddy reverse proxy').")),
			mcp.WithString("dataset", mcp.Description("Optional: Narrow search to a specific dataset (e.g. 'caddy-main').")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := request.GetString("query", "")
			dataset := request.GetString("dataset", "")

			// Fetch only the top 4 most relevant hits to prevent context bloating
			results, err := searchDocs(db, query, dataset, 4)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if len(results) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf("No results found for '%s'. Please reformulate your query (use synonyms, simplify terms, or use HyDE strategies).", query)), nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Top Segments for '%s':\n\n", query))
			for i, r := range results {
				content := r.Content

				// Truncation Safeguard: Cap individual massive chunks so they don't blow up the context window
				if len(content) > 3000 {
					content = content[:3000] + "\n\n... [SEGMENT TRUNCATED FOR LENGTH. Use 'read_doc' tool for the full file context.]"
				}

				sb.WriteString(fmt.Sprintf("### Result %d: Dataset: %s | File: %s (Chunks %d to %d)\n", i+1, r.Dataset, r.Filepath, r.StartSeq, r.EndSeq))
				sb.WriteString(fmt.Sprintf("%s\n\n", content))
			}
			sb.WriteString("To read the full context beyond these specific segments, use the 'read_doc' tool with the exact Dataset and Filepath.")

			return mcp.NewToolResultText(sb.String()), nil
		},
	)

	// TOOL 3: Read Document
	mcpSrv.AddTool(
		mcp.NewTool("read_doc",
			mcp.WithDescription("Read the full content of a specific file. Supports pagination via offset and limit if the file is too large."),
			mcp.WithString("dataset", mcp.Required(), mcp.Description("The dataset name")),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("The exact filepath")),
			mcp.WithNumber("offset", mcp.Description("Optional character offset to start reading from (default: 0)")),
			mcp.WithNumber("limit", mcp.Description("Optional maximum number of characters to return (default: 15000)")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			dataset := request.GetString("dataset", "")
			filepath := request.GetString("filepath", "")
			offset := int(request.GetFloat("offset", 0))
			limit := int(request.GetFloat("limit", 15000))

			rows, err := db.Query("SELECT content FROM document_chunks WHERE dataset = ? AND filepath = ? ORDER BY seq ASC", dataset, filepath)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			defer rows.Close()

			var sb strings.Builder
			count := 0
			for rows.Next() {
				var content string
				if err := rows.Scan(&content); err == nil {
					sb.WriteString(content)
					sb.WriteString("\n")
					count++
				}
			}

			if count == 0 {
				return mcp.NewToolResultError(fmt.Sprintf("File '%s' not found in dataset '%s'", filepath, dataset)), nil
			}

			fullText := []rune(sb.String())
			totalLen := len(fullText)

			if offset < 0 {
				offset = 0
			}
			if offset >= totalLen {
				return mcp.NewToolResultError(fmt.Sprintf("Offset %d is beyond the file length of %d characters.", offset, totalLen)), nil
			}

			end := offset + limit
			if end > totalLen {
				end = totalLen
			}

			chunk := string(fullText[offset:end])
			var header, footer string

			if end < totalLen {
				header = fmt.Sprintf("--- File: %s | Showing chars %d to %d (Total: %d) ---\n", filepath, offset, end, totalLen)
				footer = fmt.Sprintf("\n--- (File truncated. To read more, call read_doc again with offset=%d) ---", end)
			} else {
				header = fmt.Sprintf("--- File: %s ---\n", filepath)
			}

			return mcp.NewToolResultText(header + chunk + footer), nil
		},
	)

	return mcpSrv
}
