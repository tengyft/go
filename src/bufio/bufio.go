// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bufio implements buffered I/O.  It wraps an io.Reader or io.Writer
// object, creating another object (Reader or Writer) that also implements
// the interface but provides buffering and some help for textual I/O.
package bufio

import (
	"bytes"
	"errors"
	"io"
	"unicode/utf8"
)

const (
	defaultBufSize = 4096
)

var (
	ErrInvalidUnreadByte = errors.New("bufio: invalid use of UnreadByte")
	ErrInvalidUnreadRune = errors.New("bufio: invalid use of UnreadRune")
	ErrBufferFull        = errors.New("bufio: buffer full")
	ErrNegativeCount     = errors.New("bufio: negative count")
)

// Buffered input.

// Reader implements buffering for an io.Reader object.
// Reader用于实现带缓冲区的I/O。
type Reader struct {
	buf          []byte
	rd           io.Reader // reader provided by the client
	r, w         int       // buf read and write positions，其中r <= w，[r, w)之间是可读的缓存内容。
	err          error
	lastByte     int
	lastRuneSize int
}

const minReadBufferSize = 16
const maxConsecutiveEmptyReads = 100

// NewReaderSize returns a new Reader whose buffer has at least the specified
// size. If the argument io.Reader is already a Reader with large enough
// size, it returns the underlying Reader.
// NewReaderSize返回一个新的Reader，它的buf是指定的size大小。
// 如果rd的dynamic value本身是一个Reader，并且其buf的length >= size，
// 则NewReaderSize直接返回底层的Reader。
func NewReaderSize(rd io.Reader, size int) *Reader {
	// Is it already a Reader?
	b, ok := rd.(*Reader)
	if ok && len(b.buf) >= size {
		return b
	}
	if size < minReadBufferSize {
		// 保证最小缓冲区大小
		size = minReadBufferSize
	}
	// 生成一个新的*Reader，更新其底层的buf、rd
	// 读写位置为0，err为nil，lastByte、lastRuneSize均为-1。
	r := new(Reader)
	r.reset(make([]byte, size), rd)
	return r
}

// NewReader returns a new Reader whose buffer has the default size.
// NewReader返回一个新的*Reader(前提是rd不是一个*Reader)，这个新的*Reader的buf有默认的大小。
func NewReader(rd io.Reader) *Reader {
	return NewReaderSize(rd, defaultBufSize)
}

// Reset discards any buffered data, resets all state, and switches
// the buffered reader to read from r.
// Reset丢掉所有缓存的内容，并且重置所有相关状态、切换b对应的io.Reader。
func (b *Reader) Reset(r io.Reader) {
	b.reset(b.buf, r)
}

// 此方法用于重置*Reader类型的b。
// 主要是将b的buf和rd更换为输入参数buf、r。
func (b *Reader) reset(buf []byte, r io.Reader) {
	*b = Reader{
		buf:          buf,
		rd:           r,
		lastByte:     -1,
		lastRuneSize: -1,
	}
}

var errNegativeRead = errors.New("bufio: reader returned negative count from Read")

// fill reads a new chunk into the buffer.
// fill向b中读入一块数据，这块数据的大小是不定的，但是不能超过len(b.buf) - b.w个字节。
func (b *Reader) fill() {
	// Slide existing data to beginning.
	if b.r > 0 {
		// b中有缓存内容，将未读的缓存内容滑向底层存储空间的起始处。
		copy(b.buf, b.buf[b.r:b.w])
		b.w -= b.r // 更新写位置
		b.r = 0    // 更新读位置
	}

	if b.w >= len(b.buf) {
		// b的缓存区已满。
		panic("bufio: tried to fill full buffer")
	}

	// Read new data: try a limited number of times.
	for i := maxConsecutiveEmptyReads; i > 0; i-- {
		n, err := b.rd.Read(b.buf[b.w:]) // 至多读入len(b.buf) - b.w个字节的数据。
		if n < 0 {
			panic(errNegativeRead)
		}
		b.w += n // 更新写位置
		if err != nil {
			b.err = err // 如果b.rd读错误，则保存遇到的错误。
			return
		}
		if n > 0 {
			// 成功读到内容就返回。
			return
		}
	}
	b.err = io.ErrNoProgress
}

// 返回向b写入数据时遇到的错误。
func (b *Reader) readErr() error {
	err := b.err
	b.err = nil
	return err
}

