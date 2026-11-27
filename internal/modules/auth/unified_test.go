package auth_test

import (
	"errors"
	"testing"
	"time"

	auth "github.com/Massad/gin-boilerplate/internal/modules/auth"
	jwt "github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// fakeUserAuth implements the UserAuthenticator port: the pure suite drives
// the orchestrator's tenant directory branches without any module.
type fakeUserAuth struct {
	form       auth.LoginForm
	calls      int
	nextUser   auth.User
	nextTenant auth.TenantPayload
	nextToken  auth.Token
	nextErr    error
}

func (f *fakeUserAuth) Login(form auth.LoginForm) (auth.User, auth.TenantPayload, auth.Token, error) {
	f.calls++
	f.form = form
	if f.nextErr != nil {
		return auth.User{}, auth.TenantPayload{}, auth.Token{}, f.nextErr
	}
	return f.nextUser, f.nextTenant, f.nextToken, nil
}

// fakeAdminAuth implements the AdminAuthenticator port.
type fakeAdminAuth struct {
	calls     int
	email     string
	password  string
	nextAdmin auth.AdminIdentity
	nextErr   error
}

func (f *fakeAdminAuth) Login(email, password string) (auth.AdminIdentity, error) {
	f.calls++
	f.email = email
	f.password = password
	if f.nextErr != nil {
		return auth.AdminIdentity{}, f.nextErr
	}
	return f.nextAdmin, nil
}

// fakeUserReader implements the UserReader port: the refresh flow re-reads
// the view it returns.
type fakeUserReader struct {
	calls   int
	view    auth.UserWithTenantStatus
	nextErr error
}

func (f *fakeUserReader) OneWithTenant(_ uuid.UUID) (auth.UserWithTenantStatus, error) {
	f.calls++
	if f.nextErr != nil {
		return auth.UserWithTenantStatus{}, f.nextErr
	}
	return f.view, nil
}

// fakeAdminID is the superadmin identity the admin fake returns.
var fakeAdminID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// fakeTenantID and fakeUserID seed the orchestrator flows.
var (
	fakeTenantID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	fakeUserID   = uuid.MustParse("33333333-3333-3333-3333-333333333333")
)

// errFakeStore is the opaque session-store failure the pure suite injects.
var errFakeStore = errors.New("redis down")

// unifiedOrchestrator builds the orchestrator on the pure suite's service
// (fake session repository) and fresh port fakes.
func unifiedOrchestrator(t *testing.T) (*auth.Orchestrator, *fakeSessions, *fakeUserAuth, *fakeAdminAuth, *fakeUserReader) {
	t.Helper()
	svc, sessions := pureTestService(t)
	users := &fakeUserAuth{}
	admins := &fakeAdminAuth{}
	reader := &fakeUserReader{}
	return auth.NewOrchestrator(svc, users, admins, reader), sessions, users, admins, reader
}

// TestOrchestratorUnifiedLoginTenantUser proves the tenant branch: kind,
// payload passthrough and no admin attempt (the directories are never probed
// for the same account).
func TestOrchestratorUnifiedLoginTenantUser(t *testing.T) {
	orch, _, users, admins, _ := unifiedOrchestrator(t)
	users.nextUser = auth.User{ID: fakeUserID, Email: "user@test.com", Name: "Owner", TenantID: fakeTenantID, Role: "owner"}
	users.nextTenant = auth.TenantPayload{ID: fakeTenantID.String(), Slug: "acme", Name: "Acme"}
	users.nextToken = auth.Token{AccessToken: "access", RefreshToken: "refresh"}

	result, err := orch.UnifiedLogin(auth.LoginForm{Email: "user@test.com", Password: "pw"})
	require.NoError(t, err)
	require.Equal(t, auth.LoginKindTenantUser, result.Kind)
	require.Equal(t, users.nextUser, result.User)
	require.Equal(t, users.nextTenant, result.Tenant)
	require.Equal(t, users.nextToken, result.Token)
	require.Equal(t, 0, admins.calls, "a successful user login must never probe the admin directory")
	require.Equal(t, "user@test.com", users.form.Email, "the form crosses the port untouched")
}

// TestOrchestratorUnifiedLoginSuspendedPropagates proves the 403 signal: the
// auth sentinel crosses the port and the flow stops before the admin.
func TestOrchestratorUnifiedLoginSuspendedPropagates(t *testing.T) {
	orch, _, users, admins, _ := unifiedOrchestrator(t)
	users.nextErr = auth.ErrTenantSuspended

	result, err := orch.UnifiedLogin(auth.LoginForm{Email: "sus@test.com", Password: "pw"})
	require.ErrorIs(t, err, auth.ErrTenantSuspended)
	require.Equal(t, auth.UnifiedLoginResult{}, result)
	require.Equal(t, 0, admins.calls, "a suspended tenant must not reach the admin directory")
}

// TestOrchestratorUnifiedLoginUnknownEmailFallsBack proves the superadmin
// fallback: equalization cost (one dummy bcrypt), the admin credentials are
// the form's, the admin token is issued and stored.
func TestOrchestratorUnifiedLoginUnknownEmailFallsBack(t *testing.T) {
	orch, sessions, users, admins, _ := unifiedOrchestrator(t)
	users.nextErr = pgx.ErrNoRows
	admins.nextAdmin = auth.AdminIdentity{ID: fakeAdminID, Email: "root@test.com", Name: "Root"}
	before := auth.DummyHashComparisons()

	result, err := orch.UnifiedLogin(auth.LoginForm{Email: "nobody@test.com", Password: "pw"})
	require.NoError(t, err)
	require.Equal(t, auth.LoginKindSuperadmin, result.Kind)
	require.Equal(t, "nobody@test.com", admins.email)
	require.Equal(t, "pw", admins.password)
	require.Equal(t, admins.nextAdmin, result.Admin)
	require.NotEmpty(t, result.AccessToken)
	require.Greater(t, auth.DummyHashComparisons(), before, "the unknown-email path must run the dummy bcrypt (timing equalization)")
	require.Equal(t, 1, sessions.adminCreated, "the superadmin session half lives in the session store")
}

// TestOrchestratorUnifiedLoginWrongPasswordStops proves the no-overlap policy:
// a wrong password never reaches the admin directory and never runs the dummy
// bcrypt.
func TestOrchestratorUnifiedLoginWrongPasswordStops(t *testing.T) {
	orch, _, users, admins, _ := unifiedOrchestrator(t)
	users.nextErr = bcrypt.ErrMismatchedHashAndPassword
	before := auth.DummyHashComparisons()

	result, err := orch.UnifiedLogin(auth.LoginForm{Email: "user@test.com", Password: "pw"})
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	require.Equal(t, auth.UnifiedLoginResult{}, result)
	require.Equal(t, 0, admins.calls, "a wrong password must never probe the admin directory")
	require.Equal(t, before, auth.DummyHashComparisons(), "a wrong password must not run the dummy bcrypt")
}

// TestOrchestratorUnifiedLoginAdminFailureGeneric proves a superadmin lookup
// or password failure folds into the single generic error.
func TestOrchestratorUnifiedLoginAdminFailureGeneric(t *testing.T) {
	orch, _, users, admins, _ := unifiedOrchestrator(t)
	users.nextErr = pgx.ErrNoRows
	admins.nextErr = bcrypt.ErrMismatchedHashAndPassword
	before := auth.DummyHashComparisons()

	result, err := orch.UnifiedLogin(auth.LoginForm{Email: "nobody@test.com", Password: "pw"})
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	require.Equal(t, auth.UnifiedLoginResult{}, result)
	require.Greater(t, auth.DummyHashComparisons(), before)
}

// TestOrchestratorUnifiedLoginAdminStoreFailureGeneric proves the failure of
// the superadmin session store folds into the generic error too.
func TestOrchestratorUnifiedLoginAdminStoreFailureGeneric(t *testing.T) {
	orch, sessions, users, admins, _ := unifiedOrchestrator(t)
	sessions.adminCreateErr = errFakeStore
	users.nextErr = pgx.ErrNoRows
	admins.nextAdmin = auth.AdminIdentity{ID: fakeAdminID, Email: "root@test.com", Name: "Root"}

	result, err := orch.UnifiedLogin(auth.LoginForm{Email: "nobody@test.com", Password: "pw"})
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	require.Equal(t, auth.UnifiedLoginResult{}, result)
	require.Equal(t, 1, sessions.adminCreated)
}

// refreshClaims builds a signed refresh token the orchestrator parses with
// the pure suite's injected secrets.
func refreshClaims(t *testing.T, id string, userID uuid.UUID) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"refresh_uuid": id,
		"user_id":      userID.String(),
		"tenant_id":    fakeTenantID.String(),
		"exp":          time.Now().Add(time.Minute).Unix(),
	}).SignedString([]byte("refresh-key-for-pure-tests-only-32-bytes"))
	require.NoError(t, err)
	return token
}

