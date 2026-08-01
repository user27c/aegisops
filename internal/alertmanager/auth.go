package alertmanager

import (
	"crypto/subtle"
	"os"
	"strings"
	"sync"
)

// TokenValidator 校验 Bearer Token。
type TokenValidator interface {
	// Validate 校验 token 是否有效。
	Validate(token string) bool
}

// FileTokenValidator 从 Secret 文件读取共享 Token，内存中常时比较。
type FileTokenValidator struct {
	mu    sync.RWMutex
	token string
}

// NewFileTokenValidator 从文件读取 Token。文件内容为单行 token（可含换行）。
func NewFileTokenValidator(path string) (*FileTokenValidator, error) {
	v := &FileTokenValidator{}
	if err := v.reload(path); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *FileTokenValidator) reload(path string) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- 路径来自 *_FILE 配置，非用户输入
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return os.ErrInvalid
	}
	v.mu.Lock()
	v.token = token
	v.mu.Unlock()
	return nil
}

// Validate 使用 constant-time 比较。
func (v *FileTokenValidator) Validate(token string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.token == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(v.token), []byte(token)) == 1
}
