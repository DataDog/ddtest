package citransport

import (
	"errors"
	"net/url"
	"strings"
)

// sanitizedRequestError retains errors.Is/As without printing the original URL.
// net/http includes URL query values in ordinary, non-debug error messages.
type sanitizedRequestError struct {
	cause   error
	message string
}

// Responses and custom RoundTrippers can echo request credentials. Redact the
// configured key and query values in addition to removing the URL itself.
func (t *Transport) safeError(err error) error {
	if err == nil {
		return nil
	}
	safe := safeRequestError(err)
	message := safe.Error()
	for _, secret := range t.secrets {
		message = strings.ReplaceAll(message, secret, "[redacted]")
	}
	if message == safe.Error() {
		return safe
	}
	return sanitizedRequestError{cause: err, message: message}
}

func (e sanitizedRequestError) Error() string { return e.message }
func (e sanitizedRequestError) Unwrap() error { return e.cause }

func safeRequestError(err error) error {
	var request *url.Error
	if !errors.As(err, &request) {
		return err
	}
	copy := *request
	endpoint, parseErr := url.Parse(copy.URL)
	if parseErr != nil {
		copy.URL = "[redacted endpoint]"
	} else {
		endpoint.User, endpoint.RawQuery, endpoint.Fragment, endpoint.RawFragment = nil, "", "", ""
		endpoint.ForceQuery = false
		copy.URL = endpoint.String()
	}
	if _, nested := copy.Err.(*url.Error); nested {
		copy.Err = safeRequestError(copy.Err)
	}
	return sanitizedRequestError{cause: err, message: copy.Error()}
}
