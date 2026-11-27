// Package auth owns the session primitives: issuing and verifying the tenant
// and superadmin JWTs, the Redis session records with the soft-consume of the
// refresh token (design decision 7), the TokenAuth/AdminAuth/RequirePermission
// middlewares, the logout handlers and, since Phase 7, the unified login and
// the refresh flow driven through the module ports (the orchestrator). The
// context keys set by TokenAuth are "userID", "tenantID" and "role"; AdminAuth
// sets "adminID".
package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	jwt "github.com/golang-jwt/jwt/v4"
	uuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SessionRepository is the port the service consumes: the Redis session
// records with the soft-consume mechanics of the refresh token. Formats,
// keys, index and scripts stay inside the repository implementation.
type SessionRepository interface {
	CreateTenantSession(userID, tenantID uuid.UUID, role string, td *TokenDetails) error
	FetchTenantSession(accessUUID string, claimedUserID uuid.UUID) (uuid.UUID, error)
	DeleteTenantSession(givenUUID string, userID uuid.UUID) (int64, error)
	RevokeUserSessions(userID uuid.UUID) error
	// ConsumeRefresh softly consumes the refresh record and returns the
	// flow signal: 1 = first consumption claimed, 2 = recovery of a
	// not-delivered record claimed, 0 = refused.
	ConsumeRefresh(refreshUUID string, userID uuid.UUID) (int64, error)
	// ReleaseRefresh downgrades a claimed-but-not-delivered consumption so
	// the replayed token becomes recoverable during the remaining grace.
	ReleaseRefresh(refreshUUID string, userID uuid.UUID) (int64, error)
	CreateAdminSession(adminID uuid.UUID, td *AdminTokenDetails) error
	FetchAdminSession(accessUUID string, adminID uuid.UUID) (uuid.UUID, error)
	DeleteAdminSession(givenUUID string) (int64, error)
	// SweepConsumed counts the present consumed records and purges the
	// relics the TTL failed to evict.
	SweepConsumed() (consumed, purged int64, err error)
}

// DefaultSweepInterval is the production cadence of the in-process sweep
// (decision 7: a ~5 minutes ticker; TTL eviction stays primary).
const DefaultSweepInterval = 5 * time.Minute

// The module ports (design Phase 3-7): the cross-module dependencies of the
// unified login and the refresh flow, defined consumer-side in auth with
// payload structs owned by the port. The user and admin modules implement
// them and the composition root injects them; auth never imports those
// modules (the user/admin files keep their certified names as aliases of the
// port types).

// UserAuthenticator is the tenant-directory login the unified flow drives
// first, implemented by the user module (its certified login flow). The
// discriminated outcomes are the port contract: a suspended tenant is
// ErrTenantSuspended (the 403 stays observable) and an unknown email is
// pgx.ErrNoRows (the flow equalizes the cost then reaches the admin fallback).
type UserAuthenticator interface {
	Login(form LoginForm) (User, TenantPayload, Token, error)
}

// AdminAuthenticator is the superadmin fallback, implemented by the admin
// module (its certified credential verification).
type AdminAuthenticator interface {
	Login(email, password string) (AdminIdentity, error)
}

// UserReader is the refresh-time re-read port, implemented by the user
// module: the role is re-fetched from the database at every re-emission,
// never trusted from the claims.
type UserReader interface {
	OneWithTenant(userID uuid.UUID) (UserWithTenantStatus, error)
}

// Service implements the session primitives on top of its repository port.
// It is consumed by the module middlewares and handlers and, through them, by
// the root controllers during the transitory migration window.
type Service struct {
	secrets  Secrets
	sessions SessionRepository
}

// NewService builds the service on the injected signing secrets and session
// repository.
func NewService(secrets Secrets, sessions SessionRepository) *Service {
	return &Service{secrets: secrets, sessions: sessions}
}

