// Package qr draws QR codes, for the two-step sign-in setup (an
// authenticator app scans the otpauth:// link). It covers what that needs
// and nothing more: byte mode, error correction level M, versions 1 to 10
// (up to 213 bytes). It follows the QR Code standard (ISO/IEC 18004).
package qr

import (
	"errors"
	"strings"
)

// ErrTooLong means the text doesn't fit in a version 10 code.
var ErrTooLong = errors.New("qr: text too long")

const maxVersion = 10

// Error correction level M: codewords per block and number of blocks, by
// version (index 0 unused).
var (
	eccPerBlock = [maxVersion + 1]int{0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26}
	numBlocks   = [maxVersion + 1]int{0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5}
)

// Code is a QR code: Modules[y][x] is true for a dark module. It has no quiet
// zone; leave four light modules around it when drawing.
type Code struct {
	Size    int
	Modules [][]bool
}

// Rows is the code as one string per row, "1" dark and "0" light.
func (c *Code) Rows() []string {
	out := make([]string, c.Size)
	var b strings.Builder
	for y := range c.Size {
		b.Reset()
		for x := range c.Size {
			if c.Modules[y][x] {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		out[y] = b.String()
	}
	return out
}

// rawModules is the number of modules a version has for data and error
// correction, after the function patterns.
func rawModules(ver int) int {
	n := (16*ver+128)*ver + 64
	if ver >= 2 {
		align := ver/7 + 2
		n -= (25*align-10)*align - 55
		if ver >= 7 {
			n -= 36
		}
	}
	return n
}

func dataCodewords(ver int) int {
	return rawModules(ver)/8 - eccPerBlock[ver]*numBlocks[ver]
}

// Encode makes the smallest QR code (level M) that holds data.
func Encode(data []byte) (*Code, error) {
	ver := 0
	for v := 1; v <= maxVersion; v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(data) <= dataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, ErrTooLong
	}

	// The bit stream: byte mode, the length, the bytes, then padding.
	var bits []bool
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (v>>i)&1 == 1)
		}
	}
	put(0x4, 4)
	if ver >= 10 {
		put(len(data), 16)
	} else {
		put(len(data), 8)
	}
	for _, b := range data {
		put(int(b), 8)
	}
	capacity := dataCodewords(ver) * 8
	put(0, min(4, capacity-len(bits)))
	put(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capacity; pad ^= 0xEC ^ 0x11 {
		put(pad, 8)
	}
	codewords := make([]byte, len(bits)/8)
	for i, bit := range bits {
		if bit {
			codewords[i>>3] |= 1 << (7 - i&7)
		}
	}

	c := newCode(ver)
	c.drawCodewords(addECCAndInterleave(codewords, ver))
	c.chooseMask()
	return &Code{Size: c.size, Modules: c.modules}, nil
}

// addECCAndInterleave splits the data into blocks, adds Reed-Solomon error
// correction to each and interleaves them as the standard lays out.
func addECCAndInterleave(data []byte, ver int) []byte {
	blocks := numBlocks[ver]
	eccLen := eccPerBlock[ver]
	raw := rawModules(ver) / 8
	shortBlocks := blocks - raw%blocks
	shortLen := raw / blocks
	divisor := rsDivisor(eccLen)

	var all [][]byte
	k := 0
	for i := range blocks {
		n := shortLen - eccLen
		if i >= shortBlocks {
			n++
		}
		dat := append([]byte(nil), data[k:k+n]...)
		k += n
		ecc := rsRemainder(dat, divisor)
		if i < shortBlocks {
			dat = append(dat, 0) // a placeholder, skipped when interleaving
		}
		all = append(all, append(dat, ecc...))
	}
	out := make([]byte, 0, raw)
	for i := range all[0] {
		for j, b := range all {
			if i != shortLen-eccLen || j >= shortBlocks {
				out = append(out, b[i])
			}
		}
	}
	return out
}

// gfMul multiplies in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1.
func gfMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= int((y>>i)&1) * int(x)
	}
	return byte(z)
}

func rsDivisor(degree int) []byte {
	out := make([]byte, degree)
	out[degree-1] = 1
	root := byte(1)
	for range degree {
		for j := range out {
			out[j] = gfMul(out[j], root)
			if j+1 < len(out) {
				out[j] ^= out[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return out
}

func rsRemainder(data, divisor []byte) []byte {
	out := make([]byte, len(divisor))
	for _, b := range data {
		factor := b ^ out[0]
		copy(out, out[1:])
		out[len(out)-1] = 0
		for i, d := range divisor {
			out[i] ^= gfMul(d, factor)
		}
	}
	return out
}

// builder holds a code being drawn: the modules and which of them belong to
// the fixed patterns (and so take no data and no mask).
type builder struct {
	ver      int
	size     int
	modules  [][]bool
	function [][]bool
}

func newCode(ver int) *builder {
	size := ver*4 + 17
	c := &builder{ver: ver, size: size, modules: make([][]bool, size), function: make([][]bool, size)}
	for i := range size {
		c.modules[i] = make([]bool, size)
		c.function[i] = make([]bool, size)
	}
	c.drawFunctionPatterns()
	return c
}

func (c *builder) set(x, y int, dark bool) {
	c.modules[y][x] = dark
	c.function[y][x] = true
}

func (c *builder) drawFunctionPatterns() {
	for i := range c.size {
		c.set(6, i, i%2 == 0) // timing patterns
		c.set(i, 6, i%2 == 0)
	}
	c.drawFinder(3, 3)
	c.drawFinder(c.size-4, 3)
	c.drawFinder(3, c.size-4)

	pos := alignmentPositions(c.ver)
	last := len(pos) - 1
	for i, x := range pos {
		for j, y := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue // where the finder patterns are
			}
			c.drawAlignment(x, y)
		}
	}
	c.drawFormatBits(0) // reserves the area; drawn again with the real mask
	c.drawVersion()
}

func (c *builder) drawFinder(x, y int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			d := max(abs(dx), abs(dy))
			xx, yy := x+dx, y+dy
			if xx >= 0 && xx < c.size && yy >= 0 && yy < c.size {
				c.set(xx, yy, d != 2 && d != 4)
			}
		}
	}
}

