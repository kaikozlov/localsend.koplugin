package constants

import (
	"errors"
)

var (
	ErrFinished        = errors.New("no file transfer needed")
	ErrInvalidBody     = errors.New("invalid body")
	ErrRejected        = errors.New("Rejected")
	ErrInvalidPIN      = errors.New("invalid PIN")
	ErrBlockedByOthers = errors.New("block by another session")
	ErrUnknown         = errors.New("unknown error")
	ErrTooManyReq      = errors.New("too many request")
	ErrTooManyFiles    = errors.New("too many files in session")
	ErrFileIO          = errors.New("file IO")
	ErrChecksum        = errors.New("sha256 mismatch")
	ErrFingerprint     = errors.New("fingerprint mismatch")
	ErrNotFound        = errors.New("not found")
	ErrTooManySessions = errors.New("maximum concurrent sessions reached")
)

func ParseError(status int) error {
	switch status {
	case 200:
		return nil
	case 204:
		return ErrFinished
	case 400:
		return ErrInvalidBody
	case 401:
		return ErrInvalidPIN
	case 404:
		return ErrNotFound
	case 403:
		return ErrRejected
	case 409:
		return ErrBlockedByOthers
	case 429:
		return ErrTooManyReq
	case 422:
		return ErrChecksum
	default:
		return ErrUnknown
	}
}

func Status(err error) int {
	switch err {
	case nil:
		return 200
	case ErrFinished:
		return 204
	case ErrInvalidBody:
		return 400
	case ErrInvalidPIN:
		return 401
	case ErrRejected:
		return 403
	case ErrNotFound:
		return 404
	case ErrBlockedByOthers:
		return 409
	case ErrTooManyReq:
		return 429
	case ErrChecksum:
		return 422
	default:
		return 500
	}
}
