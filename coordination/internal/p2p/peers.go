package p2p

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Peer is a known remote coordinator.
type Peer struct {
	URL       string    `json:"url"`
	NodeID    string    `json:"node_id,omitempty"`
	PublicKey string    `json:"public_key,omitempty"`
	LastSync  time.Time `json:"last_sync,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

// Store persists the peer list.
type Store struct {
	mu    sync.RWMutex
	path  string
	peers map[string]Peer // keyed by normalized URL
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		path:  filepath.Join(dataDir, "peers.json"),
		peers: make(map[string]Peer),
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func NormalizeURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var list []Peer
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse peers: %w", err)
	}
	s.peers = make(map[string]Peer, len(list))
	for _, p := range list {
		p.URL = NormalizeURL(p.URL)
		if p.URL == "" {
			continue
		}
		s.peers[p.URL] = p
	}
	return nil
}

func (s *Store) save() error {
	s.mu.RLock()
	list := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		list = append(list, p)
	}
	s.mu.RUnlock()

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Upsert(p Peer) error {
	p.URL = NormalizeURL(p.URL)
	if p.URL == "" {
		return fmt.Errorf("url is required")
	}
	s.mu.Lock()
	if existing, ok := s.peers[p.URL]; ok {
		if p.NodeID == "" {
			p.NodeID = existing.NodeID
		}
		if p.PublicKey == "" {
			p.PublicKey = existing.PublicKey
		}
		if p.LastSync.IsZero() {
			p.LastSync = existing.LastSync
		}
	}
	s.peers[p.URL] = p
	s.mu.Unlock()
	return s.save()
}

func (s *Store) Remove(url string) error {
	url = NormalizeURL(url)
	s.mu.Lock()
	delete(s.peers, url)
	s.mu.Unlock()
	return s.save()
}

func (s *Store) List() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		out = append(out, p)
	}
	return out
}

func (s *Store) Get(url string) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.peers[NormalizeURL(url)]
	return p, ok
}

func (s *Store) RecordSync(url, nodeID, publicKey string, err error) error {
	url = NormalizeURL(url)
	s.mu.Lock()
	p := s.peers[url]
	p.URL = url
	if nodeID != "" {
		p.NodeID = nodeID
	}
	if publicKey != "" {
		p.PublicKey = publicKey
	}
	if err != nil {
		p.LastError = err.Error()
	} else {
		p.LastError = ""
		p.LastSync = time.Now().UTC()
	}
	s.peers[url] = p
	s.mu.Unlock()
	return s.save()
}
