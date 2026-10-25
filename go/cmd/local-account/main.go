// Operator CLI for local accounts. Passwords are read from stdin only; owner
// accounts are refused because the gateway role cannot write admin_role_grant.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/localauth"
	"github.com/jackc/pgx/v5/pgxpool"
)

const minPassword, maxPassword = 12, 256

func loadValues(path string) (map[string]string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("runtime configuration must be an owned 0600 file")
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(mustRead(path)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"'")
	}
	if values["V2_GATEWAY_DSN"] == "" {
		return nil, errors.New("V2_GATEWAY_DSN is required")
	}
	return values, nil
}

func mustRead(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		fail("cannot read runtime configuration")
	}
	return data
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "FAIL "+message)
	os.Exit(1)
}

// readPassword reads one line from stdin. The value is never echoed or logged
// by this process; operators pipe it from a protected file or type it.
func readPassword() string {
	reader := bufio.NewReader(io.LimitReader(os.Stdin, maxPassword+2))
	raw, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fail("cannot read the password from stdin")
	}
	password := strings.TrimRight(raw, "\r\n")
	if len(password) < minPassword || len(password) > maxPassword {
		fail(fmt.Sprintf("password length must be %d-%d bytes", minPassword, maxPassword))
	}
	return password
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: local-account <create|disable|enable|reset-password|list> --env FILE ...")
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ExitOnError)
	environment := flags.String("env", "", "0600 runtime configuration with V2_GATEWAY_DSN")
	username := flags.String("user", "", "account external id")
	role := flags.String("role", "operator", "create only: operator|viewer")
	actor := flags.String("actor", "", "operator performing disable/enable")
	if err := flags.Parse(os.Args[2:]); err != nil {
		fail("invalid arguments")
	}
	if *environment == "" {
		fail("--env is required")
	}
	values, err := loadValues(*environment)
	if err != nil {
		fail(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, values["V2_GATEWAY_DSN"])
	if err != nil {
		fail("database configuration failed")
	}
	defer pool.Close()
	store := localauth.NewStore(pool)
	if tenant := values["V2_IDENTITY_TENANT"]; tenant != "" {
		store.Tenant = tenant
	}

	switch command {
	case "create":
		if *username == "" {
			fail("--user is required")
		}
		if *role != "operator" && *role != "viewer" {
			fail("--role owner is refused: the first owner comes from bootstrap + /setup; later owners go through the /admin approval flow")
		}
		tenantID, err := store.ActiveTenant(ctx)
		if err != nil {
			fail("active tenant is unavailable")
		}
		password := readPassword()
		hash, err := localauth.Hash(password)
		if err != nil {
			fail("cannot hash the password")
		}
		userID, err := store.CreateOperator(ctx, tenantID, *username, *role, hash, true)
		if err != nil {
			fail("account was not created: " + err.Error())
		}
		fmt.Printf("created %s (user %d, role %s, must change password on first login)\n", *username, userID, *role)
	case "disable", "enable":
		if *username == "" {
			fail("--user is required")
		}
		target, err := store.FindUser(ctx, *username)
		if err != nil {
			fail("account not found")
		}
		actorName := *actor
		if actorName == "" {
			actorName = *username
		}
		actorUser, err := store.FindUser(ctx, actorName)
		if err != nil {
			fail("actor account not found")
		}
		if command == "disable" {
			if err := store.DisableUser(ctx, target.TenantID, target.UserID, actorUser.UserID); err != nil {
				fail("account was not disabled: " + err.Error())
			}
			fmt.Printf("disabled %s; sessions revoked\n", *username)
		} else {
			if err := store.EnableUser(ctx, target.TenantID, target.UserID, actorUser.UserID); err != nil {
				fail("account was not enabled: " + err.Error())
			}
			fmt.Printf("enabled %s; previous sessions stay revoked\n", *username)
		}
	case "reset-password":
		if *username == "" {
			fail("--user is required")
		}
		target, err := store.FindUser(ctx, *username)
		if err != nil || !target.HasCredential {
			fail("account or credential not found")
		}
		password := readPassword()
		hash, err := localauth.Hash(password)
		if err != nil {
			fail("cannot hash the password")
		}
		revoked, err := store.ResetPassword(ctx, target.TenantID, target.UserID, hash, true)
		if err != nil {
			fail("password was not reset: " + err.Error())
		}
		fmt.Printf("reset password for %s; forced change on next login; %d session(s) revoked\n", *username, revoked)
	case "list":
		tenantID, err := store.ActiveTenant(ctx)
		if err != nil {
			fail("active tenant is unavailable")
		}
		users, err := store.ListUsers(ctx, tenantID)
		if err != nil {
			fail("cannot list accounts")
		}
		writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "USER\tROLE\tCREDENTIAL\tMUST_CHANGE\tDISABLED\tLOCKED_UNTIL")
		for _, user := range users {
			disabled := "-"
			if user.RevokedAt != nil {
				disabled = "yes"
			}
			locked := "-"
			if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
				locked = user.LockedUntil.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(writer, "%s\t%s\t%t\t%t\t%s\t%s\n", user.ExternalID, user.Role,
				user.HasCredential, user.MustChange, disabled, locked)
		}
		_ = writer.Flush()
	default:
		fail("unknown command")
	}
}
