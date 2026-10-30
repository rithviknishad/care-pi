// Package exitcode maps errors to the exit codes of the contract.
package exitcode

import "errors"

// Exit codes.
const (
	OK          = 0
	Failed      = 1
	BadInput    = 2
	HostMissing = 3
)

// Error carries an exit code.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// With wraps err with code. It returns nil for a nil err.
func With(code int, err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return err
	}
	return &Error{Code: code, Err: err}
}

// Code returns the exit code for err.
func Code(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Failed
}
