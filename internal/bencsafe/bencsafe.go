// Package bencsafe looks at bencoded bytes (a .torrent file, a piece of
// torrent metadata) before the torrent library decodes them.
//
// The library's decoder calls itself once for every list or dictionary inside
// another one and has no limit on how deep that goes. A file made of millions
// of "l" characters runs the program out of stack, and a stack overflow in Go
// cannot be caught: the whole process dies. Check reads the bytes in one flat
// loop with no recursion, so it is safe on anything, and says no to input that
// is nested too deep or is not well-formed bencode.
package bencsafe

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	// MaxDepth is how many lists and dictionaries may sit inside each other.
	// Real torrents stay below ten; a version 2 torrent with a deep folder
	// tree needs a few more. This is the same limit the library's own fix uses.
	MaxDepth = 64

	// MaxFileSize is the largest .torrent file read into memory.
	MaxFileSize = 64 << 20
)

// maxIntDigits stops a number that never ends from being scanned to the end of
// a huge buffer.
const maxIntDigits = 40

// ErrTooDeep is returned for data nested more than MaxDepth levels.
var ErrTooDeep = errors.New("bencode is nested too deeply")

// ErrTooLarge is returned for a file bigger than MaxFileSize.
var ErrTooLarge = errors.New("torrent file is too large")

type frame struct {
	dict      bool
	needValue bool // dictionaries only: a key was read, its value is next
}

// Prefix checks the bencoded value at the start of data and returns how many
// bytes it takes up. Bytes after it are not looked at, which suits a message
// that carries raw data behind a bencoded header. It does not allocate beyond
// a small fixed stack and never recurses.
func Prefix(data []byte) (int, error) {
	var stack [MaxDepth]frame
	depth := 0
	pos := 0
	n := len(data)

	// valueDone runs after a whole value has been read. It reports whether
	// that was the top-level value.
	valueDone := func() bool {
		if depth == 0 {
			return true
		}
		stack[depth-1].needValue = false
		return false
	}

	if n == 0 {
		return 0, errors.New("bencode is empty")
	}
	for pos < n {
		c := data[pos]
		keyPos := depth > 0 && stack[depth-1].dict && !stack[depth-1].needValue
		switch {
		case c == 'e':
			if depth == 0 {
				return 0, fmt.Errorf("bencode: unexpected end marker at byte %d", pos)
			}
			if stack[depth-1].dict && stack[depth-1].needValue {
				return 0, fmt.Errorf("bencode: dictionary key without a value at byte %d", pos)
			}
			depth--
			pos++
			if valueDone() {
				return pos, nil
			}
		case c == 'l' || c == 'd':
			if keyPos {
				return 0, fmt.Errorf("bencode: dictionary key is not a string at byte %d", pos)
			}
			if depth >= MaxDepth {
				return 0, ErrTooDeep
			}
			stack[depth] = frame{dict: c == 'd'}
			depth++
			pos++
		case c == 'i':
			if keyPos {
				return 0, fmt.Errorf("bencode: dictionary key is not a string at byte %d", pos)
			}
			pos++
			start := pos
			if pos < n && data[pos] == '-' {
				pos++
			}
			digits := 0
			for pos < n && data[pos] >= '0' && data[pos] <= '9' {
				pos++
				digits++
				if digits > maxIntDigits {
					return 0, fmt.Errorf("bencode: number too long at byte %d", start)
				}
			}
			if digits == 0 || pos >= n || data[pos] != 'e' {
				return 0, fmt.Errorf("bencode: bad number at byte %d", start)
			}
			pos++
			if valueDone() {
				return pos, nil
			}
		case c >= '0' && c <= '9':
			start := pos
			length := 0
			digits := 0
			for pos < n && data[pos] >= '0' && data[pos] <= '9' {
				digits++
				if digits > 10 {
					return 0, fmt.Errorf("bencode: string length too long at byte %d", start)
				}
				length = length*10 + int(data[pos]-'0')
				pos++
			}
			if pos >= n || data[pos] != ':' {
				return 0, fmt.Errorf("bencode: bad string length at byte %d", start)
			}
			pos++
			if length > n-pos {
				return 0, fmt.Errorf("bencode: string at byte %d runs past the end", start)
			}
			pos += length
			if keyPos {
				stack[depth-1].needValue = true
			} else if valueDone() {
				return pos, nil
			}
		default:
			return 0, fmt.Errorf("bencode: unexpected byte %q at %d", c, pos)
		}
	}
	return 0, errors.New("bencode ends before the value is complete")
}

// Check reports whether data starts with one well-formed bencoded value that
// is not nested deeper than MaxDepth. Whatever follows that value is left for
// the caller's own decoder to judge.
func Check(data []byte) error {
	_, err := Prefix(data)
	return err
}

// Pieces of torrent metadata arrive from peers one chunk at a time, and the
// library decodes them once they are all there, so a chunk cannot be checked
// on its own the way a whole file can. What can be said about one chunk is how
// much of it is list and dictionary openers. Running the stack out takes about
// three million nested levels or more (measured: roughly 170 to 330 bytes of
// stack per level against a 1 GiB limit), and the largest metadata the library
// accepts is 16 MiB, so that many openers only fit if more than a sixth of all
// bytes are openers. Real metadata is mostly text and random hashes, where "l"
// and "d" together are a few percent. Ten percent leaves a wide margin on both
// sides.
const (
	openerMinCount = 1024
	openerPercent  = 10
)

// LooksLikeNestingBomb reports whether a chunk of metadata is mostly list and
// dictionary openers, which no real torrent looks like.
func LooksLikeNestingBomb(chunk []byte) bool {
	openers := 0
	for _, b := range chunk {
		if b == 'l' || b == 'd' {
			openers++
		}
	}
	return openers >= openerMinCount && openers*100 > len(chunk)*openerPercent
}

// ReadFile reads a .torrent file, at most MaxFileSize bytes, and runs Check on
// it. Use the returned bytes for decoding rather than opening the file a
// second time.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileSize {
		return nil, ErrTooLarge
	}
	if err := Check(data); err != nil {
		return nil, err
	}
	return data, nil
}