// CreateToken issues an access/refresh pair whose claims carry the tenant and
// the role: the tenant is always resolved from the signed token, never from
// client-provided data.
func (s *Service) CreateToken(userID, tenantID uuid.UUID, role string) (*TokenDetails, error) {

	td := &TokenDetails{}
	td.AtExpires = time.Now().Add(time.Minute * 15).Unix()
	td.AccessUUID = uuid.New().String()

	td.RtExpires = time.Now().Add(time.Hour * 24 * 7).Unix()
	td.RefreshUUID = uuid.New().String()

	var err error
	//Creating Access Token
	atClaims := jwt.MapClaims{}
	atClaims["authorized"] = true
	atClaims["access_uuid"] = td.AccessUUID
	atClaims["user_id"] = userID.String()
	atClaims["tenant_id"] = tenantID.String()
	atClaims["role"] = role
	atClaims["exp"] = td.AtExpires

	at := jwt.NewWithClaims(jwt.SigningMethodHS256, atClaims)
	td.AccessToken, err = at.SignedString([]byte(s.secrets.Access))
	if err != nil {
		return nil, err
	}
	//Creating Refresh Token
	rtClaims := jwt.MapClaims{}
	rtClaims["refresh_uuid"] = td.RefreshUUID
	rtClaims["user_id"] = userID.String()
	rtClaims["tenant_id"] = tenantID.String()
	rtClaims["exp"] = td.RtExpires
	rt := jwt.NewWithClaims(jwt.SigningMethodHS256, rtClaims)
	td.RefreshToken, err = rt.SignedString([]byte(s.secrets.Refresh))
	if err != nil {
		return nil, err
	}
	return td, nil
}

// CreateAuth stores both session halves as "userID:tenantID:role" and indexes
// them under user_sessions:{userID} for global revocation.
func (s *Service) CreateAuth(userID, tenantID uuid.UUID, role string, td *TokenDetails) error {
	return s.sessions.CreateTenantSession(userID, tenantID, role, td)
}

// ExtractToken ...
func (s *Service) ExtractToken(r *http.Request) string {
	bearToken := r.Header.Get("Authorization")
	//normally Authorization the_token_xxx
	strArr := strings.Split(bearToken, " ")
	if len(strArr) == 2 {
		return strArr[1]
	}
	return ""
}

// VerifyToken ...
func (s *Service) VerifyToken(r *http.Request) (*jwt.Token, error) {
	tokenString := s.ExtractToken(r)
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		//Make sure that the token method conform to "SigningMethodHMAC"
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.secrets.Access), nil
	})
	if err != nil {
		return nil, err
	}
	return token, nil
}

// ExtractTokenMetadata ...
func (s *Service) ExtractTokenMetadata(r *http.Request) (*AccessDetails, error) {
	token, err := s.VerifyToken(r)
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	accessUUID, ok := claims["access_uuid"].(string)
	if !ok {
		return nil, errors.New("invalid access_uuid claim")
	}

	userID, err := uuidClaim(claims, "user_id")
	if err != nil {
		return nil, err
	}
	tenantID, err := uuidClaim(claims, "tenant_id")
	if err != nil {
		return nil, err
	}
	role, ok := claims["role"].(string)
	if !ok || role == "" {
		return nil, errors.New("invalid role claim")
	}

	return &AccessDetails{
		AccessUUID: accessUUID,
		UserID:     userID,
		TenantID:   tenantID,
		Role:       role,
	}, nil
}

// uuidClaim reads a UUID claim stored as its string representation.
func uuidClaim(claims jwt.MapClaims, name string) (uuid.UUID, error) {
	value, ok := claims[name].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("invalid %s claim", name)
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid %s claim", name)
	}
	return parsed, nil
}

// FetchAuth verifies that the session exists in Redis and belongs to the
// claimed user, before handing the tenant identity back to the middleware.
func (s *Service) FetchAuth(authD *AccessDetails) (uuid.UUID, error) {
	return s.sessions.FetchTenantSession(authD.AccessUUID, authD.UserID)
}

// DeleteAuth consumes one session key and removes it from the user's session
// index.
func (s *Service) DeleteAuth(givenUUID string, userID uuid.UUID) (int64, error) {
	return s.sessions.DeleteTenantSession(givenUUID, userID)
}

// RevokeUserSessions deletes every active session of a user (role change,
// administrative logout).
func (s *Service) RevokeUserSessions(userID uuid.UUID) error {
	return s.sessions.RevokeUserSessions(userID)
}

