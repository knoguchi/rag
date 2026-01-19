#!/usr/bin/env node
/**
 * RAG Crawler - Smart web crawler for document ingestion
 *
 * Features:
 * - Converts HTML to Markdown preserving structure
 * - Maintains heading hierarchy for semantic chunking
 * - Preserves tables, code blocks, and lists
 * - Extracts metadata (title, description, breadcrumbs)
 *
 * Usage:
 *   node crawl.js --tenant-id <id> --url <start-url> [options]
 *
 * Options:
 *   --tenant-id      Required. Tenant ID for document ingestion
 *   --url            Required. Starting URL to crawl
 *   --max-depth      Max crawl depth (default: 3)
 *   --max-pages      Max pages to crawl (default: 100)
 *   --include        Glob pattern for URLs to include (can specify multiple)
 *   --exclude        Glob pattern for URLs to exclude (can specify multiple)
 *   --rag-url        RAG service URL (default: http://localhost:8080)
 *   --delay          Delay between requests in ms (default: 1000)
 *   --dry-run        Don't actually ingest, just print URLs
 *   --debug          Show converted markdown for debugging
 *
 * Examples:
 *   node crawl.js --tenant-id abc123 --url https://docs.example.com \
 *     --include "/docs/**" --exclude "/docs/old/**" --max-pages 50
 */

import { PlaywrightCrawler, Configuration } from 'crawlee';
import { parseArgs } from 'node:util';
import { htmlToMarkdown, extractMetadata, findMainContent } from './html-to-markdown.js';

// Parse command line arguments
const { values: args } = parseArgs({
  options: {
    'tenant-id': { type: 'string' },
    'url': { type: 'string' },
    'max-depth': { type: 'string', default: '3' },
    'max-pages': { type: 'string', default: '100' },
    'include': { type: 'string', multiple: true, default: [] },
    'exclude': { type: 'string', multiple: true, default: [] },
    'rag-url': { type: 'string', default: 'http://localhost:8080' },
    'delay': { type: 'string', default: '1000' },
    'dry-run': { type: 'boolean', default: false },
    'debug': { type: 'boolean', default: false },
    'help': { type: 'boolean', default: false },
  },
});

if (args.help) {
  console.log(`
RAG Crawler - Smart web crawler for document ingestion

Features:
- Converts HTML to Markdown preserving structure
- Maintains heading hierarchy for semantic chunking
- Preserves tables, code blocks, and lists
- Extracts metadata (title, description, breadcrumbs)

Usage:
  node crawl.js --tenant-id <id> --url <start-url> [options]

Options:
  --tenant-id      Required. Tenant ID for document ingestion
  --url            Required. Starting URL to crawl
  --max-depth      Max crawl depth (default: 3)
  --max-pages      Max pages to crawl (default: 100)
  --include        Glob pattern for URLs to include (can specify multiple)
  --exclude        Glob pattern for URLs to exclude (can specify multiple)
  --rag-url        RAG service URL (default: http://localhost:8080)
  --delay          Delay between requests in ms (default: 1000)
  --dry-run        Don't actually ingest, just print URLs and markdown
  --debug          Show converted markdown for debugging
  --help           Show this help message

Examples:
  node crawl.js --tenant-id abc123 --url https://docs.example.com \\
    --include "/docs/**" --exclude "/docs/old/**" --max-pages 50
`);
  process.exit(0);
}

// Validate required args
if (!args['tenant-id'] || !args['url']) {
  console.error('Error: --tenant-id and --url are required');
  console.error('Use --help for usage information');
  process.exit(1);
}

const config = {
  tenantId: args['tenant-id'],
  startUrl: args['url'],
  maxDepth: parseInt(args['max-depth'], 10),
  maxPages: parseInt(args['max-pages'], 10),
  includePatterns: args['include'],
  excludePatterns: args['exclude'],
  ragUrl: args['rag-url'],
  delayMs: parseInt(args['delay'], 10),
  dryRun: args['dry-run'],
  debug: args['debug'],
};

