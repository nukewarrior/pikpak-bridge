package pikpak

import (
	"errors"
	"fmt"
	"strings"
)

type ErrorKind string

const (
	ErrorKindAuth    ErrorKind = "auth"
	ErrorKindCaptcha ErrorKind = "captcha"
	ErrorKindQuota   ErrorKind = "quota"
	ErrorKindStorage  ErrorKind = "storage"
	ErrorKindNotFound ErrorKind = "not_found"
	ErrorKindAPI     ErrorKind = "api"
)

type Error struct {
	Kind      ErrorKind
	Op        string
	Code      int64
	HTTPStatus int
	Message   string
	VerifyURL string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Message
	if msg == "" {
		msg = string(e.Kind)
	}
	if e.VerifyURL != "" {
		msg += ": " + e.VerifyURL
	}
	if e.Code != 0 {
		return fmt.Sprintf("%s: pikpak %s error %d: %s", e.Op, e.Kind, e.Code, msg)
	}
	return fmt.Sprintf("%s: pikpak %s error: %s", e.Op, e.Kind, msg)
}

func KindOf(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

func classifyAPIError(op string, code int64, httpStatus int, message string) error {
	lower := strings.ToLower(message)
	kind := ErrorKindAPI
	switch {
	case httpStatus == 404 || strings.Contains(lower, "not found") || strings.Contains(lower, "not_found"):
		kind = ErrorKindNotFound
	case code == 4121 || code == 4122 || code == 16 || httpStatus == 401:
		kind = ErrorKindAuth
	case code == 9 || strings.Contains(lower, "captcha"):
		kind = ErrorKindCaptcha
	case strings.Contains(lower, "quota") || strings.Contains(lower, "cloud download") || strings.Contains(lower, "limit exceeded"):
		kind = ErrorKindQuota
	case strings.Contains(lower, "storage") || strings.Contains(lower, "space") || strings.Contains(lower, "capacity"):
		kind = ErrorKindStorage
	}
	return &Error{
		Kind:       kind,
		Op:         op,
		Code:       code,
		HTTPStatus: httpStatus,
		Message:    message,
	}
}
