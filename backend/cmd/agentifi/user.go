package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// User administration from the command line. On a fresh install the first
// account is made on the sign-in screen, or here with `agentifi user add
// --superuser`. The account rules live in internal/service.

func userCommand(ctx context.Context, cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("agentifi user: expected `add`, `passwd`, `admin` or `list`")
	}

	flags := flag.NewFlagSet("agentifi user "+args[0], flag.ContinueOnError)
	email := flags.String("email", "", "the account's email address")
	name := flags.String("name", "", "the person's display name")
	space := flags.String("space", "Household", "name for the space created alongside the user")
	currency := flags.String("currency", cfg.PrimaryCurrency, "the space's reporting currency")
	// A password typed here is known to whoever runs the server, so `add`
	// forces a change by default; `passwd` inverts that.
	keep := flags.Bool("no-force-change", false, "let the account keep this password instead of changing it at next login")
	force := flags.Bool("force-change", false, "require a new password at next login")
	superuser := flags.Bool("superuser", false, "let this account administer the server from the app")
	on := flags.Bool("on", false, "with admin: let this account administer the server from the app")
	off := flags.Bool("off", false, "with admin: take that away; the last active superuser keeps it")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if args[0] != "list" && strings.TrimSpace(*email) == "" {
		return errors.New("agentifi user: --email is required")
	}
	if args[0] == "admin" && *on == *off {
		return errors.New("agentifi user admin: give exactly one of --on and --off")
	}

	db, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "add":
		return addUser(ctx, db, service.NewAccount{
			Email:              *email,
			FullName:           *name,
			MustChangePassword: !*keep,
			IsSuperuser:        *superuser,
			Placement:          service.Placement{SpaceName: *space, Currency: *currency},
		})
	case "passwd":
		return setPassword(ctx, db, *email, *force)
	case "admin":
		return setSuperuser(ctx, db, *email, *on, os.Stdout)
	case "list":
		return listUsers(ctx, db, os.Stdout)
	default:
		return fmt.Errorf("agentifi user: unknown action %q", args[0])
	}
}

// superusers is what `user admin` needs of the store.
type superusers interface {
	GetUserByEmail(ctx context.Context, email string) (store.User, error)
	SetUserSuperuser(ctx context.Context, id uuid.UUID, superuser bool) error
}

// setSuperuser grants or takes away the right to administer the server. The
// store refuses to take it from the last active superuser, since only a shell
// could give it back.
func setSuperuser(ctx context.Context, db superusers, email string, on bool, out io.Writer) error {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("agentifi user: no account for %s", email)
		}
		return err
	}
	if user.IsSuperuser == on {
		fmt.Fprintf(out, "%s %s; nothing changed\n", user.Email, administers(on))
		return nil
	}
	err = db.SetUserSuperuser(ctx, user.ID, on)
	if errors.Is(err, store.ErrLastSuperuser) {
		return fmt.Errorf("agentifi user: %s is the last active superuser; "+
			"make another account one first with `agentifi user admin --email <address> --on`", user.Email)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s\n", user.Email, administers(on))
	if on {
		fmt.Fprintln(out, "Settings -> Server admin is open to it")
	}
	return nil
}

func administers(on bool) string {
	if on {
		return "administers the server"
	}
	return "does not administer the server"
}

// userLister is what `user list` needs of the store.
type userLister interface {
	ListUsers(ctx context.Context) ([]store.User, error)
}

// listUsers prints every account on the install, oldest first. Nothing a
// credential could ride on is printed.
func listUsers(ctx context.Context, db userLister, out io.Writer) error {
	users, err := db.ListUsers(ctx)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Fprintln(out, "no accounts yet: `agentifi user add --email <address> --superuser` makes the first")
		return nil
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "EMAIL\tACTIVE\tSUPERUSER")
	for _, user := range users {
		fmt.Fprintf(table, "%s\t%s\t%s\n", user.Email, yesNo(user.IsActive), yesNo(user.IsSuperuser))
	}
	return table.Flush()
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func addUser(ctx context.Context, db *store.Store, spec service.NewAccount) error {
	spec.Email = strings.ToLower(strings.TrimSpace(spec.Email))

	// Checked before prompting so a taken address does not cost two password
	// entries. CreateAccount checks it again against the row.
	if _, err := db.GetUserByEmail(ctx, spec.Email); err == nil {
		return fmt.Errorf("agentifi user: %s already exists — use `agentifi user passwd`", spec.Email)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	password, err := takePassword("Password: ", "Repeat: ")
	if err != nil {
		return err
	}
	spec.Password = password

	created, err := service.CreateAccount(ctx, db, spec, time.Now())
	if err != nil {
		if errors.Is(err, service.ErrEmailTaken) {
			return fmt.Errorf("agentifi user: %s already exists — use `agentifi user passwd`", spec.Email)
		}
		return fmt.Errorf("agentifi user: %w", err)
	}

	fmt.Printf("created %s (%s) with space %q\n",
		created.Email, created.ID, spec.Placement.SpaceName)
	if created.IsSuperuser {
		fmt.Println("this account administers the server: Settings -> Server admin is open to it")
	}
	if spec.MustChangePassword {
		fmt.Println("this password is temporary: the account must set its own before it can read anything")
	}
	return nil
}

func setPassword(ctx context.Context, db *store.Store, email string, mustChange bool) error {
	email = strings.ToLower(strings.TrimSpace(email))
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("agentifi user: no account for %s", email)
		}
		return err
	}

	password, err := takePassword("New password: ", "Repeat: ")
	if err != nil {
		return err
	}
	if err := service.SetAccountPassword(
		ctx, db, user.ID, password, mustChange, time.Now()); err != nil {
		return err
	}

	fmt.Printf("password updated for %s\n", user.Email)
	fmt.Fprintln(os.Stderr, "every existing session for this account is now signed out")
	if mustChange {
		fmt.Fprintln(os.Stderr, "the account must set its own password before it can read anything")
	}
	return nil
}

// readPassword takes the password without echoing it, and never from the
// command line, where `ps` and shell history would see it.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())

	if !term.IsTerminal(fd) {
		// Piped input, for scripted installs. Reads exactly one line so the
		// second prompt still has its own.
		line, err := readLine(os.Stdin)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		// A closed pipe or `docker exec` without `-i` reads an empty line,
		// which would create an account nobody can sign in to.
		if line == "" {
			return "", errors.New("agentifi user: no password arrived on stdin")
		}
		return line, nil
	}

	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("agentifi user: the password is empty")
	}
	return string(raw), nil
}

// takePassword reads a password twice and holds it to the same rule the API
// holds a user to.
func takePassword(first, second string) (string, error) {
	password, err := readPassword(first)
	if err != nil {
		return "", err
	}
	again, err := readPassword(second)
	if err != nil {
		return "", err
	}
	if password != again {
		return "", errors.New("agentifi user: the two passwords do not match")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return "", fmt.Errorf("agentifi user: %w", err)
	}
	return password, nil
}

func readLine(r io.Reader) (string, error) {
	var out []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			out = append(out, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	return strings.TrimRight(string(out), "\r"), nil
}
