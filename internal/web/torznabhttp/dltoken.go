package torznabhttp

import (
	"encoding/base64"
	"fmt"

	"github.com/autobrr/harbrr/internal/secrets"
)

func dlTokenPurpose(indexerID string) string {
	return "dl-proxy:" + indexerID
}

// encodeDLToken seals the pre-resolution download link into an opaque, URL-safe
// token bound to indexerID, for the grab-time /dl proxy. The link may carry a
// passkey, so it must never reach the served feed in the clear:
//
// The token is always AEAD ciphertext, including when credential storage explicitly
// runs in plaintext mode. Plaintext mode uses a process-local transient token key, so
// its tokens expire across restarts instead of becoming forgeable. The AEAD purpose
// binds the token to indexerID, preventing cross-indexer replay.
//
// The result is base64url so it drops straight into a query parameter without
// escaping.
func encodeDLToken(kr *secrets.Keyring, indexerID, link string) (string, error) {
	blob, err := kr.SealToken(dlTokenPurpose(indexerID), link)
	if err != nil {
		return "", fmt.Errorf("dl token: encrypt: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(blob)), nil
}

// decodeDLToken reverses encodeDLToken, returning the pre-resolution link. It fails
// when the token is malformed or was not minted for indexerID (an AAD mismatch, so
// a token cannot be replayed across indexers). The error never carries the link.
func decodeDLToken(kr *secrets.Keyring, indexerID, token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("dl token: decode: %w", err)
	}
	link, err := kr.OpenToken(dlTokenPurpose(indexerID), string(raw))
	if err != nil {
		return "", fmt.Errorf("dl token: decrypt: %w", err)
	}
	return link, nil
}