// Peek returns the next n bytes without advancing the reader. The bytes stop
// being valid at the next read call. If Peek returns fewer than n bytes, it
// also returns an error explaining why the read is short. The error is
// ErrBufferFull if n is larger than b's buffer size.
// Peek返回将要读取的n个字节，但是不更新b的读位置。返回的[]byte在下一次read操作之前有效(因为返回的[]byte与b.buf共享底层的存储空间)。
// 如果Peek返回的[]byte长度不足n，则也返回一个错误解释为什么不足。
func (b *Reader) Peek(n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeCount
	}

	for b.w-b.r < n && b.w-b.r < len(b.buf) && b.err == nil {
		// b.buf中可读缓存内容不足n个字节，并且b.buf未满、上一次从b.rd中读数据并向b.buf中写读到的数据没有出错。
		// 这个循环保证了：如果b.buf中可读缓存内容不足n个字节，那么b将一直从b.rd中读数据直到b.buf有n个字节或b.buf被读取的数据写满或从b.rd中读数据中遇到错误。
		b.fill() // b.w-b.r < len(b.buf) => buffer is not full
	}

	if n > len(b.buf) {
		// b.buf中可读的内容不足n个字节。
		return b.buf[b.r:b.w], ErrBufferFull
	}

	// 0 <= n <= len(b.buf)
	var err error
	if avail := b.w - b.r; avail < n {
		// not enough data in buffer
		// 在此b.buf中可读的内容不足n个字节，则说明上一个循环中读数据中出错。
		// avail < n只有两种可能 avail == len(b.buf) 和 b.err != nil
		// 显然第一种是可能的，只能是b.err != nil
		n = avail
		err = b.readErr()
		if err == nil {
			err = ErrBufferFull // 在此是为了防止万一？？
		}
	}
	return b.buf[b.r : b.r+n], err
}

// Discard skips the next n bytes, returning the number of bytes discarded.
//
// If Discard skips fewer than n bytes, it also returns an error.
// If 0 <= n <= b.Buffered(), Discard is guaranteed to succeed without
// reading from the underlying io.Reader.
// Discardq丢弃下一次将要从b中读取的n个字节。返回真正被丢弃的字节数量。
// 如果Discard丢弃的缓存内容(包括还没有从b.rd中读取的数据)不足n个字节，那么它也将返回一个错误。
// 如果0 <= n <= b.Buffered()，那么Discard保证成功丢弃n个字节的缓存内容，并且b也不会从b.rd读取数据(并写入自己的缓存中)。
func (b *Reader) Discard(n int) (discarded int, err error) {
	if n < 0 {
		return 0, ErrNegativeCount
	}
	if n == 0 {
		return
	}
	remain := n
	for {
		skip := b.Buffered()
		if skip == 0 {
			// b.buf中没有可读的缓存数据，则需要从b.rd中读取数据并写入b.buf中。
			b.fill()            // 从b.rd中读一块数据并写入到b.buf中。
			skip = b.Buffered() // 更新b中可读的内容字节数。
		}
		if skip > remain {
			skip = remain
		}
		b.r += skip    // 丢弃skip个字节的可读内容。
		remain -= skip // 更新还需要丢弃的字节数。
		if remain == 0 {
			// 成功丢弃n个字节的可读内容。
			return n, nil
		}
		if b.err != nil {
			// 检测从b.rd中读取数据出错，返回成功丢弃的字节数，以及从b.rd中读取数据时遇到的错误。
			return n - remain, b.readErr()
		}
	}
}

