package auth

import "github.com/google/uuid"

// AuthResponse is the documented payload of a refresh response.
type AuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// TokenDetails ...
type TokenDetails struct {
	AccessToken  string
	RefreshToken string
	AccessUUID   string
	RefreshUUID  string
	AtExpires    int64
	RtExpires    int64
}

// AdminTokenDetails carries the single, long-lived superadmin access token.
type AdminTokenDetails struct {
	AccessToken string
	AccessUUID  string
	AtExpires   int64
}

// AccessDetails carries the tenant-scoped session identity extracted from a
// access token: an admin token is rejected here because it lacks the claims.
type AccessDetails struct {
	AccessUUID string
	UserID     uuid.UUID
	TenantID   uuid.UUID
	Role       string
}

// AdminAccessDetails carries the platform session identity of an admin token.
type AdminAccessDetails struct {
	AccessUUID string
	AdminID    uuid.UUID
}

// Token is the session token pair of a tenant session. RefreshToken is
// omitted when empty: the unified login reuses this model for its superadmin
// branch, which carries an access token only (no refresh half).
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// AdminToken is the token payload of a superadmin session (no refresh).
type AdminToken struct {
	AccessToken string `json:"access_token"`
}

// MessageResponse is the shared message-only payload referenced by the admin,
// user and auth Swagger annotations (it leaves the root models package with
// the unified login in Phase 7).
type MessageResponse struct {
	Message string `json:"message"`
}

// User is the authenticated user identity the UserAuthenticator port returns:
// the identity fields of the tenant directory plus the hash the login flow
// compared (never serialized to clients).
type User struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	// Password is the bcrypt hash; never serialized to clients.
	Password string    `json:"-"`
	Name     string    `json:"name"`
	TenantID uuid.UUID `json:"tenant_id"`
	Role     string    `json:"role"`
}

// TenantPayload is the tenant subset embedded in the auth responses: the
// port owns its payload struct (plan Phase 5) and the modules carry their
// certified names for it, without cross-module type imports.
type TenantPayload struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// UserWithTenantStatus bundles the refresh-time re-read of a user with the
// freshness data the refresh flow needs (the UserReader port view).
type UserWithTenantStatus struct {
	User       User
	Tenant     TenantPayload
	TenantSlug string
	Status     int16
}

// AdminIdentity is the superadmin identity the AdminAuthenticator port
// returns at login time (design shape delivered in L6).
type AdminIdentity struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
}

// LoginKind discriminates which directory authenticated a unified login.
type LoginKind string

const (
	// LoginKindTenantUser marks a tenant user session.
	LoginKindTenantUser LoginKind = "tenant_user"
	// LoginKindSuperadmin marks a platform superadmin session.
	LoginKindSuperadmin LoginKind = "superadmin"
)

// UnifiedLoginResponse is the documented payload of POST /v1/login. The
// response is a union discriminated by kind: the tenant-user branch populates
// user/tenant and an access/refresh token pair, the superadmin branch
// populates admin and a single long-lived access token (8h, no refresh — the
// refresh_token key is then absent from the payload).
type UnifiedLoginResponse struct {
	Kind   LoginKind     `json:"kind"`
	User   User          `json:"user,omitempty"`
	Tenant TenantPayload `json:"tenant,omitempty"`
	Admin  AdminIdentity `json:"admin,omitempty"`
	Token  Token         `json:"token"`
}

// Secrets carries the three independent signing secrets of the session
// tokens. They are injected at construction by the composition root: the
// service never reads the environment (tests set them with t.Setenv).
type Secrets struct {
	Access      string // ACCESS_SECRET signs the tenant access tokens
	Refresh     string // REFRESH_SECRET signs the tenant refresh tokens
	AdminAccess string // ADMIN_ACCESS_SECRET signs the superadmin access token
}
