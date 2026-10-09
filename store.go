package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Item 是一条共享条目。两种形态：
//   - kind=file：只登记本机文件路径，不复制文件内容
//   - kind=text：一条纯文本
type Item struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size"`
	Mime      string `json:"mime,omitempty"`
	Source    string `json:"source"`
	Text      string `json:"text,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// PublicItem 是发给客户端的裁剪版，不暴露本机磁盘路径。
type PublicItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Source    string `json:"source"`
	Text      string `json:"text,omitempty"`
	Exists    bool   `json:"exists"`
	CreatedAt int64  `json:"createdAt"`
}

func (i Item) toPublic() PublicItem {
	exists := true
	if i.Kind == "file" {
		exists = fileExists(i.Path)
	}
	return PublicItem{
		ID:        i.ID,
		Kind:      i.Kind,
		Name:      i.Name,
		Size:      i.Size,
		Source:    i.Source,
		Text:      i.Text,
		Exists:    exists,
		CreatedAt: i.CreatedAt,
	}
}

// Store 是带磁盘持久化的共享列表，读写都由一把互斥锁保护。
type Store struct {
	mu    sync.Mutex
	path  string
	items []Item
}

func NewStore(path string) *Store {
	return &Store{path: path, items: []Item{}}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		// 文件坏掉时不要静默清空，改名留档后再从空列表开始
		_ = os.Rename(s.path, s.path+".broken-"+time.Now().Format("20060102-150405"))
		return fmt.Errorf("共享列表文件无法解析，已备份为 .broken 文件: %w", err)
	}
	if items != nil {
		s.items = items
	}
	return nil
}

// save 必须在持有锁的情况下调用。
func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) List() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(id string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range s.items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// Add 补全 ID 与创建时间后入库，并把入库后的完整条目回传，
// 这样调用方能拿到真实的 ID。
func (s *Store) Add(items ...Item) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filled := make([]Item, 0, len(items))
	for _, it := range items {
		if it.ID == "" {
			it.ID = newID()
		}
		if it.CreatedAt == 0 {
			it.CreatedAt = time.Now().Unix()
		}
		filled = append(filled, it)
	}
	s.items = append(s.items, filled...)

	return filled, s.save()
}

func (s *Store) Remove(id string) (Item, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for idx, it := range s.items {
		if it.ID != id {
			continue
		}
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		return it, true, s.save()
	}
	return Item{}, false, nil
}

// RemoveByPath 用于避免同一条本机路径被重复登记。
func (s *Store) RemoveByPath(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := s.items[:0]
	for _, it := range s.items {
		if it.Kind == "file" && strings.EqualFold(it.Path, path) {
			continue
		}
		kept = append(kept, it)
	}
	s.items = kept
	return s.save()
}

func (s *Store) HasPath(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range s.items {
		if it.Kind == "file" && strings.EqualFold(it.Path, path) {
			return true
		}
	}
	return false
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// sanitizeFilename 处理各端上传时各种奇怪的文件名写法。
// 老安卓浏览器常见两种问题：把整个路径当文件名传、以及把中文做 URL 编码。
func sanitizeFilename(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "upload.bin"
	}

	name = stripPath(name)

	// 老安卓可能发来 percent-encoded 的 UTF-8 文件名
	if strings.Contains(name, "%") {
		if decoded, err := decodePercent(name); err == nil && decoded != name && utf8.ValidString(decoded) {
			name = stripPath(strings.TrimSpace(decoded))
		}
	}

	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)

	name = strings.Trim(name, " .")
	if name == "" {
		return "upload.bin"
	}
	return truncateName(name, 180)
}

func stripPath(name string) string {
	if idx := strings.LastIndexAny(name, `/\`); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

func decodePercent(value string) (string, error) {
	var b strings.Builder
	b.Grow(len(value))

	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			b.WriteByte(value[i])
			continue
		}
		if i+2 >= len(value) {
			return "", fmt.Errorf("bad escape")
		}
		hi, ok1 := hexVal(value[i+1])
		lo, ok2 := hexVal(value[i+2])
		if !ok1 || !ok2 {
			return "", fmt.Errorf("bad escape")
		}
		b.WriteByte(hi<<4 | lo)
		i += 2
	}
	return b.String(), nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func truncateName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	keep := limit - len(ext)
	if keep < 1 {
		keep = 1
	}
	for keep > 0 && !utf8.RuneStart(base[keep-1]) {
		keep--
	}
	if keep < 1 {
		keep = 1
	}
	return base[:keep] + ext
}

// uniquePath 在 dir 下为 name 找一个不冲突的落盘路径，重名追加 " (1)" " (2)"。
func uniquePath(dir, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	candidate := filepath.Join(dir, name)
	for i := 1; i <= 9999; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, time.Now().UnixNano(), ext))
}

func textItem(text string) Item {
	trimmed := strings.TrimSpace(text)
	preview := strings.Join(strings.Fields(trimmed), " ")
	if runes := []rune(preview); len(runes) > 40 {
		preview = string(runes[:40]) + "..."
	}

	return Item{
		Kind:      "text",
		Name:      preview,
		Text:      trimmed,
		Size:      int64(len(trimmed)),
		Source:    "client",
		CreatedAt: time.Now().Unix(),
	}
}
