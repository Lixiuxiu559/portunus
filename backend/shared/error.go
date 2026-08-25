package shared

// StatusError 是携带 HTTP 状态码的领域错误，供 api 层直接映射，避免把 DB 报错暴露给客户端。
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return e.Message }
