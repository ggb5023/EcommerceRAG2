package localauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAccountNotFound     = errors.New("localauth: account not found")
	ErrAccountDisabled     = errors.New("localauth: account disabled")
	ErrSessionInvalid      = errors.New("localauth: session invalid")
	ErrSetupComplete       = errors.New("localauth: setup already completed")
	ErrSetupNeedsBootstrap = errors.New("localauth: setup requires bootstrap")
	ErrSelfDisable         = errors.New("localauth: cannot disable yourself")
	ErrLastOwner           = errors.New("localauth: last owner cannot be disabled")
)

// Account is the login-time view of one local account.
type Account struct {
	TenantID           int64
	UserID             int64
	ExternalID         string
	Role               string
	Shops              []string
	Revision           string
	RevokedAt          *time.Time
	PasswordHash       string
	MustChangePassword bool
	FailedAttempts     int
	LockedUntil        *time.Time
}

// ResolvedSession is a live cookie joined with the current account state.
type ResolvedSession struct {
	Session
	Account          Account
	TenantExternalID string
}

// Store owns every local-account write. The gateway role has no privileges on
// tenant or shop; those tables stay owned by migrations and bootstrap.
type Store struct {
	DB *pgxpool.Pool
	// Tenant names the single merchant tenant: bootstrap creates external id
	// "merchant"; deployments that still carry the development identity set
	// V2_IDENTITY_TENANT instead. Fixture tenants are never consulted.
	Tenant string
	Now    func() time.Time
}

