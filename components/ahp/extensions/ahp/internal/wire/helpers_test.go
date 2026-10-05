package wire_test

import "errors"

func asError[T any](err error, target *T) bool { return errors.As(err, target) }
