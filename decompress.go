//
// decompress.go
//
// Copyright (c) 2018 Markku Rossi
//
// All rights reserved.
//
// Streaming Lossless Data Compression Algorithm - (SLDC)
// Standard ECMA-321 June 2001

package sldc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// ErrTruncatedInput is an error that is returned when the input
// stream is truncated.
var ErrTruncatedInput = errors.New("Truncated input")

// Scheme specifies compression scheme.
type Scheme int

// Known compression schemes.
const (
	Scheme1 Scheme = iota + 1
	Scheme2
)

// Ctrl specifies control words.
type Ctrl int

// Known control words.
const (
	CtrlFlush Ctrl = iota
	CtrlScheme1
	CtrlScheme2
	CtrlFileMark
	CtrlEOR
	CtrlReset1
	CtrlReset2
	CtrlEndMarker = 0xf
)

var ctrlNames = map[Ctrl]string{
	CtrlFlush:     "Flush",
	CtrlScheme1:   "Scheme 1",
	CtrlScheme2:   "Scheme 2",
	CtrlFileMark:  "File Mark",
	CtrlEOR:       "EOR",
	CtrlReset1:    "Reset 1",
	CtrlReset2:    "Reset 2",
	CtrlEndMarker: "End Marker",
}

func (c Ctrl) String() string {
	name, ok := ctrlNames[c]
	if ok {
		return name
	}
	return fmt.Sprintf("{ctrl %d}", c)
}

// Input implements an input stream.
type Input struct {
	data []byte
	ofs  int
	bits int
}

// NewInput creates a new input object.
func NewInput(data []byte) *Input {
	return &Input{
		data: data,
		bits: 8,
	}
}

// Avail returns the number of bits available.
func (in *Input) Avail() int {
	return (len(in.data)-in.ofs-1)*8 + in.bits
}

// Get gets the specified number of bits from the input.
func (in *Input) Get(bits int) (val uint32, err error) {
	if in.bits > bits {
		left := in.bits - bits
		val = uint32(in.data[in.ofs] >> uint(left))
		val &= (0xff >> uint(8-bits))
		in.bits = left
		return
	}

	val = uint32(in.data[in.ofs])
	val &= (0xff >> uint(8-in.bits))
	bits -= in.bits
	in.ofs++
	in.bits = 8

	for bits >= 8 {
		if in.ofs >= len(in.data) {
			err = ErrTruncatedInput
			return
		}
		val <<= 8
		val |= uint32(in.data[in.ofs])
		in.ofs++
		bits -= 8
	}

	if bits > 0 {
		if in.ofs >= len(in.data) {
			err = ErrTruncatedInput
			return
		}
		val <<= uint(bits)
		in.bits = 8 - bits
		b := uint32(in.data[in.ofs]) >> uint(in.bits)
		b &= (0xff >> uint(8-bits))

		val |= b
	}

	return
}

// Peek returns the specified number of bits from the input without
// updating the input position.
func (in *Input) Peek(bits int) (val uint32, err error) {
	savedOfs := in.ofs
	savedBits := in.bits

	val, err = in.Get(bits)

	in.ofs = savedOfs
	in.bits = savedBits
	return
}

// IsCtrl tests if the next input is a control word.
func (in *Input) IsCtrl() bool {
	val, err := in.Peek(9)
	return err == nil && val == 0x1ff
}

// Ctrl gets the next control word.
func (in *Input) Ctrl() (Ctrl, error) {
	val, err := in.Get(9)
	if err != nil || val != 0x1ff {
		return CtrlEOR, err
	}
	val, err = in.Get(4)
	return Ctrl(val), err
}

// Align aligns input to the next four byte boundary.
func (in *Input) Align() error {
	for (in.ofs%4) != 0 && in.ofs < len(in.data) {
		in.ofs++
	}
	if (in.ofs % 4) != 0 {
		return ErrTruncatedInput
	}
	return nil
}

// History implements an input history.
type History struct {
	data []byte
	pos  int
}

// NewHistory creates a new history object.
func NewHistory() *History {
	return &History{
		data: make([]byte, 1024),
	}
}

// Add adds a byte to the history.
func (h *History) Add(b byte) {
	h.data[h.pos] = b
	h.pos++
	if h.pos >= len(h.data) {
		h.pos = 0
	}
}

// Get gets the byte from the specified offset of the history.
func (h *History) Get(ofs int) (byte, int) {
	if ofs < 0 || ofs >= len(h.data) {
		panic(fmt.Sprintf("Invalid displacement %d", ofs))
	}
	b := h.data[ofs]
	ofs++
	if ofs >= len(h.data) {
		ofs = 0
	}
	return b, ofs
}

// Reset resets the history.
func (h *History) Reset() {
	h.pos = 0
}

// Decompress decompresses the data.
func Decompress(data []byte) ([]byte, error) {
	input := NewInput(data)
	scheme := Scheme1
	history := NewHistory()
	var result []byte

	for {
		if input.IsCtrl() {
			ctrl, err := input.Ctrl()
			if err != nil {
				return nil, err
			}
			switch ctrl {
			case CtrlFlush:
				err = input.Align()
				if err != nil {
					return nil, err
				}

			case CtrlScheme1:
				scheme = Scheme1

			case CtrlScheme2:
				scheme = Scheme2

			case CtrlEOR:
				err = input.Align()
				if err != nil {
					return nil, err
				}
				return result, nil

			case CtrlReset1:
				scheme = Scheme1
				history.Reset()

			case CtrlReset2:
				scheme = Scheme2
				history.Reset()

			case CtrlEndMarker:
				if len(result) == 0 {
					// End marker at the beginning of the data.
					return nil, io.EOF
				}

			default:
				fmt.Printf("Unknown Control %s, result so far:\n%s",
					ctrl, hex.Dump(result))
				return nil, fmt.Errorf("Invalid control symbol %s", ctrl)
			}
		} else if scheme == Scheme1 {
			val, err := input.Get(1)
			if err != nil {
				return nil, err
			}
			if val == 0 {
				// Literal 1 Data Symbols
				val, err = input.Get(8)
				if err != nil {
					return nil, err
				}
				history.Add(byte(val))
				result = append(result, byte(val))
			} else {
				// Copy Pointer Data Symbols
				var ones int
				for ones = 0; ones < 4; ones++ {
					val, err := input.Get(1)
					if err != nil {
						return nil, err
					}
					if val == 0 {
						break
					}
				}
				var base int
				var bits int
				switch ones {
				case 0:
					// 0 x
					base = 2
					bits = 1
				case 1:
					// 10 xx
					base = 4
					bits = 2
				case 2:
					// 110 xxx
					base = 8
					bits = 3
				case 3:
					// 1110 xxxx
					base = 16
					bits = 4
				case 4:
					// 1111 xxxxxxxx
					base = 32
					bits = 8
				}
				val, err = input.Get(bits)
				if err != nil {
					return nil, err
				}
				matchCount := base + int(val)
				if matchCount > 271 {
					return nil, fmt.Errorf("Invalid match count %d", matchCount)
				}
				val, err := input.Get(10)
				if err != nil {
					return nil, err
				}
				displacement := int(val)
				var b byte
				for matchCount > 0 {
					b, displacement = history.Get(displacement)
					history.Add(b)
					result = append(result, b)
					matchCount--
				}
			}
		} else {
			fmt.Printf("- Scheme %d rules\n", scheme)
			return nil, fmt.Errorf("Scheme 2 rules not implemented yet")
		}
	}
}
