package httpx

// Code 是统一响应信封中的业务状态码：0 表示成功，非零为错误并按域分段。
// 数值是对外契约的一部分，发布后只能追加新码，不能复用或改动已有数值。
//
//	1xxx 通用错误    2xxx 认证与账号    3xxx 资源域    4xxx 面板升级    5xxx sing-box 运行时
type Code int

const (
	CodeOK Code = 0

	// 1xxx 通用
	CodeValidationFailed Code = 1001 // 参数校验失败（原 VALIDATION_FAILED）
	CodeInternalError    Code = 1002 // 内部服务器错误（原 INTERNAL_ERROR / INTERNAL）
	CodeNotFound         Code = 1003 // 资源不存在（原 NOT_FOUND）
	CodeTrafficInvalid   Code = 1004 // 流量查询参数无效（原 TRAFFIC_REQUEST_INVALID）

	// 2xxx 认证与账号
	CodeUnauthorized       Code = 2001 // 未登录或会话失效（原 UNAUTHORIZED）
	CodeInvalidCredentials Code = 2002 // 用户名或密码错误（原 INVALID_CREDENTIALS）
	CodePasswordPolicy     Code = 2003 // 密码不符合要求（原 PASSWORD_POLICY）
	CodeRateLimited        Code = 2004 // 登录尝试过于频繁（原 RATE_LIMITED）
	CodeAlreadyInitialized Code = 2005 // 管理员已初始化（原 ALREADY_INITIALIZED）
	CodeUsernameConflict   Code = 2006 // 用户名已存在（原 USERNAME_CONFLICT）

	// 3xxx 资源域
	CodeRevisionConflict           Code = 3001 // 资源已被修改（原 REVISION_CONFLICT）
	CodeResourceReferenced         Code = 3002 // 资源正在被引用（原 RESOURCE_REFERENCED）
	CodeNameConflict               Code = 3003 // 名称已存在（原 NAME_CONFLICT）
	CodeCredentialRotationRequired Code = 3004 // 凭据需先轮换（原 CREDENTIAL_ROTATION_REQUIRED）

	// 4xxx 面板系统操作（升级 / 备份恢复）
	CodeUpgradeNotConfigured  Code = 4001 // 升级源未配置（config.yaml updates 段缺失）
	CodeUpgradeCheckFailed    Code = 4002 // 查询 GitHub 最新版本失败
	CodeUpgradeBusy           Code = 4003 // 已有升级正在进行
	CodeUpgradeAlreadyLatest  Code = 4004 // 当前已是最新版本
	CodeBackupInvalid         Code = 4005 // 上传的备份文件校验未通过
	CodeWarpRotateUnavailable Code = 4006 // 一键换出口不可用（warp-cli 未部署或 sudoers 未放行）
	CodeWarpRotateFailed      Code = 4007 // 换出口执行失败（重连或复测未通过）

	// 5xxx sing-box 运行时
	CodeRuntimeUnavailable Code = 5001 // 运行时尚未配置（原 SINGBOX_RUNTIME_UNAVAILABLE）
	CodeRuntimeFailed      Code = 5002 // 运行时操作失败（原 SINGBOX_RUNTIME_FAILED）
	CodeSyncFailed         Code = 5003 // 配置同步失败（原 SINGBOX_SYNC_FAILED）
)
