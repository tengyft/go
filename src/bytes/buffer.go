// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bytes

// Simple byte buffer for marshaling data.

import (
	"errors"
	"io"
	"unicode/utf8"
)

// A Buffer is a variable-sized buffer of bytes with Read and Write methods.
// The zero value for Buffer is an empty buffer ready to use.
// Buffer是一个大小可变的字节Buffer，这个Buffer实现了读写方法。
// Buffer的零值是一个准备好的、可使用的空Buffer。
type Buffer struct {
	buf      []byte // contents are the bytes buf[off : len(buf)]
	off      int    // read at &buf[off], write at &buf[len(buf)]
	lastRead readOp // last read operation, so that Unread* can work correctly.
	// FIXME: lastRead can fit in a single byte

	// memory to hold first slice; helps small buffers avoid allocation.
	// FIXME: it would be advisable to align Buffer to cachelines to avoid false
	// sharing.
	bootstrap [64]byte
}

// The readOp constants describe the last action performed on
// the buffer, so that UnreadRune and UnreadByte can check for
// invalid usage. opReadRuneX constants are chosen such that
// converted to int they correspond to the rune size that was read.
type readOp int

const (
	opRead      readOp = -1 // Any other read operation.
	opInvalid          = 0  // Non-read operation.
	opReadRune1        = 1  // Read rune of size 1.
	opReadRune2        = 2  // Read rune of size 2.
	opReadRune3        = 3  // Read rune of size 3.
	opReadRune4        = 4  // Read rune of size 4.
)

// ErrTooLarge is passed to panic if memory cannot be allocated to store data in a buffer.
var ErrTooLarge = errors.New("bytes.Buffer: too large")

// Bytes returns a slice of length b.Len() holding the unread portion of the buffer.
// The slice is valid for use only until the next buffer modification (that is,
// only until the next call to a method like Read, Write, Reset, or Truncate).
// The slice aliases the buffer content at least until the next buffer modification,
// so immediate changes to the slice will affect the result of future reads.
// Bytes返回Buffer未读的slice，这个slice长度为b.Len()。
// 在对此Buffer的修改之前，这个返回的slice都是可用的(因为调用一些方法可能修改Buffer底层的数据结构)。
// 也就是说，Bytes返回的slice是有时效性的。
func (b *Buffer) Bytes() []byte { return b.buf[b.off:] }

// String returns the contents of the unread portion of the buffer
// as a string. If the Buffer is a nil pointer, it returns "<nil>".
// String返回未读内容的string形式。
func (b *Buffer) String() string {
	if b == nil {
		// Special case, useful in debugging.
		return "<nil>"
	}
	return string(b.buf[b.off:])
}

// Len returns the number of bytes of the unread portion of the buffer;
// b.Len() == len(b.Bytes()).
// Len返回Buffer中未读的内容长度(以字节来计算)。
func (b *Buffer) Len() int { return len(b.buf) - b.off }

// Cap returns the capacity of the buffer's underlying byte slice, that is, the
// total space allocated for the buffer's data.
// Cap返回Buffer底层的存储空间大小(已经分配好的)。
func (b *Buffer) Cap() int { return cap(b.buf) }

// Truncate discards all but the first n unread bytes from the buffer
// but continues to use the same allocated storage.
// It panics if n is negative or greater than the length of the buffer.
// Truncate只保留前n个未读字节。
// Buffer仍然保留使用相同的底层存储空间。
func (b *Buffer) Truncate(n int) {
	if n == 0 {
		b.Reset()
		return
	}
	b.lastRead = opInvalid
	if n < 0 || n > b.Len() {
		panic("bytes.Buffer: truncation out of range")
	}
	b.buf = b.buf[0 : b.off+n] // 将[b.off+n, len(b.buf))的未读字节扔掉。
}

// Reset resets the buffer to be empty,
// but it retains the underlying storage for use by future writes.
// Reset is the same as Truncate(0).
func (b *Buffer) Reset() {
	b.buf = b.buf[:0]
	b.off = 0
	b.lastRead = opInvalid
}

// tryGrowByReslice is a inlineable version of grow for the fast-case where the
// internal buffer only needs to be resliced.
// It returns the index where bytes should be written and whether it succeeded.
func (b *Buffer) tryGrowByReslice(n int) (int, bool) {
	if l := len(b.buf); l+n <= cap(b.buf) {
		b.buf = b.buf[:l+n]
		return l, true
	}
	return 0, false
}

