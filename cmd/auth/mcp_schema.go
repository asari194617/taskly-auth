package main

import (
	"context"
	"fmt"
)

// Nullable columns preserve the existing taskly:login grants and token rows.
// New MCP flows require an explicit resource and never infer one from NULL.
func (a *app) ensureMCPSchema(ctx context.Context) error {
	for _, column := range []struct{ table, name, definition string }{
		{"oauth_authorization_requests", "resource_uri", "VARCHAR(2048) NULL"},
		{"oauth_authorization_requests", "consent_user_id", "BIGINT UNSIGNED NULL"},
		{"oauth_authorization_requests", "consent_csrf_hash", "CHAR(64) NULL"},
		{"oauth_codes", "resource_uri", "VARCHAR(2048) NULL"},
		{"oauth_access_tokens", "resource_uri", "VARCHAR(2048) NULL"},
		{"oauth_access_tokens", "parent_token_hash", "CHAR(64) NULL"},
		{"oauth_refresh_tokens", "resource_uri", "VARCHAR(2048) NULL"},
	} {
		var count int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, column.table, column.name).Scan(&count); err != nil {
			return fmt.Errorf("inspect MCP OAuth schema: %w", err)
		}
		if count == 0 {
			if _, err := a.db.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("upgrade MCP OAuth schema: %w", err)
			}
		}
	}
	return nil
}
