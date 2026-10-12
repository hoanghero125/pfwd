package sshx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pageant's WM_COPYDATA protocol, as PuTTY defines it.
const (
	// pageantMaxMsg is the size of the shared buffer, and so the largest
	// request or reply, length prefix included.
	pageantMaxMsg = 8192
	// pageantMagic marks a COPYDATASTRUCT as an agent request.
	pageantMagic = 0x804e50ba
	wmCopyData   = 0x004A
)

// copyDataStruct is Windows' COPYDATASTRUCT.
type copyDataStruct struct {
	dwData uintptr
	cbData uint32
	lpData uintptr
}

// pageantConn is an agent connection to Pageant. Each complete request
// written to it is exchanged with Pageant, and the reply is read back. The
// agent client reads from its own goroutine, so Read waits for a reply.
type pageantConn struct {
	exchange func(req []byte) ([]byte, error)

	mu         sync.Mutex
	replied    *sync.Cond
	wbuf, rbuf bytes.Buffer
	closed     bool
}

func newPageantConn(exchange func([]byte) ([]byte, error)) *pageantConn {
	p := &pageantConn{exchange: exchange}
	p.replied = sync.NewCond(&p.mu)
	return p
}

func (p *pageantConn) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wbuf.Write(b)
	for p.wbuf.Len() >= 4 {
		n := binary.BigEndian.Uint32(p.wbuf.Bytes()[:4])
		if n > pageantMaxMsg-4 {
			p.wbuf.Reset()
			return 0, fmt.Errorf("agent request of %d bytes is too large for Pageant", n)
		}
		if p.wbuf.Len() < int(4+n) {
			break
		}
		reply, err := p.exchange(p.wbuf.Next(int(4 + n)))
		if err != nil {
			return 0, err
		}
		p.rbuf.Write(reply)
		p.replied.Broadcast()
	}
	return len(b), nil
}

func (p *pageantConn) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.rbuf.Len() == 0 && !p.closed {
		p.replied.Wait()
	}
	if p.rbuf.Len() == 0 {
		return 0, io.EOF
	}
	return p.rbuf.Read(b)
}

// Close ends a waiting Read.
func (p *pageantConn) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.replied.Broadcast()
	return nil
}

// pageantWindow finds Pageant's window; 0 means it isn't running.
func pageantWindow() uintptr {
	name, err := windows.UTF16PtrFromString("Pageant")
	if err != nil {
		return 0
	}
	hwnd, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("FindWindowW").Call(
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)))
	return hwnd
}

func pageantRunning() bool { return pageantWindow() != 0 }

// openPageant returns an agent connection to Pageant, if it runs.
func openPageant() (*pageantConn, error) {
	if !pageantRunning() {
		return nil, errors.New("no Pageant window is running")
	}
	return newPageantConn(pageantExchange), nil
}

// pageantExchange sends one request to Pageant: the request goes into a
// file mapping owned by this user, Pageant is sent the mapping's name with
// WM_COPYDATA, and it writes its reply into the same mapping.
func pageantExchange(req []byte) ([]byte, error) {
	hwnd := pageantWindow()
	if hwnd == 0 {
		return nil, errors.New("no Pageant window is running")
	}
	sa, err := ownerOnlyAttributes()
	if err != nil {
		return nil, fmt.Errorf("pageant: %w", err)
	}
	name := fmt.Sprintf("PageantRequest%08x", windows.GetCurrentThreadId())
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	mapping, err := windows.CreateFileMapping(windows.InvalidHandle, sa, windows.PAGE_READWRITE, 0, pageantMaxMsg, namePtr)
	if err != nil {
		return nil, fmt.Errorf("pageant: create the shared buffer: %w", err)
	}
	defer func() { _ = windows.CloseHandle(mapping) }()
	view, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_WRITE, 0, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("pageant: map the shared buffer: %w", err)
	}
	defer func() { _ = windows.UnmapViewOfFile(view) }()

	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	move := kernel32.NewProc("RtlMoveMemory")
	_, _, _ = move.Call(view, uintptr(unsafe.Pointer(&req[0])), uintptr(len(req)))

	cname := append([]byte(name), 0)
	size := uint32(len(cname)) //nolint:gosec // a short name of fixed format
	cds := copyDataStruct{dwData: pageantMagic, cbData: size, lpData: uintptr(unsafe.Pointer(&cname[0]))}
	ret, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageW").Call(
		hwnd, wmCopyData, 0, uintptr(unsafe.Pointer(&cds)))
	runtime.KeepAlive(cname)
	if ret == 0 {
		return nil, errors.New("the request was refused by Pageant")
	}

	head := make([]byte, 4)
	_, _, _ = move.Call(uintptr(unsafe.Pointer(&head[0])), view, 4)
	n := binary.BigEndian.Uint32(head)
	if n > pageantMaxMsg-4 {
		return nil, fmt.Errorf("the reply from Pageant has %d bytes, more than its buffer", n)
	}
	reply := make([]byte, 4+n)
	_, _, _ = move.Call(uintptr(unsafe.Pointer(&reply[0])), view, uintptr(len(reply)))
	return reply, nil
}

// ownerOnlyAttributes is a security descriptor owned by, and only open to,
// the current user. Pageant refuses a mapping owned by anyone else.
func ownerOnlyAttributes() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}
