package profile

import (
	"errors"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/safefile"
)

func readFile(p string, max int64) ([]byte, error) { return safefile.Read(p, max) }

func asError(err error, target **Error) bool { return errors.As(err, target) }
