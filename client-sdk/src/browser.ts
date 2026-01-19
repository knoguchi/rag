/**
 * Browser entry point - attaches SDK to window object
 */

import { RAGClient } from './client.js';
import { ChatWidget } from './widget.js';

// Attach to window for script tag usage
declare global {
  interface Window {
    RAGClient: typeof RAGClient;
    ChatWidget: typeof ChatWidget;
  }
}

window.RAGClient = RAGClient;
window.ChatWidget = ChatWidget;

export { RAGClient, ChatWidget };
