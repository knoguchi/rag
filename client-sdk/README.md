# RAG Client SDK

TypeScript SDK for querying the RAG service from browser or Node.js.

## Quick Start

### Install

```bash
npm install @knoguchi/rag-sdk
```

Or include the script directly:

```html
<script src="/path/to/rag-sdk.js"></script>
```

## SDK Usage

### Basic Query

```javascript
import { RAGClient } from '@knoguchi/rag-sdk';

const rag = new RAGClient({
  baseUrl: 'http://localhost:8080',
  tenantId: 'your-tenant-id'
});

const response = await rag.query('How do I create a VM?');
console.log(response.parts);
```

### Response Structure

The SDK returns structured parts for easy rendering:

```javascript
{
  raw: "Original markdown response...",
  parts: [
    { type: 'text', content: 'Create a VM using the Acme CLI.' },
    { type: 'list', items: ['Choose instance type', 'Select an image', 'Configure networking'] },
    { type: 'code', language: 'bash', content: 'curl -X POST http://localhost:8080/v1/query ...' },
    { type: 'sources', items: [{ title: 'Compute Docs', url: '/docs/compute.html' }] }
  ],
  sources: [...],
  metadata: {}
}
```

### Part Types

| Type | Properties | Description |
|------|------------|-------------|
| `text` | `content` | Plain text paragraph |
| `list` | `items[]` | Bulleted or numbered list |
| `code` | `language`, `content` | Code block with syntax |
| `sources` | `items[{url, title, score}]` | Source references |

## JAMstack Integration Examples

### React / Next.js

```jsx
import { useState } from 'react';
import { RAGClient } from '@knoguchi/rag-sdk';

const rag = new RAGClient({
  baseUrl: process.env.NEXT_PUBLIC_RAG_URL,
  tenantId: process.env.NEXT_PUBLIC_TENANT_ID
});

function ChatWidget() {
  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');

  const handleSend = async () => {
    if (!input.trim()) return;

    setMessages(prev => [...prev, { role: 'user', content: input }]);
    setInput('');

    const response = await rag.query(input);
    setMessages(prev => [...prev, { role: 'assistant', parts: response.parts }]);
  };

  return (
    <div className="chat-container">
      <div className="messages">
        {messages.map((msg, i) => (
          <Message key={i} {...msg} />
        ))}
      </div>
      <input
        value={input}
        onChange={e => setInput(e.target.value)}
        onKeyPress={e => e.key === 'Enter' && handleSend()}
      />
    </div>
  );
}

function Message({ role, content, parts }) {
  if (role === 'user') {
    return <div className="user-message">{content}</div>;
  }

  return (
    <div className="assistant-message">
      {parts.map((part, i) => <Part key={i} {...part} />)}
    </div>
  );
}

function Part({ type, content, items, language }) {
  switch (type) {
    case 'text':
      return <p className="rag-text">{content}</p>;
    case 'list':
      return (
        <ul className="rag-list">
          {items.map((item, i) => <li key={i}>{item}</li>)}
        </ul>
      );
    case 'code':
      return (
        <pre className={`rag-code language-${language}`}>
          <code>{content}</code>
        </pre>
      );
    case 'sources':
      return (
        <div className="rag-sources">
          Sources: {items.map((s, i) => (
            <a key={i} href={s.url}>{s.title}</a>
          ))}
        </div>
      );
    default:
      return null;
  }
}
```

### Vue 3 / Nuxt

```vue
<template>
  <div class="chat-widget">
    <div class="messages" ref="messagesEl">
      <div v-for="(msg, i) in messages" :key="i" :class="msg.role">
        <template v-if="msg.role === 'user'">
          {{ msg.content }}
        </template>
        <template v-else>
          <component
            v-for="(part, j) in msg.parts"
            :key="j"
            :is="getPartComponent(part.type)"
            v-bind="part"
          />
        </template>
      </div>
    </div>
    <input v-model="input" @keyup.enter="send" placeholder="Ask a question..." />
  </div>
</template>

<script setup>
import { ref } from 'vue';
import { RAGClient } from '@knoguchi/rag-sdk';

const rag = new RAGClient({
  baseUrl: import.meta.env.VITE_RAG_URL,
  tenantId: import.meta.env.VITE_TENANT_ID
});

const messages = ref([]);
const input = ref('');

async function send() {
  if (!input.value.trim()) return;

  messages.value.push({ role: 'user', content: input.value });
  const question = input.value;
  input.value = '';

  const response = await rag.query(question);
  messages.value.push({ role: 'assistant', parts: response.parts });
}

function getPartComponent(type) {
  return {
    text: 'TextPart',
    list: 'ListPart',
    code: 'CodePart',
    sources: 'SourcesPart'
  }[type];
}
</script>
```

