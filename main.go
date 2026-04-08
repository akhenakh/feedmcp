package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/server"
)

// corsMiddleware adds CORS headers so web-based MCP clients (like the MCP Inspector)
// can connect to the server without triggering NetworkErrors.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "ingest":
		ingestCmd := flag.NewFlagSet("ingest", flag.ExitOnError)
		dbPath := ingestCmd.String("db", "docs.sqlite", "Path to the SQLite index database")
		ingestCmd.Parse(os.Args[2:])

		if ingestCmd.NArg() < 1 {
			log.Fatal("Usage: docs-mcp ingest [--db path] <directory>")
		}
		dirPath := ingestCmd.Arg(0)

		db, err := initDB(*dbPath)
		if err != nil {
			log.Fatalf("Failed to initialize database: %v", err)
		}
		defer db.Close()

		log.Printf("Starting ingestion from directory: %s", dirPath)
		if err := ingestDirectory(db, dirPath); err != nil {
			log.Fatalf("Ingestion failed: %v", err)
		}
		log.Println("Ingestion complete.")

	case "serve":
		serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
		dbPath := serveCmd.String("db", "docs.sqlite", "Path to the SQLite index database")
		transport := serveCmd.String("transport", "stdio", "Transport type: 'stdio', 'sse', or 'streamable'")
		port := serveCmd.Int("port", 8080, "Port for HTTP server (if transport is sse or streamable)")
		serveCmd.Parse(os.Args[2:])

		db, err := initDB(*dbPath)
		if err != nil {
			log.Fatalf("Failed to initialize database: %v", err)
		}
		defer db.Close()

		// Get configured server without attaching a transport yet
		mcpSrv := setupMCPServer(db)

		// Always route logs to stderr so JSON-RPC via stdout (stdio) isn't corrupted
		log.SetOutput(os.Stderr)

		if *transport == "streamable" {
			addr := fmt.Sprintf(":%d", *port)
			log.Printf("Starting MCP Server over Streamable HTTP on %s...", addr)
			log.Printf("Endpoint URL: http://localhost%s/mcp", addr)

			// Create Streamable HTTP Server and wrap it with CORS middleware
			streamableServer := server.NewStreamableHTTPServer(mcpSrv)
			if err := http.ListenAndServe(addr, corsMiddleware(streamableServer)); err != nil {
				log.Fatalf("Streamable HTTP Server error: %v", err)
			}

		} else if *transport == "sse" {
			addr := fmt.Sprintf(":%d", *port)
			log.Printf("Starting MCP Server over Classic SSE on %s...", addr)
			log.Printf("SSE URL: http://localhost%s/sse", addr)

			// Create Classic SSE Server and wrap it with CORS middleware
			sseServer := server.NewSSEServer(mcpSrv)
			if err := http.ListenAndServe(addr, corsMiddleware(sseServer)); err != nil {
				log.Fatalf("SSE Server error: %v", err)
			}

		} else {
			// Start standard I/O server
			log.Println("Starting MCP Server over Stdio...")
			if err := server.ServeStdio(mcpSrv); err != nil {
				log.Fatalf("Stdio Server error: %v", err)
			}
		}

	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Docs RAG MCP Server
Usage: docs-mcp <command> [options]

Commands:
  ingest     Parse .zstd files in directory and insert into SQLite FTS index.
             Usage: docs-mcp ingest [--db path] <directory>

  serve      Start the MCP Server.
             Usage: docs-mcp serve [--db path] [--transport stdio|sse|streamable] [--port 8080]

Examples:
  docs-mcp serve --db my-index.db --transport streamable --port 8080
  docs-mcp serve --db my-index.db --transport sse --port 3000
  docs-mcp serve --db my-index.db --transport stdio
`)
}