// Read reads data into p.
// It returns the number of bytes read into p.
// The bytes are taken from at most one Read on the underlying Reader,
// hence n may be less than len(p).
// At EOF, the count will be zero and err will be io.EOF.
// Read从b的可读缓存中读取数据并写入p中。
// 返回成功读取并写入p的字节数。
// Read方法至多使b从b.rd读取一次数据，所以返回的n可能小于len(p)。
func (b *Reader) Read(p []byte) (n int, err error) {
	n = len(p)
	if n == 0 {
		return 0, b.readErr()
	}
	// b中没有可读的缓存内容。
	if b.r == b.w {
		// 如果上一次从b.rd中读取数据时遇到错误，则返回遇到的错误
		if b.err != nil {
			return 0, b.readErr()
		}
		if len(p) >= len(b.buf) {
			// Large read, empty buffer.
			// Read directly into p to avoid copy.
			// 需要Read的数据比b.buf大，并且b现在没有可读的缓存内容。
			// 那么将直接从b.rd中读取数据，以避免拷贝操作。
			n, b.err = b.rd.Read(p)
			if n < 0 {
				panic(errNegativeRead)
			}
			if n > 0 {
				b.lastByte = int(p[n-1])
				b.lastRuneSize = -1
			}
			return n, b.readErr()
		}
		// One read.
		// Do not use b.fill, which will loop.
		// 只从b.rd中读一次，b.fill方法会从b.rd中读取至多maxConsecutiveEmptyReads次，所以不使用b.fill方法。
		b.r = 0
		b.w = 0
		n, b.err = b.rd.Read(b.buf)
		if n < 0 {
			panic(errNegativeRead)
		}
		// n == 0这种情况在此是必不可少的，保证了下面copy及以后语句的正确工作。
		if n == 0 {
			return 0, b.readErr()
		}
		b.w += n
	}

	// copy as much as we can
	n = copy(p, b.buf[b.r:b.w])    // 从可读缓存中读取一些内容(在此n >= 1)
	b.r += n                       // 更新读位置
	b.lastByte = int(b.buf[b.r-1]) // 从b中读出最后一个字节的内容
	b.lastRuneSize = -1
	return n, nil
}

// ReadByte reads and returns a single byte.
// If no byte is available, returns an error.
// ReadByte从b中读取一个字节。
func (b *Reader) ReadByte() (byte, error) {
	b.lastRuneSize = -1
	for b.r == b.w {
		// 在此使用for循环保证了循环后面语句的正确执行，因为b.buf中一定会有可读的缓存内容。
		if b.err != nil {
			// 先检测上一次从b.rd读取数据是否遇到错误，在从b.rd读取数据之前需要先检测错误。
			return 0, b.readErr()
		}
		b.fill() // buffer is empty
	}
	c := b.buf[b.r]
	b.r++
	b.lastByte = int(c)
	return c, nil
}

// UnreadByte unreads the last byte. Only the most recently read byte can be unread.
// UnreadByte会unread上一次从b中读取数据的最后一个字节。
func (b *Reader) UnreadByte() error {
	if b.lastByte < 0 || b.r == 0 && b.w > 0 {
		return ErrInvalidUnreadByte
	}
	// b.r > 0 || b.w == 0
	if b.r > 0 {
		b.r--
	} else {
		// b.r == 0 && b.w == 0
		b.w = 1
	}
	b.buf[b.r] = byte(b.lastByte)
	b.lastByte = -1
	b.lastRuneSize = -1
	return nil
}

// ReadRune reads a single UTF-8 encoded Unicode character and returns the
// rune and its size in bytes. If the encoded rune is invalid, it consumes one byte
// and returns unicode.ReplacementChar (U+FFFD) with a size of 1.
// ReadRune会读取一个UTF-8编码的字符，并返回这个字符和它所占用的字节数。如果这个字符编码是无效的，那么它
// 会消耗一个字节，并返回unicode.ReplacementChar, size == 1
func (b *Reader) ReadRune() (r rune, size int, err error) {
	for b.r+utf8.UTFMax > b.w && !utf8.FullRune(b.buf[b.r:b.w]) && b.err == nil && b.w-b.r < len(b.buf) {
		b.fill() // b.w-b.r < len(buf) => buffer is not full
	}
	b.lastRuneSize = -1
	if b.r == b.w {
		return 0, 0, b.readErr()
	}
	r, size = rune(b.buf[b.r]), 1
	if r >= utf8.RuneSelf {
		r, size = utf8.DecodeRune(b.buf[b.r:b.w])
	}
	b.r += size
	b.lastByte = int(b.buf[b.r-1])
	b.lastRuneSize = size
	return r, size, nil
}

// UnreadRune unreads the last rune. If the most recent read operation on
// the buffer was not a ReadRune, UnreadRune returns an error.  (In this
// regard it is stricter than UnreadByte, which will unread the last byte
// from any read operation.)
func (b *Reader) UnreadRune() error {
	if b.lastRuneSize < 0 || b.r < b.lastRuneSize {
		return ErrInvalidUnreadRune
	}
	b.r -= b.lastRuneSize
	b.lastByte = -1
	b.lastRuneSize = -1
	return nil
}

// Buffered returns the number of bytes that can be read from the current buffer.
// Buffered返回b底层存储空间(b.buf)中可读的、已经缓存的字节数。
func (b *Reader) Buffered() int { return b.w - b.r }

