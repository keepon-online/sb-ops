package identity

import "errors"

var (
	ErrAlreadyInitialized   = errors.New("身份系统已初始化")
	ErrInvalidCredentials   = errors.New("用户名或密码错误")
	ErrUnauthorized         = errors.New("未授权")
	ErrPasswordPolicy       = errors.New("密码不符合安全策略")
	ErrRateLimited          = errors.New("登录尝试过于频繁")
	ErrRevisionConflict     = errors.New("资源已被修改")
	ErrBootstrapAdminAbsent = errors.New("身份系统已初始化但缺少管理员")
	ErrInvalidAccountInput  = errors.New("账号输入无效")
)