// Extract domain from start URL for domain restriction
const startUrlObj = new URL(config.startUrl);
const allowedDomain = startUrlObj.hostname;

// Build glob patterns for enqueueLinks
function buildGlobs() {
  const globs = [];

  // If include patterns specified, use them
  if (config.includePatterns.length > 0) {
    for (const pattern of config.includePatterns) {
      let fullPattern;
      // Convert relative pattern to absolute URL pattern
      if (pattern.startsWith('/')) {
        fullPattern = `${startUrlObj.origin}${pattern}`;
      } else if (pattern.startsWith('http')) {
        fullPattern = pattern;
      } else {
        fullPattern = `${startUrlObj.origin}/${pattern}`;
      }

      // If pattern is an exact file (not a glob), convert to glob
      // e.g., /pricing.html -> **/pricing.html to match the URL properly
      if (!fullPattern.includes('*') && !fullPattern.includes('?')) {
        // Extract the filename/path after origin
        const pathPart = fullPattern.replace(startUrlObj.origin, '');
        // Add both exact match and glob pattern for flexibility
        globs.push(fullPattern);
        globs.push(`${startUrlObj.origin}/**${pathPart}`);
      } else {
        globs.push(fullPattern);
      }
    }
  } else {
    // Default: all pages on the same domain
    globs.push(`${startUrlObj.origin}/**`);
  }

  return globs;
}

function buildExcludeGlobs() {
  const globs = [];

  for (const pattern of config.excludePatterns) {
    if (pattern.startsWith('/')) {
      globs.push(`${startUrlObj.origin}${pattern}`);
    } else if (pattern.startsWith('http')) {
      globs.push(pattern);
    } else {
      globs.push(`${startUrlObj.origin}/${pattern}`);
    }
  }

  // Common excludes
  globs.push('**/*.pdf');
  globs.push('**/*.zip');
  globs.push('**/*.tar.gz');
  globs.push('**/login*');
  globs.push('**/signin*');
  globs.push('**/signup*');
  globs.push('**/logout*');
  globs.push('**/admin/**');

  return globs;
}

// Statistics
const stats = {
  crawled: 0,
  ingested: 0,
  failed: 0,
  skipped: 0,
};

// Ingest document to RAG service
async function ingestDocument(url, title, content, metadata = {}) {
  if (config.dryRun) {
    console.log(`\n[DRY RUN] Would ingest: ${url}`);
    console.log(`Title: ${title}`);
    console.log(`Content length: ${content.length} chars`);
    if (config.debug) {
      console.log('\n--- Markdown Preview (first 2000 chars) ---');
      console.log(content.substring(0, 2000));
      console.log('--- End Preview ---\n');
    }
    stats.ingested++;
    return;
  }

  try {
    const response = await fetch(`${config.ragUrl}/v1/documents/ingest`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        tenant_id: config.tenantId,
        content: content,
        title: title,
        source: url,
        metadata: metadata,
      }),
    });

    if (response.ok) {
      const result = await response.json();
      console.log(`✓ Ingested: ${url} (doc: ${result.documentId || result.document_id || 'unknown'})`);
      stats.ingested++;
    } else {
      const error = await response.text();
      console.error(`✗ Failed to ingest ${url}: ${response.status} - ${error}`);
      stats.failed++;
    }
  } catch (error) {
    console.error(`✗ Error ingesting ${url}: ${error.message}`);
    stats.failed++;
  }
}

