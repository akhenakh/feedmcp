# Feedmcp

`feedmcp` is a Model Context Protocol (MCP) server designed to supercharge Large Language Models (LLMs) with your offline documentation using an Advanced RAG Pipeline natively entirely via local SQLite operations.

## Advanced Local RAG Architecture

Feedmcp natively incorporates advanced context strategies without the overhead of external Vector DBs, optimized specifically to keep LLM context windows clean and relevant:

- **Contextual Chunk Headers (CCH):** Data is grounded intelligently; before chunks are embedded they are dynamically prepended with markdown hierarchies and document descriptors.
- **Relevant Segment Extraction (RSE):** Reconstructs contiguous data chunks logically. If multiple relevant matches occur sequentially in the same file, the server merges them into a single, seamless semantic document.
- **Payload Truncation Safeguards:** Automatically caps oversized text segments to ensure the LLM's context window isn't hijacked by a single massive file, prompting the agent to paginate using the `read_doc` tool instead.
- **Agentic Query Support:** Implements built-in tool prompts instructing connected LLMs directly to employ techniques like *Hypothetical Document Embeddings (HyDE)* and dynamic *Query Reformulation* workflows automatically.
- **Natural Language Reliability:** Best run natively with Grounded Language Models to ensure hallucinations are inherently reduced via verbatim context-streaming directly from `.zstd` offline files.

## Features

- **Direct ZSTD Support**: Reads massively compressed `.md.zstd` archives, format used by [feedai](https://github.com/akhenakh/feedai).
- **Smart Semantic Chunking**: Uses LangChainGo to intelligently split long markdown files by nested headers and size.
- **Multi-Transport**: Runs over `stdio`, `sse`, or `streamable`.
- **CORS Enabled**: Out-of-the-box support for web-based MCP inspectors.

## Installation & Build

```bash
# Clone your repository
git clone https://github.com/akhenakh/feedmcp.git
cd feedmcp

# Build with the FTS5 tags enabled (REQUIRED)
go build -tags sqlite_fts5 -o feedmcp .
```

## Quick Start Workflow

### Ingest Documentation

```bash
feedmcp ingest ~/dev/feedai/docs/
```

### Start the Server

**For Claude Desktop or CLI Agents (Stdio):**
```bash
feedmcp serve --db docs.sqlite --transport stdio
```

**For Web Clients, Cursor, or Remote Agents (Streamable HTTP / SSE):**
```bash
# Modern Streamable HTTP (available at http://localhost:8080/mcp)
feedmcp serve --db docs.sqlite --transport streamable --port 8080
```

## Client Integration Examples

### Opencode

Edit `~/.config/opencode/opencode.json`:

```yaml
  "mcp": {
    "feedmcp": {
      "type": "local",
      "command": ["feedmcp", "serve", "--db", "/home/youruser/dev/feedmcp/docs.sqlite", "--transport", "stdio"]
    }
  }
```

## License
MIT
