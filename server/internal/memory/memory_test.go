package memory

import (
	"testing"
	"time"
)

func TestAddAndGetHistory(t *testing.T) {
	s := NewStoreWithLimit(10, 100, 1*time.Hour)

	s.AddUserMessage("s1", "hello")
	s.AddAssistantMessage("s1", "hi there")

	history := s.GetHistory("s1")
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "hi there" {
		t.Errorf("unexpected second message: %+v", history[1])
	}
}

func TestGetHistory_NonExistentSession(t *testing.T) {
	s := NewStoreWithLimit(10, 100, 1*time.Hour)
	if history := s.GetHistory("nonexistent"); history != nil {
		t.Errorf("expected nil for nonexistent session, got %v", history)
	}
}

func TestMaxMessages(t *testing.T) {
	s := NewStoreWithLimit(3, 100, 1*time.Hour)

	s.AddUserMessage("s1", "msg1")
	s.AddAssistantMessage("s1", "msg2")
	s.AddUserMessage("s1", "msg3")
	s.AddAssistantMessage("s1", "msg4") // Should trim msg1

	history := s.GetHistory("s1")
	if len(history) != 3 {
		t.Fatalf("expected 3 messages after trim, got %d", len(history))
	}
	if history[0].Content != "msg2" {
		t.Errorf("expected oldest to be 'msg2', got '%s'", history[0].Content)
	}
}

func TestMaxSessions_EvictsOldest(t *testing.T) {
	s := NewStoreWithLimit(10, 2, 1*time.Hour)

	s.AddUserMessage("s1", "first")
	time.Sleep(10 * time.Millisecond)
	s.AddUserMessage("s2", "second")
	time.Sleep(10 * time.Millisecond)
	s.AddUserMessage("s3", "third") // Should evict s1

	if s.GetHistory("s1") != nil {
		t.Error("expected s1 to be evicted")
	}
	if s.GetHistory("s2") == nil {
		t.Error("expected s2 to still exist")
	}
	if s.GetHistory("s3") == nil {
		t.Error("expected s3 to still exist")
	}
}

func TestClearSession(t *testing.T) {
	s := NewStoreWithLimit(10, 100, 1*time.Hour)

	s.AddUserMessage("s1", "hello")
	s.ClearSession("s1")

	if s.GetHistory("s1") != nil {
		t.Error("expected session to be cleared")
	}
}

func TestGetRecentHistory(t *testing.T) {
	s := NewStoreWithLimit(10, 100, 1*time.Hour)

	s.AddUserMessage("s1", "msg1")
	s.AddAssistantMessage("s1", "msg2")
	s.AddUserMessage("s1", "msg3")
	s.AddAssistantMessage("s1", "msg4")

	recent := s.GetRecentHistory("s1", 2)
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent messages, got %d", len(recent))
	}
	if recent[0].Content != "msg3" {
		t.Errorf("expected 'msg3', got '%s'", recent[0].Content)
	}
}

func TestCleanup(t *testing.T) {
	s := NewStoreWithLimit(10, 100, 50*time.Millisecond)

	s.AddUserMessage("s1", "hello")
	time.Sleep(100 * time.Millisecond)

	s.cleanup()

	if s.GetHistory("s1") != nil {
		t.Error("expected expired session to be cleaned up")
	}
}

func TestFormatForPrompt(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What is RAG?"},
		{Role: "assistant", Content: "RAG stands for..."},
	}

	result := FormatForPrompt(messages)
	if result == "" {
		t.Error("expected non-empty result")
	}

	if result != "User: What is RAG?\nAssistant: RAG stands for...\n" {
		t.Errorf("unexpected format: %q", result)
	}
}

func TestFormatForPrompt_Empty(t *testing.T) {
	if result := FormatForPrompt(nil); result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}
