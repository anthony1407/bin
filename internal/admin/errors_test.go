package admin

import "errors"

func asAPIError(err error, target any) bool { return errors.As(err, target) }