// grow grows the buffer to guarantee space for n more bytes.
// It returns the index where bytes should be written.
// If the buffer can't grow it will panic with ErrTooLarge.
func (b *Buffer) grow(n int) int {
	m := b.Len()
	// If buffer is empty, reset to recover space.
	if m == 0 && b.off != 0 {
		b.Reset()
	}
	// Try to grow by means of a reslice.
	if i, ok := b.tryGrowByReslice(n); ok {
		return i
	}
	// Check if we can make use of bootstrap array.
	if b.buf == nil && n <= len(b.bootstrap) {
		b.buf = b.bootstrap[:n]
		return 0
	}
	if m+n <= cap(b.buf)/2 {
		// We can slide things down instead of allocating a new
		// slice. We only need m+n <= cap(b.buf) to slide, but
		// we instead let capacity get twice as large so we
		// don't spend all our time copying.
		copy(b.buf[:], b.buf[b.off:])
	} else {
		// Not enough space anywhere, we need to allocate.
		buf := makeSlice(2*cap(b.buf) + n)
		copy(buf, b.buf[b.off:])
		b.buf = buf
	}
	// Restore b.off and len(b.buf).
	b.off = 0
	b.buf = b.buf[:m+n]
	return m
}

// Grow grows the buffer's capacity, if necessary, to guarantee space for
// another n bytes. After Grow(n), at least n bytes can be written to the
// buffer without another allocation.
// If n is negative, Grow will panic.
// If the buffer can't grow it will panic with ErrTooLarge.
// Grow增加了Buffer底层存储空间，保证至少可以再读入n个字节而不需要重要分配存储空间。
func (b *Buffer) Grow(n int) {
	if n < 0 {
		panic("bytes.Buffer.Grow: negative count")
	}
	m := b.grow(n)
	b.buf = b.buf[0:m]
}

// Write appends the contents of p to the buffer, growing the buffer as
// needed. The return value n is the length of p; err is always nil. If the
// buffer becomes too large, Write will panic with ErrTooLarge.
// Write将p代表的字节数组append到Buffer底层存储空间中。并且在需要的时候扩展底层存储空间。
func (b *Buffer) Write(p []byte) (n int, err error) {
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(len(p))
	if !ok {
		m = b.grow(len(p))
	}
	return copy(b.buf[m:], p), nil
}

// WriteString appends the contents of s to the buffer, growing the buffer as
// needed. The return value n is the length of s; err is always nil. If the
// buffer becomes too large, WriteString will panic with ErrTooLarge.
// WriteString将字符串s append到Buffer底层的存储空间中，并在需要的时候扩展底层存储空间。
func (b *Buffer) WriteString(s string) (n int, err error) {
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(len(s))
	if !ok {
		m = b.grow(len(s))
	}
	return copy(b.buf[m:], s), nil
}

// MinRead is the minimum slice size passed to a Read call by
// Buffer.ReadFrom. As long as the Buffer has at least MinRead bytes beyond
// what is required to hold the contents of r, ReadFrom will not grow the
// underlying buffer.
// MinRead是传递给Buffer.ReadFrom调用中读缓存的最小大小。
// 只要Buffer底层存储空间至少有MinRead个字节，并且大于要读入的内容的字节数，ReadFrom就不会
// 增加底层的存储空间。
const MinRead = 512

