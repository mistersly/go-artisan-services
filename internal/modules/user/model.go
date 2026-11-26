package user

import (
	auth "github.com/Massad/gin-boilerplate/internal/modules/auth"
)

// UserLoginResponse ...
type UserLoginResponse struct {
	User    User          `json:"user"`
	Tenant  TenantPayload `json:"tenant"`
	Token   auth.Token    `json:"token"`
	Message string        `json:"message"`
}

// The certified names below are aliases of the auth module's port structs
// (plan Phase 5: the ports UserAuthenticator/UserReader own their payload
// structs, the modules keep their names — the user→auth type direction is the
// one the TokenService port already certified; auth never imports user).

// TenantPayload is the tenant subset embedded in user auth responses: the
// auth port owns the struct, the module keeps the certified name.
type TenantPayload = auth.TenantPayload

// User is the stored user identity: the auth port owns the struct, the
// module keeps the certified name.
type User = auth.User

// UserWithTenantStatus bundles the refresh-time re-read of a user with the
// freshness data the refresh flow needs: the auth port owns the struct, the
// module keeps the certified name.
type UserWithTenantStatus = auth.UserWithTenantStatus
