// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Pipe adapter to connect code expecting an io.Reader
// with code expecting an io.Writer.

package io

import (
	"errors"
	"sync"
)

// ErrClosedPipe is the error used for read or write operations on a closed pipe.
var ErrClosedPipe = errors.New("io: read/write on closed pipe")

// A pipe is the shared pipe structure underlying PipeReader and PipeWriter.
// pipe结构是PipeReader和PipeWriter底层共享的管道。
type pipe struct {
	rl    sync.Mutex // gates readers one at a time 保证一次只有一个读
	wl    sync.Mutex // gates writers one at a time 保证一次只有一个写
	l     sync.Mutex // protects remaining fields 保护本结构中的其他字段，也保证了读与写不能同时进行，也就是说：读、写操作是序列化的，这保证了操作data不会引起并发错误
	data  []byte     // data remaining in pending write 管道中的数据
	rwait sync.Cond  // waiting reader 读等待
	wwait sync.Cond  // waiting writer 写等待
	rerr  error      // if reader closed, error to give writes 关闭读端，该错误会被Write方法返回
	werr  error      // if writer closed, error to give reads 关闭写端，该错误会被Read方法返回
}

func (p *pipe) read(b []byte) (n int, err error) {
	// One reader at a time.
	// 序列化读操作，防止读操作之间的乱读
	p.rl.Lock()
	defer p.rl.Unlock()

	p.l.Lock()
	defer p.l.Unlock()
	for {
		if p.rerr != nil {
			return 0, ErrClosedPipe // 读端已经关闭
		}
		if p.data != nil {
			break // 管道中有数据
		}
		if p.werr != nil {
			return 0, p.werr // 写端已经关闭
		}
		p.rwait.Wait() // 等待管道中有数据，当Wait返回时，p.l被锁上，也就是说此时写端不能写数据了，当p.rwait.Wait()在等待时，p.l会被释放
	}
	n = copy(b, p.data) // 成功读取n个字节的数据到b中
	p.data = p.data[n:] // 更新管道中的数据缓存区，将读取成功的数据排除
	if len(p.data) == 0 {
		// 管道中的数据被读完，将管道数据缓存区置空，并给写操作发信号
		p.data = nil
		p.wwait.Signal()
	}
	return
}

var zero [0]byte

func (p *pipe) write(b []byte) (n int, err error) {
	// pipe uses nil to mean not available
	if b == nil {
		b = zero[:]
	}

	// One writer at a time.
	// 序列化写操作，防止写操作之间的相互覆盖
	p.wl.Lock()
	defer p.wl.Unlock()

	p.l.Lock()
	defer p.l.Unlock()
	if p.werr != nil {
		// 写端已经关闭
		err = ErrClosedPipe
		return
	}
	p.data = b
	p.rwait.Signal() // 向读发信号，可以读了
	for {
		if p.data == nil {
			// 说明写入管道中的数据已经被读取完了
			break
		}
		if p.rerr != nil {
			// 读端已经关闭
			err = p.rerr
			break
		}
		if p.werr != nil {
			// 写端已经关闭
			err = ErrClosedPipe
			break
		}
		p.wwait.Wait() // 等待管道中的数据被读取完毕，将会由pipe的read方法来唤醒
	}
	n = len(b) - len(p.data) // 成功写入管道中的数据大小，至于为什么不是len(p.data)?原因是：管道可能被关闭了
	p.data = nil             // in case of rerr or werr
	return
}

func (p *pipe) rclose(err error) {
	if err == nil {
		err = ErrClosedPipe
	}
	p.l.Lock()
	defer p.l.Unlock()
	p.rerr = err
	p.rwait.Signal()
	p.wwait.Signal()
}

func (p *pipe) wclose(err error) {
	if err == nil {
		err = EOF
	}
	p.l.Lock()
	defer p.l.Unlock()
	p.werr = err
	p.rwait.Signal()
	p.wwait.Signal()
}

// A PipeReader is the read half of a pipe.
// 一个PipeReader是管道的读端。
type PipeReader struct {
	p *pipe
}

// Read implements the standard Read interface:
// it reads data from the pipe, blocking until a writer
// arrives or the write end is closed.
// If the write end is closed with an error, that error is
// returned as err; otherwise err is EOF.
func (r *PipeReader) Read(data []byte) (n int, err error) {
	return r.p.read(data)
}

// Close closes the reader; subsequent writes to the
// write half of the pipe will return the error ErrClosedPipe.
func (r *PipeReader) Close() error {
	return r.CloseWithError(nil)
}

// CloseWithError closes the reader; subsequent writes
// to the write half of the pipe will return the error err.
func (r *PipeReader) CloseWithError(err error) error {
	r.p.rclose(err)
	return nil
}

// A PipeWriter is the write half of a pipe.
// 一个PipeWriter对象是管道的写端。
type PipeWriter struct {
	p *pipe
}

// Write implements the standard Write interface:
// it writes data to the pipe, blocking until one or more readers
// have consumed all the data or the read end is closed.
// If the read end is closed with an error, that err is
// returned as err; otherwise err is ErrClosedPipe.
func (w *PipeWriter) Write(data []byte) (n int, err error) {
	return w.p.write(data)
}

// Close closes the writer; subsequent reads from the
// read half of the pipe will return no bytes and EOF.
func (w *PipeWriter) Close() error {
	return w.CloseWithError(nil)
}

// CloseWithError closes the writer; subsequent reads from the
// read half of the pipe will return no bytes and the error err,
// or EOF if err is nil.
//
// CloseWithError always returns nil.
func (w *PipeWriter) CloseWithError(err error) error {
	w.p.wclose(err)
	return nil
}

// Pipe creates a synchronous in-memory pipe.
// It can be used to connect code expecting an io.Reader
// with code expecting an io.Writer.
//
// Reads and Writes on the pipe are matched one to one
// except when multiple Reads are needed to consume a single Write.
// That is, each Write to the PipeWriter blocks until it has satisfied
// one or more Reads from the PipeReader that fully consume
// the written data.
// The data is copied directly from the Write to the corresponding
// Read (or Reads); there is no internal buffering.
//
// It is safe to call Read and Write in parallel with each other or with Close.
// Parallel calls to Read and parallel calls to Write are also safe:
// the individual calls will be gated sequentially.
func Pipe() (*PipeReader, *PipeWriter) {
	p := new(pipe)
	p.rwait.L = &p.l // 读条件变量的锁
	p.wwait.L = &p.l // 写条件变量的锁，这样读写就可以同步了
	r := &PipeReader{p}
	w := &PipeWriter{p}
	return r, w
}
