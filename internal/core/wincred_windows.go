//go:build windows

package core

import (
	"errors"
	"syscall"
	"unsafe"

	"codegen/internal/flow"
)

var (
	advapi32       = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric        = 1
	credPersistLocal       = 2 // this user on this computer
	credMaxBlob            = 5 * 512
	errorNotFound    int64 = 1168 // ERROR_NOT_FOUND
)

// credential is CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// wincredGet reads one generic credential: its value and user name.
func wincredGet(target string) (value, user string, ok bool) {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return "", "", false
	}
	var p *credential
	if r, _, _ := procCredReadW.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p))); r == 0 || p == nil {
		return "", "", false
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(p)))
	if p.CredentialBlobSize > 0 && p.CredentialBlob != nil {
		value = wincredValue(append([]byte(nil), unsafe.Slice(p.CredentialBlob, p.CredentialBlobSize)...))
	}
	user = utf16z(p.UserName)
	return value, user, value != ""
}

func wincredRead(ref flow.KeychainRef) (string, bool) {
	for i, t := range wincredTargets(ref) {
		v, user, ok := wincredGet(t)
		if !ok {
			continue
		}
		if i > 0 && ref.Account != "" && user != "" && user != ref.Account { // another program's entry: for this account only
			continue
		}
		return v, true
	}
	return "", false
}

func wincredWrite(ref flow.KeychainRef, value string) error {
	blob := wincredBlob(value)
	if len(blob) > credMaxBlob {
		return errors.New("the value is too long for Windows Credential Manager (at most 1,280 characters)")
	}
	t, err := syscall.UTF16PtrFromString(ref.Service + "/" + ref.Account)
	if err != nil {
		return err
	}
	u, err := syscall.UTF16PtrFromString(ref.Account)
	if err != nil {
		return err
	}
	c, err := syscall.UTF16PtrFromString("Saved by SpecWright")
	if err != nil {
		return err
	}
	cred := credential{Type: credTypeGeneric, TargetName: t, Comment: c, Persist: credPersistLocal, UserName: u, CredentialBlobSize: uint32(len(blob))}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}
	if r, _, e := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0); r == 0 {
		return errors.New("Windows Credential Manager refused it: " + e.Error())
	}
	return nil
}

func wincredDelete(ref flow.KeychainRef) error {
	t, err := syscall.UTF16PtrFromString(ref.Service + "/" + ref.Account)
	if err != nil {
		return err
	}
	if r, _, e := procCredDelete.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0); r == 0 {
		if en, ok := e.(syscall.Errno); ok && int64(en) == errorNotFound {
			return errors.New("there is no such entry in Credential Manager")
		}
		return errors.New("Windows Credential Manager: " + e.Error())
	}
	return nil
}

// utf16z reads a NUL-terminated UTF-16 string without reading past it.
func utf16z(p *uint16) string {
	if p == nil {
		return ""
	}
	var u []uint16
	for q := unsafe.Pointer(p); ; q = unsafe.Add(q, 2) {
		c := *(*uint16)(q)
		if c == 0 || len(u) > 1<<15 {
			break
		}
		u = append(u, c)
	}
	return syscall.UTF16ToString(u)
}
