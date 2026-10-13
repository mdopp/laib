// Package secrets is secrets.age: a YAML map encrypted to the box's age key.
package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"gopkg.in/yaml.v3"

	"github.com/mdopp/laib/internal/boxfs"
)

// EnsureKey creates the age identity if missing and returns its recipient.
func EnsureKey(keyPath string) (string, error) {
	if b, err := os.ReadFile(keyPath); err == nil {
		id, err := parseIdentity(b)
		if err != nil {
			return "", err
		}
		return id.Recipient().String(), nil
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return "", err
	}
	content := "# Laib age identity. Keep a copy on paper; it is the one secret outside the box.\n# public key: " + id.Recipient().String() + "\n" + id.String() + "\n"
	if err := os.WriteFile(keyPath, []byte(content), 0o600); err != nil {
		return "", err
	}
	return id.Recipient().String(), nil
}

func parseIdentity(b []byte) (*age.X25519Identity, error) {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AGE-SECRET-KEY-") {
			return age.ParseX25519Identity(line)
		}
	}
	return nil, errors.New("age.key: no AGE-SECRET-KEY line")
}

// Load decrypts secrets.age. A missing file is an empty map, not an error.
func Load(path, keyPath string) (map[string]string, error) {
	enc, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("age key: %w", err)
	}
	id, err := parseIdentity(kb)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(enc), id)
	if err != nil {
		return nil, fmt.Errorf("secrets.age: %w", err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := yaml.Unmarshal(plain, &m); err != nil {
		return nil, fmt.Errorf("secrets.age content: %w", err)
	}
	return m, nil
}

// Save encrypts the map to the key's recipient and writes it atomically.
func Save(path, keyPath string, m map[string]string) error {
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	id, err := parseIdentity(kb)
	if err != nil {
		return err
	}
	// yaml.Marshal sorts the keys and writes a value that is not UTF-8 as
	// !!binary, so every byte comes back; Go's %q did not (\xff read back as ÿ).
	plain, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	var enc bytes.Buffer
	w, err := age.Encrypt(&enc, id.Recipient())
	if err != nil {
		return err
	}
	if _, err := w.Write(plain); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return boxfs.WriteAtomic(path+".tmp", path, enc.Bytes(), 0o600)
}

// Generate returns a 32-byte random secret, URL-safe base64, no padding.
func Generate() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
