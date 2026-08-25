package api

import (
	"errors"
	"net/http"
	"testing"

	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/shared"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{"StatusError", &shared.StatusError{Status: 400, Message: "渠道字段不合法"}, 400, "渠道字段不合法"},
		{"record not found", gorm.ErrRecordNotFound, http.StatusNotFound, "记录不存在"},
		{"unique violation", errors.New("UNIQUE constraint failed: channels.name"), http.StatusConflict, "记录已存在"},
		{"raw DB error 不暴露", errors.New("no such table: foo"), http.StatusInternalServerError, "内部错误"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := classifyError(tc.err)
			if status != tc.status || msg != tc.msg {
				t.Errorf("classifyError = (%d, %q), want (%d, %q)", status, msg, tc.status, tc.msg)
			}
		})
	}
}
