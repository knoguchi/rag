/**
 * Smart HTML to Markdown converter optimized for RAG ingestion
 *
 * Preserves:
 * - Heading hierarchy (h1-h6 -> # to ######)
 * - Tables as markdown tables
 * - Code blocks with language hints
 * - Lists (ordered and unordered)
 * - Links and emphasis
 * - Semantic structure for better chunking
 */

/**
 * Convert HTML element to Markdown
 * @param {Element} element - DOM element to convert
 * @param {Object} options - Conversion options
 * @returns {string} Markdown content
 */
export function htmlToMarkdown(element, options = {}) {
  const converter = new HtmlToMarkdownConverter(options);
  return converter.convert(element);
}

class HtmlToMarkdownConverter {
  constructor(options = {}) {
    this.options = {
      // Include metadata comments for debugging
      includeMetadata: options.includeMetadata ?? false,
      // Preserve data attributes as metadata
      extractDataAttributes: options.extractDataAttributes ?? true,
      // Code language detection
      detectCodeLanguage: options.detectCodeLanguage ?? true,
      // Base URL for resolving relative links
      baseUrl: options.baseUrl ?? '',
      ...options,
    };

    this.output = [];
    this.listStack = []; // Track nested list state
    this.codeBlockLanguages = new Map([
      ['javascript', ['js', 'javascript', 'node', 'nodejs']],
      ['typescript', ['ts', 'typescript']],
      ['python', ['py', 'python', 'python3']],
      ['bash', ['sh', 'bash', 'shell', 'zsh', 'terminal', 'console']],
      ['sql', ['sql', 'mysql', 'postgresql', 'postgres']],
      ['json', ['json']],
      ['yaml', ['yaml', 'yml']],
      ['html', ['html', 'htm']],
      ['css', ['css', 'scss', 'sass', 'less']],
      ['go', ['go', 'golang']],
      ['rust', ['rust', 'rs']],
      ['java', ['java']],
      ['ruby', ['ruby', 'rb']],
      ['php', ['php']],
      ['csharp', ['csharp', 'cs', 'c#']],
      ['cpp', ['cpp', 'c++', 'cc']],
      ['c', ['c']],
    ]);
  }

  convert(element) {
    this.output = [];
    this.processNode(element);
    return this.cleanup(this.output.join(''));
  }

  processNode(node) {
    if (!node) return;

    if (node.nodeType === 3) { // Text node
      this.processText(node);
      return;
    }

    if (node.nodeType !== 1) return; // Only process element nodes

    const tagName = node.tagName?.toLowerCase();

    // Skip hidden elements
    if (this.isHidden(node)) return;

    switch (tagName) {
      // Headings
      case 'h1':
      case 'h2':
      case 'h3':
      case 'h4':
      case 'h5':
      case 'h6':
        this.processHeading(node, parseInt(tagName[1]));
        break;

      // Paragraphs and divs
      case 'p':
        this.processParagraph(node);
        break;
      case 'div':
      case 'section':
      case 'article':
      case 'main':
        this.processBlock(node);
        break;

      // Lists
      case 'ul':
        this.processList(node, 'unordered');
        break;
      case 'ol':
        this.processList(node, 'ordered');
        break;
      case 'li':
        this.processListItem(node);
        break;

      // Tables
      case 'table':
        this.processTable(node);
        break;

      // Code
      case 'pre':
        this.processPreformatted(node);
        break;
      case 'code':
        // Only process inline code here; block code handled by pre
        if (node.parentElement?.tagName?.toLowerCase() !== 'pre') {
          this.processInlineCode(node);
        }
        break;

      // Inline formatting
      case 'strong':
      case 'b':
        this.processStrong(node);
        break;
      case 'em':
      case 'i':
        this.processEmphasis(node);
        break;
      case 'a':
        this.processLink(node);
        break;

      // Line breaks
      case 'br':
        this.output.push('\n');
        break;
      case 'hr':
        this.output.push('\n\n---\n\n');
        break;

      // Blockquote
      case 'blockquote':
        this.processBlockquote(node);
        break;

      // Definition lists
      case 'dl':
        this.processDefinitionList(node);
        break;

      // Skip these elements entirely
      case 'script':
      case 'style':
      case 'noscript':
      case 'iframe':
      case 'svg':
      case 'canvas':
      case 'video':
      case 'audio':
      case 'nav':
      case 'footer':
      case 'header':
      case 'aside':
      case 'form':
      case 'input':
      case 'button':
      case 'select':
      case 'textarea':
        break;

      // Default: process children
      default:
        this.processChildren(node);
    }
  }

