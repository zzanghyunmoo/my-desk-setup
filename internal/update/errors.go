package update

import "errors"

type ErrorKind string

const (
	ErrorInvalid     ErrorKind = "invalid"
	ErrorStale       ErrorKind = "stale"
	ErrorUnreachable ErrorKind = "unreachable"
	ErrorInternal    ErrorKind = "internal"
)

type classifiedError struct {
	kind  ErrorKind
	cause error
}

func (err *classifiedError) Error() string {
	if err == nil || err.cause == nil {
		return ""
	}
	return err.cause.Error()
}

func (err *classifiedError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func KindOf(err error) ErrorKind {
	if err == nil {
		return ""
	}
	var classified *classifiedError
	if errors.As(err, &classified) {
		return classified.kind
	}
	return ErrorInternal
}

func invalid(err error) error {
	return classify(ErrorInvalid, err)
}

func stale(err error) error {
	return classify(ErrorStale, err)
}

func unreachable(err error) error {
	return classify(ErrorUnreachable, err)
}

func classify(kind ErrorKind, err error) error {
	if err == nil {
		return nil
	}
	var existing *classifiedError
	if errors.As(err, &existing) {
		return err
	}
	return &classifiedError{kind: kind, cause: err}
}