// Main crawler
async function main() {
  console.log('=== RAG Smart Crawler ===');
  console.log(`Tenant ID: ${config.tenantId}`);
  console.log(`Start URL: ${config.startUrl}`);
  console.log(`Max Depth: ${config.maxDepth}`);
  console.log(`Max Pages: ${config.maxPages}`);
  console.log(`Include patterns: ${config.includePatterns.length > 0 ? config.includePatterns.join(', ') : '(all)'}`);
  console.log(`Exclude patterns: ${config.excludePatterns.length > 0 ? config.excludePatterns.join(', ') : '(defaults only)'}`);
  console.log(`RAG URL: ${config.ragUrl}`);
  console.log(`Delay: ${config.delayMs}ms`);
  console.log(`Dry run: ${config.dryRun}`);
  console.log(`Debug: ${config.debug}`);
  console.log('');

  const includeGlobs = buildGlobs();
  const excludeGlobs = buildExcludeGlobs();

  console.log('Include globs:', includeGlobs);
  console.log('Exclude globs:', excludeGlobs);
  console.log('');

  // Configure Crawlee to not persist state (run fresh each time)
  Configuration.getGlobalConfig().set('persistStorage', false);

  const crawler = new PlaywrightCrawler({
    // Limit concurrent requests
    maxConcurrency: 3,

    // Request delay
    minConcurrency: 1,

    // Max requests
    maxRequestsPerCrawl: config.maxPages,

    // Browser options for stealth
    launchContext: {
      launchOptions: {
        headless: true,
        args: [
          '--disable-blink-features=AutomationControlled',
          '--disable-dev-shm-usage',
          '--no-sandbox',
        ],
      },
    },

    // Pre-navigation hook for stealth
    preNavigationHooks: [
      async ({ page }) => {
        // Override navigator.webdriver
        await page.addInitScript(() => {
          Object.defineProperty(navigator, 'webdriver', {
            get: () => false,
          });

          // Add fake plugins
          Object.defineProperty(navigator, 'plugins', {
            get: () => [1, 2, 3, 4, 5],
          });

          // Add fake languages
          Object.defineProperty(navigator, 'languages', {
            get: () => ['en-US', 'en'],
          });
        });
      },
    ],

    // Request handler
    async requestHandler({ request, page, enqueueLinks, log }) {
      const url = request.url;
      stats.crawled++;

      log.info(`Crawling [${stats.crawled}/${config.maxPages}]: ${url}`);

      // Wait for page to be fully loaded
      await page.waitForLoadState('networkidle', { timeout: 30000 }).catch(() => {});

      // IMPORTANT: Enqueue links BEFORE modifying the DOM (content extraction removes nav elements)
      await enqueueLinks({
        globs: includeGlobs,
        exclude: excludeGlobs,
        transformRequestFunction: (req) => {
          // Limit crawl depth
          if (request.userData.depth >= config.maxDepth) {
            return false;
          }
          req.userData.depth = (request.userData.depth || 0) + 1;
          return req;
        },
      });

      // Extract content using smart HTML-to-Markdown conversion
      const result = await page.evaluate(({ htmlToMarkdownCode, extractMetadataCode, findMainContentCode }) => {
        // Recreate the functions in browser context
        const evalCode = new Function('return ' + htmlToMarkdownCode)();
        const HtmlToMarkdownClass = evalCode;

        // Simple version of extractMetadata for browser context
        function extractMetadata(doc) {
          const metadata = {};

          const title = doc.querySelector('title');
          if (title) metadata.title = title.textContent.trim();

          const description = doc.querySelector('meta[name="description"]');
          if (description) metadata.description = description.getAttribute('content');

          const canonical = doc.querySelector('link[rel="canonical"]');
          if (canonical) metadata.canonical = canonical.getAttribute('href');

          // Breadcrumbs
          const breadcrumbs = [];
          const breadcrumbNav = doc.querySelector('[aria-label="breadcrumb"], .breadcrumb, .breadcrumbs');
          if (breadcrumbNav) {
            const items = breadcrumbNav.querySelectorAll('a, span');
            items.forEach(item => {
              const text = item.textContent.trim();
              if (text && text !== '>' && text !== '/') breadcrumbs.push(text);
            });
          }
          if (breadcrumbs.length > 0) metadata.breadcrumbs = breadcrumbs;

          return metadata;
        }

        // Find main content
        function findMainContent(doc) {
          const selectors = [
            'main', 'article', '[role="main"]',
            '.doc-content', '.documentation', '.content', '.main-content',
            '.article-content', '.post-content', '.entry-content',
            '#content', '#main', '#main-content', '#documentation',
          ];

          for (const selector of selectors) {
            const element = doc.querySelector(selector);
            if (element) {
              const text = element.textContent || '';
              if (text.trim().split(/\s+/).length > 50) {
                return element;
              }
            }
          }
          return doc.body;
        }

        // Remove unwanted elements
        const elementsToRemove = document.querySelectorAll(
          'script, style, noscript, iframe, svg, canvas, video, audio, ' +
          'nav, footer, header, aside, ' +
          '[role="navigation"], [role="banner"], [role="contentinfo"], ' +
          '.sidebar, .nav, .navigation, .menu, .footer, .header, ' +
          '.advertisement, .ad, .ads, .social-share, .comments'
        );
        elementsToRemove.forEach(el => el.remove());

        // Extract metadata
        const metadata = extractMetadata(document);

        // Find main content
        const mainContent = findMainContent(document);

        // Get title with fallbacks
        const title = metadata.title ||
                     document.querySelector('h1')?.textContent?.trim() ||
                     'Untitled';

        return {
          title,
          metadata,
          // Return the HTML of main content for conversion
          html: mainContent.innerHTML,
          baseUrl: document.baseURI,
        };
      }, {
        // We can't pass functions directly, so we'll do conversion here
        htmlToMarkdownCode: '',
        extractMetadataCode: '',
        findMainContentCode: '',
      });

      // Convert HTML to Markdown in Node.js context using JSDOM-like parsing
      // Since we can't use the browser functions directly, we'll do it with a simpler approach
      const markdown = await page.evaluate(() => {
        // Smart HTML to Markdown converter (inline version for browser)
        function convertToMarkdown(element, baseUrl = '') {
          const output = [];
          const listStack = [];

          function processNode(node) {
            if (!node) return;

            if (node.nodeType === 3) { // Text
              let text = node.textContent || '';
              text = text.replace(/[\r\n\t]+/g, ' ');
              if (text.trim()) output.push(text);
              else if (text.includes(' ') && output.length > 0) {
                const last = output[output.length - 1];
                if (last && !last.endsWith(' ') && !last.endsWith('\n')) {
                  output.push(' ');
                }
              }
              return;
            }

            if (node.nodeType !== 1) return;

            const tag = node.tagName?.toLowerCase();

            // Skip hidden
            const style = node.getAttribute?.('style') || '';
            if (node.getAttribute?.('hidden') !== null ||
                node.getAttribute?.('aria-hidden') === 'true' ||
                style.includes('display: none') ||
                style.includes('display:none')) return;

            switch (tag) {
              case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': {
                const level = parseInt(tag[1]);
                const prefix = '#'.repeat(level);
                const text = getTextContent(node).trim();
                if (text) output.push(`\n\n${prefix} ${text}\n\n`);
                break;
              }

              case 'p': {
                const content = getTextContent(node).trim();
                if (content) output.push(`\n\n${content}\n\n`);
                break;
              }

              case 'ul': {
                listStack.push({ type: 'ul', index: 0 });
                output.push('\n');
                processChildren(node);
                output.push('\n');
                listStack.pop();
                break;
              }

              case 'ol': {
                listStack.push({ type: 'ol', index: 0 });
                output.push('\n');
                processChildren(node);
                output.push('\n');
                listStack.pop();
                break;
              }

              case 'li': {
                const listInfo = listStack[listStack.length - 1];
                if (!listInfo) { processChildren(node); break; }

                const indent = '  '.repeat(listStack.length - 1);
                let marker;
                if (listInfo.type === 'ol') {
                  listInfo.index++;
                  marker = `${listInfo.index}.`;
                } else {
                  marker = '-';
                }

                const content = getTextContent(node).trim();
                if (content) {
                  const lines = content.split('\n');
                  output.push(`${indent}${marker} ${lines[0]}\n`);
                  for (let i = 1; i < lines.length; i++) {
                    if (lines[i].trim()) output.push(`${indent}  ${lines[i]}\n`);
                  }
                }
                break;
              }

              case 'table': {
                const rows = [];
                const headerRows = [];

                node.querySelectorAll('tr').forEach((tr, rowIndex) => {
                  const cells = [];
                  tr.querySelectorAll('th, td').forEach(cell => {
                    const text = getTextContent(cell).trim().replace(/\|/g, '\\|').replace(/\n/g, ' ');
                    cells.push(text);
                  });

                  if (cells.length > 0) {
                    const isHeader = tr.querySelector('th') !== null ||
                                    tr.closest('thead') !== null ||
                                    (rowIndex === 0 && rows.length === 0);

                    if (isHeader && headerRows.length === 0) {
                      headerRows.push(cells);
                    } else {
                      rows.push(cells);
                    }
                  }
                });

                if (headerRows.length === 0 && rows.length === 0) break;

                const colCount = Math.max(
                  ...headerRows.map(r => r.length),
                  ...rows.map(r => r.length)
                );

                const normalizeRow = (row) => {
                  while (row.length < colCount) row.push('');
                  return row;
                };

                output.push('\n\n');

                if (headerRows.length > 0) {
                  const header = normalizeRow(headerRows[0]);
                  output.push(`| ${header.join(' | ')} |\n`);
                  output.push(`| ${header.map(() => '---').join(' | ')} |\n`);
                } else if (rows.length > 0) {
                  const header = normalizeRow(rows.shift());
                  output.push(`| ${header.join(' | ')} |\n`);
                  output.push(`| ${header.map(() => '---').join(' | ')} |\n`);
                }

                rows.forEach(row => {
                  const normalized = normalizeRow(row);
                  output.push(`| ${normalized.join(' | ')} |\n`);
                });

                output.push('\n');
                break;
              }

              case 'pre': {
                const codeEl = node.querySelector('code');
                const codeNode = codeEl || node;
                let code = codeNode.textContent || '';

                // Detect language
                let lang = '';
                const className = (codeNode.getAttribute?.('class') || '') +
                                 (node.getAttribute?.('class') || '');
                const langMatch = className.match(/(?:language-|lang-|highlight-)(\w+)/i);
                if (langMatch) lang = langMatch[1].toLowerCase();

                // Guess from content
                if (!lang) {
                  const trimmed = code.trim();
                  if (trimmed.startsWith('$') || trimmed.startsWith('#!') ||
                      /^(curl|npm|yarn|pip|apt|brew|docker|git|kubectl|demo)\s/.test(trimmed)) {
                    lang = 'bash';
                  } else if ((trimmed.startsWith('{') && trimmed.endsWith('}')) ||
                            (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
                    try { JSON.parse(trimmed); lang = 'json'; } catch {}
                  } else if (/^(SELECT|INSERT|UPDATE|DELETE|CREATE)\s/i.test(trimmed)) {
                    lang = 'sql';
                  }
                }

                code = code.replace(/^\n+/, '').replace(/\n+$/, '');
                output.push(`\n\n\`\`\`${lang}\n${code}\n\`\`\`\n\n`);
                break;
              }

              case 'code': {
                if (node.parentElement?.tagName?.toLowerCase() !== 'pre') {
                  const text = node.textContent || '';
                  if (text) output.push(`\`${text}\``);
                }
                break;
              }

              case 'strong': case 'b': {
                const content = getTextContent(node).trim();
                if (content) output.push(`**${content}**`);
                break;
              }

              case 'em': case 'i': {
                const content = getTextContent(node).trim();
                if (content) output.push(`*${content}*`);
                break;
              }

              case 'a': {
                let href = node.getAttribute?.('href') || '';
                if (!href || href === '#') { processChildren(node); break; }

                if (baseUrl && !href.startsWith('http') && !href.startsWith('mailto:')) {
                  try { href = new URL(href, baseUrl).href; } catch {}
                }

                const text = getTextContent(node).trim();
                if (text) output.push(`[${text}](${href})`);
                break;
              }

              case 'br':
                output.push('\n');
                break;

              case 'hr':
                output.push('\n\n---\n\n');
                break;

              case 'blockquote': {
                const content = getTextContent(node).trim();
                if (content) {
                  const lines = content.split('\n').map(l => `> ${l}`).join('\n');
                  output.push(`\n\n${lines}\n\n`);
                }
                break;
              }

              case 'script': case 'style': case 'noscript': case 'iframe':
              case 'svg': case 'canvas': case 'video': case 'audio':
              case 'nav': case 'footer': case 'header': case 'aside':
              case 'form': case 'input': case 'button': case 'select':
              case 'textarea':
                break;

              default:
                processChildren(node);
            }
          }

          function processChildren(node) {
            if (!node.childNodes) return;
            for (const child of node.childNodes) {
              processNode(child);
            }
          }

          function getTextContent(node) {
            const saved = [...output];
            output.length = 0;
            processChildren(node);
            const content = output.join('');
            output.length = 0;
            output.push(...saved);
            return content;
          }

          processNode(element);

          return output.join('')
            .replace(/\n{3,}/g, '\n\n')
            .trim() + '\n';
        }

        // Remove unwanted elements first
        const elementsToRemove = document.querySelectorAll(
          'script, style, noscript, iframe, svg, canvas, video, audio, ' +
          'nav, footer, header, aside, ' +
          '[role="navigation"], [role="banner"], [role="contentinfo"], ' +
          '.sidebar, .nav, .navigation, .menu, .footer, .header, ' +
          '.advertisement, .ad, .ads, .social-share, .comments'
        );
        elementsToRemove.forEach(el => el.remove());

        // Find main content
        const selectors = [
          'main', 'article', '[role="main"]',
          '.doc-content', '.documentation', '.content', '.main-content',
          '.article-content', '.post-content', '.entry-content',
          '#content', '#main', '#main-content', '#documentation',
        ];

        let mainContent = null;
        for (const selector of selectors) {
          const element = document.querySelector(selector);
          if (element) {
            const text = element.textContent || '';
            if (text.trim().split(/\s+/).length > 50) {
              mainContent = element;
              break;
            }
          }
        }
        mainContent = mainContent || document.body;

        // Get title
        const title = document.querySelector('title')?.textContent?.trim() ||
                     document.querySelector('h1')?.textContent?.trim() ||
                     'Untitled';

        // Get metadata
        const description = document.querySelector('meta[name="description"]')?.getAttribute('content') || '';

        // Convert to markdown
        const markdown = convertToMarkdown(mainContent, document.baseURI);

        return {
          title,
          description,
          markdown,
          wordCount: markdown.split(/\s+/).length,
        };
      });

      // Skip if content is too small
      if (markdown.wordCount < 30) {
        log.info(`Skipping ${url} - content too small (${markdown.wordCount} words)`);
        stats.skipped++;
      } else {
        // Debug output
        if (config.debug) {
          console.log(`\n--- ${url} ---`);
          console.log(`Title: ${markdown.title}`);
          console.log(`Words: ${markdown.wordCount}`);
          console.log('\n' + markdown.markdown.substring(0, 1500) + '...\n');
        }

        // Ingest the document with metadata
        await ingestDocument(url, markdown.title, markdown.markdown, {
          description: markdown.description,
          word_count: String(markdown.wordCount),
        });
      }

      // Add delay between requests
      await new Promise(resolve => setTimeout(resolve, config.delayMs));
    },

    // Error handler
    async failedRequestHandler({ request, log }, error) {
      log.error(`Failed to crawl ${request.url}: ${error.message}`);
      stats.failed++;
    },
  });

  // Run the crawler
  await crawler.run([{
    url: config.startUrl,
    userData: { depth: 0 },
  }]);

  // Print summary
  console.log('');
  console.log('=== Crawl Complete ===');
  console.log(`Pages crawled: ${stats.crawled}`);
  console.log(`Documents ingested: ${stats.ingested}`);
  console.log(`Failed: ${stats.failed}`);
  console.log(`Skipped (too small): ${stats.skipped}`);
}

main().catch(error => {
  console.error('Crawler error:', error);
  process.exit(1);
});
