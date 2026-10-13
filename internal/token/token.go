// Package token is the hand-token store: one bearer token per hand, hashed at
// rest, with an optional expiry (ADR 0006). Stored as tokens.json next to the
// state repo, never inside it.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mdopp/laib/internal/boxfs"
)

// Record is one minted token (the hash, never the value).
type Record struct {
	ID      string    `json:"id"`
	Hand    string    `json:"hand"`
	Hash    string    `json:"hash"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// Expired reports whether the record is past its expiry.
func (r Record) Expired(now time.Time) bool { return !r.Expires.IsZero() && now.After(r.Expires) }

// Store is the file.
type Store struct {
	Path    string
	Records []Record
}

// Load reads tokens.json; a missing file is an empty store.
func Load(path string) (*Store, error) {
	s := &Store{Path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	return s, json.Unmarshal(b, &s.Records)
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.Records, "", "  ")
	if err != nil {
		return err
	}
	return boxfs.WriteAtomic(s.Path+".tmp", s.Path, b, 0o600)
}

// Mint creates a token for a hand. ttl 0 means no expiry. Returns the plain
// token exactly once.
func (s *Store) Mint(hand string, ttl time.Duration, note string) (string, Record, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Record{}, err
	}
	plain := "laib_" + hand + "_" + base64.RawURLEncoding.EncodeToString(raw)
	idb := make([]byte, 4)
	rand.Read(idb)
	rec := Record{ID: hex.EncodeToString(idb), Hand: hand, Hash: hash(plain), Created: time.Now().UTC(), Note: note}
	if ttl > 0 {
		rec.Expires = rec.Created.Add(ttl)
	}
	s.Records = append(s.Records, rec)
	return plain, rec, s.save()
}

// Revoke removes a token by id.
func (s *Store) Revoke(id string) error {
	kept := s.Records[:0]
	found := false
	for _, r := range s.Records {
		if r.ID == id {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return errors.New("no token with id " + id)
	}
	s.Records = kept
	return s.save()
}

// Lookup resolves a bearer value to its hand; "" when unknown or expired.
func (s *Store) Lookup(plain string, now time.Time) (Record, bool) {
	h := hash(plain)
	for _, r := range s.Records {
		if subtle.ConstantTimeCompare([]byte(r.Hash), []byte(h)) == 1 && !r.Expired(now) {
			return r, true
		}
	}
	return Record{}, false
}

// List returns the records sorted by creation.
func (s *Store) List() []Record {
	out := append([]Record(nil), s.Records...)
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func hash(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
