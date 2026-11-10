package configtransfer

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMutationCodecInclusiveSizeBoundaries(t *testing.T) {
	password := bytes.Repeat([]byte{'x'}, MaxPasswordBytes)
	if !ValidPassword(password) || ValidPassword(append(append([]byte{}, password...), 'x')) {
		t.Fatal("password byte limit must be inclusive")
	}
	// An authenticated empty message is still a complete 64-byte envelope.
	file, err := Encrypt(nil, password)
	if err != nil || len(file) != 64 {
		t.Fatal(len(file), err)
	}
	plain, err := Decrypt(file, password)
	if err != nil || len(plain) != 0 {
		t.Fatal(plain, err)
	}
	maximum := make([]byte, MaxPlaintextBytes)
	file, err = Encrypt(maximum, []byte("synthetic-size-boundary"))
	if err != nil || len(file) != MaxFileBytes || fileHeader(file) != nil {
		t.Fatal("maximum permitted plaintext/file rejected", len(file), err)
	}
	_, err = Encrypt(append(maximum, 0), []byte("x"))
	assertCode(t, err, "LIMIT_EXCEEDED")
	assertCode(t, fileHeader(append(file, 0)), "LIMIT_EXCEEDED")
	// A declared size beyond the cap is rejected before any KDF work.
	short := append([]byte{}, file[:64]...)
	binary.BigEndian.PutUint32(short[44:48], MaxPlaintextBytes+1)
	assertCode(t, fileHeader(short), "LIMIT_EXCEEDED")
}
