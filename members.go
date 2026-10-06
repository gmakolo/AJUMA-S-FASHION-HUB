package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Member is a durable Ajuma Hub account. PasswordHash is never rendered.
type Member struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
	LastLogin    time.Time `json:"last_login"`
	LoginCount   int       `json:"login_count"`
	ResetHash    string    `json:"reset_hash,omitempty"`
	ResetExpires time.Time `json:"reset_expires,omitempty"`
}

type MemberStore struct {
	mu    sync.RWMutex
	path  string
	items []Member
}

func OpenMembers(dir string) (*MemberStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create member directory: %w", err)
	}
	s := &MemberStore{path: filepath.Join(dir, "members.json")}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read members: %w", err)
	}
	if err := json.Unmarshal(raw, &s.items); err != nil {
		return nil, fmt.Errorf("members.json is not valid JSON: %w", err)
	}
	return s, nil
}

func (s *MemberStore) persistLocked() error {
	raw, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	return nil
}

func (s *MemberStore) Register(name, email, password string) (Member, error) {
	name = trimTo(oneLine(name), 60)
	email = strings.ToLower(strings.TrimSpace(email))
	if name == "" {
		return Member{}, errors.New("enter your name")
	}
	parsed, parseErr := mail.ParseAddress(email)
	if parseErr != nil || parsed.Address != email {
		return Member{}, errors.New("enter a valid email address")
	}
	cred, err := NewCredential(password)
	if err != nil {
		return Member{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.items {
		if m.Email == email {
			return Member{}, errors.New("an account with that email already exists")
		}
	}
	now := time.Now()
	m := Member{ID: NewID("u_"), Name: name, Email: email, PasswordHash: cred.String(), CreatedAt: now, LastLogin: now, LoginCount: 1}
	s.items = append(s.items, m)
	if err := s.persistLocked(); err != nil {
		s.items = s.items[:len(s.items)-1]
		return Member{}, fmt.Errorf("save account: %w", err)
	}
	return m, nil
}

func (s *MemberStore) SignIn(email, password string) (Member, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.RLock()
	var found Member
	for _, m := range s.items {
		if m.Email == email {
			found = m
			break
		}
	}
	s.mu.RUnlock()
	if found.ID == "" {
		return Member{}, false
	}
	cred, err := ParseCredential(found.PasswordHash)
	if err != nil || !cred.Verify(password) {
		return Member{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == found.ID {
			s.items[i].LastLogin = time.Now()
			s.items[i].LoginCount++
			found = s.items[i]
			_ = s.persistLocked()
			break
		}
	}
	return found, true
}

func (s *MemberStore) ByID(id string) (Member, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.items {
		if m.ID == id {
			return m, true
		}
	}
	return Member{}, false
}

func (s *MemberStore) ByEmail(email string) (Member, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.items {
		if m.Email == email {
			return m, true
		}
	}
	return Member{}, false
}

func (s *MemberStore) SetReset(email, tokenHash string, expires time.Time) error {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].Email != email {
			continue
		}
		before := s.items[i]
		s.items[i].ResetHash, s.items[i].ResetExpires = tokenHash, expires
		if err := s.persistLocked(); err != nil {
			s.items[i] = before
			return err
		}
		return nil
	}
	return nil
}

func (s *MemberStore) ResetPassword(email, codeHash, password string) (string, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	s.mu.RLock()
	valid := false
	now := time.Now()
	for _, m := range s.items {
		if m.Email == email && m.ResetHash != "" && subtle.ConstantTimeCompare([]byte(m.ResetHash), []byte(codeHash)) == 1 && now.Before(m.ResetExpires) {
			valid = true
			break
		}
	}
	s.mu.RUnlock()
	if !valid {
		return "", false, nil
	}
	cred, err := NewCredential(password)
	if err != nil {
		return "", false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		m := &s.items[i]
		if m.Email != email || m.ResetHash == "" || subtle.ConstantTimeCompare([]byte(m.ResetHash), []byte(codeHash)) != 1 || !time.Now().Before(m.ResetExpires) {
			continue
		}
		before := *m
		m.PasswordHash = cred.String()
		m.ResetHash = ""
		m.ResetExpires = time.Time{}
		if err := s.persistLocked(); err != nil {
			*m = before
			return "", false, err
		}
		return m.ID, true, nil
	}
	return "", false, nil
}