func (c *builder) drawAlignment(x, y int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			c.set(x+dx, y+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

func alignmentPositions(ver int) []int {
	if ver == 1 {
		return nil
	}
	n := ver/7 + 2
	step := (ver*4 + n*2 + 1) / (n*2 - 2) * 2
	out := make([]int, n)
	out[0] = 6
	for i, p := n-1, ver*4+10; i >= 1; i, p = i-1, p-step {
		out[i] = p
	}
	return out
}

// drawFormatBits writes the error correction level (M) and the mask, twice.
func (c *builder) drawFormatBits(mask int) {
	data := 0<<3 | mask // level M is 00
	rem := data
	for range 10 {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		c.set(8, i, bit(i))
	}
	c.set(8, 7, bit(6))
	c.set(8, 8, bit(7))
	c.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.set(14-i, 8, bit(i))
	}
	for i := range 8 {
		c.set(c.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.set(8, c.size-15+i, bit(i))
	}
	c.set(8, c.size-8, true) // the dark module
}

func (c *builder) drawVersion() {
	if c.ver < 7 {
		return
	}
	rem := c.ver
	for range 12 {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
	}
	bits := c.ver<<12 | rem
	for i := range 18 {
		dark := (bits>>i)&1 == 1
		a, b := c.size-11+i%3, i/3
		c.set(a, b, dark)
		c.set(b, a, dark)
	}
}

// drawCodewords places the data in the zig-zag order of the standard.
func (c *builder) drawCodewords(data []byte) {
	i := 0
	for right := c.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // skip the vertical timing pattern
		}
		for vert := range c.size {
			for j := range 2 {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = c.size - 1 - vert
				}
				if !c.function[y][x] && i < len(data)*8 {
					c.modules[y][x] = (data[i>>3]>>(7-i&7))&1 == 1
					i++
				}
			}
		}
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

func (c *builder) applyMask(mask int) {
	for y := range c.size {
		for x := range c.size {
			if !c.function[y][x] && maskBit(mask, x, y) {
				c.modules[y][x] = !c.modules[y][x]
			}
		}
	}
}

// chooseMask tries the eight masks and keeps the one that is easiest to scan.
func (c *builder) chooseMask() {
	best, bestScore := 0, -1
	for m := range 8 {
		c.applyMask(m)
		c.drawFormatBits(m)
		if s := c.penalty(); bestScore < 0 || s < bestScore {
			best, bestScore = m, s
		}
		c.applyMask(m) // undo
	}
	c.applyMask(best)
	c.drawFormatBits(best)
}

// penalty scores a masked code by the standard's four rules (lower is better).
func (c *builder) penalty() int {
	score := 0
	at := func(x, y int, column bool) bool {
		if column {
			return c.modules[x][y]
		}
		return c.modules[y][x]
	}
	for _, column := range []bool{false, true} {
		for y := range c.size {
			run := 1
			for x := 1; x <= c.size; x++ {
				if x < c.size && at(x, y, column) == at(x-1, y, column) {
					run++
					continue
				}
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
			// Finder-like stretches: 1011101 with four light modules on a side.
			for x := 0; x+10 < c.size; x++ {
				var w [11]bool
				for k := range 11 {
					w[k] = at(x+k, y, column)
				}
				core := w[0] && !w[1] && w[2] && w[3] && w[4] && !w[5] && w[6]
				if core && !w[7] && !w[8] && !w[9] && !w[10] {
					score += 40
				}
				core = w[4] && !w[5] && w[6] && w[7] && w[8] && !w[9] && w[10]
				if core && !w[0] && !w[1] && !w[2] && !w[3] {
					score += 40
				}
			}
		}
	}
	dark := 0
	for y := range c.size {
		for x := range c.size {
			if c.modules[y][x] {
				dark++
			}
			if x+1 < c.size && y+1 < c.size {
				v := c.modules[y][x]
				if v == c.modules[y][x+1] && v == c.modules[y+1][x] && v == c.modules[y+1][x+1] {
					score += 3
				}
			}
		}
	}
	total := c.size * c.size
	score += (abs(dark*20-total*10) + total - 1) / total * 10
	return score
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
