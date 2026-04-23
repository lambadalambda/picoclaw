package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type Session struct {
	Key             string              `json:"key"`
	Messages        []providers.Message `json:"messages"`
	Summary         string              `json:"summary,omitempty"`
	CompactionCount int                 `json:"compaction_count"`
	Created         time.Time           `json:"created"`
	Updated         time.Time           `json:"updated"`
}

type SessionInfo struct {
	Key             string    `json:"key"`
	MessageCount    int       `json:"message_count"`
	TokenEstimate   int       `json:"token_estimate"`
	CompactionCount int       `json:"compaction_count"`
	Created         time.Time `json:"created"`
	Updated         time.Time `json:"updated"`
}

type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	storage  string
	// transcripts is the directory where append-only JSONL transcripts are stored.
	// It may be empty to disable transcript persistence.
	transcripts string
}

func NewSessionManager(storage string) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		storage:  storage,
	}

	if storage != "" {
		os.MkdirAll(storage, 0755)
		sm.transcripts = transcriptsDirFromSessionStorage(storage)
		if sm.transcripts != "" {
			os.MkdirAll(sm.transcripts, 0755)
		}
		sm.loadSessions()
	}

	return sm
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.mu.RLock()
	session, ok := sm.sessions[key]
	sm.mu.RUnlock()

	if ok {
		return session
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Re-check under write lock to avoid duplicate create/overwrite when
	// multiple goroutines race on first access.
	if session, ok = sm.sessions[key]; ok {
		return session
	}

	now := time.Now()
	session = &Session{
		Key:      key,
		Messages: []providers.Message{},
		Created:  now,
		Updated:  now,
	}
	sm.sessions[key] = session

	return session
}

func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.AddFullMessage(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionKey]
	if !ok {
		session = &Session{
			Key:      sessionKey,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[sessionKey] = session
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()

	// Best-effort: append to the transcript log. Never fail the main flow.
	sm.appendTranscriptLocked(sessionKey, msg)
}

func transcriptsDirFromSessionStorage(storage string) string {
	storage = strings.TrimSpace(storage)
	if storage == "" {
		return ""
	}

	base := filepath.Base(storage)
	// In normal operation storage is <workspace>/sessions.
	if base == "sessions" {
		return filepath.Join(filepath.Dir(storage), "transcripts")
	}

	// In tests, storage may be a temp dir; keep transcripts under that directory.
	return filepath.Join(storage, "transcripts")
}

func sanitizeSessionKeyForFilename(sessionKey string) string {
	return SanitizeSessionKeyForFilename(sessionKey)
}

func (sm *SessionManager) appendTranscriptLocked(sessionKey string, msg providers.Message) {
	if sm.transcripts == "" {
		return
	}
	key := sanitizeSessionKeyForFilename(sessionKey)
	if key == "" {
		return
	}

	path := filepath.Join(sm.transcripts, key+".jsonl")
	entry := BuildTranscriptEntry(msg)
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(data)
	_, _ = f.Write([]byte("\n"))
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return []providers.Message{}
	}

	history := make([]providers.Message, len(session.Messages))
	copy(history, session.Messages)
	return history
}

func (sm *SessionManager) GetSummary(key string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Summary
}

func (sm *SessionManager) GetSessionInfo(key string) *SessionInfo {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	tokenEstimate := 0
	for _, m := range session.Messages {
		tokenEstimate += len(m.Content) / 4
	}

	return &SessionInfo{
		Key:             session.Key,
		MessageCount:    len(session.Messages),
		TokenEstimate:   tokenEstimate,
		CompactionCount: session.CompactionCount,
		Created:         session.Created,
		Updated:         session.Updated,
	}
}

func (sm *SessionManager) SetSummary(key string, summary string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		session.Summary = summary
		session.Updated = time.Now()
	}
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	truncated := session.Messages[len(session.Messages)-keepLast:]
	sanitized, _ := providers.SanitizeToolTranscript(truncated)
	session.Messages = sanitized
	session.CompactionCount++
	session.Updated = time.Now()
}

func (sm *SessionManager) TrimHistoryTo(key string, length int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}
	if length < 0 {
		length = 0
	}
	if len(session.Messages) <= length {
		return
	}

	trimmed := append([]providers.Message(nil), session.Messages[:length]...)
	session.Messages = trimmed
	session.Updated = time.Now()
}

func (sm *SessionManager) ReplaceHistory(key string, messages []providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		now := time.Now()
		session = &Session{
			Key:      key,
			Messages: []providers.Message{},
			Created:  now,
			Updated:  now,
		}
		sm.sessions[key] = session
	}

	history := append([]providers.Message(nil), messages...)
	sanitized, _ := providers.SanitizeToolTranscript(history)
	session.Messages = sanitized
	session.Updated = time.Now()
}

func (sm *SessionManager) Save(session *Session) error {
	if sm.storage == "" {
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessionPath := filepath.Join(sm.storage, session.Key+".json")

	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}

	return utils.AtomicWriteFile(sessionPath, data, 0644)
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		sessionPath := filepath.Join(sm.storage, file.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		sm.sessions[session.Key] = &session
	}

	return nil
}