// ConsumeRefreshAuth atomically verifies the stored owner and softly consumes
// a refresh token (design decision 7). The record is never deleted by the
// consumption: a first consumption marks it "consumed:1" (delivered) within a
// reduced TTL, a replay of a not-delivered record ("consumed:0", released by
// the failed delivery) claims the single recovery, and any other record
// (delivered, recovered, missing, mismatched owner or unknown state) is
// refused. Only the owner segment of the value is compared, so the stored
// format stays robust across role changes between a login and a refresh.
// Returns 1 when the first consumption is claimed, 2 when the recovery is
// claimed and 0 when the record is refused.
func (s *Service) ConsumeRefreshAuth(refreshUUID string, userID uuid.UUID) (int64, error) {
	code, err := s.sessions.ConsumeRefresh(refreshUUID, userID)
	if err != nil {
		return 0, err
	}
	// The session incident stays observable at the marking and at the
	// recovery (support-facing observability, decision 7).
	switch code {
	case 1:
		log.Printf("auth: refresh token %s consumed (delivery pending), user %s", refreshUUID, userID)
	case 2:
		log.Printf("auth: refresh token %s recovered (re-emission), user %s", refreshUUID, userID)
	}
	return code, nil
}

// ReleaseRefresh downgrades a claimed-but-not-delivered consumption: the
// consuming request failed before emitting the new pair, so the replayed
// token stays recoverable during the remaining grace. Returns 1 when the
// release happened and 0 when the record did not allow it.
func (s *Service) ReleaseRefresh(refreshUUID string, userID uuid.UUID) (int64, error) {
	return s.sessions.ReleaseRefresh(refreshUUID, userID)
}

// StartSweep runs the in-process sweep ticker until the returned stop
// function is called. Every tick counts the present consumed records for
// observability and defensively purges the relics the TTL failed to evict
// (e.g. an incident removed the expiry). It is never the purge dependency:
// TTL eviction stays primary, and the sweep never touches an active record or
// one whose grace is still running.
func (s *Service) StartSweep(interval time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				consumed, purged, err := s.sessions.SweepConsumed()
				if err != nil {
					log.Printf("auth: session sweep failed: %v", err)
					continue
				}
				if consumed > 0 || purged > 0 {
					log.Printf("auth: session sweep: %d consumed record(s) present, %d relic(s) purged", consumed, purged)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// SweepConsumed counts the present consumed session records and purges the
// relics whose TTL anomaly makes them unevictable.
func (s *Service) SweepConsumed() (consumed, purged int64, err error) {
	return s.sessions.SweepConsumed()
}

// CreateAdminToken issues the single superadmin access token: signed with the
// dedicated ADMIN_ACCESS_SECRET, ~8h TTL, no refresh.
func (s *Service) CreateAdminToken(adminID uuid.UUID) (*AdminTokenDetails, error) {
	td := &AdminTokenDetails{}
	td.AtExpires = time.Now().Add(time.Hour * 8).Unix()
	td.AccessUUID = uuid.New().String()

	claims := jwt.MapClaims{}
	claims["authorized"] = true
	claims["access_uuid"] = td.AccessUUID
	claims["admin_id"] = adminID.String()
	claims["role"] = "superadmin"
	claims["exp"] = td.AtExpires

	at := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	var err error
	td.AccessToken, err = at.SignedString([]byte(s.secrets.AdminAccess))
	if err != nil {
		return nil, err
	}
	return td, nil
}

// CreateAdminAuth stores the admin session under the admin: Redis prefix.
func (s *Service) CreateAdminAuth(adminID uuid.UUID, td *AdminTokenDetails) error {
	return s.sessions.CreateAdminSession(adminID, td)
}

// ExtractAdminTokenMetadata validates an admin token against the ADMIN_ACCESS
// SECRET: a tenant token carries a different signature and no admin_id claim.
func (s *Service) ExtractAdminTokenMetadata(r *http.Request) (*AdminAccessDetails, error) {
	tokenString := s.ExtractToken(r)
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.secrets.AdminAccess), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}
	accessUUID, ok := claims["access_uuid"].(string)
	if !ok {
		return nil, errors.New("invalid access_uuid claim")
	}
	adminID, err := uuidClaim(claims, "admin_id")
	if err != nil {
		return nil, err
	}
	return &AdminAccessDetails{AccessUUID: accessUUID, AdminID: adminID}, nil
}

// FetchAdmin verifies the admin session exists in Redis.
func (s *Service) FetchAdmin(authD *AdminAccessDetails) (uuid.UUID, error) {
	return s.sessions.FetchAdminSession(authD.AccessUUID, authD.AdminID)
}

// DeleteAdminAuth consumes an admin session: immediate logout, no refresh.
func (s *Service) DeleteAdminAuth(givenUUID string) (int64, error) {
	return s.sessions.DeleteAdminSession(givenUUID)
}