// TestOrchestratorRefreshInvalidToken rejects a malformed or wrongly signed
// token before any consumption or release.
func TestOrchestratorRefreshInvalidToken(t *testing.T) {
	orch, sessions, _, _, _ := unifiedOrchestrator(t)

	tokens, err := orch.Refresh("not-a-jwt")
	require.Error(t, err)
	require.Nil(t, tokens)
	require.Equal(t, 0, sessions.consumeCalled, "a malformed token must not reach the consumption")

	tokens, err = orch.Refresh(refreshClaims(t, uuid.NewString(), fakeUserID) + "tampered")
	require.Error(t, err)
	require.Nil(t, tokens)
}

// TestOrchestratorRefreshRefusedConsumption proves a refused token is answered
// without any release (nothing was claimed).
func TestOrchestratorRefreshRefusedConsumption(t *testing.T) {
	orch, sessions, _, _, _ := unifiedOrchestrator(t)
	sessions.consumeCode = 0

	tokens, err := orch.Refresh(refreshClaims(t, uuid.NewString(), fakeUserID))
	require.Error(t, err)
	require.Nil(t, tokens)
	require.Equal(t, 1, sessions.consumeCalled)
	require.Equal(t, 0, sessions.releaseCalled, "a refused token must not release anything")
}

// TestOrchestratorRefreshReaderFailureReleases proves a failed or non-active
// re-read releases the claimed consumption so the replay stays recoverable.
func TestOrchestratorRefreshReaderFailureReleases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		view    auth.UserWithTenantStatus
		viewErr error
	}{
		{"read error", auth.UserWithTenantStatus{}, errFakeStore},
		{"suspended tenant", auth.UserWithTenantStatus{User: auth.User{ID: fakeUserID}, Status: 2}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orch, sessions, _, _, reader := unifiedOrchestrator(t)
			sessions.consumeCode = 1
			reader.view = tc.view
			reader.nextErr = tc.viewErr

			tokens, err := orch.Refresh(refreshClaims(t, uuid.NewString(), fakeUserID))
			require.Error(t, err)
			require.Nil(t, tokens)
			require.Equal(t, 1, reader.calls, "the re-read must happen before the re-emission")
			require.Equal(t, 1, sessions.releaseCalled, "a claimed but undelivered consumption must be released")
		})
	}
}

