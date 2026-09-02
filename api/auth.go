package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	"github.com/keepon-online/sb-ops/internal/identity"
)

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewPassword     string `json:"newPassword" binding:"required"`
}

type accountResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Enabled   bool      `json:"enabled"`
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type principalResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type loginResponse struct {
	Token     string          `json:"token"`
	ExpiresAt time.Time       `json:"expiresAt"`
	User      accountResponse `json:"user"`
}

type meResponse struct {
	User principalResponse `json:"user"`
}

type setupAdminRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type setupStatusResponse struct {
	Initialized bool `json:"initialized"`
}

func (h *handler) setupStatus(c *gin.Context) {
	initialized, err := h.Store.Initialized(c.Request.Context())
	if err != nil {
		writeInternalError(c, "内部服务器错误")
		return
	}
	httpx.OK(c, setupStatusResponse{Initialized: initialized})
}

func (h *handler) initializeAdmin(c *gin.Context) {
	var request setupAdminRequest
	if !httpx.BindJSON(c, &request, "请输入用户名和密码") {
		return
	}
	account, err := h.Identity.InitializeAdmin(c.Request.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, identity.ErrAlreadyInitialized) {
			httpx.Error(c, http.StatusConflict, httpx.CodeAlreadyInitialized, "管理员已初始化", nil)
			return
		}
		writeIdentityError(c, err)
		return
	}
	c.Set(principalKey, identity.Principal{AccountID: account.ID})
	h.audit(c, actionSetupAdminInitialize, resourceAccount, account.ID, resultSuccess)
	httpx.Created(c, newAccountResponse(account))
}

func (h *handler) login(c *gin.Context) {
	var request loginRequest
	if !httpx.BindJSON(c, &request, "请输入用户名和密码") {
		return
	}
	result, err := h.Identity.Login(c.Request.Context(), request.Username, request.Password, identity.LoginMeta{
		ClientIP: clientAddress(c), UserAgent: c.GetHeader("User-Agent"),
	})
	if err != nil {
		h.audit(c, actionAuthLogin, resourceAccount, "", resultFailure)
		writeIdentityError(c, err)
		return
	}
	httpx.OK(c, loginResponse{
		Token: result.Token, ExpiresAt: result.ExpiresAt, User: newAccountResponse(result.Account),
	})
	c.Set(principalKey, identity.Principal{AccountID: result.Account.ID})
	h.audit(c, actionAuthLogin, resourceAccount, result.Account.ID, resultSuccess)
}

func (h *handler) me(c *gin.Context) {
	principal := currentPrincipal(c)
	httpx.OK(c, meResponse{User: newPrincipalResponse(principal)})
}

func (h *handler) changePassword(c *gin.Context) {
	var request changePasswordRequest
	if !httpx.BindJSON(c, &request, "请输入当前密码和新密码") {
		return
	}
	principal := currentPrincipal(c)
	account, err := h.Identity.ChangePassword(c.Request.Context(), principal.AccountID, request.CurrentPassword, request.NewPassword)
	if err != nil {
		h.audit(c, actionAuthPasswordChange, resourceAccount, principal.AccountID, resultFailure)
		writeIdentityError(c, err)
		return
	}
	h.audit(c, actionAuthPasswordChange, resourceAccount, account.ID, resultSuccess)
	httpx.OK(c, newAccountResponse(account))
}

func (h *handler) logout(c *gin.Context) {
	token, _ := c.Get(tokenKey)
	if err := h.Identity.Logout(c.Request.Context(), token.(string)); err != nil {
		writeIdentityError(c, err)
		return
	}
	h.audit(c, actionAuthLogout, resourceSession, currentPrincipal(c).SessionID, resultSuccess)
	c.Status(http.StatusNoContent)
}

func newAccountResponse(account identity.Account) accountResponse {
	return accountResponse{
		ID: account.ID, Username: account.Username, Enabled: account.Enabled,
		Revision: account.Revision, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}
}

func newPrincipalResponse(principal identity.Principal) principalResponse {
	return principalResponse{ID: principal.AccountID, Username: principal.Username}
}
