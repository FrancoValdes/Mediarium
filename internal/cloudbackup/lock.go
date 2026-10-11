// Package cloudbackup copies Mediarium's backups to storage outside the
// server (S3-style object storage or a WebDAV folder), locked with a
// passphrase first so the storage provider only ever holds scrambled data.
package cloudbackup

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

// A locked backup is:
//
//	magic (16 bytes) | salt (16) | nonce prefix (8) | chunks...
//
// Each chunk is AES-256-GCM over up to chunkSize bytes, with the nonce made
// of the prefix and the chunk number, and one extra byte of associated data
// that says whether it is the last chunk (so a cut-short file is noticed).
// The key comes from the passphrase with scrypt.

var magic = []byte("MEDIARIUM-LOCK1\n")

const (
	chunkSize = 64 << 10
	saltLen   = 16
	prefixLen = 8
)

// Scrypt cost: about 0.1 s and 32 MB on a small NAS, once per backup.
var scryptN = 1 << 15

// ErrWrongPassphrase means the passphrase doesn't open the backup (or the
// file was changed).
var ErrWrongPassphrase = errors.New("the passphrase doesn't open this backup, or the file is damaged")

// ErrNotLocked means the data isn't a locked backup.
var ErrNotLocked = errors.New("this isn't a locked Mediarium backup")

// IsLocked reports whether data starts like a locked backup.
func IsLocked(head []byte) bool {
	return bytes.HasPrefix(head, magic)
}

func deriveKey(passphrase string, salt []byte) ([]byte, error) {
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("make the key: %w", err)
	}
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonceFor(prefix []byte, n uint32) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[8:], n)
	return nonce
}

// Lock writes r to w locked with passphrase.
func Lock(w io.Writer, r io.Reader, passphrase string) error {
	if passphrase == "" {
		return errors.New("a passphrase is needed")
	}
	head := make([]byte, saltLen+prefixLen)
	if _, err := rand.Read(head); err != nil {
		return fmt.Errorf("lock the backup: %w", err)
	}
	salt, prefix := head[:saltLen], head[saltLen:]
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return err
	}
	aead, err := newGCM(key)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(append([]byte(nil), magic...), head...)); err != nil {
		return fmt.Errorf("lock the backup: %w", err)
	}
	br := bufio.NewReaderSize(r, chunkSize)
	buf := make([]byte, chunkSize)
	for n := uint32(0); ; n++ {
		if n == ^uint32(0) {
			return errors.New("the backup is too large to lock")
		}
		got, err := io.ReadFull(br, buf)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return fmt.Errorf("lock the backup: %w", err)
		}
		last := byte(0)
		if got < chunkSize {
			last = 1
		} else if _, perr := br.Peek(1); perr != nil {
			last = 1 // exactly a whole number of chunks: this full one is the end
		}
		sealed := aead.Seal(nil, nonceFor(prefix, n), buf[:got], []byte{last})
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(sealed)))
		if _, err := w.Write(append(size[:], sealed...)); err != nil {
			return fmt.Errorf("lock the backup: %w", err)
		}
		if last == 1 {
			return nil
		}
	}
}

// Unlock writes the backup locked in r to w.
func Unlock(w io.Writer, r io.Reader, passphrase string) error {
	br := bufio.NewReader(r)
	head := make([]byte, len(magic)+saltLen+prefixLen)
	if _, err := io.ReadFull(br, head); err != nil || !IsLocked(head) {
		return ErrNotLocked
	}
	salt := head[len(magic) : len(magic)+saltLen]
	prefix := head[len(magic)+saltLen:]
	key, err := deriveKey(passphrase, salt)
	if err != nil {
		return err
	}
	aead, err := newGCM(key)
	if err != nil {
		return err
	}
	maxSealed := chunkSize + aead.Overhead()
	buf := make([]byte, maxSealed)
	for n := uint32(0); ; n++ {
		var size [4]byte
		if _, err := io.ReadFull(br, size[:]); err != nil {
			return ErrWrongPassphrase // cut short before the last chunk
		}
		l := int(binary.BigEndian.Uint32(size[:]))
		if l < aead.Overhead() || l > maxSealed {
			return ErrWrongPassphrase
		}
		if _, err := io.ReadFull(br, buf[:l]); err != nil {
			return ErrWrongPassphrase
		}
		nonce := nonceFor(prefix, n)
		plain, err := aead.Open(nil, nonce, buf[:l], []byte{0})
		last := false
		if err != nil {
			plain, err = aead.Open(nil, nonce, buf[:l], []byte{1})
			if err != nil {
				return ErrWrongPassphrase
			}
			last = true
		}
		if _, err := w.Write(plain); err != nil {
			return fmt.Errorf("unlock the backup: %w", err)
		}
		if last {
			if _, err := br.Peek(1); err == nil {
				return ErrWrongPassphrase // data after the last chunk
			}
			return nil
		}
	}
}
