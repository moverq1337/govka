package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// ExecuteError is one entry of execute_errors returned by the execute method.
type ExecuteError struct {
	Method string `json:"method"`
	Code   int    `json:"error_code"`
	Msg    string `json:"error_msg"`
}

// ExecuteErrors is the list of per-call failures inside a successful execute.
type ExecuteErrors []ExecuteError

// Error implements error.
func (e ExecuteErrors) Error() string {
	if len(e) == 0 {
		return "vk api: execute: no errors"
	}
	return fmt.Sprintf("vk api: execute: %d call(s) failed, first: %s [%d] %s", len(e), e[0].Method, e[0].Code, e[0].Msg)
}

// Execute runs VKScript code with the execute method and decodes the result
// into out (may be nil). When some inner calls failed, the returned error is
// ExecuteErrors and out is still filled from the partial response.
func (c *Client) Execute(ctx context.Context, code string, out any) error {
	env, err := c.callEnvelope(ctx, "execute", Params{"code": code})
	if err != nil {
		return err
	}
	if out != nil && env.Response != nil {
		if err := json.Unmarshal(env.Response, out); err != nil {
			return fmt.Errorf("vk api: execute: decode response: %w", err)
		}
	}
	if len(env.ExecuteErrors) > 0 {
		return env.ExecuteErrors
	}
	return nil
}