### Astro

```astro
---
// src/components/ChatWidget.astro
---

<div id="chat-widget">
  <div id="messages"></div>
  <input type="text" id="chat-input" placeholder="Ask a question..." />
</div>

<script>
  import { RAGClient, RAGResponseRenderer } from '@knoguchi/rag-sdk';

  const rag = new RAGClient({
    baseUrl: import.meta.env.PUBLIC_RAG_URL,
    tenantId: import.meta.env.PUBLIC_TENANT_ID
  });

  const renderer = new RAGResponseRenderer({ classPrefix: 'rag' });
  const messagesEl = document.getElementById('messages');
  const inputEl = document.getElementById('chat-input');

  inputEl.addEventListener('keypress', async (e) => {
    if (e.key !== 'Enter') return;

    const question = inputEl.value.trim();
    if (!question) return;

    // Add user message
    messagesEl.innerHTML += `<div class="user-msg">${question}</div>`;
    inputEl.value = '';

    // Get response
    const response = await rag.query(question);

    // Render structured parts
    messagesEl.innerHTML += `<div class="assistant-msg">${renderer.render(response.parts)}</div>`;
  });
</script>

<style>
  .rag-text { margin: 0.5rem 0; }
  .rag-list { padding-left: 1.5rem; }
  .rag-code { background: #1e1e1e; color: #d4d4d4; padding: 1rem; border-radius: 4px; }
  .rag-sources { font-size: 0.875rem; color: #666; margin-top: 1rem; }
</style>
```

### Static HTML (Vanilla JS)

```html
<!DOCTYPE html>
<html>
<head>
  <title>Documentation</title>
  <style>
    .chat-container { max-width: 600px; margin: 2rem auto; }
    .messages { height: 400px; overflow-y: auto; border: 1px solid #ddd; padding: 1rem; }
    .user-msg { text-align: right; margin: 0.5rem 0; }
    .user-msg span { background: #007bff; color: white; padding: 0.5rem 1rem; border-radius: 1rem; }
    .assistant-msg { margin: 0.5rem 0; }

    /* Custom styling for response parts */
    .rag-text { line-height: 1.6; }
    .rag-list { margin: 0.5rem 0; padding-left: 1.5rem; }
    .rag-code { background: #f4f4f4; padding: 1rem; border-radius: 4px; overflow-x: auto; }
    .rag-sources { font-size: 0.85rem; color: #666; border-top: 1px solid #eee; padding-top: 0.5rem; }
    .rag-sources a { color: #007bff; }

    .chat-input { display: flex; gap: 0.5rem; margin-top: 1rem; }
    .chat-input input { flex: 1; padding: 0.75rem; border: 1px solid #ddd; border-radius: 4px; }
    .chat-input button { padding: 0.75rem 1.5rem; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; }
  </style>
</head>
<body>
  <div class="chat-container">
    <h2>Documentation Assistant</h2>
    <div class="messages" id="messages"></div>
    <div class="chat-input">
      <input type="text" id="input" placeholder="Ask about our documentation...">
      <button onclick="send()">Send</button>
    </div>
  </div>

  <script src="/path/to/rag-sdk.js"></script>
  <script>
    const rag = new RAGClient({
      baseUrl: 'http://localhost:8080',
      tenantId: 'your-tenant-id'
    });

    const renderer = new RAGResponseRenderer({ classPrefix: 'rag' });
    const messagesEl = document.getElementById('messages');
    const inputEl = document.getElementById('input');

    inputEl.addEventListener('keypress', (e) => {
      if (e.key === 'Enter') send();
    });

    async function send() {
      const question = inputEl.value.trim();
      if (!question) return;

      // Show user message
      messagesEl.innerHTML += `<div class="user-msg"><span>${escapeHtml(question)}</span></div>`;
      inputEl.value = '';
      messagesEl.scrollTop = messagesEl.scrollHeight;

      try {
        const response = await rag.query(question);
        messagesEl.innerHTML += `<div class="assistant-msg">${renderer.render(response.parts)}</div>`;
      } catch (err) {
        messagesEl.innerHTML += `<div class="assistant-msg"><p class="rag-text">Sorry, something went wrong.</p></div>`;
      }

      messagesEl.scrollTop = messagesEl.scrollHeight;
    }

    function escapeHtml(text) {
      const div = document.createElement('div');
      div.textContent = text;
      return div.innerHTML;
    }
  </script>
</body>
</html>
```

