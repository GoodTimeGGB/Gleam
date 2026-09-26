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

	"gleam/internal/atomicfile"
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
	LLMAPIKey string `json:"llm_api_key,omitempty"`
	// LLMAPIKeyHost 是这把 key 被授权发往的主机（llm.KeyScope 的形态：host:port）。
	// 只有 key 在的时候它才有意义，所以不单独校验。空值 = 老版本存的、还没绑过主机，
	// 由 ResolveLLMAPIKey 在第一次按主机取用时补上。
	LLMAPIKeyHost string        `json:"llm_api_key_host,omitempty"`
	Cloud         *CloudSession `json:"cloud,omitempty"`
	CloudCfg      *CloudConfig  `json:"cloud_config,omitempty"`
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

// write 落盘：0600 + 原子替换；Windows 上内容经 DPAPI 加密后写出。
func (s *Store) write(f *file) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data, err = encrypt(data)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data, 0o600)
}

// ---------- LLM API Key ----------

// ResolveLLMAPIKey 取出可发往 host 这把接入主机的密钥。
//
// 第二个返回值是**本地实际存着的那把 key 所属的主机**（没存过 key 则为空），
// 设置页要靠它把"当前没有可用密钥"说成"已存的密钥属于另一家厂商"——
// 只回一个 bool 的话，用户看到的就是"未设置"，然后去把上一家那把重新复制一遍。
//
// 主机不一致就是不给。这是整条规则存在的理由：在设置页把厂商从 A 换成 B，
// 旧 key 会被原样发到 B 的端点上——等于替用户把凭证转发给了第三方。
// 补绑（老数据 host 为空）只在"取用"这一刻发生一次：用户当时能正常跑这个接入，
// 说明那把 key 就是这家的；不补的话每次升级都全员"密钥凭空丢失"。
func (s *Store) ResolveLLMAPIKey(host string) (key, storedHost string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil || f.LLMAPIKey == "" {
		return "", ""
	}
	if f.LLMAPIKeyHost == "" && host != "" {
		f.LLMAPIKeyHost = host
		if err := s.write(f); err != nil {
			// 绑不上也要照常返回：少一次补绑只是下次再问一遍，
			// 而因为写失败就把可用密钥判成没有，是拿正确性换保守。
			return f.LLMAPIKey, host
		}
		return f.LLMAPIKey, host
	}
	if host != "" && f.LLMAPIKeyHost != host {
		return "", f.LLMAPIKeyHost
	}
	return f.LLMAPIKey, f.LLMAPIKeyHost
}

// SetLLMAPIKey 保存 LLM API Key 并绑定到 host（由调用方用 llm.KeyScope 算出）；
// 传空 key 表示清除（连同主机一起清掉）。
func (s *Store) SetLLMAPIKey(key, host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if f == nil {
		f = &file{}
	}
	f.LLMAPIKey, f.LLMAPIKeyHost = key, host
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
