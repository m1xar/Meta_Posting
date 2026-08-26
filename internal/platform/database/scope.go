package database

import (
	"errors"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrScopeRequired is returned when a query reaches the repository layer
// without a tenant scope. It is a programming error, surfaced loudly rather
// than defaulting to "show everything".
var ErrScopeRequired = errors.New("database: query scope is required")

// Scope is the tenant boundary applied to every list and fetch.
//
// The zero value denies. That is the whole design: a handler that forgets to
// set a scope gets an error instead of another tenant's rows, so the failure
// mode of forgetfulness is a broken endpoint rather than a silent data leak.
// The compiler cannot enforce this, so the integration suite asserts it
// endpoint by endpoint.
type Scope struct {
	UserID uuid.UUID
	Admin  bool
}

// UserScope restricts a query to one tenant.
func UserScope(userID uuid.UUID) Scope { return Scope{UserID: userID} }

// AdminScope removes the restriction. Callers should only build one on an
// explicitly administrative route, never as a fallback.
func AdminScope() Scope { return Scope{Admin: true} }

func (s Scope) Valid() bool { return s.Admin || s.UserID != uuid.Nil }

// Apply restricts a query to rows the scope may see. alias is the table or
// alias carrying connection_id.
//
// A correlated subquery rather than a join: it composes with Model()-based
// queries, cannot duplicate rows, and needs no knowledge of what else the
// caller has already joined.
func (s Scope) Apply(query *gorm.DB, alias string) *gorm.DB {
	if s.Admin {
		return query
	}
	return query.Where(
		alias+".connection_id IN (SELECT id FROM meta_connections WHERE user_id = ?)",
		s.UserID,
	)
}

// ApplyUserColumn is for tables that carry user_id directly, such as
// meta_connections and oauth_sessions.
func (s Scope) ApplyUserColumn(query *gorm.DB, alias string) *gorm.DB {
	if s.Admin {
		return query
	}
	return query.Where(alias+".user_id = ?", s.UserID)
}

// ApplyAdAccount restricts a query on a table whose only tenant link is an ad
// account reference.
func (s Scope) ApplyAdAccount(query *gorm.DB, alias string) *gorm.DB {
	if s.Admin {
		return query
	}
	return query.Where(alias+`.ad_account_id IN (
		SELECT a.id FROM ad_accounts a
		JOIN meta_connections c ON c.id = a.connection_id
		WHERE c.user_id = ?
	)`, s.UserID)
}
