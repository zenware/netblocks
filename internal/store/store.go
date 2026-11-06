// Package store implements the SQLite cache used for all lookups.
//
// Numbers are stored as fixed-width hexadecimal strings so that IPv6 values,
// which do not fit in SQLite's signed 64-bit INTEGER, sort correctly in text
// indexes. An assignment is stored as [start_hex, next_start_hex), which makes
// point and overlap queries simple range predicates.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite"

	"github.com/zenware/netblocks/internal/netutil"
)

// SchemaVersion is bumped whenever the on-disk schema changes.
const SchemaVersion = "2"

// SchemaError indicates a cache created by an incompatible version.
type SchemaError struct {
	Have string
	Want string
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("cache schema version %s is not compatible with %s", e.Have, e.Want)
}

// Store is a handle to the local cache database.
type Store struct {
	db *sql.DB
}

// Assignment is one delegation or GeoIP record.
type Assignment struct {
	Start       *big.Int
	End         *big.Int
	NumType     netutil.NumType
	CountryCode string
	SourceType  string
	SourceName  string
	Status      string
	StartDate   string
	OpaqueID    string
}

// ASNAnnouncement is one BGP prefix announcement.
type ASNAnnouncement struct {
	Start      *big.Int
	End        *big.Int
	NumType    netutil.NumType
	ASN        uint32
	SourceType string
	SourceName string
}

// Org is the ASN and description responsible for an announcement.
type Org struct {
	ASN         uint32
	Description string
	Start       *big.Int
	End         *big.Int
}

// Comparison is an overlapping pair of assignments from two sources.
type Comparison struct {
	First  Assignment
	Second Assignment
}

// Stat is a row count grouped by source and number type.
type Stat struct {
	SourceType string `json:"source_type"`
	NumType    string `json:"num_type"`
	Count      int64  `json:"count"`
}

// Open opens or creates a cache database at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("empty database path")
	}
	if path != ":memory:" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

var memoryDBCounter atomic.Int64

func dsn(path string) string {
	if path == ":memory:" {
		n := memoryDBCounter.Add(1)
		return fmt.Sprintf("file:netblocks-memory-%d?mode=memory&cache=shared&_pragma=busy_timeout(10000)&_pragma=journal_mode(MEMORY)&_pragma=synchronous(OFF)", n)
	}
	abs, _ := filepath.Abs(path)
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	q := u.Query()
	for _, p := range []string{
		"busy_timeout(10000)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"cache_size(-65536)",
		"mmap_size(268435456)",
		"temp_store(MEMORY)",
	} {
		q.Add("_pragma", p)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) initSchema() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return err
	}
	var version string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := s.createSchema(); err != nil {
			return err
		}
		_, err = s.db.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES ('schema_version', ?)`, SchemaVersion)
		return err
	case err != nil:
		return err
	case version != SchemaVersion:
		return &SchemaError{Have: version, Want: SchemaVersion}
	}
	return s.ensureIndexes()
}

func (s *Store) createSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS assignments (
			id INTEGER PRIMARY KEY,
			start_hex TEXT NOT NULL,
			next_start_hex TEXT NOT NULL,
			num_type TEXT NOT NULL,
			country_code TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL,
			source_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			start_date TEXT NOT NULL DEFAULT '',
			opaque_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS asn_assignments (
			id INTEGER PRIMARY KEY,
			start_hex TEXT NOT NULL,
			next_start_hex TEXT NOT NULL,
			num_type TEXT NOT NULL,
			as_num INTEGER NOT NULL,
			source_type TEXT NOT NULL,
			source_name TEXT NOT NULL DEFAULT '',
			UNIQUE(num_type, start_hex, next_start_hex)
		)`,
		`CREATE TABLE IF NOT EXISTS asn_descriptions (
			as_num INTEGER NOT NULL,
			source_name TEXT NOT NULL,
			description TEXT NOT NULL,
			PRIMARY KEY (as_num, source_name)
		)`,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	if err := createIndexes(tx); err != nil {
		return err
	}
	return tx.Commit()
}

