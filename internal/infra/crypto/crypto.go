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
