/**
 * RAG AI Assistant Widget - Drop-in AI assistant UI component
 */

import { RAGClient } from './client.js';
import type { RAGClientConfig } from './types.js';
import { marked } from 'marked';

// Configure marked for safe rendering
marked.setOptions({
  breaks: true,  // Convert \n to <br>
  gfm: true,     // GitHub Flavored Markdown
});

export interface ChatWidgetConfig extends RAGClientConfig {
  title?: string;
  placeholder?: string;
}

export class ChatWidget {
  private client: RAGClient;
  private title: string;
  private placeholder: string;
  private isOpen = false;
  private isLoading = false;

  // Shadow DOM container
  private shadowHost!: HTMLDivElement;
  private shadow!: ShadowRoot;

  // DOM elements (inside shadow)
  private button!: HTMLButtonElement;
  private window!: HTMLDivElement;
  private messagesEl!: HTMLDivElement;
  private inputEl!: HTMLInputElement;
  private sendEl!: HTMLButtonElement;
  private closeEl!: HTMLButtonElement;

  constructor(config: ChatWidgetConfig) {
    this.client = new RAGClient(config);
    this.title = config.title || 'AI Assistant';
    this.placeholder = config.placeholder || 'Ask me anything...';
    this.init();
  }

  private init(): void {
    this.createShadowDOM();
    this.createWidget();
    this.bindEvents();
  }

