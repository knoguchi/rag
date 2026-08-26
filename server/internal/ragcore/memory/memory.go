// Package memory provides conversation history storage for multi-turn RAG interactions.
package memory

import (
	"sync"
	"time"
)

// Message represents a single message in a conversation.
type Message struct {
	Role      string    // "user" or "assistant"
	Content   string
	Timestamp time.Time
}

// Conversation holds the message history for a session.
type Conversation struct {
	Messages  []Message
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store provides in-memory conversation storage.
// For production, consider using Redis for persistence and TTL support.
type Store struct {
	mu            sync.RWMutex
	conversations map[string]*Conversation
	maxMessages   int           // Max messages per conversation
	maxSessions   int           // Max concurrent sessions
	ttl           time.Duration // Time-to-live for conversations
	done          chan struct{} // Closed to stop the cleanup goroutine
	closeOnce     sync.Once
}

// NewStore creates a new conversation memory store.
func NewStore(maxMessages int, ttl time.Duration) *Store {
	return NewStoreWithLimit(maxMessages, 10000, ttl)
}

// NewStoreWithLimit creates a store with explicit session limit.
func NewStoreWithLimit(maxMessages, maxSessions int, ttl time.Duration) *Store {
	s := &Store{
		conversations: make(map[string]*Conversation),
		maxMessages:   maxMessages,
		maxSessions:   maxSessions,
		ttl:           ttl,
		done:          make(chan struct{}),
	}

	// Start cleanup goroutine
	go s.cleanupLoop()

	return s
}

// DefaultStore creates a store with sensible defaults.
// - Max 20 messages per conversation (10 turns)
// - Max 10000 concurrent sessions
// - 1 hour TTL (session expires after 1 hour of inactivity)
func DefaultStore() *Store {
	return NewStore(20, 1*time.Hour)
}

// AddUserMessage adds a user message to the conversation.
func (s *Store) AddUserMessage(sessionID, content string) {
	s.addMessage(sessionID, "user", content)
}

// AddAssistantMessage adds an assistant message to the conversation.
func (s *Store) AddAssistantMessage(sessionID, content string) {
	s.addMessage(sessionID, "assistant", content)
}

func (s *Store) addMessage(sessionID, role, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conv, exists := s.conversations[sessionID]
	if !exists {
		// Evict oldest session if at capacity
		if len(s.conversations) >= s.maxSessions {
			s.evictOldest()
		}
		conv = &Conversation{
			Messages:  make([]Message, 0),
			CreatedAt: time.Now(),
		}
		s.conversations[sessionID] = conv
	}

	conv.Messages = append(conv.Messages, Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})
	conv.UpdatedAt = time.Now()

	// Trim old messages if exceeding max (keep recent ones)
	if len(conv.Messages) > s.maxMessages {
		conv.Messages = conv.Messages[len(conv.Messages)-s.maxMessages:]
	}
}

// GetHistory returns the conversation history for a session.
// Returns nil if session doesn't exist.
func (s *Store) GetHistory(sessionID string) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	conv, exists := s.conversations[sessionID]
	if !exists {
		return nil
	}

	// Return a copy to avoid race conditions
	messages := make([]Message, len(conv.Messages))
	copy(messages, conv.Messages)
	return messages
}

// GetRecentHistory returns the last N messages for context window management.
func (s *Store) GetRecentHistory(sessionID string, n int) []Message {
	history := s.GetHistory(sessionID)
	if history == nil || len(history) <= n {
		return history
	}
	return history[len(history)-n:]
}

// ClearSession removes a conversation from memory.
func (s *Store) ClearSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conversations, sessionID)
}

// cleanupLoop periodically removes expired conversations until Close is called.
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.cleanup()
		case <-s.done:
			return
		}
	}
}

// Close stops the cleanup goroutine. Safe to call multiple times.
func (s *Store) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// evictOldest removes the least recently updated session. Must be called with mu held.
func (s *Store) evictOldest() {
	var oldestID string
	var oldestTime time.Time
	first := true
	for id, conv := range s.conversations {
		if first || conv.UpdatedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = conv.UpdatedAt
			first = false
		}
	}
	if oldestID != "" {
		delete(s.conversations, oldestID)
	}
}

func (s *Store) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, conv := range s.conversations {
		if now.Sub(conv.UpdatedAt) > s.ttl {
			delete(s.conversations, id)
		}
	}
}

// FormatForPrompt formats the conversation history for inclusion in an LLM prompt.
// Returns empty string if no history exists.
func FormatForPrompt(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	var result string
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			result += "User: " + msg.Content + "\n"
		case "assistant":
			result += "Assistant: " + msg.Content + "\n"
		}
	}
	return result
}