func NewStore(db *pgxpool.Pool) *Store {
	tenant := os.Getenv("V2_IDENTITY_TENANT")
	if tenant == "" {
		tenant = "merchant"
	}
	return &Store{DB: db, Tenant: tenant, Now: time.Now}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// FindAccount resolves a username for login. Ambiguous matches (two tenants with
// the same external id) are reported as "not found" so the response shape stays
// identical to a wrong password.
func (s *Store) FindAccount(ctx context.Context, username string) (Account, error) {
	var account Account
	rows, err := s.DB.Query(ctx, `SELECT u.tenant_id,u.id,u.external_id,u.role,
		COALESCE(u.shop_ids,ARRAY[]::text[]),u.permission_revision,u.revoked_at,
		c.password_hash,c.must_change_password,c.failed_attempts,c.locked_until
		FROM app_user u
		JOIN local_credential c ON c.tenant_id=u.tenant_id AND c.user_id=u.id
		JOIN tenant t ON t.id=u.tenant_id AND t.status='active'
		WHERE u.external_id=$1 ORDER BY u.id LIMIT 2`, username)
	if err != nil {
		return account, err
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		seen++
		if seen > 1 {
			return Account{}, ErrAccountNotFound
		}
		if err := rows.Scan(&account.TenantID, &account.UserID, &account.ExternalID, &account.Role,
			&account.Shops, &account.Revision, &account.RevokedAt, &account.PasswordHash,
			&account.MustChangePassword, &account.FailedAttempts, &account.LockedUntil); err != nil {
			return Account{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return Account{}, err
	}
	if seen == 0 {
		return Account{}, ErrAccountNotFound
	}
	return account, nil
}

// RegisterFailure increments the counter and locks the account in one statement.
// Nothing is read first: two concurrent failures cannot both observe the old
// count and undercount the lockout.
func (s *Store) RegisterFailure(ctx context.Context, tenantID, userID int64, maxAttempts int, lockFor time.Duration) (int, *time.Time, error) {
	now := s.now()
	var attempts int
	var lockedUntil *time.Time
	err := s.DB.QueryRow(ctx, `UPDATE local_credential SET
			failed_attempts = CASE WHEN locked_until IS NOT NULL AND locked_until > $3
				THEN failed_attempts ELSE failed_attempts + 1 END,
			last_failed_at = $3,
			locked_until = CASE WHEN (locked_until IS NULL OR locked_until <= $3)
				AND (failed_attempts + 1) >= $4 THEN $3 + $5::interval ELSE locked_until END,
			updated_at = $3
		WHERE tenant_id=$1 AND user_id=$2
		RETURNING failed_attempts, locked_until`,
		tenantID, userID, now, maxAttempts, fmt.Sprintf("%d seconds", int(lockFor.Seconds()))).
		Scan(&attempts, &lockedUntil)
	if err != nil {
		return 0, nil, err
	}
	return attempts, lockedUntil, nil
}

// ClearFailures resets the counter after a successful login.
func (s *Store) ClearFailures(ctx context.Context, tenantID, userID int64) error {
	_, err := s.DB.Exec(ctx, `UPDATE local_credential SET failed_attempts=0, locked_until=NULL,
		last_failed_at=NULL, updated_at=$3
		WHERE tenant_id=$1 AND user_id=$2 AND (failed_attempts>0 OR locked_until IS NOT NULL)`,
		tenantID, userID, s.now())
	return err
}

// CreateSession stores only the hashes; the raw cookie and CSRF stay in memory.
func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO local_session(id_hash,tenant_id,user_id,csrf_hash,
		created_at,last_seen_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,$5,$6,$7)`,
		session.IDHash, session.TenantID, session.UserID, session.CSRFHash,
		session.CreatedAt, session.IdleExpiresAt, session.AbsoluteExpiresAt)
	return err
}

// ResolveSession joins the cookie row with the live account state.
func (s *Store) ResolveSession(ctx context.Context, idHash string) (ResolvedSession, error) {
	var out ResolvedSession
	err := s.DB.QueryRow(ctx, `SELECT s.tenant_id,s.user_id,s.csrf_hash,s.created_at,
			s.idle_expires_at,s.absolute_expires_at,s.revoked_at,COALESCE(s.revoked_reason,''),
			u.external_id,u.role,COALESCE(u.shop_ids,ARRAY[]::text[]),u.permission_revision,
			u.revoked_at,c.must_change_password,t.external_id
		FROM local_session s
		JOIN app_user u ON u.tenant_id=s.tenant_id AND u.id=s.user_id
		JOIN tenant t ON t.id=s.tenant_id
		JOIN local_credential c ON c.tenant_id=s.tenant_id AND c.user_id=s.user_id
		WHERE s.id_hash=$1`, idHash).Scan(
		&out.TenantID, &out.UserID, &out.CSRFHash, &out.CreatedAt, &out.IdleExpiresAt,
		&out.AbsoluteExpiresAt, &out.RevokedAt, &out.RevokedReason,
		&out.Account.ExternalID, &out.Account.Role, &out.Account.Shops,
		&out.Account.Revision, &out.Account.RevokedAt, &out.MustChangePassword, &out.TenantExternalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrSessionInvalid
	}
	if err != nil {
		return out, err
	}
	// Check the account before the session: disable revokes sessions too, and the
	// caller must still see account_revoked rather than a generic invalid session.
	if out.Account.RevokedAt != nil {
		return out, ErrAccountDisabled
	}
	if out.RevokedAt != nil || out.Expired(s.now()) {
		return out, ErrSessionInvalid
	}
	return out, nil
}

// TouchSession slides the idle window at most once per TouchInterval. The
// database decides, so a busy request path cannot turn into one UPDATE per call.
func (s *Store) TouchSession(ctx context.Context, idHash string, idle time.Time) error {
	now := s.now()
	_, err := s.DB.Exec(ctx, `UPDATE local_session
		SET last_seen_at=$2, idle_expires_at=LEAST($3,absolute_expires_at)
		WHERE id_hash=$1 AND revoked_at IS NULL
		  AND last_seen_at <= $2 - $4::interval`,
		idHash, now, idle, fmt.Sprintf("%d seconds", int(TouchInterval.Seconds())))
	return err
}

// RevokeSession revokes one session with a reason.
func (s *Store) RevokeSession(ctx context.Context, idHash, reason string) error {
	_, err := s.DB.Exec(ctx, `UPDATE local_session SET revoked_at=$2, revoked_reason=$3
		WHERE id_hash=$1 AND revoked_at IS NULL`, idHash, s.now(), reason)
	return err
}

// RevokeUserSessions revokes every live session of one account.
func (s *Store) RevokeUserSessions(ctx context.Context, tenantID, userID int64, reason string) (int64, error) {
	tag, err := s.DB.Exec(ctx, `UPDATE local_session SET revoked_at=$3, revoked_reason=$4
		WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`, tenantID, userID, s.now(), reason)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Event is one append-only authentication audit row.
type Event struct {
	Name        string
	SubjectHash string
	TenantID    *int64
	UserID      *int64
	IP          string
}

func (s *Store) RecordEvent(ctx context.Context, event Event) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO local_auth_event(event,subject_hash,tenant_id,user_id,ip,created_at)
		VALUES($1,NULLIF($2,''),$3,$4,NULLIF($5,'')::inet,$6)`,
		event.Name, event.SubjectHash, event.TenantID, event.UserID, event.IP, s.now())
	return err
}

// DisableUser is the account-level switch: revoked_at plus both revocation
// columns, every live session revoked, and one audit row, in one transaction.
func (s *Store) DisableUser(ctx context.Context, tenantID, targetID, actorID int64) error {
	return s.withTenantLock(ctx, tenantID, func(tx pgx.Tx) error {
		if actorID == targetID {
			return ErrSelfDisable
		}
		var role string
		var revokedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT role,revoked_at FROM app_user
			WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, targetID).Scan(&role, &revokedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountNotFound
		}
		if err != nil {
			return err
		}
		if role == "owner" {
			var owners int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM app_user
				WHERE tenant_id=$1 AND role='owner' AND revoked_at IS NULL`, tenantID).Scan(&owners); err != nil {
				return err
			}
			if owners <= 1 && revokedAt == nil {
				return ErrLastOwner
			}
		}
		if revokedAt == nil {
			now := s.now()
			if _, err := tx.Exec(ctx, `UPDATE app_user SET revoked_at=$3, revoked_by=$4,
				revoked_reason='account_disabled' WHERE tenant_id=$1 AND id=$2`,
				tenantID, targetID, now, actorID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE local_session SET revoked_at=$3,
				revoked_reason='account_revoked' WHERE tenant_id=$1 AND user_id=$2
				AND revoked_at IS NULL`, tenantID, targetID, now); err != nil {
				return err
			}
		}
		return s.recordEvent(ctx, tx, "account_disabled", tenantID, targetID)
	})
}

