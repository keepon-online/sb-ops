package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	servicepolicy "github.com/keepon-online/sb-ops/internal/service/policy"
)

type policyWriteRequest struct {
	Revision      int64  `json:"revision"`
	Name          string `json:"name" binding:"required"`
	Description   string `json:"description"`
	Enabled       *bool  `json:"enabled" binding:"required"`
	DefaultAction string `json:"defaultAction" binding:"required"`
}

type ruleWriteRequest struct {
	Revision   int64                    `json:"revision"`
	Name       string                   `json:"name" binding:"required"`
	Enabled    *bool                    `json:"enabled" binding:"required"`
	Priority   int                      `json:"priority"`
	Conditions servicepolicy.Conditions `json:"conditions"`
	Action     string                   `json:"action" binding:"required"`
	NoResolve  bool                     `json:"noResolve"`
}

type policiesResponse struct {
	Items []servicepolicy.Policy `json:"items"`
}

func (h *handler) listPolicyRuleSets(c *gin.Context) {
	httpx.OK(c, itemsResponse[servicepolicy.RuleSetOption]{Items: servicepolicy.RuleSetOptions()})
}

func (h *handler) listPolicies(c *gin.Context) {
	values, err := h.Policies.List(c.Request.Context())
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[servicepolicy.Policy]{Items: values})
}

func (h *handler) createPolicy(c *gin.Context) {
	request, ok := bindPolicyRequest(c)
	if !ok {
		return
	}
	value, err := h.Policies.Create(c.Request.Context(), request)
	if !h.mutate(c, err, actionPolicyCreate, resourcePolicy, "", value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) getPolicy(c *gin.Context) {
	value, err := h.Policies.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) updatePolicy(c *gin.Context) {
	request, ok := bindPolicyRequest(c)
	if !ok {
		return
	}
	request.ID = c.Param("id")
	before, beforeErr := h.Policies.Get(c.Request.Context(), request.ID)
	value, err := h.Policies.Update(c.Request.Context(), request)
	if !h.mutate(c, err, actionPolicyUpdate, resourcePolicy, request.ID, value.ID, diffable(beforeErr, err, before, value)) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deletePolicy(c *gin.Context) {
	err := h.Policies.Delete(c.Request.Context(), c.Param("id"))
	if !h.mutate(c, err, actionPolicyDelete, resourcePolicy, c.Param("id"), c.Param("id")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) clonePolicy(c *gin.Context) {
	var request servicepolicy.CloneInput
	if !httpx.BindJSON(c, &request, "请输入名称") {
		return
	}
	value, err := h.Policies.Clone(c.Request.Context(), c.Param("id"), request)
	if !h.mutate(c, err, actionPolicyClone, resourcePolicy, c.Param("id"), value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) listPolicyRules(c *gin.Context) {
	values, err := h.Policies.ListRules(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, itemsResponse[servicepolicy.Rule]{Items: values})
}

func (h *handler) createPolicyRule(c *gin.Context) {
	request, ok := bindRuleRequest(c)
	if !ok {
		return
	}
	request.PolicyID = c.Param("id")
	value, err := h.Policies.CreateRule(c.Request.Context(), request)
	if !h.mutate(c, err, actionRuleCreate, resourceRule, request.PolicyID, value.ID) {
		return
	}
	httpx.Created(c, value)
}

func (h *handler) updatePolicyRule(c *gin.Context) {
	request, ok := bindRuleRequest(c)
	if !ok {
		return
	}
	request.PolicyID, request.ID = c.Param("id"), c.Param("ruleId")
	before, beforeErr := h.Policies.GetRule(c.Request.Context(), request.PolicyID, request.ID)
	value, err := h.Policies.UpdateRule(c.Request.Context(), request)
	if !h.mutate(c, err, actionRuleUpdate, resourceRule, request.ID, value.ID, diffable(beforeErr, err, before, value)) {
		return
	}
	httpx.OK(c, value)
}

func (h *handler) deletePolicyRule(c *gin.Context) {
	err := h.Policies.DeleteRule(c.Request.Context(), c.Param("id"), c.Param("ruleId"))
	if !h.mutate(c, err, actionRuleDelete, resourceRule, c.Param("ruleId"), c.Param("ruleId")) {
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *handler) orderPolicyRules(c *gin.Context) {
	var request servicepolicy.OrderInput
	if !httpx.BindJSON(c, &request, "请选择要处理的数据") {
		return
	}
	values, err := h.Policies.OrderRules(c.Request.Context(), c.Param("id"), request)
	if !h.mutate(c, err, actionRuleOrder, resourcePolicy, c.Param("id"), c.Param("id")) {
		return
	}
	httpx.OK(c, itemsResponse[servicepolicy.Rule]{Items: values})
}

func (h *handler) dryRunPolicy(c *gin.Context) {
	var request servicepolicy.DryRunInput
	if !httpx.BindJSON(c, &request, "测试输入无效") {
		return
	}
	value, err := h.Policies.DryRun(c.Request.Context(), c.Param("id"), request)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func (h *handler) validatePolicy(c *gin.Context) {
	value, err := h.Policies.Validate(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeResourceError(c, err)
		return
	}
	httpx.OK(c, value)
}

func bindPolicyRequest(c *gin.Context) (servicepolicy.PolicyWriteInput, bool) {
	var request policyWriteRequest
	if !httpx.BindJSON(c, &request, "策略信息无效") {
		return servicepolicy.PolicyWriteInput{}, false
	}
	return servicepolicy.PolicyWriteInput{Revision: request.Revision, Name: request.Name, Description: request.Description, Enabled: *request.Enabled, DefaultAction: request.DefaultAction}, true
}

func bindRuleRequest(c *gin.Context) (servicepolicy.RuleWriteInput, bool) {
	var request ruleWriteRequest
	if !httpx.BindJSON(c, &request, "规则信息无效") {
		return servicepolicy.RuleWriteInput{}, false
	}
	return servicepolicy.RuleWriteInput{Revision: request.Revision, Name: request.Name, Enabled: *request.Enabled, Priority: request.Priority, Conditions: request.Conditions, Action: request.Action, NoResolve: request.NoResolve}, true
}
