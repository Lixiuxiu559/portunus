package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Lixiuxiu559/portunus/backend/configtransfer"
	"github.com/gin-gonic/gin"
)

// RegisterConfigTransfer keeps the existing Register API unchanged and applies
// the same real-peer boundary to every new transfer route.
func RegisterConfigTransfer(r *gin.RouterGroup, s *configtransfer.Service) {
	g := r.Group("/config-transfer")
	g.Use(func(c *gin.Context) {
		c.Header("X-Portunus-Instance", s.InstanceID())
		c.Header("Cache-Control", "no-store")
		c.Next()
	}, localManagementOnly())
	g.GET("/summary", func(c *gin.Context) {
		if !ctNoBody(c) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		d, err := s.Summary(ctx)
		if err != nil {
			ctError(c, err)
			return
		}
		c.JSON(200, d)
	})
	g.Use(func(c *gin.Context) {
		if c.GetHeader("X-Portunus-Instance") != s.InstanceID() {
			ctError(c, configtransfer.NewError("BACKEND_CHANGED", "input"))
			c.Abort()
			return
		}
		if !s.Ready() {
			ctError(c, s.Unavailable())
			c.Abort()
			return
		}
		c.Next()
	})
	heavy := func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()
		ctx, release, err := s.Acquire(ctx)
		if err != nil {
			ctError(c, err)
			c.Abort()
			return
		}
		defer release()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	responseScope := func(c *gin.Context) {
		ctx, finish := s.ResponseScope(c.Request.Context())
		defer finish()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	g.POST("/exports", heavy, func(c *gin.Context) {
		var request struct {
			Password   string `json:"password"`
			AppVersion string `json:"app_version"`
		}
		if !ctSmallJSON(c, &request) {
			return
		}
		password := []byte(request.Password)
		request.Password = ""
		defer clear(password)
		file, counts, err := s.Export(c.Request.Context(), password, request.AppVersion)
		if err != nil {
			ctError(c, err)
			return
		}
		defer clear(file)
		c.Header("Content-Length", strconv.Itoa(len(file)))
		c.Header("X-Portunus-Channels", strconv.Itoa(counts.Channels))
		c.Header("X-Portunus-Models", strconv.Itoa(counts.Models))
		c.Header("X-Portunus-Groups", strconv.Itoa(counts.Groups))
		c.Data(200, "application/octet-stream", file)
	})
	g.POST("/previews", heavy, func(c *gin.Context) {
		password, file, err := ctMultipart(c)
		if err != nil {
			ctError(c, err)
			return
		}
		defer clear(password)
		defer clear(file)
		d, err := s.Preview(c.Request.Context(), file, password)
		if err != nil {
			ctError(c, err)
			return
		}
		c.JSON(200, d)
	})
	g.DELETE("/previews/:preview_id", func(c *gin.Context) {
		if !ctNoBody(c) || !ctPreviewID(c) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		d, err := s.Discard(ctx, c.Param("preview_id"))
		if err != nil {
			ctError(c, err)
			return
		}
		c.JSON(200, gin.H{"operation_id": d.OperationID, "state": d.State})
	})
	g.POST("/operations/:operation_id/commit", responseScope, func(c *gin.Context) {
		if !ctOperationID(c) {
			return
		}
		var request configtransfer.CommitRequest
		if !ctSmallJSON(c, &request) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()
		d, status, err := s.Commit(ctx, c.Param("operation_id"), request)
		if err != nil {
			ctError(c, err)
			return
		}
		ctJSON(c, s, ctx, status, d)
	})
	g.GET("/operations/:operation_id", responseScope, func(c *gin.Context) {
		if !ctNoBody(c) || !ctOperationID(c) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()
		d, status, err := s.Result(ctx, c.Param("operation_id"))
		if err != nil {
			ctError(c, err)
			return
		}
		ctJSON(c, s, ctx, status, d)
	})
	g.POST("/operations/:operation_id/recover", responseScope, func(c *gin.Context) {
		if !ctNoBody(c) || !ctOperationID(c) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
		defer cancel()
		d, status, err := s.Recover(ctx, c.Param("operation_id"))
		if err != nil {
			ctError(c, err)
			return
		}
		ctJSON(c, s, ctx, status, d)
	})
	g.POST("/operations/:operation_id/ack", func(c *gin.Context) {
		if !ctNoBody(c) || !ctOperationID(c) {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if err := s.Ack(ctx, c.Param("operation_id")); err != nil {
			ctError(c, err)
			return
		}
		c.Status(204)
	})
	g.GET("/operations", func(c *gin.Context) {
		if !ctNoBody(c) {
			return
		}
		q := c.Request.URL.Query()
		for key, values := range q {
			if (key != "unacknowledged" && key != "limit" && key != "cursor") || len(values) != 1 {
				ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
				return
			}
		}
		if q.Get("unacknowledged") != "1" {
			ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
			return
		}
		limit := 20
		var err error
		if q.Has("limit") {
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil {
				ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
				return
			}
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		d, err := s.RecoveryList(ctx, q.Get("cursor"), limit)
		if err != nil {
			ctError(c, err)
			return
		}
		c.JSON(200, d)
	})
}
func ctJSON(c *gin.Context, s *configtransfer.Service, ctx context.Context, status int, d any) {
	// Hold the shared heavy slot while encoding potentially large terminal DTOs.
	if _, ok := d.(configtransfer.ResultDTO); ok {
		_, release, err := s.Acquire(ctx)
		if err != nil {
			ctError(c, err)
			return
		}
		defer release()
	}
	c.JSON(status, d)
}
func ctError(c *gin.Context, err error) {
	var e *configtransfer.Error
	if !errors.As(err, &e) {
		e = configtransfer.NewError("TRANSFER_UNAVAILABLE", "input")
	}
	c.JSON(e.Status, e)
}
func ctPreviewID(c *gin.Context) bool {
	if !configtransfer.ValidPreviewID(c.Param("preview_id")) {
		ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
		return false
	}
	return true
}
func ctOperationID(c *gin.Context) bool {
	if !configtransfer.ValidOperationID(c.Param("operation_id")) {
		ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
		return false
	}
	return true
}
func ctRead(c *gin.Context, reader io.Reader, max int64) ([]byte, error) {
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	b, err := io.ReadAll(io.LimitReader(reader, max+1))
	if len(b) > int(max) {
		clear(b)
		return nil, configtransfer.NewError("LIMIT_EXCEEDED", "body_read")
	}
	if err != nil {
		clear(b)
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, configtransfer.NewError("LIMIT_EXCEEDED", "body_read")
		}
		return nil, configtransfer.NewError("INVALID_INPUT", "body_read")
	}
	return b, nil
}
func ctSmallJSON(c *gin.Context, target any) bool {
	if c.ContentType() != "application/json" {
		ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
		return false
	}
	b, err := ctRead(c, c.Request.Body, configtransfer.MaxSmallBodyBytes)
	if err != nil {
		ctError(c, err)
		return false
	}
	defer clear(b)
	if err := configtransfer.DecodeStrict(b, target); err != nil {
		ctError(c, err)
		return false
	}
	return true
}
func ctNoBody(c *gin.Context) bool {
	b, err := ctRead(c, c.Request.Body, 0)
	if err != nil || len(b) != 0 {
		ctError(c, configtransfer.NewError("INVALID_INPUT", "input"))
		return false
	}
	return true
}
func ctMultipart(c *gin.Context) (password, file []byte, err error) {
	if c.Request.ContentLength > configtransfer.MaxMultipartBytes {
		return nil, nil, configtransfer.NewError("LIMIT_EXCEEDED", "body_read")
	}
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer controller.SetReadDeadline(time.Time{})
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, configtransfer.MaxMultipartBytes)
	reader, readErr := c.Request.MultipartReader()
	if readErr != nil {
		return nil, nil, configtransfer.NewError("INVALID_INPUT", "body_read")
	}
	seen := map[string]bool{}
	var allocated [][]byte
	ok := false
	defer func() {
		if !ok {
			for _, b := range allocated {
				clear(b)
			}
		}
	}()
	for {
		part, readErr := reader.NextPart()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, nil, ctBodyError(readErr)
		}
		name := part.FormName()
		if (name != "password" && name != "file") || seen[name] || len(seen) >= 2 {
			return nil, nil, configtransfer.NewError("INVALID_INPUT", "body_read")
		}
		seen[name] = true
		max := int64(configtransfer.MaxFileBytes)
		if name == "password" {
			max = configtransfer.MaxPasswordBytes
		}
		b, readErr := io.ReadAll(io.LimitReader(part, max+1))
		allocated = append(allocated, b)
		if len(b) > int(max) {
			clear(b)
			return nil, nil, configtransfer.NewError("LIMIT_EXCEEDED", "body_read")
		}
		if readErr != nil {
			clear(b)
			return nil, nil, ctBodyError(readErr)
		}
		if name == "password" {
			password = b
		} else {
			file = b
		}
	}
	if len(seen) != 2 || !configtransfer.ValidPassword(password) {
		return nil, nil, configtransfer.NewError("INVALID_INPUT", "input")
	}
	// MultipartReader may stop at the final boundary before reading an epilogue.
	// Drain the remaining bounded raw body so chunked epilogues cannot bypass the
	// total request limit or leave unbounded unread input on a keep-alive socket.
	if _, readErr := io.Copy(io.Discard, c.Request.Body); readErr != nil {
		return nil, nil, ctBodyError(readErr)
	}
	ok = true
	return password, file, nil
}
func ctBodyError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return configtransfer.NewError("LIMIT_EXCEEDED", "body_read")
	}
	return configtransfer.NewError("INVALID_INPUT", "body_read")
}
