package storage

// Backup is the backend-neutral, versioned offline database format. Sessions
// are intentionally excluded: moving or restarting a deployment revokes login.
// Only the encrypted archive may serialize these records outside storage.
type Backup struct {
	Format    int
	Inventory Inventory
	KeyCheck  []byte
	Versions  []CredentialVersion
	Sources   []Source
	Audit     []AuditRecord
	Admin     *BackupAdmin
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