// ReadFrom reads data from r until EOF and appends it to the buffer, growing
// the buffer as needed. The return value n is the number of bytes read. Any
// error except io.EOF encountered during the read is also returned. If the
// buffer becomes too large, ReadFrom will panic with ErrTooLarge.
// ReadFrom从r中读取内容直到遇到EOF，并且将读到的内容append到Buffer中的底层存储空间中，
// Buffer底层存储可以在需要的时候自动增加。返回值n是读入的字节数。ReadFrom返回
// 遇到的所有错误(除了EOF)。如果Buffer底层存储空间太大，则ReadFrom将会以ErrTooLarge进行panic。
func (b *Buffer) ReadFrom(r io.Reader) (n int64, err error) {
	b.lastRead = opInvalid
	// If buffer is empty, reset to recover space.
	if b.off >= len(b.buf) {
		b.Reset()
	}
	for {
		if free := cap(b.buf) - len(b.buf); free < MinRead {
			// not enough space at end
			// Buffer底层存储空间的可写缓存不足MinRead个字节
			newBuf := b.buf
			if b.off+free < MinRead {
				// not enough space using beginning of buffer;
				// double buffer capacity
				// Buffer底层存储空间中有三部分存储空间：
				// 1. 已经读完但未释放的空间;
				// 2. 可读的存储空间即Buffer中所存储的内容的空间;
				// 3. 可写的存储空间。
				// 这里第一部分和第三部分存储空间加在一起也不足MinRead个字节，则重新分配一个大的底层存储空间。
				newBuf = makeSlice(2*cap(b.buf) + MinRead)
			}
			copy(newBuf, b.buf[b.off:])       // 到这里Buffer底层存储空间已经足够大，可以容纳MinRead个字节和Buffer原来的未读的内容。
			b.buf = newBuf[:len(b.buf)-b.off] // len(b.buf) - b.off表示Buffer中原来还未读的内容的字节数。
			b.off = 0                         // 更新新的可读位置
		}
		m, e := r.Read(b.buf[len(b.buf):cap(b.buf)]) // 从r中读取最多cap(b.buf)-len(b.buf)个字节，这个总数是大于MinRead的
		b.buf = b.buf[0 : len(b.buf)+m]              // 从r中成功读取m个字节
		n += int64(m)                                // 更新成功读取的字节数
		if e == io.EOF {                             // 已经从r中读完
			break
		}
		if e != nil { // 从r中读遇到错误，返回已经成功读取的字节数和遇到的错误
			return n, e
		}
	}
	return n, nil // err is EOF, so return nil explicitly
}

// makeSlice allocates a slice of size n. If the allocation fails, it panics
// with ErrTooLarge.
func makeSlice(n int) []byte {
	// If the make fails, give a known error.
	defer func() {
		if recover() != nil {
			panic(ErrTooLarge)
		}
	}()
	return make([]byte, n)
}

// WriteTo writes data to w until the buffer is drained or an error occurs.
// The return value n is the number of bytes written; it always fits into an
// int, but it is int64 to match the io.WriterTo interface. Any error
// encountered during the write is also returned.
// WriteTo将Buffer中的内容写入到w中，写入过程持续到Buffer的内容写完或遇到错误
// 返回的n是成功写入w的字节数。写入w的过程中遇到的任何错误都会被返回。
func (b *Buffer) WriteTo(w io.Writer) (n int64, err error) {
	b.lastRead = opInvalid
	if b.off < len(b.buf) {
		// Buffer底层存储空间中有内容可读。
		nBytes := b.Len()
		m, e := w.Write(b.buf[b.off:])
		if m > nBytes {
			panic("bytes.Buffer.WriteTo: invalid Write count")
		}
		b.off += m
		n = int64(m)
		if e != nil {
			return n, e
		}
		// all bytes should have been written, by definition of
		// Write method in io.Writer
		if m != nBytes {
			return n, io.ErrShortWrite
		}
	}
	// Buffer is now empty; reset.
	b.Reset()
	return
}

// WriteByte appends the byte c to the buffer, growing the buffer as needed.
// The returned error is always nil, but is included to match bufio.Writer's
// WriteByte. If the buffer becomes too large, WriteByte will panic with
// ErrTooLarge.
// WriteByte将字节c append到Buffer底层的存储空间中，并在需要的时候扩展底层存储空间。
func (b *Buffer) WriteByte(c byte) error {
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(1)
	if !ok {
		m = b.grow(1)
	}
	b.buf[m] = c
	return nil
}

// WriteRune appends the UTF-8 encoding of Unicode code point r to the
// buffer, returning its length and an error, which is always nil but is
// included to match bufio.Writer's WriteRune. The buffer is grown as needed;
// if it becomes too large, WriteRune will panic with ErrTooLarge.
// WriteRune将rune r append到Buffer底层的存储空间中，并在需要的时候扩展底层存储空间。
// r是UTF-8编码的。WriteRune返回r的长度和遇到的错误(在此总是nil)。
func (b *Buffer) WriteRune(r rune) (n int, err error) {
	if r < utf8.RuneSelf {
		// r是一个字节的字符
		b.WriteByte(byte(r))
		return 1, nil
	}
	b.lastRead = opInvalid
	m, ok := b.tryGrowByReslice(utf8.UTFMax)
	if !ok {
		m = b.grow(utf8.UTFMax)
	}
	n = utf8.EncodeRune(b.buf[m:m+utf8.UTFMax], r)
	b.buf = b.buf[:m+n]
	return n, nil
}

