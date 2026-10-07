package backup

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Postgres is one database as the client tools reach it. The password goes to
// them in PGPASSWORD, never on the command line, where a process listing
// would show it.
type Postgres struct {
	// URL is a libpq URL, password included.
	URL string
}

func (p Postgres) parsed() (*url.URL, string, error) {
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, "", fmt.Errorf("backup: DATABASE_URL must be a postgres:// URL")
	}
	password, _ := u.User.Password()
	query := u.Query()
	if password == "" {
		password = query.Get("password")
	}
	query.Del("password")
	u.RawQuery = query.Encode()
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	return u, password, nil
}

// Database is the database name the URL points at.
func (p Postgres) Database() (string, error) {
	u, _, err := p.parsed()
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", fmt.Errorf("backup: DATABASE_URL names no database")
	}
	return name, nil
}

// On is the same server and credentials, another database.
func (p Postgres) On(database string) Postgres {
	u, err := url.Parse(p.URL)
	if err != nil {
		return p
	}
	u.Path = "/" + database
	u.RawPath = ""
	return Postgres{URL: u.String()}
}

func (p Postgres) command(ctx context.Context, tool string, args ...string) (*exec.Cmd, error) {
	u, password, err := p.parsed()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, tool, append(args, "--no-password", "--dbname="+u.String())...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+password)
	return cmd, nil
}

func (p Postgres) connect(ctx context.Context) (*pgx.Conn, error) {
	conn, err := pgx.Connect(ctx, p.URL)
	if err != nil {
		return nil, fmt.Errorf("backup: connecting to the database: %w", err)
	}
	return conn, nil
}

// Tools is what the client tools and the server say about their versions.
type Tools struct {
	DumpVersion   string
	ServerVersion string
	// Problem is why a backup cannot be taken with these tools, or "".
	Problem string
}

var majorVersion = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)

// CheckTools compares pg_dump's major version with the server's. pg_dump can
// read an older server but refuses a newer one, and pg_restore cannot read an
// archive from a newer pg_dump, so the image carries the server's own major.
func CheckTools(ctx context.Context, db Postgres) Tools {
	var tools Tools
	out, err := exec.CommandContext(ctx, "pg_dump", "--version").Output()
	if err != nil {
		tools.Problem = "pg_dump is not installed where this process can run it: " + err.Error()
		return tools
	}
	tools.DumpVersion = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "pg_dump (PostgreSQL)"))
	if _, err := exec.LookPath("pg_restore"); err != nil {
		tools.Problem = "pg_restore is not installed where this process can run it"
		return tools
	}

	conn, err := db.connect(ctx)
	if err != nil {
		tools.Problem = err.Error()
		return tools
	}
	defer conn.Close(context.Background())
	var serverNum int
	if err := conn.QueryRow(ctx, `SELECT current_setting('server_version'), current_setting('server_version_num')::int`).
		Scan(&tools.ServerVersion, &serverNum); err != nil {
		tools.Problem = "reading the server's version: " + err.Error()
		return tools
	}
	dumpMajor := 0
	if match := majorVersion.FindStringSubmatch(tools.DumpVersion); match != nil {
		dumpMajor, _ = strconv.Atoi(match[1])
	}
	if serverMajor := serverNum / 10000; dumpMajor < serverMajor {
		tools.Problem = fmt.Sprintf("pg_dump is version %s and the server is %s; "+
			"pg_dump must be the server's major version or newer", tools.DumpVersion, tools.ServerVersion)
	}
	return tools
}

// tail keeps the end of a tool's stderr for an error message.
type tail struct{ bytes.Buffer }

func (t *tail) String() string {
	text := strings.TrimSpace(t.Buffer.String())
	if len(text) > 2000 {
		text = "…" + text[len(text)-2000:]
	}
	return text
}

func toolError(tool string, err error, stderr *tail) error {
	if detail := stderr.String(); detail != "" {
		return fmt.Errorf("backup: %s: %w: %s", tool, err, detail)
	}
	return fmt.Errorf("backup: %s: %w", tool, err)
}

// lockKey is the advisory lock every backup and restore holds, so a nightly
// run, a migrate's backup in its own container and a restore never overlap.
const lockKey int64 = 0x6167_6966_6262_6b70

// Lock waits for the backup lock and returns its release. The lock lives as
// long as its connection, which is to the maintenance database: advisory locks
// are per database, and a restore renames the live one, which it cannot do
// while a session holds it open.
func Lock(ctx context.Context, db Postgres) (func(), error) {
	conn, err := connectMaintenance(ctx, db)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		_ = conn.Close(context.Background())
		return nil, fmt.Errorf("backup: taking the backup lock: %w", err)
	}
	return func() { _ = conn.Close(context.Background()) }, nil
}
