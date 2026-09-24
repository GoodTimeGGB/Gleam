// Package credentials 管理 Gleam 的本地敏感凭证：LLM API Key 与云端登录会话。
// 所有数据仅存于本地独立文件（dataDir/credentials.json，权限 0600；Windows 上内容
// 经 DPAPI 当前用户域加密），不随 settings.yaml 序列化，绝不上传云端。
package credentials

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File 是凭证文件名（位于数据目录下）。
const File = "credentials.json"

// ErrNotFound 凭证文件不存在（首次使用）。
var ErrNotFound = errors.New("凭证尚未创建")

// CloudSession 云端（Supabase）登录会话。
type CloudSession struct {
	Provider     string    `json:"provider,omitempty"` // supabase
	UserID       string    `json:"user_id,omitempty"`
	Email        string    `json:"email,omitempty"`
	AvatarURL    string    `json:"avatar_url,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// CloudConfig 云端连接配置（anon public key 非机密，仅用于建立连接）。
type CloudConfig struct {
	SupabaseURL     string `json:"supabase_url,omitempty"`
	SupabaseAnonKey string `json:"supabase_anon_key,omitempty"`
}

// Store 本地凭证存储。
type Store struct {
	path string
	mu   sync.Mutex
}

// file 磁盘结构。
type file struct {
	LLMAPIKey string        `json:"llm_api_key,omitempty"`
	Cloud     *CloudSession `json:"cloud,omitempty"`
	CloudCfg  *CloudConfig  `json:"cloud_config,omitempty"`
}

// Open 打开（或创建）数据目录下的凭证存储。
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dataDir, File)}, nil
}

// Path 返回凭证文件绝对路径（用于设置页展示存储位置）。
func (s *Store) Path() string { return s.path }

func (s *Store) read() (*file, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	data, err = decrypt(data)
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// write 原子落盘并收紧权限（0600）；Windows 上内容经 DPAPI 加密后写出。
func (s *Store) write(f *file) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data, err = encrypt(data)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

// ---------- LLM API Key ----------

// GetLLMAPIKey 读取本地保存的 LLM API Key（不存在返回空串）。
func (s *Store) GetLLMAPIKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return ""
	}
	return f.LLMAPIKey
}

// SetLLMAPIKey 保存 LLM API Key；传空串表示清除。
func (s *Store) SetLLMAPIKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if f == nil {
		f = &file{}
	}
	f.LLMAPIKey = key
	return s.write(f)
}

// ---------- 云端配置 ----------

// GetCloudConfig 读取云端连接配置。
func (s *Store) GetCloudConfig() *CloudConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil || f.CloudCfg == nil {
		return &CloudConfig{}
	}
	return f.CloudCfg
}

// SetCloudConfig 保存云端连接配置（仅在非空字段更新）。
func (s *Store) SetCloudConfig(cfg CloudConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if f == nil {
		f = &file{}
	}
	if f.CloudCfg == nil {
		f.CloudCfg = &CloudConfig{}
	}
	if cfg.SupabaseURL != "" {
		f.CloudCfg.SupabaseURL = cfg.SupabaseURL
	}
	if cfg.SupabaseAnonKey != "" {
		f.CloudCfg.SupabaseAnonKey = cfg.SupabaseAnonKey
	}
	return s.write(f)
}

// ---------- 云端会话 ----------

// GetSession 读取登录会话；未登录返回 nil。
func (s *Store) GetSession() *CloudSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil || f.Cloud == nil {
		return nil
	}
	return f.Cloud
}

// SetSession 保存登录会话。
func (s *Store) SetSession(sess *CloudSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if f == nil {
		f = &file{}
	}
	f.Cloud = sess
	return s.write(f)
}

// ClearSession 登出：仅清除云端会话，保留连接配置与本地数据。
func (s *Store) ClearSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	f.Cloud = nil
	return s.write(f)
}