// ReadSlice reads until the first occurrence of delim in the input,
// returning a slice pointing at the bytes in the buffer.
// The bytes stop being valid at the next read.
// If ReadSlice encounters an error before finding a delimiter,
// it returns all the data in the buffer and the error itself (often io.EOF).
// ReadSlice fails with error ErrBufferFull if the buffer fills without a delim.
// Because the data returned from ReadSlice will be overwritten
// by the next I/O operation, most clients should use
// ReadBytes or ReadString instead.
// ReadSlice returns err != nil if and only if line does not end in delim.
// ReadSlice会从b.buf中一直读取内容(如果b.buf中不满，则从b.rd中读取内容)，直到遇到第一个delim字节。这个方法
// 会返回一个指向b.buf的slice，这个slice包含了读取到的内容。返回的line在下次读取之前都是有效的。如果ReadSlice
// 在读取内容、遇到第一个delim字节之前，发生了错误，那么这个方法会返回已经读到的内容和遇到的错误。如果b.buf已经
// 满了，并且也没有一个字节是delim，那么ReadSlice就会以ErrBufferFull这个错误结束。因为ReadSlice返回的数据
// 会被下一个I/O操作覆盖，所有大多数客户应该使用ReadBytes或者ReadString。
// ReadSlice会返回err != nil, 当且仅当line不以delim结尾。
// b.buf中如果有delim，则line以delim结尾。
func (b *Reader) ReadSlice(delim byte) (line []byte, err error) {
	for {
		// Search buffer.
		if i := bytes.IndexByte(b.buf[b.r:b.w], delim); i >= 0 {
			line = b.buf[b.r : b.r+i+1] // line中包含delim字节
			b.r += i + 1                // 更新读位置(即下次从b中读的位置)
			break
		}

		// Pending error?
		// 从b.rd中读取内容出错
		if b.err != nil {
			line = b.buf[b.r:b.w] // 将b缓存的内容全部返回
			b.r = b.w             // 更新读位置
			err = b.readErr()     // 返回读b.rd时出现的错误
			break
		}

		// Buffer full?
		// b.buf已经满了
		if b.Buffered() >= len(b.buf) {
			b.r = b.w    // 更新读位置
			line = b.buf // 全部返回b.buf
			err = ErrBufferFull
			break
		}

		// 从b.rd中读取一块数据
		b.fill() // buffer is not full
	}

	// Handle last byte, if any.
	if i := len(line) - 1; i >= 0 {
		b.lastByte = int(line[i])
		b.lastRuneSize = -1
	}

	return
}

// ReadLine is a low-level line-reading primitive. Most callers should use
// ReadBytes('\n') or ReadString('\n') instead or use a Scanner.
//
// ReadLine tries to return a single line, not including the end-of-line bytes.
// If the line was too long for the buffer then isPrefix is set and the
// beginning of the line is returned. The rest of the line will be returned
// from future calls. isPrefix will be false when returning the last fragment
// of the line. The returned buffer is only valid until the next call to
// ReadLine. ReadLine either returns a non-nil line or it returns an error,
// never both.
//
// The text returned from ReadLine does not include the line end ("\r\n" or "\n").
// No indication or error is given if the input ends without a final line end.
// Calling UnreadByte after ReadLine will always unread the last byte read
// (possibly a character belonging to the line end) even if that byte is not
// part of the line returned by ReadLine.
// ReadLine是一个底层的、读取一行的原语。大多数调用者应该使用ReadBytes('\n')或者ReadString('\n')或者使用一个Scanner。
//
// ReadLine尝试返回一行内容，这行内容不包括结尾的换行字节。如果这行内容太长，以致于b.buf没有足够的空间容纳它，
// 那么isPrefix就为true，并且这个方法会返回这行内容的前面部分。这行内容的剩余部分
func (b *Reader) ReadLine() (line []byte, isPrefix bool, err error) {
	line, err = b.ReadSlice('\n')
	if err == ErrBufferFull {
		// Handle the case where "\r\n" straddles the buffer.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			// Put the '\r' back on buf and drop it from line.
			// Let the next call to ReadLine check for "\r\n".
			if b.r == 0 {
				// should be unreachable
				panic("bufio: tried to rewind past start of buffer")
			}
			b.r--
			line = line[:len(line)-1]
		}
		return line, true, nil
	}

	if len(line) == 0 {
		if err != nil {
			line = nil
		}
		return
	}
	err = nil

	if line[len(line)-1] == '\n' {
		drop := 1
		if len(line) > 1 && line[len(line)-2] == '\r' {
			drop = 2
		}
		line = line[:len(line)-drop]
	}
	return
}

