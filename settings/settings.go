// Package settings keeps the app's settings in the database: one JSON value per key, encrypted
// with AES-256-GCM when there is a key (base64 of the nonce, then the sealed value).
package settings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// Schema creates the table; the app's migration runs it.
const Schema = `CREATE TABLE settings (key TEXT PRIMARY KEY NOT NULL, value TEXT NOT NULL)`

// App is the app's own settings, under the key "app". Fly secrets override the mail ones.
type App struct {
	Name          string `json:"name"`
	URL           string `json:"url"` // no trailing slash
	SenderName    string `json:"senderName"`
	SenderAddress string `json:"senderAddress"`
	SMTP          SMTP   `json:"smtp"`
}

type SMTP struct {
	Host     string `json:"host"` // "" for none
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	TLS      bool   `json:"tls"` // TLS from the first byte; otherwise STARTTLS
}

// DBTX runs queries; *sql.DB and *sql.Tx both do.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Get loads key's value into v, decrypting it with encKey if it's encrypted. A missing key
// leaves v as it is and returns false.
func Get(ctx context.Context, r DBTX, encKey, key string, v any) (bool, error) {
	var s string
	err := r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	b, err := Open(s, encKey)
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(b, v)
}

// Set stores v under key, encrypted if encKey isn't "".
func Set(ctx context.Context, w DBTX, encKey, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s := string(b)
	if encKey != "" {
		if s, err = encrypt(b, encKey); err != nil {
			return err
		}
	}
	_, err = w.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, s)
	return err
}

// Open is a stored value as JSON: as it is if it's plain JSON, else decrypted with encKey.
func Open(s, encKey string) ([]byte, error) {
	if json.Valid([]byte(s)) {
		return []byte(s), nil
	}
	if encKey == "" {
		return nil, errors.New("settings: the value is encrypted and there is no key")
	}
	gcm, err := newGCM(encKey)
	if err != nil {
		return nil, err
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) < gcm.NonceSize() {
		return nil, errors.New("settings: the value is neither JSON nor encrypted")
	}
	return gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], nil)
}

func encrypt(b []byte, encKey string) (string, error) {
	gcm, err := newGCM(encKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, b, nil)), nil
}

// newGCM takes the key as text: 32 characters for AES-256.
func newGCM(encKey string) (cipher.AEAD, error) {
	block, err := aes.NewCipher([]byte(encKey))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