  processText(node) {
    let text = node.textContent || '';
    // Normalize whitespace but preserve single spaces
    text = text.replace(/[\r\n\t]+/g, ' ');
    if (text.trim()) {
      this.output.push(text);
    } else if (text.includes(' ') && this.output.length > 0) {
      // Preserve a single space between elements
      const last = this.output[this.output.length - 1];
      if (last && !last.endsWith(' ') && !last.endsWith('\n')) {
        this.output.push(' ');
      }
    }
  }

  processHeading(node, level) {
    const prefix = '#'.repeat(level);
    const text = this.getTextContent(node).trim();
    if (text) {
      this.output.push(`\n\n${prefix} ${text}\n\n`);
    }
  }

  processParagraph(node) {
    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const content = this.output.join('').trim();
    this.output = savedOutput;

    if (content) {
      this.output.push(`\n\n${content}\n\n`);
    }
  }

  processBlock(node) {
    this.processChildren(node);
  }

  processList(node, type) {
    this.listStack.push({ type, index: 0 });
    this.output.push('\n');
    this.processChildren(node);
    this.output.push('\n');
    this.listStack.pop();
  }

  processListItem(node) {
    const listInfo = this.listStack[this.listStack.length - 1];
    if (!listInfo) {
      this.processChildren(node);
      return;
    }

    const indent = '  '.repeat(this.listStack.length - 1);
    let marker;

    if (listInfo.type === 'ordered') {
      listInfo.index++;
      marker = `${listInfo.index}.`;
    } else {
      marker = '-';
    }

    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const content = this.output.join('').trim();
    this.output = savedOutput;

    if (content) {
      // Handle multi-line list items
      const lines = content.split('\n');
      this.output.push(`${indent}${marker} ${lines[0]}\n`);
      for (let i = 1; i < lines.length; i++) {
        if (lines[i].trim()) {
          this.output.push(`${indent}  ${lines[i]}\n`);
        }
      }
    }
  }