// ReadBytes reads until the first occurrence of delim in the input,
// returning a slice containing the data up to and including the delimiter.
// If ReadBytes encounters an error before finding a delimiter,
// it returns the data read before the error and the error itself (often io.EOF).
// ReadBytes returns err != nil if and only if the returned data does not end in
// delim.
// For simple uses, a Scanner may be more convenient.
func (b *Reader) ReadBytes(delim byte) ([]byte, error) {
	// Use ReadSlice to look for array,
	// accumulating full buffers.
	var frag []byte
	var full [][]byte
	var err error
	for {
		var e error
		frag, e = b.ReadSlice(delim)
		if e == nil { // got final fragment
			break
		}
		if e != ErrBufferFull { // unexpected error
			err = e
			break
		}

		// Make a copy of the buffer.
		buf := make([]byte, len(frag))
		copy(buf, frag)
		full = append(full, buf)
	}

	// Allocate new buffer to hold the full pieces and the fragment.
	n := 0
	for i := range full {
		n += len(full[i])
	}
	n += len(frag)

	// Copy full pieces and fragment in.
	buf := make([]byte, n)
	n = 0
	for i := range full {
		n += copy(buf[n:], full[i])
	}
	copy(buf[n:], frag)
	return buf, err
}

// ReadString reads until the first occurrence of delim in the input,
// returning a string containing the data up to and including the delimiter.
// If ReadString encounters an error before finding a delimiter,
// it returns the data read before the error and the error itself (often io.EOF).
// ReadString returns err != nil if and only if the returned data does not end in
// delim.
// For simple uses, a Scanner may be more convenient.
func (b *Reader) ReadString(delim byte) (string, error) {
	bytes, err := b.ReadBytes(delim)
	return string(bytes), err
}

// WriteTo implements io.WriterTo.
func (b *Reader) WriteTo(w io.Writer) (n int64, err error) {
	n, err = b.writeBuf(w)
	if err != nil {
		return
	}

	if r, ok := b.rd.(io.WriterTo); ok {
		m, err := r.WriteTo(w)
		n += m
		return n, err
	}

	if w, ok := w.(io.ReaderFrom); ok {
		m, err := w.ReadFrom(b.rd)
		n += m
		return n, err
	}

	if b.w-b.r < len(b.buf) {
		b.fill() // buffer not full
	}

	for b.r < b.w {
		// b.r < b.w => buffer is not empty
		m, err := b.writeBuf(w)
		n += m
		if err != nil {
			return n, err
		}
		b.fill() // buffer is empty
	}

	if b.err == io.EOF {
		b.err = nil
	}

	return n, b.readErr()
}

var errNegativeWrite = errors.New("bufio: writer returned negative count from Write")

// writeBuf writes the Reader's buffer to the writer.
func (b *Reader) writeBuf(w io.Writer) (int64, error) {
	n, err := w.Write(b.buf[b.r:b.w])
	if n < 0 {
		panic(errNegativeWrite)
	}
	b.r += n
	return int64(n), err
}

// buffered output

// Writer implements buffering for an io.Writer object.
// If an error occurs writing to a Writer, no more data will be
// accepted and all subsequent writes will return the error.
// After all data has been written, the client should call the
// Flush method to guarantee all data has been forwarded to
// the underlying io.Writer.
type Writer struct {
	err error
	buf []byte
	n   int
	wr  io.Writer
}

// NewWriterSize returns a new Writer whose buffer has at least the specified
// size. If the argument io.Writer is already a Writer with large enough
// size, it returns the underlying Writer.
func NewWriterSize(w io.Writer, size int) *Writer {
	// Is it already a Writer?
	b, ok := w.(*Writer)
	if ok && len(b.buf) >= size {
		return b
	}
	if size <= 0 {
		size = defaultBufSize
	}
	return &Writer{
		buf: make([]byte, size),
		wr:  w,
	}
}

// NewWriter returns a new Writer whose buffer has the default size.
func NewWriter(w io.Writer) *Writer {
	return NewWriterSize(w, defaultBufSize)
}

// Reset discards any unflushed buffered data, clears any error, and
// resets b to write its output to w.
func (b *Writer) Reset(w io.Writer) {
	b.err = nil
	b.n = 0
	b.wr = w
}

