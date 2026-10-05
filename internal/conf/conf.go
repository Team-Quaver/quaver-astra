// Package conf 管理 quaver-astra.json 配置（键值对，落盘原子写）。
package conf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

type Store struct {
	path string
	mu   sync.Mutex
	data map[string]any
}

// Open 读取 <dir>/quaver-astra.json（没有则用默认值）。
func Open(dir string) *Store {
	s := &Store{path: filepath.Join(dir, "quaver-astra.json"), data: map[string]any{}}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.data)
	}
	return s
}

func (s *Store) Get(key string, def any) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.data[key]; ok {
		return v
	}
	return def
}

func (s *Store) Set(key string, val any) {
	s.mu.Lock()
	s.data[key] = val
	s.mu.Unlock()
	s.Save()
}

func (s *Store) String(key, def string) string {
	if v, ok := s.Get(key, def).(string); ok {
		return v
	}
	return def
}

func (s *Store) Float(key string, def float64) float64 {
	switch v := s.Get(key, def).(type) {
	case float64:
		return v
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func (s *Store) Bool(key string, def bool) bool {
	if v, ok := s.Get(key, def).(bool); ok {
		return v
	}
	return def
}

// Save 原子落盘（tmp + rename）。
func (s *Store) Save() {
	s.mu.Lock()
	b, err := json.MarshalIndent(s.data, "", "  ")
	p := s.path
	s.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}