var assignmentIndexes = []struct{ name, sql string }{
	{"idx_assignments_lookup", `CREATE INDEX IF NOT EXISTS idx_assignments_lookup ON assignments(num_type, source_type, start_hex)`},
	{"idx_assignments_cc", `CREATE INDEX IF NOT EXISTS idx_assignments_cc ON assignments(num_type, country_code, source_type, start_hex)`},
	{"idx_assignments_source", `CREATE INDEX IF NOT EXISTS idx_assignments_source ON assignments(source_type)`},
	{"idx_assignments_start", `CREATE INDEX IF NOT EXISTS idx_assignments_start ON assignments(num_type, start_hex)`},
}

var asnIndexes = []struct{ name, sql string }{
	{"idx_asn_assignments_lookup", `CREATE INDEX IF NOT EXISTS idx_asn_assignments_lookup ON asn_assignments(num_type, start_hex)`},
	{"idx_asn_assignments_asn", `CREATE INDEX IF NOT EXISTS idx_asn_assignments_asn ON asn_assignments(as_num)`},
	{"idx_asn_descriptions_asn", `CREATE INDEX IF NOT EXISTS idx_asn_descriptions_asn ON asn_descriptions(as_num)`},
}

func createIndexes(tx *sql.Tx) error {
	for _, idx := range append(assignmentIndexes, asnIndexes...) {
		if _, err := tx.Exec(idx.sql); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureIndexes() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := createIndexes(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Erase closes and removes a cache database, including WAL sidecars.
func Erase(path string) error {
	if path == "" || path == ":memory:" {
		return nil
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// SetMeta writes a metadata value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`, key, value)
	return err
}

// AssignmentWriter inserts records in one transaction.
type AssignmentWriter struct {
	ctx   context.Context
	stmt  *sql.Stmt
	Count int64
}

// Insert adds an assignment.
func (w *AssignmentWriter) Insert(a Assignment) error {
	if err := validateAssignment(a); err != nil {
		return err
	}
	_, err := w.stmt.ExecContext(w.ctx,
		netutil.HexKey(a.Start, a.NumType),
		netutil.NextKey(a.End, a.NumType),
		string(a.NumType),
		a.CountryCode,
		a.SourceType,
		a.SourceName,
		a.Status,
		a.StartDate,
		a.OpaqueID,
	)
	if err != nil {
		return err
	}
	w.Count++
	return nil
}

func validateAssignment(a Assignment) error {
	if err := validateRange(a.Start, a.End, a.NumType); err != nil {
		return err
	}
	if a.SourceType == "" {
		return errors.New("assignment has no source type")
	}
	return nil
}

func validateRange(start, end *big.Int, numType netutil.NumType) error {
	if !numType.Valid() {
		return fmt.Errorf("invalid number type %q", numType)
	}
	if start == nil || end == nil || start.Sign() < 0 || end.Sign() < 0 {
		return errors.New("invalid assignment range")
	}
	if start.Cmp(end) > 0 {
		return fmt.Errorf("assignment start %s is after end %s", start, end)
	}
	limit := new(big.Int).Lsh(big.NewInt(1), uint(numType.Bits()))
	if end.Cmp(limit) >= 0 {
		return fmt.Errorf("assignment end %s exceeds %s range", end, numType)
	}
	return nil
}

// BulkAssignments atomically replaces all assignments for sourceType.
func (s *Store) BulkAssignments(ctx context.Context, sourceType string, fn func(*AssignmentWriter) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := dropAssignmentIndexes(tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM assignments WHERE source_type=?`, sourceType); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO assignments
		(start_hex, next_start_hex, num_type, country_code, source_type, source_name, status, start_date, opaque_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	w := &AssignmentWriter{ctx: ctx, stmt: stmt}
	if err := fn(w); err != nil {
		_ = stmt.Close()
		return err
	}
	if w.Count == 0 {
		_ = stmt.Close()
		return fmt.Errorf("no %s assignments parsed; cache left unchanged", sourceType)
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	if err := createIndexes(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func dropAssignmentIndexes(tx *sql.Tx) error {
	for _, idx := range assignmentIndexes {
		if _, err := tx.Exec(`DROP INDEX IF EXISTS ` + idx.name); err != nil {
			return err
		}
	}
	return nil
}

// ASNWriter inserts BGP announcements in one transaction.
type ASNWriter struct {
	ctx   context.Context
	stmt  *sql.Stmt
	Count int64
}

// Insert adds an announcement. Duplicate prefix entries are ignored so the
// first path in a RIB snapshot wins, matching router best-path ordering.
func (w *ASNWriter) Insert(a ASNAnnouncement) error {
	if a.NumType == netutil.ASN {
		return fmt.Errorf("invalid announcement number type %q", a.NumType)
	}
	if err := validateRange(a.Start, a.End, a.NumType); err != nil {
		return err
	}
	res, err := w.stmt.ExecContext(w.ctx,
		netutil.HexKey(a.Start, a.NumType),
		netutil.NextKey(a.End, a.NumType),
		string(a.NumType),
		int64(a.ASN),
		a.SourceType,
		a.SourceName,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		w.Count++
	}
	return nil
}

// BulkASNAnnouncements atomically replaces all BGP announcements for the
// named source names.
func (s *Store) BulkASNAnnouncements(ctx context.Context, sourceType string, sourceNames []string, fn func(*ASNWriter) error) error {
	if len(sourceNames) == 0 {
		return errors.New("no BGP source names provided")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_asn_assignments_lookup`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS idx_asn_assignments_asn`); err != nil {
		return err
	}
	for _, name := range sourceNames {
		if _, err := tx.ExecContext(ctx, `DELETE FROM asn_assignments WHERE source_type=? AND source_name=?`, sourceType, name); err != nil {
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO asn_assignments
		(start_hex, next_start_hex, num_type, as_num, source_type, source_name)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	w := &ASNWriter{ctx: ctx, stmt: stmt}
	if err := fn(w); err != nil {
		_ = stmt.Close()
		return err
	}
	if w.Count == 0 {
		_ = stmt.Close()
		return fmt.Errorf("no %s announcements parsed from %s; cache left unchanged", sourceType, strings.Join(sourceNames, ","))
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_asn_assignments_lookup ON asn_assignments(num_type, start_hex)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_asn_assignments_asn ON asn_assignments(as_num)`); err != nil {
		return err
	}
	return tx.Commit()
}

// BulkASNDescriptions atomically replaces the ASN description table.
func (s *Store) BulkASNDescriptions(ctx context.Context, fn func(insert func(asn uint32, source, description string) error) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM asn_descriptions`); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO asn_descriptions(as_num, source_name, description) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	var count int64
	insert := func(asn uint32, source, description string) error {
		_, err := stmt.ExecContext(ctx, int64(asn), source, description)
		if err == nil {
			count++
		}
		return err
	}
	if err := fn(insert); err != nil {
		_ = stmt.Close()
		return err
	}
	if count == 0 {
		_ = stmt.Close()
		return errors.New("no ASN descriptions parsed; cache left unchanged")
	}
	if err := stmt.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

// AssignmentsByCountry returns [start,end] assignments for a country.
func (s *Store) AssignmentsByCountry(ctx context.Context, numType netutil.NumType, country string, sources []string) ([]Assignment, error) {
	q := `SELECT start_hex, next_start_hex, num_type, country_code, source_type, source_name, status, start_date, opaque_id
		FROM assignments WHERE num_type=? AND country_code=?`
	args := []any{string(numType), country}
	q, args = addSourceFilter(q, args, sources)
	q += ` ORDER BY start_hex`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssignments(rows)
}

func addSourceFilter(q string, args []any, sources []string) (string, []any) {
	if len(sources) == 0 {
		return q, args
	}
	ph := make([]string, len(sources))
	for i, src := range sources {
		ph[i] = "?"
		args = append(args, src)
	}
	return q + ` AND source_type IN (` + strings.Join(ph, ",") + `)`, args
}

func scanAssignments(rows *sql.Rows) ([]Assignment, error) {
	var out []Assignment
	for rows.Next() {
		var (
			startHex, nextHex string
			numType           string
			a                 Assignment
		)
		if err := rows.Scan(&startHex, &nextHex, &numType, &a.CountryCode, &a.SourceType, &a.SourceName, &a.Status, &a.StartDate, &a.OpaqueID); err != nil {
			return nil, err
		}
		start, err := netutil.ParseHexKey(startHex)
		if err != nil {
			return nil, err
		}
		next, err := netutil.ParseHexKey(nextHex)
		if err != nil {
			return nil, err
		}
		a.NumType = netutil.NumType(numType)
		a.Start = start
		a.End = new(big.Int).Sub(next, big.NewInt(1))
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountryAt looks up the country assigned to a number in one source.
func (s *Store) CountryAt(ctx context.Context, numType netutil.NumType, source string, n *big.Int) (Assignment, bool, error) {
	key := netutil.HexKey(n, numType)
	row := s.db.QueryRowContext(ctx, `SELECT start_hex, next_start_hex, num_type, country_code, source_type, source_name, status, start_date, opaque_id
		FROM assignments WHERE num_type=? AND source_type=? AND start_hex<=? AND next_start_hex>?
		ORDER BY start_hex DESC, next_start_hex ASC LIMIT 1`, string(numType), source, key, key)
	var (
		startHex, nextHex string
		numTypeStr        string
		a                 Assignment
	)
	err := row.Scan(&startHex, &nextHex, &numTypeStr, &a.CountryCode, &a.SourceType, &a.SourceName, &a.Status, &a.StartDate, &a.OpaqueID)
	if errors.Is(err, sql.ErrNoRows) {
		return Assignment{}, false, nil
	}
	if err != nil {
		return Assignment{}, false, err
	}
	a.NumType = netutil.NumType(numTypeStr)
	a.Start, _ = netutil.ParseHexKey(startHex)
	next, _ := netutil.ParseHexKey(nextHex)
	a.End = new(big.Int).Sub(next, big.NewInt(1))
	return a, true, nil
}

// Overlaps returns assignments from one source that overlap [start,end].
func (s *Store) Overlaps(ctx context.Context, numType netutil.NumType, source string, start, end *big.Int) ([]Assignment, error) {
	return s.overlaps(ctx, s.db, numType, source, start, end)
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) overlaps(ctx context.Context, q queryer, numType netutil.NumType, source string, start, end *big.Int) ([]Assignment, error) {
	rows, err := q.QueryContext(ctx, `SELECT start_hex, next_start_hex, num_type, country_code, source_type, source_name, status, start_date, opaque_id
		FROM assignments WHERE num_type=? AND source_type=? AND start_hex<? AND next_start_hex>?
		ORDER BY start_hex`, string(numType), source, netutil.NextKey(end, numType), netutil.HexKey(start, numType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAssignments(rows)
}

// Compare returns every overlapping pair between firstSources and
// secondSources for a country. Exact self-pairs are included; callers that
// want conflict-only output should use Conflicts.
func (s *Store) Compare(ctx context.Context, country string, numTypes []netutil.NumType, firstSources, secondSources []string) ([]Comparison, error) {
	return s.compareEach(ctx, country, numTypes, firstSources, secondSources, false)
}

// Conflicts returns overlapping pairs whose second assignment has a different
// country code. This is the useful comparison mode for large LIR datasets.
func (s *Store) Conflicts(ctx context.Context, country string, numTypes []netutil.NumType, firstSources, secondSources []string) ([]Comparison, error) {
	return s.compareEach(ctx, country, numTypes, firstSources, secondSources, true)
}

func (s *Store) compareEach(ctx context.Context, country string, numTypes []netutil.NumType, firstSources, secondSources []string, conflictOnly bool) ([]Comparison, error) {
	var out []Comparison
	for _, numType := range numTypes {
		firsts, err := s.AssignmentsByCountry(ctx, numType, country, firstSources)
		if err != nil {
			return nil, err
		}
		if len(firsts) == 0 {
			continue
		}
		q := `SELECT start_hex, next_start_hex, num_type, country_code, source_type, source_name, status, start_date, opaque_id
			FROM assignments WHERE num_type=?`
		args := []any{string(numType)}
		if conflictOnly {
			q += ` AND country_code<>?`
			args = append(args, country)
		}
		q, args = addSourceFilter(q, args, secondSources)
		q += ` ORDER BY start_hex`
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}

		var pending *Assignment
		var active []Assignment
		exhausted := false
		closeRows := func() error {
			rows.Close()
			return rows.Err()
		}
		for i, first := range firsts {
			if i%1000 == 0 {
				if err := ctx.Err(); err != nil {
					rows.Close()
					return nil, err
				}
			}
			// Drop ranges that ended before this first range starts. Since
			// firsts are sorted by start, they cannot overlap any later first.
			keep := active[:0]
			for _, sec := range active {
				if sec.End.Cmp(first.Start) >= 0 {
					keep = append(keep, sec)
				}
			}
			active = keep

			for {
				var sec Assignment
				if pending != nil {
					sec = *pending
					pending = nil
				} else if !exhausted {
					if rows.Next() {
						var startHex, nextHex, numTypeStr string
						if err := rows.Scan(&startHex, &nextHex, &numTypeStr, &sec.CountryCode, &sec.SourceType, &sec.SourceName, &sec.Status, &sec.StartDate, &sec.OpaqueID); err != nil {
							rows.Close()
							return nil, err
						}
						sec.NumType = netutil.NumType(numTypeStr)
						sec.Start, err = netutil.ParseHexKey(startHex)
						if err != nil {
							rows.Close()
							return nil, err
						}
						next, err := netutil.ParseHexKey(nextHex)
						if err != nil {
							rows.Close()
							return nil, err
						}
						sec.End = new(big.Int).Sub(next, big.NewInt(1))
					} else {
						exhausted = true
						if err := closeRows(); err != nil {
							return nil, err
						}
						break
					}
				} else {
					break
				}

				if sec.Start.Cmp(first.End) > 0 {
					// This range starts after the current first but may
					// overlap a later, larger first range.
					pending = &sec
					break
				}
				if sec.End.Cmp(first.Start) >= 0 {
					active = append(active, sec)
				}
			}
			for _, sec := range active {
				out = append(out, Comparison{First: first, Second: sec})
			}
		}
		if !exhausted {
			if err := closeRows(); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// OrgForIP returns announcements covering an address, with descriptions.
func (s *Store) OrgForIP(ctx context.Context, numType netutil.NumType, n *big.Int) ([]Org, error) {
	key := netutil.HexKey(n, numType)
	rows, err := s.db.QueryContext(ctx, `SELECT a.as_num, COALESCE(
			(SELECT d.description FROM asn_descriptions d WHERE d.as_num=a.as_num
			 ORDER BY CASE d.source_name WHEN 'cidr_report' THEN 0 ELSE 1 END, d.source_name LIMIT 1), ''),
			a.start_hex, a.next_start_hex
		FROM asn_assignments a
		WHERE a.num_type=? AND a.start_hex<=? AND a.next_start_hex>?
		ORDER BY a.start_hex DESC, a.next_start_hex ASC`, string(numType), key, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrgs(rows)
}

// OrgForRange returns announcements fully contained in an address range.
func (s *Store) OrgForRange(ctx context.Context, numType netutil.NumType, start, end *big.Int) ([]Org, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.as_num, COALESCE(
			(SELECT d.description FROM asn_descriptions d WHERE d.as_num=a.as_num
			 ORDER BY CASE d.source_name WHEN 'cidr_report' THEN 0 ELSE 1 END, d.source_name LIMIT 1), ''),
			a.start_hex, a.next_start_hex
		FROM asn_assignments a
		WHERE a.num_type=? AND a.start_hex>=? AND a.next_start_hex<=?
		ORDER BY a.start_hex`, string(numType), netutil.HexKey(start, numType), netutil.NextKey(end, numType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrgs(rows)
}

func scanOrgs(rows *sql.Rows) ([]Org, error) {
	var out []Org
	for rows.Next() {
		var (
			asn               int64
			startHex, nextHex string
			o                 Org
		)
		if err := rows.Scan(&asn, &o.Description, &startHex, &nextHex); err != nil {
			return nil, err
		}
		o.ASN = uint32(asn)
		o.Start, _ = netutil.ParseHexKey(startHex)
		next, _ := netutil.ParseHexKey(nextHex)
		o.End = new(big.Int).Sub(next, big.NewInt(1))
		out = append(out, o)
	}
	return out, rows.Err()
}

// ASNDescription returns the best description for an ASN.
func (s *Store) ASNDescription(ctx context.Context, asn uint32) (string, error) {
	var description string
	err := s.db.QueryRowContext(ctx, `SELECT description FROM asn_descriptions WHERE as_num=?
		ORDER BY CASE source_name WHEN 'cidr_report' THEN 0 ELSE 1 END, source_name LIMIT 1`, int64(asn)).Scan(&description)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return description, err
}

// ASNAnnouncementCount returns how many prefixes an ASN originates.
func (s *Store) ASNAnnouncementCount(ctx context.Context, asn uint32) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asn_assignments WHERE as_num=?`, int64(asn)).Scan(&n)
	return n, err
}

// CountAssignments returns the number of delegation/GeoIP records.
func (s *Store) CountAssignments(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assignments`).Scan(&n)
	return n, err
}

// CountryCodes returns country codes present in the delegation cache.
func (s *Store) CountryCodes(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT country_code FROM assignments
		WHERE country_code<>'' AND country_code<>'*' ORDER BY country_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cc string
		if err := rows.Scan(&cc); err != nil {
			return nil, err
		}
		out = append(out, cc)
	}
	return out, rows.Err()
}

// StatusCounts returns delegation record counts grouped by status.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM assignments
		WHERE source_type='rir' GROUP BY status ORDER BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		if status == "" {
			status = "(none)"
		}
		out[status] = count
	}
	return out, rows.Err()
}

