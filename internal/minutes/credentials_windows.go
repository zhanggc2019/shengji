package minutes

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectCredential(_ string, scope, secret string) ([]byte, error) {
	input := []byte(secret)
	defer clear(input)
	entropy := []byte(scope)
	in, binding := credentialBlob(input), credentialBlob(entropy)
	var out windows.DataBlob
	err := windows.CryptProtectData(&in, nil, &binding, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func unprotectCredential(_ string, scope string, ciphertext []byte) (string, error) {
	if len(ciphertext) == 0 {
		return "", errors.New("empty credential")
	}
	entropy := []byte(scope)
	in, binding := credentialBlob(ciphertext), credentialBlob(entropy)
	var out windows.DataBlob
	err := windows.CryptUnprotectData(&in, nil, &binding, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	if err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	plaintext := unsafe.Slice(out.Data, out.Size)
	defer clear(plaintext)
	return string(plaintext), nil
}

func credentialBlob(value []byte) windows.DataBlob {
	return windows.DataBlob{Size: uint32(len(value)), Data: unsafe.SliceData(value)}
}