  private createShadowDOM(): void {
    // Create host element for shadow DOM
    this.shadowHost = document.createElement('div');
    this.shadowHost.id = 'rag-ai-assistant';
    document.body.appendChild(this.shadowHost);

    // Attach shadow root (closed for better isolation)
    this.shadow = this.shadowHost.attachShadow({ mode: 'open' });

    // Add styles inside shadow DOM
    const style = document.createElement('style');
    style.textContent = `
      .rag-button {
        position: fixed; bottom: 20px; right: 20px;
        width: 60px; height: 60px; border-radius: 50%;
        background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
        border: none; cursor: pointer;
        box-shadow: 0 4px 12px rgba(102, 126, 234, 0.4);
        z-index: 9999; display: flex; align-items: center; justify-content: center;
        transition: transform 0.2s;
      }
      .rag-button:hover { transform: scale(1.05); }
      .rag-button svg { width: 28px; height: 28px; fill: white; }

      .rag-window {
        position: fixed; bottom: 20px; right: 20px;
        width: 400px; height: 550px;
        background: white; border-radius: 16px;
        box-shadow: 0 8px 32px rgba(0,0,0,0.15);
        display: none; flex-direction: column;
        z-index: 9998; overflow: hidden;
        font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      }
      .rag-window.open { display: flex; }

      .rag-header {
        background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
        color: white; padding: 16px 20px;
        display: flex; align-items: center; justify-content: space-between;
      }
      .rag-header h3 { margin: 0; font-size: 16px; font-weight: 600; }
      .rag-close { background: none; border: none; color: white; cursor: pointer; font-size: 24px; opacity: 0.8; }
      .rag-close:hover { opacity: 1; }

      .rag-messages {
        flex: 1; overflow-y: auto; padding: 16px;
        display: flex; flex-direction: column; gap: 16px;
      }

      .rag-message { max-width: 90%; }
      .rag-message.user { align-self: flex-end; }
      .rag-message.user .rag-bubble {
        background: #667eea; color: white;
        border-radius: 16px 16px 4px 16px; padding: 12px 16px; font-size: 14px;
      }
      .rag-message.assistant { align-self: flex-start; }
      .rag-message.assistant .rag-bubble {
        background: #f8f9fa; border-radius: 16px 16px 16px 4px; padding: 4px;
      }

      .rag-content {
        padding: 10px 14px;
        font-size: 14px; line-height: 1.6; color: #333;
      }
      .rag-content p { margin: 0 0 10px; }
      .rag-content p:last-child { margin-bottom: 0; }
      .rag-content ul, .rag-content ol {
        margin: 8px 0; padding-left: 20px;
      }
      .rag-content li { margin: 4px 0; }
      .rag-content pre {
        margin: 8px 0; padding: 12px 14px;
        background: #1e1e1e; border-radius: 8px;
        overflow-x: auto; font-size: 13px;
      }
      .rag-content pre code {
        color: #d4d4d4;
        font-family: 'SF Mono', Monaco, 'Courier New', monospace;
        white-space: pre; background: none; padding: 0;
      }
      .rag-content code {
        background: #e9ecef; padding: 2px 6px; border-radius: 4px;
        font-family: 'SF Mono', Monaco, monospace; font-size: 0.9em; color: #e83e8c;
      }
      .rag-content a { color: #667eea; text-decoration: none; }
      .rag-content a:hover { text-decoration: underline; }
      .rag-content strong { font-weight: 600; }
      .rag-content em { font-style: italic; }
      .rag-content h1, .rag-content h2, .rag-content h3 {
        margin: 12px 0 8px; font-weight: 600;
      }
      .rag-content h1 { font-size: 1.3em; }
      .rag-content h2 { font-size: 1.2em; }
      .rag-content h3 { font-size: 1.1em; }
      .rag-content blockquote {
        margin: 8px 0; padding: 8px 12px;
        border-left: 3px solid #667eea; background: #f8f9fa;
      }
      .rag-content table { width: 100%; border-collapse: collapse; margin: 8px 0; font-size: 13px; }
      .rag-content th, .rag-content td { padding: 6px 10px; border: 1px solid #e5e5e5; text-align: left; }
      .rag-content th { background: #f8f9fa; font-weight: 600; }
      .rag-sources {
        margin: 8px 14px 10px; padding-top: 10px;
        border-top: 1px solid #e9ecef; font-size: 12px; color: #6c757d;
      }
      .rag-sources a { color: #667eea; text-decoration: none; margin-left: 4px; }
      .rag-sources a:hover { text-decoration: underline; }

      .rag-input-area { padding: 16px; border-top: 1px solid #eee; display: flex; gap: 8px; }
      .rag-input {
        flex: 1; padding: 12px 16px; border: 1px solid #ddd;
        border-radius: 24px; font-size: 14px; outline: none;
      }
      .rag-input:focus { border-color: #667eea; }
      .rag-input:disabled { background: #f5f5f5; }
      .rag-send {
        width: 44px; height: 44px; border-radius: 50%;
        background: #667eea; border: none; cursor: pointer;
        display: flex; align-items: center; justify-content: center;
      }
      .rag-send:hover:not(:disabled) { background: #5a6fd6; }
      .rag-send:disabled { background: #ccc; cursor: not-allowed; }
      .rag-send svg { width: 20px; height: 20px; fill: white; }

      .rag-welcome { text-align: center; padding: 40px 20px; color: #666; }
      .rag-welcome h4 { margin: 0 0 8px; color: #333; }
      .rag-welcome p { margin: 0; font-size: 14px; }

      .rag-typing { display: inline-flex; gap: 4px; padding: 12px 16px; }
      .rag-typing span {
        width: 8px; height: 8px; background: #888; border-radius: 50%;
        animation: rag-typing 1.4s infinite ease-in-out;
      }
      .rag-typing span:nth-child(1) { animation-delay: 0s; }
      .rag-typing span:nth-child(2) { animation-delay: 0.2s; }
      .rag-typing span:nth-child(3) { animation-delay: 0.4s; }
      @keyframes rag-typing {
        0%, 60%, 100% { transform: translateY(0); }
        30% { transform: translateY(-4px); }
      }
    `;
    this.shadow.appendChild(style);
  }

  private createWidget(): void {
    this.button = document.createElement('button');
    this.button.className = 'rag-button';
    this.button.innerHTML = '<svg viewBox="0 0 24 24"><path d="M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm0 14H6l-2 2V4h16v12z"/></svg>';

    this.window = document.createElement('div');
    this.window.className = 'rag-window';
    this.window.innerHTML = `
      <div class="rag-header">
        <h3>${this.escapeHtml(this.title)}</h3>
        <button class="rag-close">&times;</button>
      </div>
      <div class="rag-messages">
        <div class="rag-welcome">
          <h4>Hi there!</h4>
          <p>Ask me anything about our documentation.</p>
        </div>
      </div>
      <div class="rag-input-area">
        <input type="text" class="rag-input" placeholder="${this.escapeHtml(this.placeholder)}">
        <button class="rag-send">
          <svg viewBox="0 0 24 24"><path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z"/></svg>
        </button>
      </div>
    `;

    // Append to shadow DOM instead of document.body
    this.shadow.appendChild(this.button);
    this.shadow.appendChild(this.window);

    this.messagesEl = this.window.querySelector('.rag-messages')!;
    this.inputEl = this.window.querySelector('.rag-input')!;
    this.sendEl = this.window.querySelector('.rag-send')!;
    this.closeEl = this.window.querySelector('.rag-close')!;
  }

