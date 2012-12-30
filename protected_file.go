package gdsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"io"
	"os"
	)

const (
	salt_length = 16
	key_length = 32
	iv_length = aes.BlockSize
)

type ProtectedFileReader struct {
	cipher.StreamReader
	File *os.File
}

type ProtectedFileWriter struct {
	cipher.StreamWriter
	File *os.File
}

func createKeyAndIV(passphrase string, salt []byte) []byte {
	buf := make([]byte, key_length + iv_length)
	index := 0
	var previous_buf []byte
	for index < len(buf) {
		md5sum := md5.New()
		if len(previous_buf) > 0 {
			md5sum.Write(previous_buf)
		}
		io.WriteString(md5sum, passphrase)
		sum := md5sum.Sum(salt)
		for i := 0; i < len(sum) && index < len(buf); i++ {
			buf[index] = sum[i]
			index++
		}
		previous_buf = sum
	}
	return buf
}

func NewProtectedFileReader(filename, passphrase string) (*ProtectedFileReader, error) {
	fin, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, salt_length)
	if length, err := fin.Read(salt); length != salt_length || err != nil {
		return nil, err
	}

	key_iv := createKeyAndIV(passphrase, salt)

	block, err := aes.NewCipher(key_iv[:key_length])
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCFBDecrypter(block, key_iv[key_length:])
	return &ProtectedFileReader{
		StreamReader: cipher.StreamReader{
			S: stream,
			R: fin,
		},
		File: fin,
	}, nil
}

func NewProtectedFileWriter(filename, passphrase string) (*ProtectedFileWriter, error) {
	fout, err := os.Create(filename)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, salt_length)
	if n, err := io.ReadFull(rand.Reader, salt); n != len(salt) || err != nil {
		return nil, err
	}
	if n, err:= fout.Write(salt); n != len(salt) || err != nil {
		return nil, err
	}

	key_iv := createKeyAndIV(passphrase, salt)

	block, err := aes.NewCipher(key_iv[:key_length])
	if err != nil {
		return nil, err
	}
	stream := cipher.NewCFBEncrypter(block, key_iv[key_length:])
	return &ProtectedFileWriter{
		StreamWriter: cipher.StreamWriter{
			S: stream,
			W: fout,
		},
		File: fout,
	}, nil
}