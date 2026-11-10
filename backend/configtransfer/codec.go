package configtransfer

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

var magic = []byte{'P', 'O', 'R', 'T', 'C', 'F', 'G', 0}

func ValidPassword(password []byte) bool {
	return len(password) > 0 && len(password) <= MaxPasswordBytes && utf8.Valid(password)
}
func wipe(b []byte) { clear(b) }
func fileHeader(file []byte) error {
	if len(file) > MaxFileBytes {
		return NewError("LIMIT_EXCEEDED", "decrypt")
	}
	if len(file) < 64 || !bytes.Equal(file[:8], magic) {
		return NewError("FILE_CORRUPT", "decrypt")
	}
	if binary.BigEndian.Uint16(file[8:10]) != 1 || binary.BigEndian.Uint16(file[10:12]) != 1 {
		return NewError("FORMAT_UNSUPPORTED", "decrypt")
	}
	if file[12] != 1 || file[13] != 1 || file[14] != 0 || file[15] != 0 {
		return NewError("CRYPTO_PROFILE_UNSUPPORTED", "decrypt")
	}
	p := uint64(binary.BigEndian.Uint32(file[44:48]))
	if p > MaxPlaintextBytes {
		return NewError("LIMIT_EXCEEDED", "decrypt")
	}
	if uint64(len(file)) != p+64 {
		return NewError("FILE_CORRUPT", "decrypt")
	}
	return nil
}
func aead(password, salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey(password, salt, 3, 65536, 1, 32)
	defer wipe(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func Encrypt(plaintext, password []byte) ([]byte, error) {
	if !ValidPassword(password) {
		return nil, NewError("INVALID_INPUT", "input")
	}
	if len(plaintext) > MaxPlaintextBytes {
		return nil, NewError("LIMIT_EXCEEDED", "encrypt")
	}
	header := make([]byte, 48, 48+len(plaintext)+16)
	copy(header, magic)
	binary.BigEndian.PutUint16(header[8:10], 1)
	binary.BigEndian.PutUint16(header[10:12], 1)
	header[12], header[13] = 1, 1
	if _, err := rand.Read(header[16:44]); err != nil {
		return nil, NewError("TRANSFER_UNAVAILABLE", "encrypt")
	}
	binary.BigEndian.PutUint32(header[44:48], uint32(len(plaintext)))
	a, err := aead(password, header[16:32])
	if err != nil {
		return nil, NewError("TRANSFER_UNAVAILABLE", "encrypt")
	}
	return a.Seal(header, header[32:44], plaintext, header[:48]), nil
}
func Decrypt(file, password []byte) ([]byte, error) {
	if !ValidPassword(password) {
		return nil, NewError("INVALID_INPUT", "input")
	}
	if err := fileHeader(file); err != nil {
		return nil, err
	}
	a, err := aead(password, file[16:32])
	if err != nil {
		return nil, NewError("DECRYPT_FAILED", "decrypt")
	}
	p, err := a.Open(nil, file[32:44], file[48:], file[:48])
	if err != nil {
		return nil, NewError("DECRYPT_FAILED", "decrypt")
	}
	return p, nil
}
