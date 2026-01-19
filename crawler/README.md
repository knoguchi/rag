# RAG Crawler

A Crawlee-based web crawler for ingesting documents into the RAG service. This crawler uses Playwright (headless Chrome) to render JavaScript-heavy pages and extract content.

## Why Crawlee?

- **Built-in URL filtering** with glob patterns via `enqueueLinks({ globs: [...] })`
- **Automatic request queuing** and deduplication
- **Stealth mode** support to avoid bot detection
- **Playwright integration** for JavaScript rendering
- **Battle-tested** by Apify for production web scraping

## Installation

```bash
cd tools/crawler
npm install
```

This will install:
- `crawlee` - The crawling framework
- `playwright` - Browser automation

Playwright will automatically download Chromium on first run.

## Usage

```bash
node crawl.js --tenant-id <id> --url <start-url> [options]
```

### Required Options

| Option | Description |
|--------|-------------|
| `--tenant-id` | Your RAG tenant ID |
| `--url` | Starting URL to crawl |

### Optional Options

| Option | Default | Description |
|--------|---------|-------------|
| `--max-depth` | 3 | Maximum crawl depth from start URL |
| `--max-pages` | 100 | Maximum number of pages to crawl |
| `--include` | (all) | Glob pattern for URLs to include (can specify multiple) |
| `--exclude` | (common) | Glob pattern for URLs to exclude (can specify multiple) |
| `--rag-url` | http://localhost:8080 | RAG service URL |
| `--delay` | 1000 | Delay between requests in milliseconds |
| `--dry-run` | false | Print URLs without ingesting |

### Examples

**Basic crawl:**
```bash
node crawl.js \
  --tenant-id a68cb506-9e74-4e23-8cdc-6502d27cc3a1 \
  --url https://docs.example.com
```

**Crawl with URL filtering:**
```bash
node crawl.js \
  --tenant-id a68cb506-9e74-4e23-8cdc-6502d27cc3a1 \
  --url https://clickhouse.com/docs/sql-reference/functions/string-functions \
  --include "/docs/sql-reference/**" \
  --exclude "/docs/sql-reference/old/**" \
  --max-pages 50
```

**Dry run to see what would be crawled:**
```bash
node crawl.js \
  --tenant-id abc123 \
  --url https://docs.example.com \
  --dry-run
```

**Multiple include patterns:**
```bash
node crawl.js \
  --tenant-id abc123 \
  --url https://example.com \
  --include "/docs/**" \
  --include "/guides/**" \
  --include "/api/**"
```

## Glob Pattern Syntax

The crawler uses glob patterns for URL filtering:

| Pattern | Matches |
|---------|---------|
| `/docs/**` | All URLs under /docs/ |
| `/docs/*.html` | HTML files directly in /docs/ |
| `/api/v?/**` | /api/v1/, /api/v2/, etc. |
| `**/reference/**` | Any URL containing /reference/ |

## Default Exclusions

The crawler automatically excludes:
- PDF, ZIP, and archive files
- Login/signup/logout pages
- Admin pages

## Stealth Features

The crawler includes basic stealth measures:
- Disables `navigator.webdriver` detection
- Adds fake browser plugins
- Uses standard browser headers
- Respects delays between requests

## Running from Tenant Infrastructure

For production use, tenants should run this crawler from their own infrastructure to:
1. Avoid IP reputation issues with the RAG service
2. Maintain control over crawl frequency and scope
3. Handle authentication if needed

```bash
# From tenant's server
RAG_URL=https://rag.yourservice.com node crawl.js \
  --tenant-id $TENANT_ID \
  --url https://their-docs.com \
  --rag-url $RAG_URL
```

## Troubleshooting

### Playwright browser not found
```bash
npx playwright install chromium
```

### Permission denied on Linux
```bash
npx playwright install-deps
```

### Rate limiting / 429 errors
Increase the delay:
```bash
node crawl.js --delay 3000 ...
```

### Content not extracted (JavaScript-heavy site)
The crawler waits for `networkidle` state, but some SPAs may need longer. You can modify the `waitForLoadState` timeout in `crawl.js`.
