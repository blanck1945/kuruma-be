package httpapi

import (
	"sync"
	"time"
)

// Gemini wire types — shared between session store and chat handler.
type gPart struct {
	Text             string         `json:"text,omitempty"`
	FunctionCall     map[string]any `json:"functionCall,omitempty"`
	FunctionResponse map[string]any `json:"functionResponse,omitempty"`
}

type gContent struct {
	Role  string  `json:"role"`
	Parts []gPart `json:"parts"`
}

// chatSession holds a single conversation thread's full Gemini history,
// including function call / function response turns that the frontend never sees.
type chatSession struct {
	mu       sync.Mutex
	contents []gContent
	lastUsed time.Time
}

type chatSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*chatSession
}

func newChatSessionStore() *chatSessionStore {
	s := &chatSessionStore{sessions: make(map[string]*chatSession)}
	go s.cleanupLoop()
	return s
}

func (s *chatSessionStore) getOrCreate(key string) *chatSession {
	s.mu.RLock()
	sess, ok := s.sessions[key]
	s.mu.RUnlock()
	if ok {
		return sess
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check after acquiring write lock.
	if sess, ok = s.sessions[key]; ok {
		return sess
	}
	sess = &chatSession{lastUsed: time.Now()}
	s.sessions[key] = sess
	return sess
}

func (s *chatSessionStore) delete(key string) {
	s.mu.Lock()
	delete(s.sessions, key)
	s.mu.Unlock()
}

const sessionTTL = 30 * time.Minute

func (s *chatSessionStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for k, sess := range s.sessions {
			sess.mu.Lock()
			idle := time.Since(sess.lastUsed)
			sess.mu.Unlock()
			if idle > sessionTTL {
				delete(s.sessions, k)
			}
		}
		s.mu.Unlock()
	}
}
