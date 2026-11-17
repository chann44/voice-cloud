package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"voice-cloud/db"
)

func configuredSecretCipher() (cipher.AEAD, error) {
	value := os.Getenv("SECRETS_MASTER_KEY")
	if value == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, errors.New("SECRETS_MASTER_KEY must be a base64-encoded 32-byte random key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func validateProviderKey(key string) error {
	if len(key) > 4096 || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return invalid("provider API key must be at most 4096 bytes without whitespace or control characters")
	}
	return nil
}

func environmentCredential(reference string) string {
	if reference == "SECRETS_MASTER_KEY" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(reference))
}

func secretContext(connection db.Connection) []byte {
	return []byte(fmt.Sprintf("voice-cloud/provider/v1/%d/%s", connection.ID, connection.BaseUrl))
}

func (a *api) decryptProviderSecret(connection db.Connection, value []byte) (string, error) {
	if a.secrets == nil || len(value) < a.secrets.NonceSize()+a.secrets.Overhead()+1 {
		return "", errors.New("stored provider credential cannot be decrypted; check SECRETS_MASTER_KEY")
	}
	size := a.secrets.NonceSize()
	plain, err := a.secrets.Open(nil, value[:size], value[size:], secretContext(connection))
	if err != nil {
		return "", errors.New("stored provider credential cannot be decrypted; check SECRETS_MASTER_KEY")
	}
	return string(plain), nil
}

func (a *api) storeProviderSecret(ctx context.Context, queries *db.Queries, connection db.Connection, key string) error {
	if a.secrets == nil {
		return httpError{503, "provider secret storage is not configured; set SECRETS_MASTER_KEY in the API service"}
	}
	nonce := make([]byte, a.secrets.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return errors.New("could not encrypt provider credential")
	}
	encrypted := a.secrets.Seal(nonce, nonce, []byte(key), secretContext(connection))
	return queries.SetProviderSecret(ctx, db.SetProviderSecretParams{ConnectionID: connection.ID, Ciphertext: encrypted, UpdatedAt: time.Now().UnixMilli()})
}

// Stored credentials take precedence; decryption failure must never fall back to another key.
func (a *api) providerCredential(ctx context.Context, queries *db.Queries, connection db.Connection) (string, bool, error) {
	stored, err := queries.GetProviderSecret(ctx, connection.ID)
	if err == nil {
		key, err := a.decryptProviderSecret(connection, stored.Ciphertext)
		return key, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	if connection.CredentialRef.Valid {
		credential := environmentCredential(connection.CredentialRef.String)
		if credential == "" || strings.ContainsAny(credential, "\r\n") {
			return "", false, invalid("provider credential is missing or invalid in the API environment")
		}
		return credential, false, nil
	}
	return "", false, nil
}

func (a *api) authenticateProvider(ctx context.Context, request *http.Request, connection db.Connection) error {
	credential, _, err := a.providerCredential(ctx, a.queries, connection)
	if err != nil {
		return err
	}
	if credential != "" {
		switch connection.Adapter {
		case "deepgram":
			request.Header.Set("Authorization", "Token "+credential)
		case "elevenlabs":
			request.Header.Set("xi-api-key", credential)
		case "azure":
			request.Header.Set("api-key", credential)
		default:
			request.Header.Set("Authorization", "Bearer "+credential)
		}
	}
	return nil
}

func validateStoredSecrets(ctx context.Context, database *sql.DB, secretCipher cipher.AEAD) error {
	a := &api{queries: db.New(database), secrets: secretCipher}
	secrets, err := a.queries.ListProviderSecrets(ctx)
	if err != nil {
		return err
	}
	for _, secret := range secrets {
		connection, err := a.queries.GetConnection(ctx, secret.ConnectionID)
		if err != nil {
			return err
		}
		if _, err := a.decryptProviderSecret(connection, secret.Ciphertext); err != nil {
			return err
		}
	}
	return nil
}