// Read reads the next len(p) bytes from the buffer or until the buffer
// is drained. The return value n is the number of bytes read. If the
// buffer has no data to return, err is io.EOF (unless len(p) is zero);
// otherwise it is nil.
// Read从Buffer中读出至多len(p)个字节。返回的错误只有io.EOF和nil。
func (b *Buffer) Read(p []byte) (n int, err error) {
	b.lastRead = opInvalid
	if b.off >= len(b.buf) {
		// Buffer is empty, reset to recover space.
		b.Reset()
		if len(p) == 0 {
			return
		}
		return 0, io.EOF
	}
	n = copy(p, b.buf[b.off:])
	b.off += n
	if n > 0 {
		b.lastRead = opRead
	}
	return
}

// Next returns a slice containing the next n bytes from the buffer,
// advancing the buffer as if the bytes had been returned by Read.
// If there are fewer than n bytes in the buffer, Next returns the entire buffer.
// The slice is only valid until the next call to a read or write method.
// Next返回一个slice，这个slice包含了Buffer中下一次要读取的n个字节。
// Next将把Buffer向前推进n个字节，就像使用Read从Buffer读取了n个字节。
// 如果Buffer中不足n个可读字节，Next将返回整个可读的内容。
// 需要注意的是，返回的slice与Buffer共享底层存储空间，所以，当Buffer底层存储空间被修改了之后，返回的slice就变得不可信。
func (b *Buffer) Next(n int) []byte {
	b.lastRead = opInvalid
	m := b.Len()
	if n > m {
		n = m
	}
	data := b.buf[b.off : b.off+n]
	b.off += n
	if n > 0 {
		b.lastRead = opRead
	}
	return data
}

// ReadByte reads and returns the next byte from the buffer.
// If no byte is available, it returns error io.EOF.
// ReadByte从Buffer中读取一个字节，如果Buffer中没有可读内容，Buffer会重新使用整个底层存储空间，并返回io.EOF。
func (b *Buffer) ReadByte() (byte, error) {
	b.lastRead = opInvalid
	if b.off >= len(b.buf) {
		// Buffer is empty, reset to recover space.
		b.Reset()
		return 0, io.EOF
	}
	c := b.buf[b.off]
	b.off++
	b.lastRead = opRead
	return c, nil
}

// ReadRune reads and returns the next UTF-8-encoded
// Unicode code point from the buffer.
// If no bytes are available, the error returned is io.EOF.
// If the bytes are an erroneous UTF-8 encoding, it
// consumes one byte and returns U+FFFD, 1.
// ReadRune从Buffer中读取下一个可读的UTF-8编码的字符。
// 如果Buffer中没有可读内容，则返回io.EOF错误。
// 如果Buffer中下一个可读字符是错误的UTF-8编码，ReadRune将消耗一个字节，并返回U+FFFD, 1。
func (b *Buffer) ReadRune() (r rune, size int, err error) {
	b.lastRead = opInvalid
	if b.off >= len(b.buf) {
		// Buffer is empty, reset to recover space.
		b.Reset()
		return 0, 0, io.EOF
	}
	c := b.buf[b.off]
	if c < utf8.RuneSelf {
		// 单字节字符
		b.off++
		b.lastRead = opReadRune1
		return rune(c), 1, nil
	}
	r, n := utf8.DecodeRune(b.buf[b.off:])
	b.off += n
	b.lastRead = readOp(n)
	return r, n, nil
}

// UnreadRune unreads the last rune returned by ReadRune.
// If the most recent read or write operation on the buffer was
// not a successful ReadRune, UnreadRune returns an error.  (In this regard
// it is stricter than UnreadByte, which will unread the last byte
// from any read operation.)
// UnreadRune unread上一次通过ReadRune方法读取的rune。
// 如果Buffer最近的读、写操作不是ReadRune，则UnreadRune返回错误(在这点上，
// UnreadRune与UnreadByte要严格，因为UnreadByte总会unread上一次读的一个字节)。
func (b *Buffer) UnreadRune() error {
	if b.lastRead <= opInvalid {
		return errors.New("bytes.Buffer: UnreadRune: previous operation was not a successful ReadRune")
	}
	if b.off >= int(b.lastRead) {
		// 保证可以UnreadRune
		b.off -= int(b.lastRead)
	}
	b.lastRead = opInvalid
	return nil
}

