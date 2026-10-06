package credfile

import (
	"context"
	"errors"
)

func isContextErr(err error) bool { return errors.Is(err, context.Canceled) }
