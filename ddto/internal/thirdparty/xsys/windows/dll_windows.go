// Copyright 2011 The Go Authors. All rights reserved.
// Adapted from golang.org/x/sys v0.47.0; BSD license in ../LICENSE.

//go:build windows

package windows

import (
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// syscall explicitly exposes this hook for x/sys. It uses
// LOAD_LIBRARY_SEARCH_SYSTEM32, excluding application-controlled DLL paths.
//
//go:linkname syscall_loadsystemlibrary syscall.loadsystemlibrary
func syscall_loadsystemlibrary(filename *uint16) (uintptr, syscall.Errno)

type DLLError struct {
	Err     error
	ObjName string
	Msg     string
}

type DLL struct {
	Name   string
	Handle Handle
}

type Proc struct {
	Dll  *DLL
	Name string
	addr uintptr
}

type LazyDLL struct {
	Name string

	// System is retained from the pinned constructor. This subset always
	// loads from the Windows system directory, regardless of its value.
	System bool

	mu  sync.Mutex
	dll *DLL // non nil once DLL is loaded
}

type LazyProc struct {
	Name string

	mu   sync.Mutex
	l    *LazyDLL
	proc *Proc
}

func (e *DLLError) Error() string { return e.Msg }

func (e *DLLError) Unwrap() error { return e.Err }

func (d *DLL) FindProc(name string) (*Proc, error) {
	if _, err := syscall.BytePtrFromString(name); err != nil {
		return nil, err
	}
	addr, err := syscall.GetProcAddress(d.Handle, name)
	if err != nil {
		return nil, &DLLError{Err: err, ObjName: name, Msg: "Failed to find " + name + " procedure in " + d.Name + ": " + err.Error()}
	}
	return &Proc{Dll: d, Name: name, addr: addr}, nil
}

func (p *Proc) Addr() uintptr {
	return p.addr
}

func (d *LazyDLL) mustLoad() {
	e := d.Load()
	if e != nil {
		panic(e)
	}
}

func (d *LazyDLL) Handle() uintptr {
	d.mustLoad()
	return uintptr(d.dll.Handle)
}

func (d *LazyDLL) NewProc(name string) *LazyProc {
	return &LazyProc{l: d, Name: name}
}

func NewLazySystemDLL(name string) *LazyDLL {
	return &LazyDLL{Name: name, System: true}
}

func (p *LazyProc) Find() error {
	// Non-racy version of:
	// if p.proc == nil {
	if atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&p.proc))) == nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.proc == nil {
			e := p.l.Load()
			if e != nil {
				return e
			}
			proc, e := p.l.dll.FindProc(p.Name)
			if e != nil {
				return e
			}
			// Non-racy version of:
			// p.proc = proc
			atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&p.proc)), unsafe.Pointer(proc))
		}
	}
	return nil
}

func (p *LazyProc) mustFind() {
	e := p.Find()
	if e != nil {
		panic(e)
	}
}

func (p *LazyProc) Addr() uintptr {
	p.mustFind()
	return p.proc.Addr()
}

//go:uintptrescapes
func (p *Proc) Call(a ...uintptr) (r1, r2 uintptr, lastErr error) {
	return syscall.SyscallN(p.Addr(), a...)
}

//go:uintptrescapes
func (p *LazyProc) Call(a ...uintptr) (r1, r2 uintptr, lastErr error) {
	p.mustFind()
	return p.proc.Call(a...)
}

func loadSystemDLL(name string) (dll *DLL, err error) {
	namep, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, e := syscall_loadsystemlibrary(namep)
	if e != 0 {
		return nil, &DLLError{
			Err:     e,
			ObjName: name,
			Msg:     "Failed to load " + name + ": " + e.Error(),
		}
	}
	d := &DLL{
		Name:   name,
		Handle: Handle(h),
	}
	return d, nil
}

func (d *LazyDLL) Load() error {
	// Non-racy version of:
	// if d.dll != nil {
	if atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&d.dll))) != nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dll != nil {
		return nil
	}

	dll, err := loadSystemDLL(d.Name)
	if err != nil {
		return err
	}

	// Non-racy version of:
	// d.dll = dll
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&d.dll)), unsafe.Pointer(dll))
	return nil
}
