package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	common "github.com/keepon-online/sb-ops/internal/service/common"
)

// 备份操作不改变内核配置，绝不能走 h.mutate（成功收尾会全量重编译并重启
// 代理内核）——照 upgrade / 额度调整先例直接 h.audit + httpx 响应。

// listBackups 返回 <data_dir>/backups/ 下的备份清单（只读不审计）。
func (h *handler) listBackups(c *gin.Context) {
	values, err := h.Backup.List()
	if err != nil {
		writeInternalError(c, "查询备份列表失败")
		return
	}
	httpx.OK(c, gin.H{"items": values})
}

// createBackup 在线生成数据库快照（VACUUM INTO，WAL 安全）。
func (h *handler) createBackup(c *gin.Context) {
	value, err := h.Backup.Create(c.Request.Context())
	if err != nil {
		h.audit(c, actionSystemBackupCreate, resourceSystemBackup, "", resultFailure, err)
		writeBackupError(c, "创建数据库备份失败", err)
		return
	}
	h.audit(c, actionSystemBackupCreate, resourceSystemBackup, value.Name, ResultSuccess)
	httpx.Created(c, value)
}

// deleteBackup 删除指定备份文件。
func (h *handler) deleteBackup(c *gin.Context) {
	name := c.Param("name")
	if err := h.Backup.Delete(name); err != nil {
		h.audit(c, actionSystemBackupDelete, resourceSystemBackup, name, resultFailure, err)
		writeBackupError(c, "删除备份失败", err)
		return
	}
	h.audit(c, actionSystemBackupDelete, resourceSystemBackup, name, ResultSuccess)
	c.Status(http.StatusNoContent)
}

// downloadBackup 流式下发备份文件（二进制产物，信封豁免，照订阅下载先例）。
// 备份含凭据主密钥，属敏感数据外发，下载动作记审计。
func (h *handler) downloadBackup(c *gin.Context) {
	name := c.Param("name")
	path, err := h.Backup.Path(name)
	if err != nil {
		h.audit(c, actionSystemBackupDownload, resourceSystemBackup, name, resultFailure, err)
		writeBackupError(c, "下载备份失败", err)
		return
	}
	h.audit(c, actionSystemBackupDownload, resourceSystemBackup, name, ResultSuccess)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Header("Content-Type", "application/octet-stream")
	c.File(path)
}

func writeBackupError(c *gin.Context, message string, err error) {
	switch {
	case errors.Is(err, common.ErrDuplicate):
		httpx.Error(c, http.StatusConflict, httpx.CodeNameConflict, "同一秒内已有备份，请稍候重试", map[string]string{"name": "已存在"})
	case errors.Is(err, common.ErrNotFound):
		httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "备份不存在", nil)
	default:
		writeInternalError(c, message)
	}
}
