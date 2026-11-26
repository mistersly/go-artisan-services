package admin

import (
	auth "github.com/Massad/gin-boilerplate/internal/modules/auth"
)

// AdminIdentity is the superadmin identity verified at login time: the auth
// port owns the struct (design shape delivered in L6), the module keeps the
// certified name — the admin→auth type direction is the one the TokenService
// port already certified.
type AdminIdentity = auth.AdminIdentity
