// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package strings

import (
	"errors"
	"io"
	"unicode/utf8"
)

// A Reader implements the io.Reader, io.ReaderAt, io.Seeker, io.WriterTo,
// io.ByteScanner, and io.RuneScanner interfaces by reading
// from a string.
// *Reader实现了如下接口:
// (1) io.Reader
// (2) io.ReaderAt
// (3) io.Seeker
// (4) io.WriterTo
// (5) io.ByteScanner
// (6) io.RuneScanner
// Reader用于从一个string中读取内容。
type Reader struct {
	s        string
	i        int64 // current reading index i指示的字节还未读
	prevRune int   // index of previous rune; or < 0 读取的上一次utf8字符在字符串中的索引
}

// Len returns the number of bytes of the unread portion of the
// string.
// Len用于返回r.s中还未读的字节数。
func (r *Reader) Len() int {
	if r.i >= int64(len(r.s)) {
		return 0 // 已经读完
	}
	return int(int64(len(r.s)) - r.i) // 返回未读的字节数
}

// Size returns the original length of the underlying string.
// Size is the number of bytes available for reading via ReadAt.
// The returned value is always the same and is not affected by calls
// to any other method.
// Size返回底层字符串的字节长度。
func (r *Reader) Size() int64 { return int64(len(r.s)) }

func (r *Reader) Read(b []byte) (n int, err error) {
	if r.i >= int64(len(r.s)) {
		return 0, io.EOF
	}
	r.prevRune = -1
	n = copy(b, r.s[r.i:])
	r.i += int64(n)
	return
}

// off的范围为: [0, len(r.s))。
func (r *Reader) ReadAt(b []byte, off int64) (n int, err error) {
	// cannot modify state - see io.ReaderAt
	if off < 0 {
		return 0, errors.New("strings.Reader.ReadAt: negative offset")
	}
	// off范围太大
	if off >= int64(len(r.s)) {
		return 0, io.EOF
	}
	n = copy(b, r.s[off:])
	// 未读的字节数不足以填满b，所以会读完。
	if n < len(b) {
		err = io.EOF
	}
	return
}

func (r *Reader) ReadByte() (byte, error) {
	r.prevRune = -1
	// 没有可读的内容，已经读完。
	if r.i >= int64(len(r.s)) {
		return 0, io.EOF
	}
	b := r.s[r.i]
	r.i++
	return b, nil
}

func (r *Reader) UnreadByte() error {
	r.prevRune = -1
	// 读位置为0，不允许UnreadByte。
	if r.i <= 0 {
		return errors.New("strings.Reader.UnreadByte: at beginning of string")
	}
	r.i--
	return nil
}

// 读取一个utf8字符
func (r *Reader) ReadRune() (ch rune, size int, err error) {
	// 没有可读的内容，已经读完。
	if r.i >= int64(len(r.s)) {
		r.prevRune = -1
		return 0, 0, io.EOF
	}
	r.prevRune = int(r.i)
	if c := r.s[r.i]; c < utf8.RuneSelf {
		r.i++
		return rune(c), 1, nil
	}
	ch, size = utf8.DecodeRuneInString(r.s[r.i:])
	r.i += int64(size)
	return
}

// UnreadRune会Unread一个utf8字符。
func (r *Reader) UnreadRune() error {
	if r.prevRune < 0 {
		return errors.New("strings.Reader.UnreadRune: previous operation was not ReadRune")
	}
	r.i = int64(r.prevRune)
	r.prevRune = -1
	return nil
}

// Seek implements the io.Seeker interface.
// Seek定位r.i，这可能导致r.i >= len(r.s)，所以Read相关方法需要做一些检测。
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	r.prevRune = -1
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.i + offset
	case io.SeekEnd:
		abs = int64(len(r.s)) + offset
	default:
		return 0, errors.New("strings.Reader.Seek: invalid whence")
	}
	if abs < 0 {
		return 0, errors.New("strings.Reader.Seek: negative position")
	}
	r.i = abs
	return abs, nil
}

// WriteTo implements the io.WriterTo interface.
func (r *Reader) WriteTo(w io.Writer) (n int64, err error) {
	r.prevRune = -1
	// r中没有可读的内容，已经读完。
	if r.i >= int64(len(r.s)) {
		return 0, nil
	}
	// s为未读的字节构成的字符串。
	s := r.s[r.i:]
	m, err := io.WriteString(w, s)
	if m > len(s) {
		panic("strings.Reader.WriteTo: invalid WriteString count")
	}
	r.i += int64(m) // 更新读位置
	n = int64(m)
	if m != len(s) && err == nil {
		err = io.ErrShortWrite
	}
	return
}

// Reset resets the Reader to be reading from s.
// Reset重置底层的r.s。
func (r *Reader) Reset(s string) { *r = Reader{s, 0, -1} }

// NewReader returns a new Reader reading from s.
// It is similar to bytes.NewBufferString but more efficient and read-only.
// NewReader返回一个*Reader，其底层字符串为输入的参数s。
// 这个函数与bytes.NewBufferString类似，但是NewReader更高效、并且*Reader是只读的。
func NewReader(s string) *Reader { return &Reader{s, 0, -1} }
