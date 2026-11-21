//go:build !linux && !darwin

package ui

// @module-feature ui

import (
	"fmt"
	"io/fs"
	"time"
)

// statID names a file's identity by size, mode and mtime: this system
// reports no ctime or inode through the standard library.
func statID(st fs.FileInfo) string {
	return fmt.Sprintf("%d %o %d", st.Size(), st.Mode(), st.ModTime().UnixNano())
}

// changeTime reports no ctime on this system.
func changeTime(fs.FileInfo) (time.Time, bool) { return time.Time{}, false }
