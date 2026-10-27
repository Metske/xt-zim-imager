package zim

import (
	"encoding/binary"
	"io"
)

// read a little endian uint64
func readInt64(b []byte, err error) (v uint64, aerr error) {
	if err != nil {
		return 0, err
	}
	if len(b) < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	return binary.LittleEndian.Uint64(b), nil
}

// read a little endian uint32
func readInt32(b []byte, err error) (v uint32, aerr error) {
	if err != nil {
		return 0, err
	}
	if len(b) < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	return binary.LittleEndian.Uint32(b), nil
}

// read a little endian uint16
func readInt16(b []byte, err error) (v uint16, aerr error) {
	if err != nil {
		return 0, err
	}
	if len(b) < 2 {
		return 0, io.ErrUnexpectedEOF
	}
	return binary.LittleEndian.Uint16(b), nil
}

