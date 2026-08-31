// Package crypto 提供 AES-256-GCM 加解密。
// 第一波用于预留（配置文件已承载全部凭据）；第二波 OAuth 令牌入库时启用。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Encryptor AES-256-GCM 加解密器；密钥为 32 字节（由 64 位 hex 配置解析）。
type Encryptor struct {
	aead cipher.AEAD
}

// New 从 hex 密钥构造（64 hex 字符 = 32 字节 = AES-256）。
func New(keyHex string) (*Encryptor, error) {
	if keyHex == "" {
		return nil, errors.New("加密密钥为空")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("加密密钥不是合法 hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("加密密钥必须为 64 位 hex（32 字节），当前 %d 字节", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encryptor{aead: aead}, nil
}

// NewOrCreate 优先使用显式配置的主密钥；未配置时在本机创建权限为 0600 的
// 持久化密钥文件。这样平台配置密钥不会明文入库，也不会因服务重启而失效。
func NewOrCreate(keyHex, keyFile string) (*Encryptor, error) {
	if strings.TrimSpace(keyHex) != "" {
		return New(strings.TrimSpace(keyHex))
	}
	keyFile = strings.TrimSpace(keyFile)
	if keyFile == "" {
		return nil, errors.New("加密密钥与密钥文件不能同时为空")
	}
	if raw, err := os.ReadFile(keyFile); err == nil {
		return New(strings.TrimSpace(string(raw)))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取平台配置密钥文件: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, fmt.Errorf("创建平台配置密钥目录: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	encoded := hex.EncodeToString(key)
	file, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		raw, readErr := os.ReadFile(keyFile)
		if readErr != nil {
			return nil, readErr
		}
		return New(strings.TrimSpace(string(raw)))
	}
	if err != nil {
		return nil, fmt.Errorf("创建平台配置密钥文件: %w", err)
	}
	if _, err = file.WriteString(encoded + "\n"); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	return New(encoded)
}

// Encrypt 输出 base64(nonce + 密文)。
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(e.aead.Seal(nonce, nonce, []byte(plaintext), nil)), nil
}

// Decrypt 解密 Encrypt 的产物；密文损坏返回错误。
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	ns := e.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("密文长度不足")
	}
	plain, err := e.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