// Flush writes any buffered data to the underlying io.Writer.
func (b *Writer) Flush() error {
	if b.err != nil {
		return b.err
	}
	if b.n == 0 {
		return nil
	}
	n, err := b.wr.Write(b.buf[0:b.n])
	if n < b.n && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		if n > 0 && n < b.n {
			copy(b.buf[0:b.n-n], b.buf[n:b.n])
		}
		b.n -= n
		b.err = err
		return err
	}
	b.n = 0
	return nil
}

// Available returns how many bytes are unused in the buffer.
func (b *Writer) Available() int { return len(b.buf) - b.n }

// Buffered returns the number of bytes that have been written into the current buffer.
func (b *Writer) Buffered() int { return b.n }

// Write writes the contents of p into the buffer.
// It returns the number of bytes written.
// If nn < len(p), it also returns an error explaining
// why the write is short.
func (b *Writer) Write(p []byte) (nn int, err error) {
	for len(p) > b.Available() && b.err == nil {
		var n int
		if b.Buffered() == 0 {
			// Large write, empty buffer.
			// Write directly from p to avoid copy.
			n, b.err = b.wr.Write(p)
		} else {
			n = copy(b.buf[b.n:], p)
			b.n += n
			b.Flush()
		}
		nn += n
		p = p[n:]
	}
	if b.err != nil {
		return nn, b.err
	}
	n := copy(b.buf[b.n:], p)
	b.n += n
	nn += n
	return nn, nil
}

// WriteByte writes a single byte.
func (b *Writer) WriteByte(c byte) error {
	if b.err != nil {
		return b.err
	}
	if b.Available() <= 0 && b.Flush() != nil {
		return b.err
	}
	b.buf[b.n] = c
	b.n++
	return nil
}

// WriteRune writes a single Unicode code point, returning
// the number of bytes written and any error.
func (b *Writer) WriteRune(r rune) (size int, err error) {
	if r < utf8.RuneSelf {
		err = b.WriteByte(byte(r))
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	if b.err != nil {
		return 0, b.err
	}
	n := b.Available()
	if n < utf8.UTFMax {
		if b.Flush(); b.err != nil {
			return 0, b.err
		}
		n = b.Available()
		if n < utf8.UTFMax {
			// Can only happen if buffer is silly small.
			return b.WriteString(string(r))
		}
	}
	size = utf8.EncodeRune(b.buf[b.n:], r)
	b.n += size
	return size, nil
}

// WriteString writes a string.
// It returns the number of bytes written.
// If the count is less than len(s), it also returns an error explaining
// why the write is short.
func (b *Writer) WriteString(s string) (int, error) {
	nn := 0
	for len(s) > b.Available() && b.err == nil {
		n := copy(b.buf[b.n:], s)
		b.n += n
		nn += n
		s = s[n:]
		b.Flush()
	}
	if b.err != nil {
		return nn, b.err
	}
	n := copy(b.buf[b.n:], s)
	b.n += n
	nn += n
	return nn, nil
}

// ReadFrom implements io.ReaderFrom.
func (b *Writer) ReadFrom(r io.Reader) (n int64, err error) {
	if b.Buffered() == 0 {
		if w, ok := b.wr.(io.ReaderFrom); ok {
			return w.ReadFrom(r)
		}
	}
	var m int
	for {
		if b.Available() == 0 {
			if err1 := b.Flush(); err1 != nil {
				return n, err1
			}
		}
		nr := 0
		for nr < maxConsecutiveEmptyReads {
			m, err = r.Read(b.buf[b.n:])
			if m != 0 || err != nil {
				break
			}
			nr++
		}
		if nr == maxConsecutiveEmptyReads {
			return n, io.ErrNoProgress
		}
		b.n += m
		n += int64(m)
		if err != nil {
			break
		}
	}
	if err == io.EOF {
		// If we filled the buffer exactly, flush preemptively.
		if b.Available() == 0 {
			err = b.Flush()
		} else {
			err = nil
		}
	}
	return n, err
}

// buffered input and output

// ReadWriter stores pointers to a Reader and a Writer.
// It implements io.ReadWriter.
type ReadWriter struct {
	*Reader
	*Writer
}

// NewReadWriter allocates a new ReadWriter that dispatches to r and w.
func NewReadWriter(r *Reader, w *Writer) *ReadWriter {
	return &ReadWriter{r, w}
}
