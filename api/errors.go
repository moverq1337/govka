package api

import (
	"errors"
	"fmt"
)

// Well-known VK API error codes.
const (
	CodeUnknown            = 1
	CodeAppDisabled        = 2
	CodeUnknownMethod      = 3
	CodeAuthFailed         = 5
	CodeTooManyRequests    = 6
	CodePermissionDenied   = 7
	CodeInvalidRequest     = 8
	CodeFlood              = 9
	CodeInternal           = 10
	CodeCaptchaNeeded      = 14
	CodeAccessDenied       = 15
	CodeValidationRequired = 17
	CodeRateLimit          = 29
	CodeInvalidParam       = 100
)

// Sentinel errors matched with errors.Is against *Error by code.
var (
	ErrAuthFailed         = &Error{Code: CodeAuthFailed}
	ErrTooManyRequests    = &Error{Code: CodeTooManyRequests}
	ErrPermissionDenied   = &Error{Code: CodePermissionDenied}
	ErrFlood              = &Error{Code: CodeFlood}
	ErrInternal           = &Error{Code: CodeInternal}
	ErrCaptchaNeeded      = &Error{Code: CodeCaptchaNeeded}
	ErrAccessDenied       = &Error{Code: CodeAccessDenied}
	ErrValidationRequired = &Error{Code: CodeValidationRequired}
	ErrRateLimit          = &Error{Code: CodeRateLimit}
)

// KV is one entry of request_params in an error response.
type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Error is the error object VK returns inside the {"error": ...} envelope.
type Error struct {
	Code          int    `json:"error_code"`
	Msg           string `json:"error_msg"`
	Subcode       int    `json:"error_subcode,omitempty"`
	Text          string `json:"error_text,omitempty"`
	RequestParams []KV   `json:"request_params,omitempty"`

	// Captcha (code 14). RedirectURI is the VK ID captcha page; the legacy
	// CaptchaSID/CaptchaImg pair may still be present.
	CaptchaSID string `json:"captcha_sid,omitempty"`
	CaptchaImg string `json:"captcha_img,omitempty"`
	// RedirectURI is set for captcha (14) and validation (17) errors.
	RedirectURI string `json:"redirect_uri,omitempty"`

	// Method is filled by Client.Call.
	Method string `json:"-"`
}

// Error implements error.
func (e *Error) Error() string {
	if e.Method != "" {
		return fmt.Sprintf("vk api: %s: [%d] %s", e.Method, e.Code, e.Msg)
	}
	return fmt.Sprintf("vk api: [%d] %s", e.Code, e.Msg)
}

// Is makes errors.Is(err, ErrAuthFailed) match by code.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// Retryable reports whether the error is transient and the same call may
// succeed after a short pause.
func (e *Error) Retryable() bool {
	switch e.Code {
	case CodeUnknown, CodeTooManyRequests, CodeInternal:
		return true
	}
	return false
}

// AsError extracts *Error from err.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
