package analysisclient

// errors.go 存放错误辅助函数（类型定义在 types.go，重试判定在此扩展）。

import "errors"

// IsNotFound 判断是否为 404。
func IsNotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 404
	}
	return false
}
