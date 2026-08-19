package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/keepon-online/sb-ops/internal/service/common"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Encrypt(ctx context.Context, keyName string, plaintext []byte) ([]byte, error) {
	key, err := s.key(ctx, keyName)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *Store) Decrypt(ctx context.Context, keyName string, ciphertext []byte) ([]byte, error) {
	key, err := s.key(ctx, keyName)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("加密数据被截断")
	}
	nonce, value := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, value, nil)
	if err != nil {
		return nil, fmt.Errorf("解密数据: %w", err)
	}
	return plaintext, nil
}

func (s *Store) key(ctx context.Context, name string) ([]byte, error) {
	var key []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM control_plane_secrets WHERE name=?`, name).Scan(&key)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO control_plane_secrets(name,value,created_at) VALUES(?,?,?)`, name, key, common.UnixTime(time.Now()))
	if err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM control_plane_secrets WHERE name=?`, name).Scan(&key); err != nil {
		return nil, err
	}
	return key, nil
}
