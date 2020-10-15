package vfstest

import (
	"os"

	"github.com/clive2000/rclone/vfs"
)

// vfsOs is an implementation of Oser backed by the "vfs" package
type vfsOs struct {
	*vfs.VFS
}

// Stat
func (v vfsOs) Stat(path string) (os.FileInfo, error) {
	return v.VFS.Stat(path)
}

// Check interfaces
var _ Oser = vfsOs{}
