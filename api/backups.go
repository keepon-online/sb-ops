package api

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/keepon-online/sb-ops/internal/httpx"
	servicebackup "github.com/keepon-online/sb-ops/internal/service/backup"
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

// maxRestoreUploadBytes 限制恢复上传的备份体积（整库快照，个人规模远小于
// 此值；超限在读取阶段即中断）。
const maxRestoreUploadBytes = 256 << 20

// restoreUploadedBackup 接收 multipart 上传的备份快照并恢复：同步校验通过
// 即触发换库重启。响应必须赶在停机 drain 之前发出（照 startUpgrade 先例，
// 返回 200 只代表恢复已受理，面板随后重启）。
func (h *handler) restoreUploadedBackup(c *gin.Context) {
	if h.upgradeBusy() {
		httpx.Error(c, http.StatusConflict, httpx.CodeUpgradeBusy, "面板升级进行中，请等待完成后再恢复", nil)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRestoreUploadBytes)
	file, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.Error(c, http.StatusRequestEntityTooLarge, httpx.CodeValidationFailed, "备份文件超过 256MB 上限", nil)
			return
		}
		httpx.Error(c, http.StatusBadRequest, httpx.CodeValidationFailed, "请以 multipart/form-data 的 file 字段上传备份文件", map[string]string{"file": "缺少文件"})
		return
	}
	source, err := file.Open()
	if err != nil {
		writeInternalError(c, "读取上传的备份失败")
		return
	}
	defer source.Close()
	if err := h.Backup.PrepareUpload(c.Request.Context(), source); err != nil {
		h.audit(c, actionSystemBackupRestore, resourceSystemBackup, uploadResourceID(file.Filename), resultFailure, err)
		writeRestoreError(c, "导入备份失败", err)
		return
	}
	if err := h.Backup.TriggerRestore(); err != nil {
		h.audit(c, actionSystemBackupRestore, resourceSystemBackup, uploadResourceID(file.Filename), resultFailure, err)
		writeRestoreError(c, "触发恢复失败", err)
		return
	}
	h.audit(c, actionSystemBackupRestore, resourceSystemBackup, uploadResourceID(file.Filename), ResultSuccess)
	httpx.OK(c, gin.H{"message": "备份校验通过，面板正在重启并以该备份恢复"})
}

// restoreExistingBackup 用备份列表中已有的快照触发恢复（复制到暂存，列表
// 原件保留），流程与上传恢复一致。
func (h *handler) restoreExistingBackup(c *gin.Context) {
	name := c.Param("name")
	if h.upgradeBusy() {
		httpx.Error(c, http.StatusConflict, httpx.CodeUpgradeBusy, "面板升级进行中，请等待完成后再恢复", nil)
		return
	}
	if err := h.Backup.StageExisting(c.Request.Context(), name); err != nil {
		h.audit(c, actionSystemBackupRestore, resourceSystemBackup, name, resultFailure, err)
		writeRestoreError(c, "准备恢复备份失败", err)
		return
	}
	if err := h.Backup.TriggerRestore(); err != nil {
		h.audit(c, actionSystemBackupRestore, resourceSystemBackup, name, resultFailure, err)
		writeRestoreError(c, "触发恢复失败", err)
		return
	}
	h.audit(c, actionSystemBackupRestore, resourceSystemBackup, name, ResultSuccess)
	httpx.OK(c, gin.H{"message": "备份校验通过，面板正在重启并以该备份恢复"})
}

// upgradeBusy 报告是否有一次面板升级正在进行：升级中触发恢复会让进程在
// 半途重生，两败俱伤。反向（恢复中再升级）由恢复触发即重启天然截断。
func (h *handler) upgradeBusy() bool {
	return h.Upgrade != nil && h.Upgrade.Status().Phase.Active()
}

// uploadResourceID 取客户端文件名做审计 resourceID：只留 basename 并截断，
// 展示用途，服务端实际落盘走固定暂存名。
func uploadResourceID(filename string) string {
	name := filepath.Base(filepath.Clean(filename))
	if name == "." || name == "/" || name == "" {
		return "upload"
	}
	if len(name) > 128 {
		name = name[len(name)-128:]
	}
	return name
}

func writeRestoreError(c *gin.Context, message string, err error) {
	switch {
	case errors.Is(err, servicebackup.ErrInvalidSnapshot):
		httpx.Error(c, http.StatusBadRequest, httpx.CodeBackupInvalid, err.Error(), nil)
	case errors.Is(err, servicebackup.ErrRestoreBusy):
		httpx.Error(c, http.StatusConflict, httpx.CodeUpgradeBusy, "已有恢复操作正在进行", nil)
	case errors.Is(err, servicebackup.ErrRestoreUnsupported):
		writeInternalError(c, "当前环境不支持恢复操作")
	case errors.Is(err, common.ErrNotFound):
		httpx.Error(c, http.StatusNotFound, httpx.CodeNotFound, "备份不存在", nil)
	default:
		writeInternalError(c, message)
	}
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
