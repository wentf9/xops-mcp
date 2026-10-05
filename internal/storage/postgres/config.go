package postgres

import (
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

func connectionConfig(dsn string) (*pgx.ConnConfig, error) {
	if os.Getenv("PGSERVICE") != "" {
		return nil, errors.New("PGSERVICE must be unset; use the explicit deployment URL")
	}
	u, err := url.Parse(dsn)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() == "" || strings.Trim(u.Path, "/") == "" || u.Fragment != "" {
		return nil, errors.New("PostgreSQL requires an explicit URL with host, user and database")
	}
	password, hasPassword := u.User.Password()
	if !hasPassword {
		return nil, errors.New("PostgreSQL URL must explicitly include a password (empty for external authentication)")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL URL parameters")
	}
	for key, values := range q {
		switch key {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "sslpassword":
		default:
			return nil, errors.New("unsupported PostgreSQL URL parameter")
		}
		if len(values) != 1 {
			return nil, errors.New("duplicate PostgreSQL URL parameter")
		}
	}
	// Shadow libpq environment/personal-file defaults. Only the deployment URL
	// can select credentials and TLS files; sslmode defaults to verify-full.
	for _, key := range []string{"passfile", "sslcert", "sslkey", "sslrootcert", "sslpassword"} {
		if !q.Has(key) {
			q.Set(key, "")
		}
	}
	if !q.Has("sslmode") {
		q.Set("sslmode", "verify-full")
	}
	q.Set("password", password)
	q.Set("host", u.Hostname())
	q.Set("user", u.User.Username())
	q.Set("dbname", strings.TrimPrefix(u.Path, "/"))
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	q.Set("port", port)
	q.Set("target_session_attrs", "read-write")
	q.Set("options", "")
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection string or TLS configuration")
	}
	return cfg, nil
}