// UnreadByte unreads the last byte returned by the most recent successful
// read operation that read at least one byte. If a write has happened since
// the last read, if the last read returned an error, or if the read read zero
// bytes, UnreadByte returns an error.
func (b *Buffer) UnreadByte() error {
	if b.lastRead == opInvalid {
		return errors.New("bytes.Buffer: UnreadByte: previous operation was not a successful read")
	}
	b.lastRead = opInvalid
	if b.off > 0 {
		b.off--
	}
	return nil
}

// ReadBytes reads until the first occurrence of delim in the input,
// returning a slice containing the data up to and including the delimiter.
// If ReadBytes encounters an error before finding a delimiter,
// it returns the data read before the error and the error itself (often io.EOF).
// ReadBytes returns err != nil if and only if the returned data does not end in
// delim.
// ReadBytes会从Buffer读缓存中一直读，直到遇到delim。
// ReadBytes返回成功读取的字节，并且包括delim。
// 如果在找到delim之前，ReadBytes遇到错误，那么ReadBytes将返回读取到的数据和遇到的错误。
// 当且仅当ReadBytes从Buffer中读取数据，没有找到delim时，ReadBytes才返回错误。
// 需要注意的是ReadBytes返回的数据与Buffer底层存储空间不共享，所以这个数据是可以随意修改而不影响Buffer的。
func (b *Buffer) ReadBytes(delim byte) (line []byte, err error) {
	slice, err := b.readSlice(delim)
	// return a copy of slice. The buffer's backing array may
	// be overwritten by later calls.
	line = append(line, slice...)
	return
}

// readSlice is like ReadBytes but returns a reference to internal buffer data.
// readSlice与ReadBytes功能类似，只是readSlice返回的line与Buffer底层存储空间是共享存储空间的。
func (b *Buffer) readSlice(delim byte) (line []byte, err error) {
	i := IndexByte(b.buf[b.off:], delim)
	end := b.off + i + 1 // 如果i != -1，则end指示了delim在Buffer底层存储空间中的位置+1。
	if i < 0 {
		// Buffer中没有delim。
		end = len(b.buf)
		err = io.EOF
	}
	line = b.buf[b.off:end] // 包含delim
	b.off = end             // 更新读位置
	b.lastRead = opRead     // 读操作
	return line, err
}

// ReadString reads until the first occurrence of delim in the input,
// returning a string containing the data up to and including the delimiter.
// If ReadString encounters an error before finding a delimiter,
// it returns the data read before the error and the error itself (often io.EOF).
// ReadString returns err != nil if and only if the returned data does not end
// in delim.
// ReadString从Buffer中一直读，直到遇到delim。
// ReadString会返回这些成功读取内容(Buffer中如果有delim就包含，没有就是全部读缓存内容)对应的字符串。
func (b *Buffer) ReadString(delim byte) (line string, err error) {
	slice, err := b.readSlice(delim)
	return string(slice), err
}

// NewBuffer creates and initializes a new Buffer using buf as its
// initial contents. The new Buffer takes ownership of buf, and the
// caller should not use buf after this call. NewBuffer is intended to
// prepare a Buffer to read existing data. It can also be used to size
// the internal buffer for writing. To do that, buf should have the
// desired capacity but a length of zero.
//
// In most cases, new(Buffer) (or just declaring a Buffer variable) is
// sufficient to initialize a Buffer.
// NewBuffer创建和初始化了一个新的Buffer，这个新的Buffer使用buf指定的存储空间作为底层的存储空间。
// 这个函数的主要目的是使用一个已经存在的待读数据。
func NewBuffer(buf []byte) *Buffer { return &Buffer{buf: buf} }

// NewBufferString creates and initializes a new Buffer using string s as its
// initial contents. It is intended to prepare a buffer to read an existing
// string.
//
// In most cases, new(Buffer) (or just declaring a Buffer variable) is
// sufficient to initialize a Buffer.
func NewBufferString(s string) *Buffer {
	return &Buffer{buf: []byte(s)}
}