  private bindEvents(): void {
    this.button.addEventListener('click', () => this.toggle());
    this.closeEl.addEventListener('click', () => this.close());
    this.sendEl.addEventListener('click', () => this.send());

    // Stop key events from propagating to host page
    this.inputEl.addEventListener('keydown', (e) => {
      e.stopPropagation();
      if (e.key === 'Enter') { e.preventDefault(); this.send(); }
      if (e.key === 'Escape') { this.close(); }
    });
    this.inputEl.addEventListener('keyup', (e) => e.stopPropagation());
    this.inputEl.addEventListener('keypress', (e) => e.stopPropagation());
  }

  private toggle(): void { this.isOpen ? this.close() : this.open(); }
  private open(): void { this.isOpen = true; this.window.classList.add('open'); this.button.style.display = 'none'; this.inputEl.focus(); }
  private close(): void { this.isOpen = false; this.window.classList.remove('open'); this.button.style.display = 'flex'; }

  private async send(): Promise<void> {
    const question = this.inputEl.value.trim();
    if (!question || this.isLoading) return;

    const welcome = this.messagesEl.querySelector('.rag-welcome');
    if (welcome) welcome.remove();

    this.addUserMessage(question);
    this.inputEl.value = '';
    this.isLoading = true;
    this.inputEl.disabled = true;
    this.sendEl.disabled = true;

    const loadingEl = this.addLoadingMessage();

    try {
      const response = await this.client.query(question);
      loadingEl.remove();
      this.addAssistantMessage(response.answer, response.sources);
    } catch (error) {
      loadingEl.remove();
      this.addAssistantMessage('Sorry, an error occurred. Please try again.');
      console.error('RAG Widget error:', error);
    }

    this.isLoading = false;
    this.inputEl.disabled = false;
    this.sendEl.disabled = false;
    this.inputEl.focus();
  }

  private parseSources(sources: { content: string; metadata?: Record<string, unknown> }[]): { url: string; title: string }[] {
    const seen = new Set<string>();
    const items: { url: string; title: string }[] = [];
    for (const s of sources) {
      const url = (s.metadata?.source as string) || '';
      if (url && !seen.has(url)) {
        seen.add(url);
        const title = (s.metadata?.title as string) || url.split('/').pop()?.replace(/\.html?$/, '') || 'Source';
        items.push({ url, title });
      }
    }
    return items.slice(0, 3);
  }

  private addUserMessage(text: string): void {
    const el = document.createElement('div');
    el.className = 'rag-message user';
    el.innerHTML = `<div class="rag-bubble">${this.escapeHtml(text)}</div>`;
    this.messagesEl.appendChild(el);
    this.scroll();
  }

  private addLoadingMessage(): HTMLDivElement {
    const el = document.createElement('div');
    el.className = 'rag-message assistant';
    el.innerHTML = '<div class="rag-bubble"><div class="rag-typing"><span></span><span></span><span></span></div></div>';
    this.messagesEl.appendChild(el);
    this.scroll();
    return el;
  }

  private addAssistantMessage(answer: string, sources?: { content: string; metadata?: Record<string, unknown> }[]): void {
    const el = document.createElement('div');
    el.className = 'rag-message assistant';
    const bubble = document.createElement('div');
    bubble.className = 'rag-bubble';

    // Render markdown content
    const content = document.createElement('div');
    content.className = 'rag-content';
    content.innerHTML = marked.parse(answer) as string;
    bubble.appendChild(content);

    // Add sources if available
    if (sources && sources.length > 0) {
      const sourceItems = this.parseSources(sources);
      if (sourceItems.length > 0) {
        const sourcesEl = document.createElement('div');
        sourcesEl.className = 'rag-sources';
        sourcesEl.innerHTML = '<strong>Sources:</strong> ' +
          sourceItems.map(s => `<a href="${s.url}" target="_blank">${this.escapeHtml(s.title)}</a>`).join(', ');
        bubble.appendChild(sourcesEl);
      }
    }

    el.appendChild(bubble);
    this.messagesEl.appendChild(el);
    this.scroll();
  }

  private escapeHtml(text: string): string {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  private scroll(): void {
    this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
  }
}
