// migrate-auth copies the existing identity rows to an empty, separate MySQL
// database. Cutover requires paused identity writes; snapshot and verify modes
// are read-only on the source. It never deletes source rows and refuses to
// overwrite a non-empty target table.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

var tables = []string{"users", "totp_secrets", "recovery_codes", "passkeys", "login_codes", "sessions", "blocks"}

type sourceReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	sourceDSN := os.Getenv("SOURCE_DSN")
	targetDSN := os.Getenv("TARGET_DSN")
	if sourceDSN == "" || targetDSN == "" || sourceDSN == targetDSN {
		fail("SOURCE_DSN and TARGET_DSN must be set to different databases")
	}
	mode := os.Getenv("AUTH_MIGRATION_MODE")
	if mode == "" {
		mode = "cutover"
	}
	if mode != "cutover" && mode != "snapshot" && mode != "verify" {
		fail("AUTH_MIGRATION_MODE must be cutover, snapshot, or verify")
	}
	if mode == "cutover" && os.Getenv("AUTH_WRITES_PAUSED") != "1" {
		fail("AUTH_WRITES_PAUSED=1 is required after stopping identity writes")
	}
	// Raw DATETIME bytes must be copied unchanged; parseTime=true would turn
	// them into RFC3339 strings that MySQL DATETIME cannot accept on insert.
	sourceConfig, err := mysql.ParseDSN(sourceDSN)
	if err != nil {
		fail("invalid SOURCE_DSN")
	}
	targetConfig, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		fail("invalid TARGET_DSN")
	}
	sourceConfig.ParseTime = false
	targetConfig.ParseTime = false
	source, err := sql.Open("mysql", sourceConfig.FormatDSN())
	if err != nil {
		fail(err)
	}
	defer source.Close()
	target, err := sql.Open("mysql", targetConfig.FormatDSN())
	if err != nil {
		fail(err)
	}
	defer target.Close()
	if err := source.PingContext(ctx); err != nil {
		fail("source connection: ", err)
	}
	if err := target.PingContext(ctx); err != nil {
		fail("target connection: ", err)
	}
	var sourceDB, targetDB string
	if err := source.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&sourceDB); err != nil {
		fail(err)
	}
	if err := target.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&targetDB); err != nil {
		fail(err)
	}
	if sourceDB == targetDB {
		fail("source and target database names must differ")
	}
	if mode == "verify" {
		if err := verifyExisting(ctx, source, target); err != nil {
			fail("read-only identity verification: ", err)
		}
		log.Println("read-only identity verification matched; writes were not paused, so this is not a cutover approval")
		return
	}
	var reading sourceReader = source
	if mode == "snapshot" {
		// A consistent read-only source snapshot permits a rehearsal while the
		// legacy app stays live. Its target is never a cutover-ready database.
		tx, err := source.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			fail("source snapshot: ", err)
		}
		defer tx.Rollback()
		reading = tx
	}
	for _, table := range tables {
		if err := copyTable(ctx, reading, target, table); err != nil {
			failf("%s: %v", table, err)
		}
	}
	for _, table := range tables {
		sCount, sHash, err := fingerprint(ctx, reading, table)
		if err != nil {
			fail(err)
		}
		tCount, tHash, err := fingerprint(ctx, target, table)
		if err != nil {
			fail(err)
		}
		if sCount != tCount || sHash != tHash {
			failf("%s changed during copy; keep old login active and reconcile before retry", table)
		}
	}
	if mode == "snapshot" {
		log.Println("snapshot rehearsal verified; target is not eligible for traffic cutover")
	} else {
		log.Println("identity copy verified; old tables remain intact; reconcile writes before switching traffic")
	}
}

