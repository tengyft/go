// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package os

import (
	"syscall"
	"time"
)

// Getpagesize returns the underlying system's memory page size.
func Getpagesize() int { return syscall.Getpagesize() }

// File represents an open file descriptor.
type File struct {
	*file // os specific
}

// A FileInfo describes a file and is returned by Stat and Lstat.
type FileInfo interface {
	Name() string       // base name of the file
	Size() int64        // length in bytes for regular files; system-dependent for others
	Mode() FileMode     // file mode bits
	ModTime() time.Time // modification time
	IsDir() bool        // abbreviation for Mode().IsDir()
	Sys() interface{}   // underlying data source (can return nil)
}

// A FileMode represents a file's mode and permission bits.
// The bits have the same definition on all systems, so that
// information about files can be moved from one system
// to another portably. Not all bits apply to all systems.
// The only required bit is ModeDir for directories.
type FileMode uint32

// The defined file mode bits are the most significant bits of the FileMode.
// The nine least-significant bits are the standard Unix rwxrwxrwx permissions.
// The values of these bits should be considered part of the public API and
// may be used in wire protocols or disk representations: they must not be
// changed, although new bits might be added.
const (
	// The single letters are the abbreviations
	// used by the String method's formatting.
	ModeDir        FileMode = 1 << (32 - 1 - iota) // d: is a directory  0x80000000
	ModeAppend                                     // a: append-only     0x40000000
	ModeExclusive                                  // l: exclusive use   0x20000000
	ModeTemporary                                  // T: temporary file (not backed up)  0x10000000
	ModeSymlink                                    // L: symbolic link       0x08000000
	ModeDevice                                     // D: device file         0x04000000
	ModeNamedPipe                                  // p: named pipe (FIFO)   0x02000000
	ModeSocket                                     // S: Unix domain socket  0x01000000
	ModeSetuid                                     // u: setuid              0x00800000
	ModeSetgid                                     // g: setgid              0x00400000
	ModeCharDevice                                 // c: Unix character device, when ModeDevice is set  0x00200000
	ModeSticky                                     // t: sticky              0x00100000

	// Mask for the type bits. For regular files, none will be set.
	ModeType = ModeDir | ModeSymlink | ModeNamedPipe | ModeSocket | ModeDevice

	ModePerm FileMode = 0777 // Unix permission bits
)

func (m FileMode) String() string {
	const str = "dalTLDpSugct"
	var buf [32]byte // Mode is uint32.
	w := 0
	// 检测除Unix权限之外的其它权限
	for i, c := range str {
		if m&(1<<uint(32-1-i)) != 0 {
			buf[w] = byte(c)
			w++
		}
	}
	if w == 0 {
		buf[w] = '-'
		w++
	}
	// 检测Unix权限
	const rwx = "rwxrwxrwx"
	for i, c := range rwx {
		if m&(1<<uint(9-1-i)) != 0 {
			buf[w] = byte(c)
		} else {
			buf[w] = '-'
		}
		w++
	}
	return string(buf[:w])
}

// IsDir reports whether m describes a directory.
// That is, it tests for the ModeDir bit being set in m.
// IsDir用于检测m是否打开了ModeDir，也就是说，有没有目录权限
func (m FileMode) IsDir() bool {
	return m&ModeDir != 0
}

// IsRegular reports whether m describes a regular file.
// That is, it tests that no mode type bits are set.
// IsRegular用于检测m是否是普通文件权限
func (m FileMode) IsRegular() bool {
	return m&ModeType == 0
}

// Perm returns the Unix permission bits in m.
// Perm返回Unix权限
func (m FileMode) Perm() FileMode {
	return m & ModePerm
}

func (fs *fileStat) Name() string { return fs.name }
func (fs *fileStat) IsDir() bool  { return fs.Mode().IsDir() }

// SameFile reports whether fi1 and fi2 describe the same file.
// For example, on Unix this means that the device and inode fields
// of the two underlying structures are identical; on other systems
// the decision may be based on the path names.
// SameFile only applies to results returned by this package's Stat.
// It returns false in other cases.
// SameFile返回fi1和fi2是否描述的同一个文件。
// 例如：在类Unix系统中，这意味着这两个文件的底层数据结构中的device域和inode域完全一样；而在其他系统中
// 这两个的文件的比较可能基于路径名。
// SameFile仅能用于本包中的Stat函数返回的结果上，在其他情况中使用SameFile会返回错误。
func SameFile(fi1, fi2 FileInfo) bool {
	fs1, ok1 := fi1.(*fileStat)
	fs2, ok2 := fi2.(*fileStat)
	if !ok1 || !ok2 {
		return false
	}
	return sameFile(fs1, fs2)
}
