//go:build !linux

package host

import "errors"

// statfsOf is only used on Linux, where the mounts are read.
func statfsOf(string) (fsUsage, error) {
	return fsUsage{}, errors.New("not supported on this system")
}