  processTable(node) {
    const rows = [];
    const headerRows = [];

    // Find all rows
    const trElements = node.querySelectorAll('tr');

    trElements.forEach((tr, rowIndex) => {
      const cells = [];
      const cellElements = tr.querySelectorAll('th, td');

      cellElements.forEach(cell => {
        const text = this.getTextContent(cell).trim().replace(/\|/g, '\\|').replace(/\n/g, ' ');
        cells.push(text);
      });

      if (cells.length > 0) {
        // Check if this is a header row (contains th or is in thead)
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

    if (headerRows.length === 0 && rows.length === 0) return;

    // Determine column count
    const colCount = Math.max(
      ...headerRows.map(r => r.length),
      ...rows.map(r => r.length)
    );

    // Normalize rows to same column count
    const normalizeRow = (row) => {
      while (row.length < colCount) row.push('');
      return row;
    };

    this.output.push('\n\n');

    // Output header
    if (headerRows.length > 0) {
      const header = normalizeRow(headerRows[0]);
      this.output.push(`| ${header.join(' | ')} |\n`);
      this.output.push(`| ${header.map(() => '---').join(' | ')} |\n`);
    } else if (rows.length > 0) {
      // Use first row as header if no explicit header
      const header = normalizeRow(rows.shift());
      this.output.push(`| ${header.join(' | ')} |\n`);
      this.output.push(`| ${header.map(() => '---').join(' | ')} |\n`);
    }

    // Output data rows
    rows.forEach(row => {
      const normalized = normalizeRow(row);
      this.output.push(`| ${normalized.join(' | ')} |\n`);
    });

    this.output.push('\n');
  }

  processPreformatted(node) {
    const codeElement = node.querySelector('code');
    const codeNode = codeElement || node;

    // Get raw text content preserving whitespace
    let code = codeNode.textContent || '';

    // Detect language from class names
    let language = this.detectLanguage(codeNode) || this.detectLanguage(node);

    // Also try to detect from content if no class hint
    if (!language) {
      language = this.guessLanguageFromContent(code);
    }

    // Clean up the code
    code = code.replace(/^\n+/, '').replace(/\n+$/, '');

    this.output.push(`\n\n\`\`\`${language}\n${code}\n\`\`\`\n\n`);
  }

  processInlineCode(node) {
    const text = node.textContent || '';
    if (text) {
      this.output.push(`\`${text}\``);
    }
  }

  processStrong(node) {
    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const content = this.output.join('');
    this.output = savedOutput;

    if (content.trim()) {
      this.output.push(`**${content.trim()}**`);
    }
  }

  processEmphasis(node) {
    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const content = this.output.join('');
    this.output = savedOutput;

    if (content.trim()) {
      this.output.push(`*${content.trim()}*`);
    }
  }

  processLink(node) {
    let href = node.getAttribute('href') || '';

    // Skip empty or anchor-only links
    if (!href || href === '#') {
      this.processChildren(node);
      return;
    }

    // Resolve relative URLs
    if (this.options.baseUrl && !href.startsWith('http') && !href.startsWith('mailto:')) {
      try {
        href = new URL(href, this.options.baseUrl).href;
      } catch (e) {
        // Keep original href if URL resolution fails
      }
    }

    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const text = this.output.join('').trim();
    this.output = savedOutput;

    if (text) {
      this.output.push(`[${text}](${href})`);
    }
  }

  processBlockquote(node) {
    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const content = this.output.join('').trim();
    this.output = savedOutput;

    if (content) {
      const lines = content.split('\n').map(line => `> ${line}`).join('\n');
      this.output.push(`\n\n${lines}\n\n`);
    }
  }

  processDefinitionList(node) {
    const items = node.querySelectorAll('dt, dd');
    this.output.push('\n\n');

    items.forEach(item => {
      const text = this.getTextContent(item).trim();
      if (item.tagName.toLowerCase() === 'dt') {
        this.output.push(`**${text}**\n`);
      } else {
        this.output.push(`: ${text}\n\n`);
      }
    });
  }

  processChildren(node) {
    if (!node.childNodes) return;
    for (const child of node.childNodes) {
      this.processNode(child);
    }
  }

  getTextContent(node) {
    // Get text content while preserving some structure
    const savedOutput = this.output;
    this.output = [];
    this.processChildren(node);
    const content = this.output.join('');
    this.output = savedOutput;
    return content;
  }

  isHidden(node) {
    if (!node.getAttribute) return false;

    const style = node.getAttribute('style') || '';
    const hidden = node.getAttribute('hidden');
    const ariaHidden = node.getAttribute('aria-hidden');

    return hidden !== null ||
           ariaHidden === 'true' ||
           style.includes('display: none') ||
           style.includes('display:none') ||
           style.includes('visibility: hidden') ||
           style.includes('visibility:hidden');
  }

  detectLanguage(node) {
    if (!node.getAttribute) return '';

    const className = node.getAttribute('class') || '';
    const dataLang = node.getAttribute('data-language') ||
                     node.getAttribute('data-lang') ||
                     node.getAttribute('lang') || '';

    // Check data attributes first
    if (dataLang) {
      return this.normalizeLanguage(dataLang);
    }

    // Check class names for language hints
    const classMatch = className.match(/(?:language-|lang-|highlight-|brush:\s*)(\w+)/i);
    if (classMatch) {
      return this.normalizeLanguage(classMatch[1]);
    }

    // Check for common class patterns
    for (const [lang, aliases] of this.codeBlockLanguages) {
      for (const alias of aliases) {
        if (className.toLowerCase().includes(alias)) {
          return lang;
        }
      }
    }

    return '';
  }

  normalizeLanguage(lang) {
    lang = lang.toLowerCase().trim();

    for (const [normalized, aliases] of this.codeBlockLanguages) {
      if (aliases.includes(lang)) {
        return normalized;
      }
    }

    return lang;
  }

  guessLanguageFromContent(code) {
    // Simple heuristics to detect language from content
    const trimmed = code.trim();

    // Shell/bash
    if (trimmed.startsWith('$') || trimmed.startsWith('#!') ||
        /^(curl|npm|yarn|pip|apt|brew|docker|git|kubectl|demo)\s/.test(trimmed)) {
      return 'bash';
    }

    // JSON
    if ((trimmed.startsWith('{') && trimmed.endsWith('}')) ||
        (trimmed.startsWith('[') && trimmed.endsWith(']'))) {
      try {
        JSON.parse(trimmed);
        return 'json';
      } catch (e) {}
    }

    // YAML
    if (/^[\w-]+:\s/.test(trimmed) && !trimmed.includes('{')) {
      return 'yaml';
    }

    // SQL
    if (/^(SELECT|INSERT|UPDATE|DELETE|CREATE|ALTER|DROP)\s/i.test(trimmed)) {
      return 'sql';
    }

    // HTML
    if (trimmed.startsWith('<') && trimmed.includes('>')) {
      return 'html';
    }

    return '';
  }

  cleanup(markdown) {
    return markdown
      // Normalize multiple blank lines to two
      .replace(/\n{3,}/g, '\n\n')
      // Remove leading/trailing whitespace
      .trim()
      // Ensure file ends with newline
      + '\n';
  }
}

/**
 * Extract metadata from HTML document
 * @param {Document|Element} doc - Document or element to extract from
 * @returns {Object} Extracted metadata
 */
export function extractMetadata(doc) {
  const metadata = {};

  // Title
  const title = doc.querySelector('title');
  if (title) {
    metadata.title = title.textContent.trim();
  }

  // Meta description
  const description = doc.querySelector('meta[name="description"]');
  if (description) {
    metadata.description = description.getAttribute('content');
  }

  // Open Graph
  const ogTitle = doc.querySelector('meta[property="og:title"]');
  if (ogTitle) {
    metadata.ogTitle = ogTitle.getAttribute('content');
  }

  const ogDescription = doc.querySelector('meta[property="og:description"]');
  if (ogDescription) {
    metadata.ogDescription = ogDescription.getAttribute('content');
  }

  // Canonical URL
  const canonical = doc.querySelector('link[rel="canonical"]');
  if (canonical) {
    metadata.canonical = canonical.getAttribute('href');
  }

  // Breadcrumbs (common patterns)
  const breadcrumbs = [];
  const breadcrumbNav = doc.querySelector('[aria-label="breadcrumb"], .breadcrumb, .breadcrumbs, nav.crumbs');
  if (breadcrumbNav) {
    const items = breadcrumbNav.querySelectorAll('a, span');
    items.forEach(item => {
      const text = item.textContent.trim();
      if (text && text !== '>' && text !== '/') {
        breadcrumbs.push(text);
      }
    });
  }
  if (breadcrumbs.length > 0) {
    metadata.breadcrumbs = breadcrumbs;
  }

  // Published/Modified dates
  const datePublished = doc.querySelector('time[datetime], meta[property="article:published_time"]');
  if (datePublished) {
    metadata.datePublished = datePublished.getAttribute('datetime') ||
                             datePublished.getAttribute('content');
  }

  const dateModified = doc.querySelector('meta[property="article:modified_time"]');
  if (dateModified) {
    metadata.dateModified = dateModified.getAttribute('content');
  }

  // Author
  const author = doc.querySelector('meta[name="author"], [rel="author"]');
  if (author) {
    metadata.author = author.getAttribute('content') || author.textContent.trim();
  }

  // Keywords/Tags
  const keywords = doc.querySelector('meta[name="keywords"]');
  if (keywords) {
    metadata.keywords = keywords.getAttribute('content').split(',').map(k => k.trim());
  }

  return metadata;
}

/**
 * Find the main content element in a document
 * @param {Document|Element} doc - Document to search
 * @returns {Element|null} Main content element
 */
export function findMainContent(doc) {
  // Priority order for content selection
  const selectors = [
    // Semantic HTML5
    'main',
    'article',
    '[role="main"]',

    // Common content classes
    '.doc-content',
    '.documentation',
    '.content',
    '.main-content',
    '.article-content',
    '.post-content',
    '.entry-content',
    '.page-content',

    // Common content IDs
    '#content',
    '#main',
    '#main-content',
    '#article',
    '#documentation',

    // Fallback
    '.container',
    '.wrapper',
  ];

  for (const selector of selectors) {
    const element = doc.querySelector(selector);
    if (element && hasSubstantialContent(element)) {
      return element;
    }
  }

  // Last resort: body
  return doc.body || doc;
}

/**
 * Check if an element has substantial content
 * @param {Element} element - Element to check
 * @returns {boolean} True if element has substantial content
 */
function hasSubstantialContent(element) {
  const text = element.textContent || '';
  const wordCount = text.trim().split(/\s+/).length;
  return wordCount > 50; // At least 50 words
}

export default {
  htmlToMarkdown,
  extractMetadata,
  findMainContent,
};