// ReservedCount counts RIR records that are not assigned to a country.
func (s *Store) ReservedCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM assignments
		WHERE source_type='rir' AND (country_code='*' OR country_code='' OR status IN ('reserved','available'))`).Scan(&n)
	return n, err
}

// Stats returns row counts by source and number type, plus BGP counts.
func (s *Store) Stats(ctx context.Context) ([]Stat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_type, num_type, COUNT(*) FROM assignments
		GROUP BY source_type, num_type ORDER BY source_type, num_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stat
	for rows.Next() {
		var st Stat
		if err := rows.Scan(&st.SourceType, &st.NumType, &st.Count); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	bgpRows, err := s.db.QueryContext(ctx, `SELECT num_type, COUNT(*) FROM asn_assignments GROUP BY num_type ORDER BY num_type`)
	if err != nil {
		return nil, err
	}
	defer bgpRows.Close()
	for bgpRows.Next() {
		var st Stat
		if err := bgpRows.Scan(&st.NumType, &st.Count); err != nil {
			return nil, err
		}
		st.SourceType = "bgp_snapshot"
		out = append(out, st)
	}
	if err := bgpRows.Err(); err != nil {
		return nil, err
	}
	var desc int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM asn_descriptions`).Scan(&desc); err != nil {
		return nil, err
	}
	out = append(out, Stat{SourceType: "cidr_report", NumType: "descriptions", Count: desc})
	return out, nil
}