// EnableUser clears all three revocation columns; old sessions stay revoked.
func (s *Store) EnableUser(ctx context.Context, tenantID, targetID, actorID int64) error {
	return s.withTenantLock(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app_user SET revoked_at=NULL, revoked_by=NULL,
			revoked_reason=NULL WHERE tenant_id=$1 AND id=$2 AND revoked_at IS NOT NULL`,
			tenantID, targetID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return s.recordEvent(ctx, tx, "account_enabled", tenantID, targetID)
	})
}

func (s *Store) recordEvent(ctx context.Context, tx pgx.Tx, name string, tenantID, userID int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO local_auth_event(event,tenant_id,user_id,created_at)
		VALUES($1,$2,$3,$4)`, name, tenantID, userID, s.now())
	return err
}

func (s *Store) withTenantLock(ctx context.Context, tenantID int64, fn func(pgx.Tx) error) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(742019,$1)`, tenantID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetupTarget is the owner skeleton that bootstrap created for this tenant.
type SetupTarget struct {
	TenantID        int64
	OwnerUserID     int64
	OwnerExternalID string
}

// SetupTarget resolves the one-time setup target. The gate is the per-tenant
// credential count, not the account count: bootstrap already created the owner
// skeleton, and counting app_user would close /setup before it ever runs.
func (s *Store) SetupTarget(ctx context.Context) (SetupTarget, error) {
	tenantID, err := s.ActiveTenant(ctx)
	if err != nil {
		return SetupTarget{}, ErrSetupNeedsBootstrap
	}
	var credentials int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM local_credential WHERE tenant_id=$1`, tenantID).Scan(&credentials); err != nil {
		return SetupTarget{}, err
	}
	if credentials > 0 {
		return SetupTarget{}, ErrSetupComplete
	}
	var owner SetupTarget
	owners, err := s.DB.Query(ctx, `SELECT id,external_id FROM app_user
		WHERE tenant_id=$1 AND role='owner' AND revoked_at IS NULL ORDER BY id LIMIT 2`, tenantID)
	if err != nil {
		return SetupTarget{}, err
	}
	found := 0
	for owners.Next() {
		found++
		if err := owners.Scan(&owner.OwnerUserID, &owner.OwnerExternalID); err != nil {
			owners.Close()
			return SetupTarget{}, err
		}
	}
	owners.Close()
	if err := owners.Err(); err != nil {
		return SetupTarget{}, err
	}
	if found != 1 {
		return SetupTarget{}, ErrSetupNeedsBootstrap
	}
	owner.TenantID = tenantID
	return owner, nil
}

