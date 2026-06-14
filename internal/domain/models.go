// Package domain holds harbrr's persisted entity structs — plain data shared by
// the database repositories, the auth service, and the indexer registry. Keeping
// them in a neutral leaf package lets the storage layer return typed entities
// without coupling it to the business packages that consume them (the qui
// internal/domain pattern).
package domain

import "time"

// IndexerInstance is a configured tracker: a definition id plus user-chosen
// identity and base URL. The integer ID is internal and stable (it backs the
// encryption AAD of its secret settings); Slug is the stable user-facing
// identifier used as the Torznab {indexerId} path segment and the management
// resource id.
type IndexerInstance struct {
	ID           int64
	Slug         string
	DefinitionID string
	Name         string
	BaseURL      string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IndexerSetting is one configured setting of an instance. A secret setting
// (IsSecret) stores its value in ValueEncrypted (base64 nonce‖ciphertext‖tag)
// with the KeyID that encrypted it and an empty Value; a plaintext setting stores
// Value and leaves ValueEncrypted/KeyID empty.
type IndexerSetting struct {
	Name           string
	Value          string
	ValueEncrypted string
	KeyID          string
	IsSecret       bool
}
