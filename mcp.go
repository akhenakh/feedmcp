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
	mcpSrv := server.NewMCPServer("docs-mcp-server", "1.0.0")

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
			mcp.WithDescription("Search documentation using BM25 full-text search. Returns file snippets and paths. Use this to find which documents contain the information you need."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Keywords to search for. Keep it concise (e.g. 'caddy reverse proxy').")),
			mcp.WithString("dataset", mcp.Description("Optional: Narrow search to a specific dataset (e.g. 'caddy-main').")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query := request.GetString("query", "")
			dataset := request.GetString("dataset", "")

			results, err := searchDocs(db, query, dataset, 15)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if len(results) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf("No results found for '%s'.", query)), nil
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Top results for '%s':\n\n", query))
			for i, r := range results {
				sb.WriteString(fmt.Sprintf("%d. Dataset: %s | File: %s (Chunk %d)\n", i+1, r.Dataset, r.Filepath, r.Seq))
				sb.WriteString(fmt.Sprintf("   Match: %s\n\n", r.Snippet))
			}
			sb.WriteString("To read the full context, use the 'read_doc' tool with the specific Dataset and Filepath.")

			return mcp.NewToolResultText(sb.String()), nil
		},
	)

	// TOOL 3: Read Document
	mcpSrv.AddTool(
		mcp.NewTool("read_doc",
			mcp.WithDescription("Read the complete content of a specific file from a dataset."),
			mcp.WithString("dataset", mcp.Required(), mcp.Description("The dataset name (e.g. 'bun-v1.3.11')")),
			mcp.WithString("filepath", mcp.Required(), mcp.Description("The exact filepath as returned by search_docs")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			dataset := request.GetString("dataset", "")
			filepath := request.GetString("filepath", "")

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

			return mcp.NewToolResultText(sb.String()), nil
		},
	)

	return mcpSrv
}
