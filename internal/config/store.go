package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	encryptedFileVersion = 1
	saltSize             = 16
	nonceSize            = 12
	keyIterations        = 120_000
)

type Store struct {
	path string
}

type encryptedFile struct {
	Version    int    `json:"version"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// Exists reports whether an encrypted config file is already present. It is used
// to tell a first run (set + confirm a new master password) apart from unlocking
// an existing vault.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path)
	return err == nil
}

func (s *Store) Save(masterPassword string, vault Vault) error {
	if masterPassword == "" {
		return errors.New("master password is required")
	}
	plaintext, err := json.Marshal(vault)
	if err != nil {
		return fmt.Errorf("marshal vault: %w", err)
	}

	salt := make([]byte, saltSize)
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	block, err := aes.NewCipher(deriveKey(masterPassword, salt))
	if err != nil {
		return fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("create gcm: %w", err)
	}

	payload := encryptedFile{
		Version:    encryptedFileVersion,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: gcm.Seal(nil, nonce, plaintext, nil),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal encrypted file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}
	return nil
}

func (s *Store) Load(masterPassword string) (Vault, error) {
	if masterPassword == "" {
		return Vault{}, errors.New("master password is required")
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Vault{}, nil
	}
	if err != nil {
		return Vault{}, fmt.Errorf("read config file: %w", err)
	}

	var payload encryptedFile
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Vault{}, fmt.Errorf("parse config file: %w", err)
	}
	if payload.Version != encryptedFileVersion {
		return Vault{}, fmt.Errorf("unsupported config version %d", payload.Version)
	}
	if len(payload.Salt) != saltSize || len(payload.Nonce) != nonceSize {
		return Vault{}, errors.New("invalid encrypted config header")
	}

	block, err := aes.NewCipher(deriveKey(masterPassword, payload.Salt))
	if err != nil {
		return Vault{}, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Vault{}, fmt.Errorf("create gcm: %w", err)
	}
	plaintext, err := gcm.Open(nil, payload.Nonce, payload.Ciphertext, nil)
	if err != nil {
		return Vault{}, errors.New("decrypt config: invalid master password or corrupted file")
	}

	var vault Vault
	if err := json.Unmarshal(plaintext, &vault); err != nil {
		return Vault{}, fmt.Errorf("parse vault: %w", err)
	}
	return vault, nil
}

func deriveKey(masterPassword string, salt []byte) []byte {
	keySeed := sha256.Sum256(append(append([]byte{}, salt...), []byte(masterPassword)...))
	key := keySeed[:]
	var counter [8]byte
	for i := uint64(0); i < keyIterations; i++ {
		binary.BigEndian.PutUint64(counter[:], i)
		h := sha256.New()
		h.Write(key)
		h.Write(salt)
		h.Write([]byte(masterPassword))
		h.Write(counter[:])
		key = h.Sum(nil)
	}
	return key
}
