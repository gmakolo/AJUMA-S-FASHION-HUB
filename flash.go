package main

// Flash is a one-shot message shown to the admin after a redirect: the
// confirmation of a save, or the reason a save was refused.
type Flash struct {
	Kind string // "ok", "warn" or "error" — drives the colour of the strip
	Text string
}

// Flash queues a message against a session.
func (s *Sessions) Flash(token, kind, text string) {
	if token == "" || text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if item, ok := s.items[token]; ok && len(item.flashes) < 8 {
		item.flashes = append(item.flashes, Flash{Kind: kind, Text: text})
	}
}

// TakeFlashes returns and clears the queued messages for a session.
func (s *Sessions) TakeFlashes(token string) []Flash {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[token]
	if !ok || len(item.flashes) == 0 {
		return nil
	}
	out := item.flashes
	item.flashes = nil
	return out
}
