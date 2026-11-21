package ui

// @module-feature ui

import (
	"fmt"
	"io/fs"
	"syscall"
	"time"
)

// statID names a file's identity: size, mode, mtime, and where the system
// reports them, ctime, inode and device. Any write changes ctime, even one
// that restores the mtime.
func statID(st fs.FileInfo) string {
	id := fmt.Sprintf("%d %o %d", st.Size(), st.Mode(), st.ModTime().UnixNano())
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		id += fmt.Sprintf(" %d %d %d", time.Unix(s.Ctim.Unix()).UnixNano(), uint64(s.Ino), uint64(s.Dev))
	}
	return id
}

// changeTime is the file's ctime, when the system reports one.
func changeTime(st fs.FileInfo) (time.Time, bool) {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return time.Unix(s.Ctim.Unix()), true
	}
	return time.Time{}, false
}
