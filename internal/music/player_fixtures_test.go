package music_test

import (
	"bytes"
	"encoding/binary"
)

// Small valid-looking audio files for the tag readers: headers and tags only.

type flacSpec struct {
	sampleRate, channels, bps, seconds, audioBytes int
	comments                                       []string // "KEY=value"
}

func buildFLAC(s flacSpec) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	total := uint64(s.seconds) * uint64(s.sampleRate)
	d := make([]byte, 34)
	d[10] = byte(s.sampleRate >> 12)
	d[11] = byte(s.sampleRate >> 4)
	d[12] = byte(s.sampleRate&0xF)<<4 | byte(s.channels-1)<<1 | byte((s.bps-1)>>4)
	d[13] = byte((s.bps-1)&0xF)<<4 | byte(total>>32&0xF)
	binary.BigEndian.PutUint32(d[14:18], uint32(total))
	block := func(typ byte, last bool, payload []byte) {
		h := []byte{typ, byte(len(payload) >> 16), byte(len(payload) >> 8), byte(len(payload))}
		if last {
			h[0] |= 0x80
		}
		b.Write(h)
		b.Write(payload)
	}
	block(0, len(s.comments) == 0, d)
	if len(s.comments) > 0 {
		var vc bytes.Buffer
		binary.Write(&vc, binary.LittleEndian, uint32(4))
		vc.WriteString("test")
		binary.Write(&vc, binary.LittleEndian, uint32(len(s.comments)))
		for _, c := range s.comments {
			binary.Write(&vc, binary.LittleEndian, uint32(len(c)))
			vc.WriteString(c)
		}
		block(4, true, vc.Bytes())
	}
	b.Write(make([]byte, s.audioBytes))
	return b.Bytes()
}

// id3v2 builds an ID3v2.3 tag from frame id / text pairs.
func id3v2(frames [][2]string) []byte {
	var body bytes.Buffer
	for _, f := range frames {
		text := append([]byte{0}, []byte(f[1])...)
		body.WriteString(f[0])
		binary.Write(&body, binary.BigEndian, uint32(len(text)))
		body.Write([]byte{0, 0})
		body.Write(text)
	}
	n := body.Len()
	h := []byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
	return append(h, body.Bytes()...)
}

// mp3Frame44 is one MPEG-1 Layer III 320 kbit/s frame at 44.1 kHz.
func mp3Frame44() []byte {
	f := make([]byte, 144*320*1000/44100)
	f[0], f[1], f[2] = 0xFF, 0xFB, 14<<4
	return f
}