// ErrTenantSuspended is the suspension signal the tenant user login
// propagates: the unified answers keep the 403 observable (the user module
// carries the certified value as this sentinel).
var ErrTenantSuspended = errors.New("tenant suspended")

// ErrInvalidCredentials is the single generic failure of the unified login:
// neither the status nor the body nor the response time may reveal whether an
// email is unknown, belongs to a tenant user, or belongs to a superadmin.
var ErrInvalidCredentials = errors.New("invalid login details")

// ErrRefreshEmission marks the refresh failures the contract answers 403: the
// consumption claimed the record but the new session pair could not be issued
// or stored.
var ErrRefreshEmission = errors.New("session re-emission failed")

// errRefreshUnauthorized is the generic failure of every other refresh path
// (parse, claims, consumption, re-read, suspended tenant): the contract
// answers 401 with the fixed message.
var errRefreshUnauthorized = errors.New("invalid authorization, please login again")

// dummyPasswordHash is a fixed bcrypt digest (DefaultCost, like the stored
// passwords). It is compared against whenever the email matches no tenant
// user, so an unknown email costs exactly one bcrypt like a wrong password.
const dummyPasswordHash = "$2a$10$94FFhZ.MSC3aFhOoaREEq.qnfTqQBnN5B7E71PAqFYlVzyiGDF/fO"

// dummyHashComparisons counts the dummy bcrypt comparisons executed; the
// integration suite observes it to prove the timing-equalization runs.
var dummyHashComparisons atomic.Int64

// DummyHashComparisons reports how many dummy bcrypt comparisons ran since
// process start (test observability of the enumeration mitigation).
func DummyHashComparisons() int64 { return dummyHashComparisons.Load() }

// UnifiedLoginResult is the discriminated payload of the unified login:
// exactly one of the tenant-user or superadmin session kinds is meaningful.
type UnifiedLoginResult struct {
	Kind LoginKind
	// User, Tenant and Token are set when Kind is LoginKindTenantUser.
	User   User
	Tenant TenantPayload
	Token  Token
	// Admin and AccessToken are set when Kind is LoginKindSuperadmin.
	Admin       AdminIdentity
	AccessToken string
}

// Orchestrator is the auth-only consumer of the module cross-module ports:
// it carries the unified login (tenant directory first, superadmin fallback)
// and the refresh flow. The composition root builds it after the user and
// admin services (they consume the token service first); auth never imports
// them.
type Orchestrator struct {
	// tokens is the session service of this module: the issuance and the
	// soft-consume primitives, its unique implementation (no port — the
	// pure suite exercises it through the fake session repository).
	tokens  *Service
	users   UserAuthenticator
	admins  AdminAuthenticator
	readers UserReader
}

// NewOrchestrator builds the orchestrator on the injected session service
// and the module ports.
func NewOrchestrator(tokens *Service, users UserAuthenticator, admins AdminAuthenticator, readers UserReader) *Orchestrator {
	return &Orchestrator{tokens: tokens, users: users, admins: admins, readers: readers}
}

// UnifiedLogin authenticates against the tenant directory first and falls back
// to the superadmin directory only when the email matches no user. The client
// observes exactly three outcomes: 200 tenant_user, 200 superadmin, or a
// generic failure:
//
//   - user found + wrong password → ErrInvalidCredentials, no admin attempt
//     (an email must never have its two passwords probed);
//   - user found + suspended tenant → ErrTenantSuspended propagates as 403;
//   - unknown email → one dummy bcrypt (constant cost) then the admin path;
//   - admin not found or wrong password → ErrInvalidCredentials.
//
// The no-overlap policy is explicit: a superadmin email must never match a
// tenant user email, so the two directories are never probed for the same
// account.
func (o *Orchestrator) UnifiedLogin(form LoginForm) (UnifiedLoginResult, error) {
	usr, tenantPayload, token, userErr := o.users.Login(form)
	switch {
	case userErr == nil:
		return UnifiedLoginResult{Kind: LoginKindTenantUser, User: usr, Tenant: tenantPayload, Token: token}, nil
	case errors.Is(userErr, ErrTenantSuspended):
		return UnifiedLoginResult{}, userErr
	case errors.Is(userErr, pgx.ErrNoRows):
		// Unknown email: equalize the cost below, then try the superadmin
		// directory.
	default:
		// Wrong password or an infrastructure failure: never reach the admin
		// directory.
		return UnifiedLoginResult{}, ErrInvalidCredentials
	}

	// The comparison result is deliberately discarded: the password is wrong
	// by construction. The call exists so that the unknown-email response
	// costs one bcrypt exactly like a wrong-password response.
	dummyHashComparisons.Add(1)
	_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(form.Password))

	admin, adminErr := o.admins.Login(form.Email, form.Password)
	if adminErr != nil {
		return UnifiedLoginResult{}, ErrInvalidCredentials
	}

	td, err := o.tokens.CreateAdminToken(admin.ID)
	if err != nil {
		return UnifiedLoginResult{}, ErrInvalidCredentials
	}
	if err := o.tokens.CreateAdminAuth(admin.ID, td); err != nil {
		return UnifiedLoginResult{}, ErrInvalidCredentials
	}

	return UnifiedLoginResult{
		Kind:        LoginKindSuperadmin,
		Admin:       admin,
		AccessToken: td.AccessToken,
	}, nil
}