// TestOrchestratorRefreshReemitsFreshIdentity proves the re-emission: the
// fresh pair carries the re-read identity (role from the reader, never from
// the claims) for a first consumption and for the single recovery.
func TestOrchestratorRefreshReemitsFreshIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		consume int64
	}{
		{"first consumption", 1},
		{"recovery", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			orch, sessions, _, _, reader := unifiedOrchestrator(t)
			sessions.consumeCode = tc.consume
			reader.view = auth.UserWithTenantStatus{
				User:   auth.User{ID: fakeUserID, TenantID: fakeTenantID, Role: "member"},
				Status: 1,
			}

			tokens, err := orch.Refresh(refreshClaims(t, uuid.NewString(), fakeUserID))
			require.NoError(t, err)
			require.NotEmpty(t, tokens["access_token"])
			require.NotEmpty(t, tokens["refresh_token"])
			require.Equal(t, 1, reader.calls)
			require.Equal(t, 1, sessions.created, "the re-emitted pair is stored")
			require.Equal(t, fakeUserID.String()+":"+fakeTenantID.String()+":member", sessions.createdValue,
				"the re-emitted claims carry the re-read identity")
			if tc.consume == 1 {
				require.Equal(t, 0, sessions.releaseCalled, "a delivered consumption is not released")
			}

			access, err := jwt.Parse(tokens["access_token"], func(token *jwt.Token) (interface{}, error) {
				return []byte("access-key-for-pure-tests-only-32-bytes"), nil
			})
			require.NoError(t, err)
			claims := access.Claims.(jwt.MapClaims)
			require.Equal(t, "member", claims["role"], "the role must be re-fetched from the database, never trusted from the claims")
		})
	}
}

// TestOrchestratorRefreshEmissionFailureReleases proves the contract's 403
// path: a failing store leaves the record released and the error is the
// emission sentinel.
func TestOrchestratorRefreshEmissionFailureReleases(t *testing.T) {
	orch, sessions, _, _, reader := unifiedOrchestrator(t)
	sessions.consumeCode = 1
	reader.view = auth.UserWithTenantStatus{User: auth.User{ID: fakeUserID, TenantID: fakeTenantID, Role: "owner"}, Status: 1}
	sessions.createdErr = errFakeStore

	tokens, err := orch.Refresh(refreshClaims(t, uuid.NewString(), fakeUserID))
	require.ErrorIs(t, err, auth.ErrRefreshEmission)
	require.Nil(t, tokens)
	require.Equal(t, 1, sessions.created)
	require.Equal(t, 1, sessions.releaseCalled)
}
