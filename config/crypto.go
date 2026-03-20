package config

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Encrypt string using Windows DPAPI
func Encrypt(data []byte) ([]byte, error) {
	var outBlob windows.DataBlob
	inBlob := windows.DataBlob{
		Data: &data[0],
		Size: uint32(len(data)),
	}
	err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, 0, &outBlob)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(outBlob.Data))))

	result := make([]byte, outBlob.Size)
	copy(result, (*[1 << 30]byte)(unsafe.Pointer(outBlob.Data))[:outBlob.Size:outBlob.Size])
	return result, nil
}

// Decrypt string using Windows DPAPI
func Decrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return []byte{}, nil
	}
	var outBlob windows.DataBlob
	inBlob := windows.DataBlob{
		Data: &data[0],
		Size: uint32(len(data)),
	}
	err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, 0, &outBlob)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(outBlob.Data))))

	result := make([]byte, outBlob.Size)
	copy(result, (*[1 << 30]byte)(unsafe.Pointer(outBlob.Data))[:outBlob.Size:outBlob.Size])
	return result, nil
}