// CompleteSetup writes the first credential. It re-checks the gate inside the
// same transaction under the tenant lock, so two concurrent submissions cannot
// both create a credential.
func (s *Store) CompleteSetup(ctx context.Context, target SetupTarget, passwordHash string) error {
	return s.withTenantLock(ctx, target.TenantID, func(tx pgx.Tx) error {
		var credentials int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM local_credential WHERE tenant_id=$1`,
			target.TenantID).Scan(&credentials); err != nil {
			return err
		}
		if credentials > 0 {
			return ErrSetupComplete
		}
		var owners int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app_user
			WHERE tenant_id=$1 AND id=$2 AND role='owner' AND revoked_at IS NULL`,
			target.TenantID, target.OwnerUserID).Scan(&owners); err != nil {
			return err
		}
		if owners != 1 {
			return ErrSetupNeedsBootstrap
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `INSERT INTO local_credential(tenant_id,user_id,password_hash,
			must_change_password,created_at,updated_at,password_changed_at)
			VALUES($1,$2,$3,false,$4::timestamptz,$4::timestamptz,$4::timestamptz)`,
			target.TenantID, target.OwnerUserID, passwordHash, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO admin_audit(tenant_id,actor_user_id,action,
			resource_type,resource_id,reason,result,metadata_json)
			VALUES($1,$2,'setup.completed','local_credential',$3,
			'first local credential created by /setup','success',jsonb_build_object('username',$4::text))`,
			target.TenantID, target.OwnerUserID,
			fmt.Sprintf("%d:%d", target.TenantID, target.OwnerUserID), target.OwnerExternalID)
		return err
	})
}

var ErrTenantUnavailable = errors.New("localauth: active tenant is unavailable")

// ActiveTenant returns the single active tenant. Multi-tenant routing is out of
// scope for M4, so ambiguity is an error rather than a guess.
func (s *Store) ActiveTenant(ctx context.Context) (int64, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT id FROM tenant WHERE external_id=$1 AND status='active'`,
		s.Tenant).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrTenantUnavailable
	}
	return id, err
}

// User is the CLI view of an account, without credential material.
type User struct {
	TenantID       int64
	UserID         int64
	ExternalID     string
	Role           string
	RevokedAt      *time.Time
	HasCredential  bool
	MustChange     bool
	LockedUntil    *time.Time
	FailedAttempts int
}

// FindUser resolves an account for operator commands, with or without a
// credential row.
func (s *Store) FindUser(ctx context.Context, username string) (User, error) {
	var user User
	err := s.DB.QueryRow(ctx, `SELECT u.tenant_id,u.id,u.external_id,u.role,u.revoked_at,
			c.user_id IS NOT NULL,COALESCE(c.must_change_password,false),
			c.locked_until,COALESCE(c.failed_attempts,0)
		FROM app_user u
		JOIN tenant t ON t.id=u.tenant_id AND t.status='active'
		LEFT JOIN local_credential c ON c.tenant_id=u.tenant_id AND c.user_id=u.id
		WHERE u.external_id=$1 ORDER BY u.id LIMIT 2`, username).Scan(
		&user.TenantID, &user.UserID, &user.ExternalID, &user.Role, &user.RevokedAt,
		&user.HasCredential, &user.MustChange, &user.LockedUntil, &user.FailedAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return user, ErrAccountNotFound
	}
	return user, err
}