func verifyExisting(ctx context.Context, source, target *sql.DB) error {
	// Each database is read through a consistent snapshot. The two snapshots
	// are not atomic together; a final cutover still requires paused writes.
	sourceTx, err := source.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("source snapshot: %w", err)
	}
	defer sourceTx.Rollback()
	targetTx, err := target.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("target snapshot: %w", err)
	}
	defer targetTx.Rollback()
	var mismatches []string
	for _, table := range tables {
		sCount, sHash, err := fingerprint(ctx, sourceTx, table)
		if err != nil {
			return fmt.Errorf("source %s: %w", table, err)
		}
		tCount, tHash, err := fingerprint(ctx, targetTx, table)
		if err != nil {
			return fmt.Errorf("target %s: %w", table, err)
		}
		if sCount != tCount || sHash != tHash {
			log.Printf("%s: drift source_count=%d target_count=%d", table, sCount, tCount)
			mismatches = append(mismatches, table)
		} else {
			log.Printf("%s: %d rows match", table, sCount)
		}
	}
	if len(mismatches) != 0 {
		return fmt.Errorf("drift in %s; keep legacy login active and recopy to a fresh rehearsal database", strings.Join(mismatches, ", "))
	}
	return nil
}

func fail(values ...any)                 { notifyFailure(); log.Fatal(values...) }
func failf(format string, values ...any) { notifyFailure(); log.Fatalf(format, values...) }
func notifyFailure() {
	webhook := os.Getenv("DISCORD_WEBHOOK_URL")
	if webhook == "" {
		return
	}
	data, _ := json.Marshal(map[string]string{"content": "Taskly auth database migration failed; keep the legacy login active and inspect operator logs."})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, strings.NewReader(string(data)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TasklyAuth/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func copyTable(ctx context.Context, source sourceReader, target *sql.DB, table string) error {
	// table is from the fixed allowlist above; never interpolate an input table.
	var ignored, ddl string
	if err := source.QueryRowContext(ctx, "SHOW CREATE TABLE `"+table+"`").Scan(&ignored, &ddl); err != nil {
		return err
	}
	var exists int
	err := target.QueryRowContext(ctx, "SELECT 1 FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?", table).Scan(&exists)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var count int64
		if err := target.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("target table contains %d rows; refusing to overwrite", count)
		}
	} else if _, err := target.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create: %w", err)
	}

	rows, err := source.QueryContext(ctx, "SELECT * FROM `"+table+"` ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	quoted := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	for i, col := range columns {
		quoted[i] = "`" + strings.ReplaceAll(col, "`", "``") + "`"
		placeholders[i] = "?"
	}
	query := "INSERT INTO `" + table + "` (" + strings.Join(quoted, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	values := make([]sql.RawBytes, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	var copied int64
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		args := make([]any, len(columns))
		for i, v := range values {
			if v != nil {
				args[i] = append([]byte(nil), v...)
			}
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("insert row %d: %w", copied+1, err)
		}
		copied++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	sCount, sHash, err := fingerprint(ctx, source, table)
	if err != nil {
		return err
	}
	tCount, tHash, err := fingerprint(ctx, target, table)
	if err != nil {
		return err
	}
	if sCount != copied || tCount != copied || sHash != tHash {
		return fmt.Errorf("verification failed: source count=%d target count=%d copied=%d source hash=%s target hash=%s", sCount, tCount, copied, sHash, tHash)
	}
	log.Printf("%s: %d rows verified sha256=%s", table, copied, sHash)
	return nil
}

func fingerprint(ctx context.Context, db sourceReader, table string) (int64, string, error) {
	rows, err := db.QueryContext(ctx, "SELECT * FROM `"+table+"` ORDER BY id")
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, "", err
	}
	values := make([]sql.RawBytes, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	h := sha256.New()
	var count int64
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return 0, "", err
		}
		for _, v := range values {
			hashField(h, v)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return count, hex.EncodeToString(h.Sum(nil)), nil
}

func hashField(h hash.Hash, value []byte) {
	var length [8]byte
	if value == nil {
		binary.BigEndian.PutUint64(length[:], ^uint64(0))
		h.Write(length[:])
		return
	}
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	h.Write(length[:])
	h.Write(value)
}
