package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	servicecertificate "github.com/keepon-online/sb-ops/internal/service/certificate"
)

type certificateSelfSignedRequest struct {
	Name       string `json:"name" binding:"required"`
	ServerName string `json:"serverName" binding:"required"`
}

type certificateRegenerateRequest struct {
	Revision   int64  `json:"revision" binding:"required"`
	ServerName string `json:"serverName" binding:"required"`
}

type certificateWriteRequest struct {
	Revision       int64  `json:"revision"`
	Name           string `json:"name" binding:"required"`
	CertificatePEM string `json:"certificatePem" binding:"required"`
	PrivateKeyPEM  string `json:"privateKeyPem" binding:"required"`
}

func (h *handler) acmeOptions() servicecertificate.AcmeOptions {
	cfg := h.Config()
	return servicecertificate.AcmeOptions{Email: cfg.Acme.Email, Provider: cfg.Acme.DNSProvider, APIToken: cfg.Acme.APIToken}
}

func (h *handler) listCertificates(c *gin.Context) {
	values, err := h.Certificates.List(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[servicecertificate.Certificate]{Items: values})
}

func (h *handler) createSelfSignedCertificate(c *gin.Context) {
	var request certificateSelfSignedRequest
	if !httpx.BindJSON(c, &request, "请输入名称与服务器名称") {
		return
	}
	value, err := h.Certificates.CreateSelfSigned(c.Request.Context(), servicecertificate.SelfSignedInput{Name: request.Name, ServerName: request.ServerName})
	if !h.mutate(c, err, actionCertificateSelfSignedCreate, resourceCertificate, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) regenerateSelfSignedCertificate(c *gin.Context) {
	var request certificateRegenerateRequest
	if !httpx.BindJSON(c, &request, "请提供修订号与服务器名称") {
		return
	}
	value, err := h.Certificates.RegenerateSelfSigned(c.Request.Context(), servicecertificate.RegenerateInput{ID: c.Param("id"), Revision: request.Revision, ServerName: request.ServerName})
	if !h.mutate(c, err, actionCertificateSelfSignedRegenerate, resourceCertificate, c.Param("id"), value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) createCertificate(c *gin.Context) {
	request, ok := bindCertificateRequest(c)
	if !ok {
		return
	}
	value, err := h.Certificates.Create(c.Request.Context(), request)
	if !h.mutate(c, err, actionCertificateCreate, resourceCertificate, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) createAcmeCertificate(c *gin.Context) {
	var request struct {
		Name   string `json:"name" binding:"required"`
		Domain string `json:"domain" binding:"required"`
	}
	if !httpx.BindJSON(c, &request, "请输入名称与域名") {
		return
	}
	value, err := h.Certificates.IssueACME(c.Request.Context(), servicecertificate.AcmeInput{Name: request.Name, Domain: request.Domain}, h.acmeOptions())
	if !h.mutate(c, err, actionCertificateAcmeCreate, resourceCertificate, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) renewAcmeCertificate(c *gin.Context) {
	value, err := h.Certificates.RenewACME(c.Request.Context(), c.Param("id"), h.acmeOptions())
	if !h.mutate(c, err, actionCertificateAcmeRenew, resourceCertificate, c.Param("id"), value.ID) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) getCertificate(c *gin.Context) {
	value, err := h.Certificates.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) updateCertificate(c *gin.Context) {
	request, ok := bindCertificateRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	before, beforeErr := h.Certificates.Get(c.Request.Context(), request.ID)
	value, err := h.Certificates.Update(c.Request.Context(), request)
	if !h.mutate(c, err, actionCertificateUpdate, resourceCertificate, request.ID, value.ID, diffable(beforeErr, err, before, value)) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deleteCertificate(c *gin.Context) {
	err := h.Certificates.Delete(c.Request.Context(), c.Param("id"))
	if !h.mutate(c, err, actionCertificateDelete, resourceCertificate, c.Param("id"), c.Param("id")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func bindCertificateRequest(c *gin.Context) (servicecertificate.WriteInput, bool) {
	var request certificateWriteRequest
	if !httpx.BindJSON(c, &request, "请提供证书与私钥") {
		return servicecertificate.WriteInput{}, false
	}
	return servicecertificate.WriteInput{
		Revision: request.Revision, Name: request.Name,
		CertificatePEM: request.CertificatePEM, PrivateKeyPEM: request.PrivateKeyPEM,
	}, true
}
