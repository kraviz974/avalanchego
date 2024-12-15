// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package tests

import (
	"net"
	"os"
	"syscall"
)

// IsPotentiallyRecoverable determines if an error does not obviously
// represent a permanently fatal condition.
func IsPotentiallyRecoverable(err error) bool {
	return isConnectionRefused(err) ||
		isTimeout(err) ||
		isConnectionReset(err) ||
		isRecoverableOperationError(err)
}

// isConnectionRefused checks if the error is a "connection refused" error
func isConnectionRefused(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if sysErr, ok := opErr.Err.(*os.SyscallError); ok {
			if sysErr.Err == syscall.ECONNREFUSED {
				return true
			}
		}
	}
	return false
}

// isTimeout checks if the error is a timeout error
func isTimeout(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return false
}

// isConnectionReset checks if the error is a "connection reset by peer" error
func isConnectionReset(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if sysErr, ok := opErr.Err.(*os.SyscallError); ok {
			if sysErr.Err == syscall.ECONNRESET {
				return true
			}
		}
	}
	return false
}

func isRecoverableOperationError(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		return opErr.Op == "dial" || opErr.Op == "read" || opErr.Op == "write"
	}
	return false
}