// Refresh parses the refresh token against the injected signing secret,
// softly consumes the record (decision 7), re-reads the user through the
// reader port (fresh role from the database, active tenant required) and
// re-emits a fresh pair. Every failed attempt releases a claimed
// but-not-delivered consumption so the replayed token stays recoverable
// during the remaining grace.
func (o *Orchestrator) Refresh(refreshToken string) (map[string]string, error) {
	//verify the token
	token, err := jwt.Parse(refreshToken, func(token *jwt.Token) (interface{}, error) {
		//Make sure that the token method conform to "SigningMethodHMAC"
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(o.tokens.secrets.Refresh), nil
	})
	//if there is an error, the token must have expired
	if err != nil {
		return nil, errRefreshUnauthorized
	}
	//is token valid?
	claims, ok := token.Claims.(jwt.MapClaims) //the token claims should conform to MapClaims
	if !ok || !token.Valid {
		return nil, errRefreshUnauthorized
	}
	refreshUUID, ok := claims["refresh_uuid"].(string)
	if !ok {
		return nil, errRefreshUnauthorized
	}
	userIDClaim, ok := claims["user_id"].(string)
	if !ok {
		return nil, errRefreshUnauthorized
	}
	userID, err := uuid.Parse(userIDClaim)
	if err != nil {
		return nil, errRefreshUnauthorized
	}
	// Verify ownership and softly consume the previous refresh token: 1
	// claims the first consumption (the record is marked delivered and a
	// failing flow must release it), 2 claims the single recovery of a
	// not-delivered record, 0 refuses the token.
	consumed, consumeErr := o.tokens.ConsumeRefreshAuth(refreshUUID, userID)
	if consumeErr != nil || consumed == 0 { //if any goes wrong
		return nil, errRefreshUnauthorized
	}

	// Re-read the user through the platform pool: the role is never read back
	// from the claims, the user must still exist and its tenant stay active.
	row, readErr := o.readers.OneWithTenant(userID)
	if readErr != nil {
		// The consumption delivered nothing: release the record so the
		// replayed token stays recoverable during the grace.
		if consumed == 1 {
			_, _ = o.tokens.ReleaseRefresh(refreshUUID, userID)
		}
		return nil, errRefreshUnauthorized
	}
	if row.Status != 1 {
		if consumed == 1 {
			_, _ = o.tokens.ReleaseRefresh(refreshUUID, userID)
		}
		return nil, errRefreshUnauthorized
	}

	//Create new pairs of refresh and access tokens
	ts, createErr := o.tokens.CreateToken(row.User.ID, row.User.TenantID, row.User.Role)
	if createErr != nil {
		if consumed == 1 {
			_, _ = o.tokens.ReleaseRefresh(refreshUUID, userID)
		}
		return nil, ErrRefreshEmission
	}
	//save the tokens metadata to redis
	saveErr := o.tokens.CreateAuth(row.User.ID, row.User.TenantID, row.User.Role, ts)
	if saveErr != nil {
		if consumed == 1 {
			_, _ = o.tokens.ReleaseRefresh(refreshUUID, userID)
		}
		return nil, ErrRefreshEmission
	}
	tokens := map[string]string{
		"access_token":  ts.AccessToken,
		"refresh_token": ts.RefreshToken,
	}
	return tokens, nil
}
