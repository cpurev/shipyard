package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cpurev/shipyard/internal/providers"
)

// FileStore writes every credential into one JSON file.
// The file mode is 0600 and the directory mode is 0700.
type FileStore struct {
	path string
}

type fileDocument struct {
	Providers map[string]providers.Credentials `json:"providers"`
}

// NewFileStore stores credentials at path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Save replaces the credentials for one provider.
func (s *FileStore) Save(_ context.Context, provider string, creds providers.Credentials) error {
	doc, err := s.read()
	if err != nil {
		return err
	}
	if doc.Providers == nil {
		doc.Providers = map[string]providers.Credentials{}
	}
	doc.Providers[provider] = creds
	return s.write(doc)
}

// Load returns one provider's credentials.
func (s *FileStore) Load(_ context.Context, provider string) (providers.Credentials, bool, error) {
	doc, err := s.read()
	if err != nil {
		return providers.Credentials{}, false, err
	}
	creds, ok := doc.Providers[provider]
	return creds, ok, nil
}

// LoadAll returns every stored provider.
func (s *FileStore) LoadAll(context.Context) (map[string]providers.Credentials, error) {
	doc, err := s.read()
	if err != nil {
		return nil, err
	}
	if doc.Providers == nil {
		return map[string]providers.Credentials{}, nil
	}
	return doc.Providers, nil
}

func (s *FileStore) read() (fileDocument, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return fileDocument{}, nil
	}
	if err != nil {
		return fileDocument{}, fmt.Errorf("read credentials: %w", err)
	}
	if len(data) == 0 {
		return fileDocument{}, nil
	}
	var doc fileDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fileDocument{}, fmt.Errorf("parse credentials: %w", err)
	}
	return doc, nil
}

func (s *FileStore) write(doc fileDocument) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod credential dir: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return fmt.Errorf("create credential file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod credential file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write credential file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credential file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	cleanup = false
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("chmod credential file: %w", err)
	}
	return nil
}