## Streaming Responses

For a more interactive experience, use streaming:

```javascript
const rag = new RAGClient({
  baseUrl: 'http://localhost:8080',
  tenantId: 'your-tenant-id'
});

await rag.queryStream(
  'How do I deploy an app?',
  (part) => {
    if (part.type === 'token') {
      // Append token to current response
      appendToResponse(part.content);
    } else if (part.type === 'source') {
      // Source retrieved
      addSource(part);
    }
  }
);
```

## Styling Guide

The SDK uses consistent CSS class prefixes for easy customization:

### Drop-in Widget Classes

Override these classes to customize the floating widget:

```css
/* Button */
.agent-button { /* Floating chat button */ }

/* Window */
.agent-window { /* Chat window container */ }
.agent-header { /* Header with title */ }
.agent-messages { /* Messages container */ }
.agent-input-area { /* Input section */ }

/* Response Parts */
.agent-part-text { /* Text paragraphs */ }
.agent-part-list { /* Bullet lists */ }
.agent-part-code { /* Code blocks */ }
.agent-part-sources { /* Source links */ }
.agent-inline-code { /* Inline code spans */ }
```

### SDK Renderer Classes

When using `RAGResponseRenderer`, customize with your prefix:

```javascript
const renderer = new RAGResponseRenderer({ classPrefix: 'docs' });
```

This generates classes like `docs-text`, `docs-list`, `docs-code`, etc.

## Configuration Options

### RAGClient Constructor

```javascript
const rag = new RAGClient({
  baseUrl: 'http://localhost:8080',  // Required: API endpoint
  tenantId: 'your-tenant-id',             // Required: Your tenant ID
  options: {                               // Optional: Default query options
    topK: 10,                              // Number of chunks to retrieve
    minScore: 0.3                          // Minimum similarity score
  }
});
```

### Query Options

```javascript
const response = await rag.query('question', {
  topK: 5,           // Override default topK
  minScore: 0.5,     // Override minimum score
  filter: {          // Metadata filters
    category: 'compute'
  }
});
```

## Error Handling

```javascript
try {
  const response = await rag.query('How do I...?');
  renderResponse(response.parts);
} catch (error) {
  if (error.message.includes('401')) {
    // Invalid tenant ID
    showError('Configuration error');
  } else if (error.message.includes('429')) {
    // Rate limited
    showError('Too many requests, please slow down');
  } else {
    showError('Something went wrong');
  }
}
```

## Best Practices

1. **Environment Variables**: Never hardcode tenant IDs in client code
   ```javascript
   // Next.js
   tenantId: process.env.NEXT_PUBLIC_TENANT_ID

   // Vite
   tenantId: import.meta.env.VITE_TENANT_ID
   ```

2. **Loading States**: Show typing indicators while waiting
   ```javascript
   setLoading(true);
   const response = await rag.query(question);
   setLoading(false);
   ```

3. **Error Boundaries**: Wrap chat components in error boundaries

4. **Accessibility**: Ensure keyboard navigation and screen reader support

5. **Mobile**: Test the chat widget on mobile viewports

## Installation

```bash
npm install @knoguchi/rag-sdk
```

Or copy `dist/rag-sdk.js` to serve from your own CDN.
