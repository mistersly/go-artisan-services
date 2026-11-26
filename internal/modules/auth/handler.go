package auth

import (
	"errors"
	"net/http"

	validate "github.com/Massad/gin-boilerplate/internal/platform/validate"
	"github.com/gin-gonic/gin"
)

// Handler serves the module HTTP surface: the two logout handlers plus,
// since Phase 7, the unified login and the refresh (bind/response only — the
// flows live in the orchestrator).
type Handler struct {
	svc  *Service
	flow *Orchestrator
}

// NewHandler builds the auth handlers on the injected service and
// orchestrator.
func NewHandler(svc *Service, flow *Orchestrator) *Handler {
	return &Handler{svc: svc, flow: flow}
}

// Logout User godoc
// @Summary Logout User example
// @Schemes
// @Description Logout User example
// @Tags User
// @Accept json
// @Produce json
// @Success 	 200  {object}  auth.MessageResponse
// @Failure      406  {object}  auth.MessageResponse
// @Router /user/logout [GET]
func (h *Handler) UserLogout(c *gin.Context) {
	au, err := h.svc.ExtractTokenMetadata(c.Request)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "User not logged in"})
		return
	}

	deleted, delErr := h.svc.DeleteAuth(au.AccessUUID, au.UserID)
	if delErr != nil || deleted == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Invalid request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Successfully logged out"})
}

// Logout Admin godoc
// @Summary Logout Admin example
// @Schemes
// @Description Logout Admin example
// @Tags Admin
// @Success 	 200  {object}  auth.MessageResponse
// @Failure      406  {object}  auth.MessageResponse
// @Router /admin/logout [get]
func (h *Handler) AdminLogout(c *gin.Context) {
	details, err := h.svc.ExtractAdminTokenMetadata(c.Request)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Admin not logged in"})
		return
	}

	deleted, delErr := h.svc.DeleteAdminAuth(details.AccessUUID)
	if delErr != nil || deleted == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Invalid request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Successfully logged out"})
}

// Login Unified godoc
// @Summary Unified login example
// @Schemes
// @Description Unified login: tenant user first, superadmin fallback
// @Description The response is discriminated by kind: kind=tenant_user carries user, tenant and an access/refresh token pair; kind=superadmin carries admin and an access token only (no refresh_token).
// @Tags Auth
// @Accept json
// @Produce json
// @Param auth body auth.LoginForm true "Auth"
// @Success 	 200  {object}  auth.UnifiedLoginResponse
// @Failure      403  {object}  auth.MessageResponse
// @Failure      406  {object}  auth.MessageResponse
// @Router /login [post]
func (h *Handler) Login(c *gin.Context) {
	var loginForm LoginForm

	if validationErr := c.ShouldBindJSON(&loginForm); validationErr != nil {
		message := validate.Translate(validationErr, LoginMessages)
		c.AbortWithStatusJSON(http.StatusNotAcceptable, gin.H{"message": message})
		return
	}

	result, err := h.flow.UnifiedLogin(loginForm)
	if err != nil {
		if errors.Is(err, ErrTenantSuspended) {
			// A suspended tenant stays observable: the 403 is not masked.
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "Tenant suspended"})
			return
		}
		c.AbortWithStatusJSON(http.StatusNotAcceptable, gin.H{"message": "Invalid login details"})
		return
	}

	if result.Kind == LoginKindSuperadmin {
		// The superadmin session has no refresh half: the token model omits
		// the empty refresh_token key.
		c.JSON(http.StatusOK, gin.H{
			"kind":  result.Kind,
			"admin": result.Admin,
			"token": Token{AccessToken: result.AccessToken},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"kind":   result.Kind,
		"user":   result.User,
		"tenant": result.Tenant,
		"token":  result.Token,
	})
}

// Refresh Token godoc
// @Summary Refresh Token example
// @Schemes
// @Description Refresh Token example
// @Tags Auth
// @Accept json
// @Produce json
// @Param auth body auth.RefreshForm true "Auth"
// @Success 	 200  {object}  auth.AuthResponse
// @Failure      406  {object}  auth.MessageResponse
// @Router /token/refresh [POST]
func (h *Handler) Refresh(c *gin.Context) {
	var tokenForm RefreshForm

	if c.ShouldBindJSON(&tokenForm) != nil {
		c.JSON(http.StatusNotAcceptable, gin.H{"message": "Invalid form", "form": tokenForm})
		c.Abort()
		return
	}

	tokens, err := h.flow.Refresh(tokenForm.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrRefreshEmission) {
			// The consumption claimed the record but the new pair could not
			// be issued or stored: the contract answers 403.
			c.JSON(http.StatusForbidden, gin.H{"message": "Invalid authorization, please login again"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"message": "Invalid authorization, please login again"})
		return
	}
	c.JSON(http.StatusOK, tokens)
}
