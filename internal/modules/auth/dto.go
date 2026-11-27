package auth

import (
	validate "github.com/Massad/gin-boilerplate/internal/platform/validate"
)

// LoginForm is the unified login form; the binding tags and the validation
// messages are byte-identical to the shapes the user and admin modules carry
// for their own login routes (L6 precedent: admin.LoginForm).
type LoginForm struct {
	Email    string `form:"email" json:"email" binding:"required,email"`
	Password string `form:"password" json:"password" binding:"required,min=3,max=50"`
}

// LoginMessages defines the validation error messages for the unified login
// form.
var LoginMessages = validate.ValidationMessages{
	"Email": {
		"required": "Please enter your email",
		"email":    "Please enter a valid email",
	},
	"Password": {
		"required": "Please enter your password",
		"min":      "Your password should be between 3 and 50 characters",
		"max":      "Your password should be between 3 and 50 characters",
		"eqfield":  "Your passwords does not match",
	},
}

// RefreshForm carries the refresh token the endpoint re-emits against.
type RefreshForm struct {
	RefreshToken string `form:"refresh_token" json:"refresh_token" binding:"required"`
}
