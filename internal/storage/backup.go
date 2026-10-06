package storage

// BackupFormat includes MCP credentials and client identities. Earlier readers
// must reject this format instead of silently discarding these records.
const BackupFormat = 2

// Backup is the backend-neutral, versioned offline database format. Sessions
// are not persisted: JWT validity is determined by external keys and expiry.
// Only the encrypted archive may serialize these records outside storage.
type Backup struct {
	Format    int
	Inventory Inventory
	KeyCheck  []byte
	Versions  []CredentialVersion
	Sources   []Source
	Audit     []AuditRecord
	Admin     *BackupAdmin
	MCPTokens []MCPTokenRecord
}

type CredentialVersion struct {
	ID, Version, Kind string
	Ciphertext        []byte
}

type BackupAdmin struct {
	Username     string
	PasswordHash []byte
	Version      string
}