// ExportCountryRows streams country assignments ordered by start.
func (s *Store) ExportCountryRows(ctx context.Context, numType netutil.NumType, sources []string, fn func(Assignment) error) error {
	q := `SELECT start_hex, next_start_hex, num_type, country_code, source_type, source_name, status, start_date, opaque_id
		FROM assignments WHERE num_type=?`
	args := []any{string(numType)}
	q, args = addSourceFilter(q, args, sources)
	q += ` ORDER BY start_hex, country_code, source_name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			startHex, nextHex string
			numTypeStr        string
			a                 Assignment
		)
		if err := rows.Scan(&startHex, &nextHex, &numTypeStr, &a.CountryCode, &a.SourceType, &a.SourceName, &a.Status, &a.StartDate, &a.OpaqueID); err != nil {
			return err
		}
		a.NumType = netutil.NumType(numTypeStr)
		a.Start, _ = netutil.ParseHexKey(startHex)
		next, _ := netutil.ParseHexKey(nextHex)
		a.End = new(big.Int).Sub(next, big.NewInt(1))
		if err := fn(a); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ExportASNRows streams announcements ordered by start.
func (s *Store) ExportASNRows(ctx context.Context, numType netutil.NumType, fn func(ASNAnnouncement) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT start_hex, next_start_hex, num_type, as_num, source_type, source_name
		FROM asn_assignments WHERE num_type=? ORDER BY start_hex`, string(numType))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			startHex, nextHex string
			asn               int64
			a                 ASNAnnouncement
		)
		if err := rows.Scan(&startHex, &nextHex, &a.NumType, &asn, &a.SourceType, &a.SourceName); err != nil {
			return err
		}
		a.ASN = uint32(asn)
		a.Start, _ = netutil.ParseHexKey(startHex)
		next, _ := netutil.ParseHexKey(nextHex)
		a.End = new(big.Int).Sub(next, big.NewInt(1))
		if err := fn(a); err != nil {
			return err
		}
	}
	return rows.Err()
}
