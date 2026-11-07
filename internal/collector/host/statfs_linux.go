//go:build linux

package host

import "syscall"

// statfsOf returns the usage of the file system mounted at path.
func statfsOf(path string) (fsUsage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return fsUsage{}, err
	}
	bsize := uint64(st.Bsize) //nolint:gosec // a block size is never negative
	return fsUsage{
		Total:      st.Blocks * bsize,
		Free:       st.Bfree * bsize,
		Avail:      st.Bavail * bsize,
		Inodes:     st.Files,
		InodesFree: st.Ffree,
	}, nil
}