// CreateOperator adds a non-owner account and its credential. Owner and admin
// roles are refused here: the gateway role cannot write admin_role_grant, so an
// owner created this way would own nothing.
func (s *Store) CreateOperator(ctx context.Context, tenantID int64, username, role, passwordHash string, mustChange bool) (int64, error) {
	if role != "operator" && role != "viewer" {
		return 0, errors.New("localauth: CLI only creates operator or viewer accounts")
	}
	var userID int64
	err := s.withTenantLock(ctx, tenantID, func(tx pgx.Tx) error {
		var existing int64
		err := tx.QueryRow(ctx, `SELECT id FROM app_user WHERE tenant_id=$1 AND external_id=$2`,
			tenantID, username).Scan(&existing)
		if err == nil {
			return errors.New("localauth: account already exists")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		now := s.now()
		if err := tx.QueryRow(ctx, `INSERT INTO app_user(tenant_id,external_id,role,shop_ids)
			VALUES($1,$2,$3,(SELECT ARRAY[id] FROM shop WHERE tenant_id=$1 AND status='active' ORDER BY id LIMIT 1))
			RETURNING id`, tenantID, username, role).Scan(&userID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO local_credential(tenant_id,user_id,password_hash,
			must_change_password,created_at,updated_at,password_changed_at)
			VALUES($1,$2,$3,$4,$5::timestamptz,$5::timestamptz,$5::timestamptz)`, tenantID, userID, passwordHash, mustChange, now)
		return err
	})
	return userID, err
}

// ResetPassword replaces the hash, forces a change on next login, clears lock
// state and revokes every live session as admin_reset.
func (s *Store) ResetPassword(ctx context.Context, tenantID, userID int64, passwordHash string, mustChange bool) (int64, error) {
	var revoked int64
	err := s.withTenantLock(ctx, tenantID, func(tx pgx.Tx) error {
		now := s.now()
		tag, err := tx.Exec(ctx, `UPDATE local_credential SET password_hash=$3,
			must_change_password=$4,failed_attempts=0,locked_until=NULL,last_failed_at=NULL,
			password_changed_at=$5::timestamptz,updated_at=$5::timestamptz WHERE tenant_id=$1 AND user_id=$2`,
			tenantID, userID, passwordHash, mustChange, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrAccountNotFound
		}
		tag, err = tx.Exec(ctx, `UPDATE local_session SET revoked_at=$3,revoked_reason='admin_reset'
			WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`, tenantID, userID, now)
		if err != nil {
			return err
		}
		revoked = tag.RowsAffected()
		_, err = tx.Exec(ctx, `INSERT INTO local_auth_event(event,tenant_id,user_id,created_at)
			VALUES('password_changed',$1,$2,$3)`, tenantID, userID, now)
		return err
	})
	return revoked, err
}

// ListUsers is the operator listing; it never selects credential material.
func (s *Store) ListUsers(ctx context.Context, tenantID int64) ([]User, error) {
	rows, err := s.DB.Query(ctx, `SELECT u.tenant_id,u.id,u.external_id,u.role,u.revoked_at,
			c.user_id IS NOT NULL,COALESCE(c.must_change_password,false),
			c.locked_until,COALESCE(c.failed_attempts,0)
		FROM app_user u
		JOIN tenant t ON t.id=u.tenant_id AND t.status='active'
		LEFT JOIN local_credential c ON c.tenant_id=u.tenant_id AND c.user_id=u.id
		WHERE u.tenant_id=$1 ORDER BY u.external_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.TenantID, &user.UserID, &user.ExternalID, &user.Role,
			&user.RevokedAt, &user.HasCredential, &user.MustChange, &user.LockedUntil,
			&user.FailedAttempts); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

// FindAccountByID loads one account for an authenticated session.
func (s *Store) FindAccountByID(ctx context.Context, tenantID, userID int64) (Account, error) {
	var account Account
	err := s.DB.QueryRow(ctx, `SELECT u.tenant_id,u.id,u.external_id,u.role,
		COALESCE(u.shop_ids,ARRAY[]::text[]),u.permission_revision,u.revoked_at,
		c.password_hash,c.must_change_password,c.failed_attempts,c.locked_until
		FROM app_user u
		JOIN local_credential c ON c.tenant_id=u.tenant_id AND c.user_id=u.id
		JOIN tenant t ON t.id=u.tenant_id AND t.status='active'
		WHERE u.tenant_id=$1 AND u.id=$2`, tenantID, userID).Scan(
		&account.TenantID, &account.UserID, &account.ExternalID, &account.Role,
		&account.Shops, &account.Revision, &account.RevokedAt, &account.PasswordHash,
		&account.MustChangePassword, &account.FailedAttempts, &account.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return account, ErrAccountNotFound
	}
	return account, err
}

// ChangePassword is the self-service path: clear the forced-change flag, revoke
// every session with password_changed (including the current one) and audit it.
func (s *Store) ChangePassword(ctx context.Context, tenantID, userID int64, passwordHash string) (int64, error) {
	var revoked int64
	err := s.withTenantLock(ctx, tenantID, func(tx pgx.Tx) error {
		now := s.now()
		tag, err := tx.Exec(ctx, `UPDATE local_credential SET password_hash=$3,
			must_change_password=false,failed_attempts=0,locked_until=NULL,last_failed_at=NULL,
			password_changed_at=$4,updated_at=$4 WHERE tenant_id=$1 AND user_id=$2`,
			tenantID, userID, passwordHash, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrAccountNotFound
		}
		tag, err = tx.Exec(ctx, `UPDATE local_session SET revoked_at=$3,revoked_reason='password_changed'
			WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`, tenantID, userID, now)
		if err != nil {
			return err
		}
		revoked = tag.RowsAffected()
		_, err = tx.Exec(ctx, `INSERT INTO local_auth_event(event,tenant_id,user_id,created_at)
			VALUES('password_changed',$1,$2,$3)`, tenantID, userID, now)
		return err
	})
	return revoked, err
}
