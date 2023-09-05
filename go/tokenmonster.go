package tokenmonster

import (
	"os"
	"io"
	"bytes"
	"unsafe"
	"errors"
	"strings"
	"strconv"
	"unicode"
	"unicode/utf8"
	"unicode/utf16"
	"encoding/hex"
	"encoding/binary"
	"gopkg.in/yaml.v3"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Conv"
	"github.com/AlasdairF/Sort/Uint32Float32"
	"github.com/alasdairforsythe/norm"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/branchless"
	"github.com/alasdairforsythe/capcode/go"
)

const (
	minHighSurrogate = 0xD800 // Start of high surrogate range
	maxHighSurrogate = 0xDBFF // End of high surrogate range
	minLowSurrogate  = 0xDC00 // Start of low surrogate range
	maxLowSurrogate  = 0xDFFF // End of low surrogate range
	runeError = '\uFFFD'
	DOES_NOT_EXIST = 16777215
)

var isLittleEndian = *(*byte)(unsafe.Pointer(&[]uint16{256}[0])) == 0

// The main struct for the vocabulary
type Vocab struct {
	dictionary *pansearch.Fast
	info []tokenInfo
	reverse [][]byte
	deleted []deletedStruct // deleted tokens are stored here and can later be restored
	beginByte [256]byte
	vocabSize int
	maxTokenLength int
	deleteToken uint32 // ID of the delete token, or DOES_NOT_EXIST
	unkToken uint32 // ID of the UNK token, or DOES_NOT_EXIST
	usingCapcode uint8
	charset uint8
	level uint8
	reserve uint8
	normalizer norm.Normalizer // uint8
}

// A decoder object for sequential decoding.
// Use the NewDecoder function of the Vocab struct.
type Decoder struct {
	vocab Vocab
	remainder []byte
	capcodeDecoder *capcode.Decoder
}

type tokenInfo struct {
	alt		tokenOuter
	token 	[]byte
	score	float32
}

type tokenOuter struct {
	data	tokenInner
	length	int			// length of alternative1
	length2 int			// length of alternative2
	index	uint32		// index of alternative 1
	index2  uint32		// index of alternative 2
	id		uint32		// my ID
	id1		uint32		// ID of alternative1
	id2		uint32		// ID of alternative2
}

type tokenInner struct {
	flag	uint8
	nWords 	uint8
}

type deletedStruct struct {
	token 	[]byte
	id		uint32
	score 	float32
}

/*
'flag' bits:
	1	ends with a letter
	2	begins with a letter
	4 	begins with a space OR characterToken OR wordToken
	8 	ends on capcode
	16	begins on capcode
	32 	a single straight word, beginning space, no punctuation
	64 	is a special token
	128 is either all letters or no letters

beginByte
	1 = letter
	10 = anything else
	12 = space >>2 & 1 == 1
	>>3 means not a letter
*/

// --------- HELPER FUNCTIONS ---------

/*
func norm_UTF16_NFD(input []byte) ([]byte, error) {
	// Assume LittleEndian by default
	endian := uni.LittleEndian
	bomPolicy := uni.IgnoreBOM
	if len(input) >= 2 {
		if input[0] == 0xFE && input[1] == 0xFF {
			endian = uni.BigEndian
			bomPolicy = uni.ExpectBOM
		} else if input[0] == 0xFF && input[1] == 0xFE {
			endian = uni.LittleEndian
			bomPolicy = uni.ExpectBOM
		}
	}
	// Attempt to decode the input with decided endian
	utf16Decoder := uni.UTF16(endian, bomPolicy)
	// Create a transformer to decode to UTF-16 and normalize the text to NFD
	transformer := transform.Chain(utf16Decoder.NewDecoder(), norm.NFD)
	// Create a reader with the transformer
	reader := transform.NewReader(bytes.NewReader(input), transformer)
	// Read normalized NFD UTF-16 bytes
	nfdBytes, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("Error normalizing content: %w", err)
	}
	// Encode normalized NFD back to UTF-16LE
	utf16LEEncoder := uni.UTF16(uni.LittleEndian, uni.UseBOM).NewEncoder()
	reader = transform.NewReader(bytes.NewReader(nfdBytes), utf16LEEncoder)
	// Read UTF-16LE bytes
	utf16LEBytes, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("Error converting content to []byte: %w", err)
	}
	return utf16LEBytes, nil
}
*/

// Returns the number of bytes at the end of the slice of bytes that are part of an incomplete UTF-8 sequence.
func incompleteUTF8Bytes(bytes []byte) int {
    bytesLen := len(bytes)
    // Single byte or empty string
    if bytesLen == 0 {
        return 0
    }
    if bytes[bytesLen-1]&0b10000000 == 0 {
        return 0
    }
    // Find the start of the last character sequence
    seqStart := bytesLen - 1
    for seqStart >= 0 && (bytes[seqStart]&0b11000000) == 0b10000000 {
        seqStart--
    }
    // If no sequence start found, all bytes are continuation bytes and thus are all incomplete
    if seqStart == -1 {
        return bytesLen
    }
    // Determine expected sequence length from leading byte
    firstByte := bytes[seqStart]
    var seqLen int
    if (firstByte & 0b10000000) == 0 {
        seqLen = 1
    } else if (firstByte & 0b11100000) == 0b11000000 {
        seqLen = 2
    } else if (firstByte & 0b11110000) == 0b11100000 {
        seqLen = 3
    } else if (firstByte & 0b11111000) == 0b11110000 {
        seqLen = 4
    } else {
        // This is not a valid UTF-8 starting byte
        return bytesLen - seqStart
    }
    // If sequence length is larger than the remaining bytes, it's incomplete
    if bytesLen-seqStart < seqLen {
        return seqLen - (bytesLen - seqStart)
    }
    // If the sequence start byte was not the start of a multi-byte sequence, then the array is incomplete.
    if seqLen == 1 && (bytes[seqStart] & 0b11000000) != 0 {
        return bytesLen
    }
    return 0
}

func incompleteUTF16Bytes(bytes []byte) int {
	bytesLen := len(bytes)
	if bytesLen == 0 {
		return 0
	}
	if bytesLen % 2 != 0 {
		var lastThreeBytes uint16
		if bytesLen >= 3 {
			lastThreeBytes = binary.LittleEndian.Uint16(bytes[bytesLen-3 : bytesLen-1])
			if lastThreeBytes >= 0xD800 && lastThreeBytes <= 0xDBFF {
				return 3 // High surrogate followed by a stray byte
			}
		}
		return 1 // Single stray byte
	}
	// Check if last 16-bit unit is a high surrogate
	lastTwoBytes := binary.LittleEndian.Uint16(bytes[bytesLen-2 : bytesLen])
	if lastTwoBytes >= 0xD800 && lastTwoBytes <= 0xDBFF {
		return 2 // High surrogate without a following low surrogate
	}
	// Check if first 16-bit unit is a low surrogate
	firstTwoBytes := binary.LittleEndian.Uint16(bytes[:2])
	if firstTwoBytes >= 0xDC00 && firstTwoBytes <= 0xDFFF {
		return 2 // Low surrogate without a preceding high surrogate
	}
	return 0
}

func convertStringToUTF16(s string) []byte {
	return []byte(s)
	/*
	b := []byte(s)
	buf := &bytes.Buffer{}
	w := transform.NewWriter(buf, uni.UTF16(uni.LittleEndian, uni.IgnoreBOM).NewEncoder())
	w.Write(b)
	w.Close()
	return buf.Bytes()
	*/
}

func applyCapcode(data []byte, usingCapcode uint8) []byte {
	if usingCapcode == 2 {
		return capcode.Encode(data)
	} else if usingCapcode == 1 {
		return capcode.NoCapcodeEncode(data)
	}
	return data
}

func normalize(data []byte, usingCapcode uint8, normalizer norm.Normalizer) ([]byte, error) {
	processed, err := normalizer.Normalize(data)
	if err == nil {
		return applyCapcode(processed, usingCapcode), nil
	} else { // if failed try it the other way around
		if !normalizer.SpecifiedLowercase() {
			processed = applyCapcode(data, usingCapcode)
			processed, err = normalizer.Normalize(processed)
		}
	}
	return processed, err
}

// normalizes but avoids double encoding with capcode
func normalizeSafe(b []byte, usingCapcode uint8, normalizer norm.Normalizer) ([]byte, error) {
	var err error
	var okay bool = true
	if usingCapcode == 2 {
		for _, v := range b {
			if v == capcode.DeleteToken || v == capcode.CharacterToken || v == capcode.WordToken {
				okay = false
				break
			}
		}
		if okay {
			b, err = normalizer.Normalize(b)
			b = capcode.Encode(b)
		}
		return b, err
	} else if usingCapcode == 1 {
		for _, v := range b {
			if v == capcode.NoCapcodeDeleteToken {
				okay = false
				break
			}
		}
		if okay {
			b, err = normalizer.Normalize(b)
			b = capcode.NoCapcodeEncode(b)
		}
		return b, err
	}
	return normalizer.Normalize(b)
}

func hasSuffixPos(ungreedySuffixesB [][]byte, key []byte, charset uint8, usingCapcode uint8) int {
	for _, suffix := range ungreedySuffixesB {
		if bytes.HasSuffix(key, suffix) {
			if len(suffix) < len(key) {
				r := decodeLastRune(key[:len(key)-len(suffix)], charset)
				if isLetter(r, usingCapcode) {
					return len(key) - len(suffix)
				}
			}
		}
	}
	return -1
}

func genUTF8bytes(list []bool, usingCapcode uint8) {
	genASCIIbytes(list, usingCapcode)
    // Continuation bytes in multi-byte characters
    for i := 0x80; i <= 0xBF; i++ {
		list[i] = true
    }
    // Starting bytes of multi-byte characters excluding overlongs
    for i := 0xC2; i <= 0xF4; i++ {
		list[i] = true
    }
}

func genASCIIbytes(list []bool, usingCapcode uint8) {
	for i:=32; i<127; i++ {
		if usingCapcode != 2 || (!(i >= 'A' && i <= 'Z' && i != 'C' && i != 'W' && i != 'D')) {
			list[i] = true
		}
	}
	list[9] = true
	list[10] = true
	list[13] = true
	if usingCapcode == 1 {
		list[127] = true
	}
}

func genExtendedbytes(list []bool, usingCapcode uint8, normalizer norm.Normalizer) {
	s := `£€©®™°%¢¥—–•‘’“”áéíóúýàèìòùâêîôûäëïöüñãõçåæœ`
	if usingCapcode != 2 && !normalizer.SpecifiedLowercase() {
		s += `ÁÉÍÓÚÝÀÈÌÒÙÂÊÎÔÛÄËÏÖÜÑÃÕÇÅÆŒ`
	}
	s2, _ := normalizer.Normalize([]byte(s))
	for _, b := range s2 {
		list[b] = true
	}
	genASCIIbytes(list, usingCapcode)
}

func gen128bytes(list []bool, usingCapcode uint8) {
	var b byte
	for i:=0; i<128; i++ {
		b = byte(i)
		if usingCapcode != 2 || (!(b >= 'A' && b <= 'Z' && b != 'C' && b != 'W' && b != 'D')) {
			list[i] = true
		}
	}
}

func gen256bytes(list []bool, usingCapcode uint8) {
	var b byte
	for i:=0; i<256; i++ {
		b = byte(i)
		if usingCapcode != 2 || (!(b >= 'A' && b <= 'Z' && b != 'C' && b != 'W' && b != 'D')) {
			list[i] = true
		}
	}
}

func isLetter(r rune, usingCapcode uint8) bool {
	return (unicode.IsLetter(r) && (usingCapcode!=2 || (r != 'W' && r != 'C' && r != 'D'))) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isAlphaNum(r rune, usingCapcode uint8) bool {
	return (unicode.IsLetter(r) && (usingCapcode!=2 || (r != 'W' && r != 'C' && r != 'D'))) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isCapcode(r rune, usingCapcode uint8) bool {
	return (usingCapcode == 1 && r == '\x7F') || (usingCapcode==2 && (r == 'C' || r == 'W' || r == 'D'))
}

func decodeRune(b []byte, charsetFlag uint8) (rune, int) {
	switch charsetFlag {
		case 0, 1: // UTF-8
			return utf8.DecodeRune(b)
		case 2: // UTF-16
			if len(b) < 2 {
				return runeError, 0
			}
			u := binary.LittleEndian.Uint16(b)
			if u >= minHighSurrogate && u <= maxHighSurrogate {
				// This is a surrogate pair. We need another two bytes.
				if len(b) < 4 {
					return runeError, 0
				}
				u2 := binary.LittleEndian.Uint16(b[2:])
				if u2 < minLowSurrogate || u2 > maxLowSurrogate {
					return runeError, 0
				}
				r := utf16.Decode([]uint16{u, u2})
				if len(r) == 0 {
					return runeError, 0
				}
				return r[0], 4 // surrogate pair is 4 bytes in UTF-16
			}
			return rune(u), 2 // normal character is 2 bytes in UTF-16
		default:
			return -1, 0
	}
}

func decodeLastRune(b []byte, charsetFlag uint8) rune {
	switch charsetFlag {
		case 0, 1: // UTF-8
			r, _ := utf8.DecodeLastRune(b)
			return r
		case 2: // UTF-16
			if len(b) < 2 {
				return runeError
			}
			u := binary.LittleEndian.Uint16(b[len(b)-2:])
			if u >= minLowSurrogate && u <= maxLowSurrogate {
				// This is a surrogate pair. We need another two bytes.
				if len(b) < 4 {
					return runeError
				}
				u2 := binary.LittleEndian.Uint16(b[len(b)-4:])
				if u2 < minHighSurrogate || u2 > maxHighSurrogate {
					return runeError
				}
				r := utf16.Decode([]uint16{u2, u})
				if len(r) == 0 {
					return runeError
				}
				return r[0]
			}
			return rune(u)
		default:
			return -1
	}
}

func unleak(b []byte) []byte {
	new := make([]byte, len(b))
	copy(new, b)
	return new
}

func canHaveUnkToken(i int, usingCapcode uint8) bool {
	if (i < 256 && usingCapcode != 2) || i < 233 {
		return true
	}
	return false
}

// --------- DECODER ---------

// Creates a new Decoder instance.
// This is for decoding tokens in a sequence when they are to be decoded individually or in batches.
// If you are decoding all in one go, you can use the Vocab's Decode method.
func (vocab *Vocab) NewDecoder() *Decoder {
	return &Decoder{vocab:*vocab, capcodeDecoder: new(capcode.Decoder)}
}

// Flushes the remainder from the Decoder instance
// These will any trailing incomplete UTF-8 sequences or capcode encoding marks
func (d *Decoder) Flush() []byte {
	data := d.remainder
	d.remainder = nil
	return data
}

// Decodes tokens from a serialized bytes slice.
// `encodingLength` must be one of: 0, 2, 3, 4.
// If you enter `encodingLength` 0 then it will determine the encoding length from the vocabulary size.
// `buffer` is optional, you can send it `nil` and it will allocate a new slice.
func (d *Decoder) DecodeSerialized(b []byte, encodingLength uint8, buffer []byte) []byte {
	if encodingLength <= 1 {
		if len(d.vocab.reverse) <= 65536 {
			encodingLength = 2
		} else {
			encodingLength = 3
		}
	}
	if encodingLength == 2 {
		var tokens []uint16
		var l uint64 = uint64(len(b)) >> 1
		if isLittleEndian {
			tokens = (*(*[]uint16)(unsafe.Pointer(&b)))[:l:l]
		} else {
			tokens = make([]uint16, l)
			var to uint64 = uint64(len(b))
			var i uint64
			for ; i<to; i+=2 {
				tokens[i >> 1] = uint16(b[i]) | (uint16(b[i+1]) << 8)
			}
		}
		reverse := d.vocab.reverse
		if len(reverse) == 0 {
			return []byte{}
		}
		nTokens := uint16(len(reverse) - 1)
		var i int
		if d.vocab.charset == 0 {
			for _, v := range tokens {
				if v <= nTokens {
					i += len(reverse[v])
				}
			}
			// Make the exact size array
			if i > len(buffer) {
				buffer = make([]byte, i)
			} else {
				buffer = buffer[0:i]
			}
			// Copy the keys into it
			i = 0
			for _, v := range tokens {
				if v <= nTokens {
					copy(buffer[i:], reverse[v])
					i += len(reverse[v])
				}
			}
			return buffer
		}
		// Get the size
		i = len(d.remainder)
		for _, v := range tokens {
			if v <= nTokens {
				i += len(reverse[v])
			}
		}
		// Make the exact size array
		if i > len(buffer) {
			buffer = make([]byte, i)
		} else {
			buffer = buffer[0:i]
		}
		// Copy the keys into it
		copy(buffer, d.remainder)
		i = len(d.remainder)
		for _, v := range tokens {
			if v <= nTokens {
				copy(buffer[i:], reverse[v])
				i += len(reverse[v])
			}
		}
		if d.vocab.charset == 1 { // UTF-8
			remaining := len(buffer) - incompleteUTF8Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		} else { // UTF-16
			remaining := len(buffer) - incompleteUTF16Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		}
		if d.vocab.usingCapcode == 2 {
			buffer = d.capcodeDecoder.Decode(buffer)
		} else if d.vocab.usingCapcode == 1 {
			buffer = d.capcodeDecoder.NoCapcodeDecode(buffer)
		}
		return buffer
	} else if encodingLength == 3 {
		var on uint64
		var to uint64 = uint64(len(b))
		var v uint32
		reverse := d.vocab.reverse
		nTokens := uint32(len(reverse))
		var i int
		if d.vocab.charset == 0 {
			for on=0; on<to; on+=3 {
				v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
				if v < nTokens {
					i += len(reverse[v])
				}
			}
			// Make the exact size array
			if i > len(buffer) {
				buffer = make([]byte, i)
			} else {
				buffer = buffer[0:i]
			}
			// Copy the keys into it
			for on=0; on<to; on+=3 {
				v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
				if v < nTokens {
					copy(buffer[i:], reverse[v])
					i += len(reverse[v])
				}
			}
			return buffer
		}
		// Get the size
		i = len(d.remainder)
		for on=0; on<to; on+=3 {
			v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
			if v < nTokens {
				i += len(reverse[v])
			}
		}
		// Make the exact size array
		if i > len(buffer) {
			buffer = make([]byte, i)
		} else {
			buffer = buffer[0:i]
		}
		// Copy the keys into it
		copy(buffer, d.remainder)
		i = len(d.remainder)
		for on=0; on<to; on+=3 {
			v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
			if v < nTokens {
				copy(buffer[i:], reverse[v])
				i += len(reverse[v])
			}
		}
		if d.vocab.charset == 1 { // UTF-8
			remaining := len(buffer) - incompleteUTF8Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		} else { // UTF-16
			remaining := len(buffer) - incompleteUTF16Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		}
		if d.vocab.usingCapcode == 2 {
			buffer = d.capcodeDecoder.Decode(buffer)
		} else if d.vocab.usingCapcode == 1 {
			buffer = d.capcodeDecoder.NoCapcodeDecode(buffer)
		}
		return buffer
	} else if encodingLength == 4 {
		var tokens []uint32
		var l uint64 = uint64(len(b)) >> 2
		if isLittleEndian {
			tokens = (*(*[]uint32)(unsafe.Pointer(&b)))[:l:l]
		} else {
			tokens = make([]uint32, l)
			var to uint64 = uint64(len(b))
			var i uint64
			for ; i<to; i+=4 {
				tokens[i >> 2] = uint32(b[i]) | (uint32(b[i+1]) << 8) | (uint32(b[i+2]) << 16) | (uint32(b[i+3]) << 24)
			}
		}
		reverse := d.vocab.reverse
		nTokens := uint32(len(reverse))
		var i int
		if d.vocab.charset == 0 {
			for _, v := range tokens {
				if v < nTokens {
					i += len(reverse[v])
				}
			}
			// Make the exact size array
			if i > len(buffer) {
				buffer = make([]byte, i)
			} else {
				buffer = buffer[0:i]
			}
			// Copy the keys into it
			i = 0
			for _, v := range tokens {
				if v < nTokens {
					copy(buffer[i:], reverse[v])
					i += len(reverse[v])
				}
			}
			return buffer
		}
		// Get the size
		i = len(d.remainder)
		for _, v := range tokens {
			if v < nTokens {
				i += len(reverse[v])
			}
		}
		// Make the exact size array
		if i > len(buffer) {
			buffer = make([]byte, i)
		} else {
			buffer = buffer[0:i]
		}
		// Copy the keys into it
		copy(buffer, d.remainder)
		i = len(d.remainder)
		for _, v := range tokens {
			if v < nTokens {
				copy(buffer[i:], reverse[v])
				i += len(reverse[v])
			}
		}
		if d.vocab.charset == 1 { // UTF-8
			remaining := len(buffer) - incompleteUTF8Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		} else { // UTF-16
			remaining := len(buffer) - incompleteUTF16Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		}
		if d.vocab.usingCapcode == 2 {
			buffer = d.capcodeDecoder.Decode(buffer)
		} else if d.vocab.usingCapcode == 1 {
			buffer = d.capcodeDecoder.NoCapcodeDecode(buffer)
		}
		return buffer
	}
	return nil
}

// Decodes tokens IDs back into bytes.
func (d *Decoder) Decode(tokens []uint32) []byte {
	if d.vocab.charset == 0 {
		return d.vocab.decode(tokens)
	}
	// Get the size
	reverse := d.vocab.reverse
	nTokens := uint32(len(reverse))
	var i int = len(d.remainder)
	for _, v := range tokens {
		if v < nTokens {
			i += len(reverse[v])
		}
	}
	// Make the exact size array
	data := make([]byte, i)
	// Copy the keys into it
	copy(data, d.remainder)
	i = len(d.remainder)
	for _, v := range tokens {
		if v < nTokens {
			copy(data[i:], reverse[v])
			i += len(reverse[v])
		}
	}
	if d.vocab.charset == 1 { // UTF-8
		remaining := len(data) - incompleteUTF8Bytes(data)
		d.remainder = data[remaining:]
		data = data[:remaining]
	} else { // UTF-16
		remaining := len(data) - incompleteUTF16Bytes(data)
		d.remainder = data[remaining:]
		data = data[:remaining]
	}
	if d.vocab.usingCapcode == 2 {
		data = d.capcodeDecoder.Decode(data)
	} else if d.vocab.usingCapcode == 1 {
		data = d.capcodeDecoder.NoCapcodeDecode(data)
	}
	return data
}

// Deserializes tokens encoded in a bytes stream into a slice of uint32 token IDs.
// `encodingLength` must be one of: 0, 2, 3, 4.
// If you enter `encodingLength` 0 then it will determine the encoding length from the vocabulary size.
func (d *Decoder) Deserialize(data []byte, encodingLength uint8) []uint32 {
	return d.vocab.Deserialize(data, encodingLength)
}

func (vocab *Vocab) Deserialize(data []byte, encodingLength uint8) (tokens []uint32) {
	if encodingLength == 0 {
		if len(vocab.reverse) <= 65536 {
			encodingLength = 2
		} else {
			encodingLength = 3
		}
	}
	if encodingLength == 2 {
		tokens = make([]uint32, len(data) / 2)
		var l uint64 = uint64(len(data))
		var i uint64
		for ; i<l; i+=2 {
			tokens[i >> 1] = uint32(data[i]) | (uint32(data[i+1]) << 8)
		}
		return
	} else if encodingLength == 3 {
		tokens = make([]uint32, len(data) / 3)
		var l uint64 = uint64(len(data))
		var i, on uint64
		for ; i<l; i+=3 {
			tokens[on] = uint32(data[i]) | (uint32(data[i+1]) << 8) | (uint32(data[i+2]) << 16)
			on++
		}
		return
	} else if encodingLength == 4 {
		tokens = make([]uint32, len(data) / 4)
		var l uint64 = uint64(len(data))
		var i uint64
		for ; i<l; i+=4 {
			tokens[i >> 2] = uint32(data[i]) | (uint32(data[i+1]) << 8) | (uint32(data[i+2]) << 16) | (uint32(data[i+3]) << 24)
		}
		return
	}
	return
}

// Decodes tokens backs into bytes.
// If you are decoding a stream of tokens individually or in batches, instead of all at once, you should use the Decode method for the Decoder struct instead.
func (vocab *Vocab) Decode(tokens []uint32) []byte {
	data := vocab.decode(tokens)
	if vocab.usingCapcode == 2 {
		return capcode.Decode(data)
	} else if vocab.usingCapcode == 1 {
		return capcode.NoCapcodeDecode(data)
	}
	return data
}

// Decodes tokens from a serialized bytes slice.
// `encodingLength` must be one of: 0, 2, 3, 4.
// If you enter `encodingLength` 0 then it will determine the encoding length from the vocabulary size.
// `buffer` is optional, you can send it `nil` and it will allocate a new slice.
// If you are decoding a stream of tokens individually or in batches, instead of all at once, you should use the Decode method for the Decoder struct instead.
func (vocab *Vocab) DecodeSerialized(b []byte, encodingLength uint8, buffer []byte) []byte {
	data := vocab.decodeSerialized(b, encodingLength, buffer)
	if vocab.usingCapcode == 2 {
		return capcode.Decode(data)
	} else if vocab.usingCapcode == 1 {
		return capcode.NoCapcodeDecode(data)
	}
	return data
}

func (vocab *Vocab) decode(tokens []uint32) []byte {
	// Get the size
	reverse := vocab.reverse
	nTokens := uint32(len(reverse))
	var i int
	for _, v := range tokens {
		if v < nTokens {
			i += len(reverse[v])
		}
	}
	// Make the exact size array
	data := make([]byte, i)
	// Copy the keys into it
	i = 0
	for _, v := range tokens {
		if v < nTokens {
			copy(data[i:], reverse[v])
			i += len(reverse[v])
		}
	}
	return data
}

func (vocab *Vocab) decodeSerialized(b []byte, encodingLength uint8, buffer []byte) []byte {
	reverse := vocab.reverse
	if encodingLength <= 1 {
		if len(reverse) <= 65536 {
			encodingLength = 2
		} else {
			encodingLength = 3
		}
	}
	if encodingLength == 2 {
		var tokens []uint16
		var l uint64 = uint64(len(b)) >> 1
		if isLittleEndian {
			tokens = (*(*[]uint16)(unsafe.Pointer(&b)))[:l:l] // interpret the serialized bytes as a slice of uint16
		} else {
			tokens = make([]uint16, l)
			var to uint64 = uint64(len(b))
			var i uint64
			for ; i<to; i+=2 {
				tokens[i >> 1] = uint16(b[i]) | (uint16(b[i+1]) << 8)
			}
		}
		if len(reverse) == 0 {
			return []byte{}
		}
		nTokens := uint16(len(reverse) - 1)
		var i int
		for _, v := range tokens {
			if v <= nTokens {
				i += len(reverse[v])
			}
		}
		// Make the exact size array
		if i > len(buffer) {
			buffer = make([]byte, i)
		} else {
			buffer = buffer[0:i]
		}
		// Copy the keys into it
		i = 0
		for _, v := range tokens {
			if v <= nTokens {
				copy(buffer[i:], reverse[v])
				i += len(reverse[v])
			}
		}
		return buffer
	} else if encodingLength == 3 {
		var on uint64
		var to uint64 = uint64(len(b))
		var v uint32
		nTokens := uint32(len(reverse))
		var i int
		for on=0; on<to; on+=3 {
			v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
			if v < nTokens {
				i += len(reverse[v])
			}
		}
		// Make the exact size array
		if i > len(buffer) {
			buffer = make([]byte, i)
		} else {
			buffer = buffer[0:i]
		}
		// Copy the keys into it
		i = 0
		for on=0; on<to; on+=3 {
			v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
			if v < nTokens {
				copy(buffer[i:], reverse[v])
				i += len(reverse[v])
			}
		}
		return buffer
	} else if encodingLength == 4 {
		var tokens []uint32
		var l uint64 = uint64(len(b)) >> 2
		if isLittleEndian {
			tokens = (*(*[]uint32)(unsafe.Pointer(&b)))[:l:l]
		} else {
			tokens = make([]uint32, l)
			var to uint64 = uint64(len(b))
			var i uint64
			for ; i<to; i+=4 {
				tokens[i >> 2] = uint32(b[i]) | (uint32(b[i+1]) << 8) | (uint32(b[i+2]) << 16) | (uint32(b[i+3]) << 24)
			}
		}
		nTokens := uint32(len(reverse))
		var i int
		for _, v := range tokens {
			if v < nTokens {
				i += len(reverse[v])
			}
		}
		// Make the exact size array
		if i > len(buffer) {
			buffer = make([]byte, i)
		} else {
			buffer = buffer[0:i]
		}
		// Copy the keys into it
		i = 0
		for _, v := range tokens {
			if v < nTokens {
				copy(buffer[i:], reverse[v])
				i += len(reverse[v])
			}
		}
		return buffer
	}
	return nil
}

// --------- TOKENIZE ---------

// Applies all normalizations to the bytes, including capcode and NFD.
func (vocab *Vocab) Normalize(data []byte) ([]byte, error) {
	return normalize(data, vocab.usingCapcode, vocab.normalizer)
}

// Tokenizes text from bytes slice to token IDs.
// The 2nd returned value (int) is the number of characters for which there were no tokens and were replaced with Unk token.
func (vocab *Vocab) Tokenize(data []byte) ([]uint32, int, error) {
	if vocab.maxTokenLength == 0 {
		return []uint32{}, 0, nil
	}
	normalized, err := normalize(data, vocab.usingCapcode, vocab.normalizer)
	if err != nil {
		return nil, 0, err
	}
	return vocab.tokenize(normalized)
}

// Tokenizes but returns the number of tokens instead of the tokens.
func (vocab *Vocab) Count(data []byte) (int, int, error) {
	if vocab.maxTokenLength == 0 {
		return 0, 0, nil
	}
	normalized, err := normalize(data, vocab.usingCapcode, vocab.normalizer)
	if err != nil {
		return 0, 0, err
	}
	return vocab.tokenizeCount(normalized)
}

// Tokenizes directly into serialized bytes with either 16-bit, 24-bit or 32-bit encoded unsigned integers depending on the vocabulary size.
// Set encodingLength to 0 for it to be chosen automatically, or set `encodingLength` to 2, 3 or 4.
// The 2rd return value is the encodingLength that was used, and the 3rd is the number of characters for which there were no tokens.
// `buffer` is an optional reusable buffer, you can send nil.
func (vocab *Vocab) TokenizeToSerialized(data []byte, encodingLength uint8, buffer []byte) ([]byte, uint8, int, error) {
	if vocab.maxTokenLength == 0 {
		return []byte{}, 2, 0, nil
	}
	if encodingLength <= 1 {
		if len(vocab.reverse) <= 65536 {
			encodingLength = 2
		} else {
			encodingLength = 3
		}
	}
	normalized, err := normalize(data, vocab.usingCapcode, vocab.normalizer)
	if err != nil {
		return nil, 0, 0, err
	}
	switch encodingLength {
		case 2:
			b, missing := vocab.tokenizeToSerialized16(normalized, buffer)
			return b, 2, missing, nil
		case 3:
			b, missing := vocab.tokenizeToSerialized24(normalized, buffer)
			return b, 3, missing, nil
		case 4:
			b, missing := vocab.tokenizeToSerialized32(normalized, buffer)
			return b, 4, missing, nil
		default:
			return nil, 0, 0, errors.New(`Invalid encoding length`)
	}
}


func (vocab Vocab) tokenize(data []byte) ([]uint32, int, error) {
	var i, i1, i2, i3, length, length1, length2, length3, length1b, length2b, length3b int
	var index, index1, index2, index3, index1b, index2b, index3b uint32
	var branchLength, missing, nWords int
	var found, found1, found2, found3 bool
	var score1, score2, score3, score1b, score2b, score3b, maxScore int
	var forwardDelete int
	var nextByte uint8
	var original tokenOuter
	var first, second tokenInner
	tokens := make([]uint32, 0, (len(data) / 4) + 4)

	lilbuf := make([]byte, vocab.maxTokenLength)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxTokenLengthWithSpace := vocab.maxTokenLength - lilbufOffset

	// Add 1 extra byte to the end because we look ahead 1 byte
	lenData := len(data)
	if cap(data) > len(data) {
		data = data[0 : len(data) + 1]
	} else {
		data2 := make([]byte, len(data) + 1)
		copy(data2, data)
		data = data2
	}

	for i < lenData {
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxTokenLength) ]); found {
			
			checkpoint:

				original = vocab.info[index].alt
				i1 = i + length

				// Skip checking alternatives if the longest first match is a single whole word of only letters: begins _A + ends A + next_is_space + 1word
				if (i1 < lenData && (original.data.flag & 32 == 0 || vocab.beginByte[data[i1]] != 12)) {
					
					score1 = -1000000
					score2 = -1000000
					score3 = -1000000
					score1b = -1000000
					score2b = -1000000
					score3b = -1000000
					maxScore = -1000000

					// First lookahead to the next token after me
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxTokenLength) ])

					if found1 {
						nWords = int(original.data.nWords) - forwardDelete
						second = vocab.info[index1].alt.data
						nextByte = vocab.beginByte[data[i1 + length1]]

						score1 = ((	length + length1 + 										// the total length of the branch
							int((original.data.flag >> 7) + (second.flag >> 7)) +			// 1 point for each token being either all letters or all punctuation
							branchless.MaxZeroAnd(nWords - 1) + 							// 1 less than the number of word beginnings in the 1st token, min 0									
							branchless.MaxZeroAnd(int(second.nWords) - 1) +					// 1 less than the number of word beginnings in the second token, min 0
							int((second.flag >> 2) & 1) +										// 1 if the second token begins with a space
							int((nextByte >> 2) & 1) +										// 1 if the next character after the 2nd token is a space
							((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -		// 100x the number of whole words covered by this and next token
							( (int(original.data.flag & 1 & (second.flag >> 1)) * 103) + 	// Deduct 103 if the first and second token split a word
							(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
							((int(second.flag & 1 & nextByte) * 3)) )) 						// Deduct 3 if the second token ends inside a word
						maxScore = score1
						
						// Check if we're in the middle of a word
						if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
							length1b = branchless.Min(lenData - i1, maxTokenLengthWithSpace)
							copy(lilbufStart, data[ i1 : i1 + length1b ])
							index1b, length1b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length1b + lilbufOffset])
							if length1b > length1 + 1 {
								length1b -= lilbufOffset
								second = vocab.info[index1b].alt.data
								nextByte = vocab.beginByte[data[i1 + length1b]]
								score1b = ((	length + length1b + 							// the total length of the branch
									int((original.data.flag >> 7) + (second.flag >> 7)) +		// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(original.data.flag & 1) * 103) + 				// Deduct 103 if the first and second token split a word
									(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									1 )) 														// Deduct 1 for using an extra token
								maxScore = branchless.Max(maxScore, score1b)
							}
						}
					}

					if original.index != DOES_NOT_EXIST {
						i2 = i + original.length - forwardDelete
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxTokenLength) ])

						if found2 {
							first = vocab.info[original.index].alt.data
							nWords = int(first.nWords) - forwardDelete
							second = vocab.info[index2].alt.data
							nextByte = vocab.beginByte[data[i2 + length2]]
							branchLength = original.length + length2 - forwardDelete

							score2 = ((	branchLength + 										// the total length of the branch
								int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
								branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
								branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
								int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
								int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
								((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
								( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
								(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
								((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
								(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
								(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
							maxScore = branchless.Max(maxScore, score2)

							// Check if we're in the middle of a word
							if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
								length2b = branchless.Min(lenData - i2, maxTokenLengthWithSpace)
								copy(lilbufStart, data[ i2 : i2 + length2b ])
								index2b, length2b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length2b + lilbufOffset])
								if length2b > length2 + 1 {
									length2b -= lilbufOffset
									second = vocab.info[index2b].alt.data
									branchLength = original.length + length2b - forwardDelete
									nextByte = vocab.beginByte[data[i2 + length2b]]
									score2b = (( branchLength + 									// the total length of the branch
										int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
										branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
										branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
										int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
										((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
										( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
										(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
										((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
										1 +															// Deduct 1 for using an extra token
										(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
										(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
									maxScore = branchless.Max(maxScore, score2b)
								}
							}
						}

						if original.index2 != DOES_NOT_EXIST {
							i3 = i + original.length2 - forwardDelete
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxTokenLength) ])

							if found3 {
								first = vocab.info[original.index2].alt.data
								nWords = int(first.nWords) - forwardDelete
								second = vocab.info[index3].alt.data
								nextByte = vocab.beginByte[data[i3 + length3]]
								branchLength = original.length2 + length3 - forwardDelete

								score3 = ((	branchLength + 										// the total length of the branch
									int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
									(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
									(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
								maxScore = branchless.Max(maxScore, score3)

								// Check if we're in the middle of a word
								if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
									length3b = branchless.Min(lenData - i3, maxTokenLengthWithSpace)
									copy(lilbufStart, data[ i3 : i3 + length3b ])
									index3b, length3b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length3b + lilbufOffset])
									if length3b > length3 + 1 {
										length3b -= lilbufOffset
										second = vocab.info[index3b].alt.data
										branchLength = original.length2 + length3b - forwardDelete
										nextByte = vocab.beginByte[data[i3 + length3b]]
										score3b = (( branchLength + 									// the total length of the branch
											int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
											(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											1 +															// Deduct 1 for using an extra token
											(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
											(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
										maxScore = branchless.Max(maxScore, score3b)
									}
								}
							}
						}
					}

					switch maxScore {
						case -1000000:
							// Do nothing
						case score1:
							tokens = append(tokens, original.id)
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							tokens = append(tokens, original.id1)
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							tokens = append(tokens, original.id2)
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							tokens = append(tokens, original.id, vocab.deleteToken)
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							tokens = append(tokens, original.id1, vocab.deleteToken)
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							tokens = append(tokens, original.id2, vocab.deleteToken)
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				tokens = append(tokens, original.id)
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.unkToken != DOES_NOT_EXIST {
				tokens = append(tokens, vocab.unkToken)
			}
			i++
			missing++
			forwardDelete = 0
		}
	}	
	return tokens, missing, nil
}

func (vocab Vocab) tokenizeCount(data []byte) (int, int, error) {
	var i, i1, i2, i3, length, length1, length2, length3, length1b, length2b, length3b int
	var index, index1, index2, index3, index1b, index2b, index3b uint32
	var branchLength, missing, nWords int
	var found, found1, found2, found3 bool
	var score1, score2, score3, score1b, score2b, score3b, maxScore int
	var forwardDelete int
	var nextByte uint8
	var original tokenOuter
	var first, second tokenInner
	var tokens int

	lilbuf := make([]byte, vocab.maxTokenLength)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxTokenLengthWithSpace := vocab.maxTokenLength - lilbufOffset

	// Add 1 extra byte to the end because we look ahead 1 byte
	lenData := len(data)
	if cap(data) > len(data) {
		data = data[0 : len(data) + 1]
	} else {
		data2 := make([]byte, len(data) + 1)
		copy(data2, data)
		data = data2
	}

	for i < lenData {
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxTokenLength) ]); found {
			
			checkpoint:

				original = vocab.info[index].alt
				i1 = i + length

				// Skip checking alternatives if the longest first match is a single whole word of only letters: begins _A + ends A + next_is_space + 1word
				if (i1 < lenData && (original.data.flag & 32 == 0 || vocab.beginByte[data[i1]] != 12)) {
					
					score1 = -1000000
					score2 = -1000000
					score3 = -1000000
					score1b = -1000000
					score2b = -1000000
					score3b = -1000000
					maxScore = -1000000

					// First lookahead to the next token after me
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxTokenLength) ])

					if found1 {
						nWords = int(original.data.nWords) - forwardDelete
						second = vocab.info[index1].alt.data
						nextByte = vocab.beginByte[data[i1 + length1]]

						score1 = ((	length + length1 + 										// the total length of the branch
							int((original.data.flag >> 7) + (second.flag >> 7)) +			// 1 point for each token being either all letters or all punctuation
							branchless.MaxZeroAnd(nWords - 1) + 							// 1 less than the number of word beginnings in the 1st token, min 0									
							branchless.MaxZeroAnd(int(second.nWords) - 1) +					// 1 less than the number of word beginnings in the second token, min 0
							int((second.flag >> 2) & 1) +										// 1 if the second token begins with a space
							int((nextByte >> 2) & 1) +										// 1 if the next character after the 2nd token is a space
							((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -		// 100x the number of whole words covered by this and next token
							( (int(original.data.flag & 1 & (second.flag >> 1)) * 103) + 	// Deduct 103 if the first and second token split a word
							(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
							((int(second.flag & 1 & nextByte) * 3)) )) 						// Deduct 3 if the second token ends inside a word
						maxScore = score1
						
						// Check if we're in the middle of a word
						if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
							length1b = branchless.Min(lenData - i1, maxTokenLengthWithSpace)
							copy(lilbufStart, data[ i1 : i1 + length1b ])
							index1b, length1b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length1b + lilbufOffset])
							if length1b > length1 + 1 {
								length1b -= lilbufOffset
								second = vocab.info[index1b].alt.data
								nextByte = vocab.beginByte[data[i1 + length1b]]
								score1b = ((	length + length1b + 							// the total length of the branch
									int((original.data.flag >> 7) + (second.flag >> 7)) +		// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(original.data.flag & 1) * 103) + 				// Deduct 103 if the first and second token split a word
									(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									1 )) 														// Deduct 1 for using an extra token
								maxScore = branchless.Max(maxScore, score1b)
							}
						}
					}

					if original.index != DOES_NOT_EXIST {
						i2 = i + original.length - forwardDelete
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxTokenLength) ])

						if found2 {
							first = vocab.info[original.index].alt.data
							nWords = int(first.nWords) - forwardDelete
							second = vocab.info[index2].alt.data
							nextByte = vocab.beginByte[data[i2 + length2]]
							branchLength = original.length + length2 - forwardDelete

							score2 = ((	branchLength + 										// the total length of the branch
								int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
								branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
								branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
								int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
								int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
								((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
								( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
								(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
								((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
								(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
								(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
							maxScore = branchless.Max(maxScore, score2)

							// Check if we're in the middle of a word
							if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
								length2b = branchless.Min(lenData - i2, maxTokenLengthWithSpace)
								copy(lilbufStart, data[ i2 : i2 + length2b ])
								index2b, length2b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length2b + lilbufOffset])
								if length2b > length2 + 1 {
									length2b -= lilbufOffset
									second = vocab.info[index2b].alt.data
									branchLength = original.length + length2b - forwardDelete
									nextByte = vocab.beginByte[data[i2 + length2b]]
									score2b = (( branchLength + 									// the total length of the branch
										int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
										branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
										branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
										int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
										((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
										( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
										(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
										((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
										1 +															// Deduct 1 for using an extra token
										(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
										(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
									maxScore = branchless.Max(maxScore, score2b)
								}
							}
						}

						if original.index2 != DOES_NOT_EXIST {
							i3 = i + original.length2 - forwardDelete
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxTokenLength) ])

							if found3 {
								first = vocab.info[original.index2].alt.data
								nWords = int(first.nWords) - forwardDelete
								second = vocab.info[index3].alt.data
								nextByte = vocab.beginByte[data[i3 + length3]]
								branchLength = original.length2 + length3 - forwardDelete

								score3 = ((	branchLength + 										// the total length of the branch
									int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
									(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
									(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
								maxScore = branchless.Max(maxScore, score3)

								// Check if we're in the middle of a word
								if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
									length3b = branchless.Min(lenData - i3, maxTokenLengthWithSpace)
									copy(lilbufStart, data[ i3 : i3 + length3b ])
									index3b, length3b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length3b + lilbufOffset])
									if length3b > length3 + 1 {
										length3b -= lilbufOffset
										second = vocab.info[index3b].alt.data
										branchLength = original.length2 + length3b - forwardDelete
										nextByte = vocab.beginByte[data[i3 + length3b]]
										score3b = (( branchLength + 									// the total length of the branch
											int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
											(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											1 +															// Deduct 1 for using an extra token
											(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
											(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
										maxScore = branchless.Max(maxScore, score3b)
									}
								}
							}
						}
					}

					switch maxScore {
						case -1000000:
							// Do nothing
						case score1:
							tokens++
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							tokens++
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							tokens++
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							tokens++
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							tokens++
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							tokens++
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				tokens++
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.unkToken != DOES_NOT_EXIST {
				tokens++
			}
			i++
			missing++
			forwardDelete = 0
		}
	}	
	return tokens, missing, nil
}

func (vocab Vocab) tokenizeToSerialized16(data []byte, buffer []byte) ([]byte, int) {
	var i, i1, i2, i3, length, length1, length2, length3, length1b, length2b, length3b int
	var index, index1, index2, index3, index1b, index2b, index3b uint32
	var branchLength, missing, nWords int
	var found, found1, found2, found3 bool
	var score1, score2, score3, score1b, score2b, score3b, maxScore int
	var forwardDelete int
	var nextByte uint8
	var original tokenOuter
	var first, second tokenInner

	length = (len(data) / 2) + 4
	if cap(buffer) > length {
		buffer = buffer[0:0]
	} else {
		buffer = make([]byte, 0, length)
	}

	lilbuf := make([]byte, vocab.maxTokenLength)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxTokenLengthWithSpace := vocab.maxTokenLength - lilbufOffset

	// Add 1 extra byte to the end because we look ahead 1 byte
	lenData := len(data)
	if cap(data) > len(data) {
		data = data[0 : len(data) + 1]
	} else {
		data2 := make([]byte, len(data) + 1)
		copy(data2, data)
		data = data2
	}

	for i < lenData {
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxTokenLength) ]); found {
			
			checkpoint:

				original = vocab.info[index].alt
				i1 = i + length

				// Skip checking alternatives if the longest first match is a single whole word of only letters: begins _A + ends A + next_is_space + 1word
				if (i1 < lenData && (original.data.flag & 32 == 0 || vocab.beginByte[data[i1]] != 12)) {
					
					score1 = -1000000
					score2 = -1000000
					score3 = -1000000
					score1b = -1000000
					score2b = -1000000
					score3b = -1000000
					maxScore = -1000000

					// First lookahead to the next token after me
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxTokenLength) ])

					if found1 {
						nWords = int(original.data.nWords) - forwardDelete
						second = vocab.info[index1].alt.data
						nextByte = vocab.beginByte[data[i1 + length1]]

						score1 = ((	length + length1 + 										// the total length of the branch
							int((original.data.flag >> 7) + (second.flag >> 7)) +			// 1 point for each token being either all letters or all punctuation
							branchless.MaxZeroAnd(nWords - 1) + 							// 1 less than the number of word beginnings in the 1st token, min 0									
							branchless.MaxZeroAnd(int(second.nWords) - 1) +					// 1 less than the number of word beginnings in the second token, min 0
							int((second.flag >> 2) & 1) +										// 1 if the second token begins with a space
							int((nextByte >> 2) & 1) +										// 1 if the next character after the 2nd token is a space
							((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -		// 100x the number of whole words covered by this and next token
							( (int(original.data.flag & 1 & (second.flag >> 1)) * 103) + 	// Deduct 103 if the first and second token split a word
							(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
							((int(second.flag & 1 & nextByte) * 3)) )) 						// Deduct 3 if the second token ends inside a word
						maxScore = score1
						
						// Check if we're in the middle of a word
						if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
							length1b = branchless.Min(lenData - i1, maxTokenLengthWithSpace)
							copy(lilbufStart, data[ i1 : i1 + length1b ])
							index1b, length1b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length1b + lilbufOffset])
							if length1b > length1 + 1 {
								length1b -= lilbufOffset
								second = vocab.info[index1b].alt.data
								nextByte = vocab.beginByte[data[i1 + length1b]]
								score1b = ((	length + length1b + 							// the total length of the branch
									int((original.data.flag >> 7) + (second.flag >> 7)) +		// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(original.data.flag & 1) * 103) + 				// Deduct 103 if the first and second token split a word
									(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									1 )) 														// Deduct 1 for using an extra token
								maxScore = branchless.Max(maxScore, score1b)
							}
						}
					}

					if original.index != DOES_NOT_EXIST {
						i2 = i + original.length - forwardDelete
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxTokenLength) ])

						if found2 {
							first = vocab.info[original.index].alt.data
							nWords = int(first.nWords) - forwardDelete
							second = vocab.info[index2].alt.data
							nextByte = vocab.beginByte[data[i2 + length2]]
							branchLength = original.length + length2 - forwardDelete

							score2 = ((	branchLength + 										// the total length of the branch
								int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
								branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
								branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
								int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
								int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
								((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
								( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
								(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
								((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
								(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
								(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
							maxScore = branchless.Max(maxScore, score2)

							// Check if we're in the middle of a word
							if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
								length2b = branchless.Min(lenData - i2, maxTokenLengthWithSpace)
								copy(lilbufStart, data[ i2 : i2 + length2b ])
								index2b, length2b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length2b + lilbufOffset])
								if length2b > length2 + 1 {
									length2b -= lilbufOffset
									second = vocab.info[index2b].alt.data
									branchLength = original.length + length2b - forwardDelete
									nextByte = vocab.beginByte[data[i2 + length2b]]
									score2b = (( branchLength + 									// the total length of the branch
										int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
										branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
										branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
										int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
										((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
										( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
										(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
										((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
										1 +															// Deduct 1 for using an extra token
										(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
										(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
									maxScore = branchless.Max(maxScore, score2b)
								}
							}
						}

						if original.index2 != DOES_NOT_EXIST {
							i3 = i + original.length2 - forwardDelete
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxTokenLength) ])

							if found3 {
								first = vocab.info[original.index2].alt.data
								nWords = int(first.nWords) - forwardDelete
								second = vocab.info[index3].alt.data
								nextByte = vocab.beginByte[data[i3 + length3]]
								branchLength = original.length2 + length3 - forwardDelete

								score3 = ((	branchLength + 										// the total length of the branch
									int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
									(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
									(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
								maxScore = branchless.Max(maxScore, score3)

								// Check if we're in the middle of a word
								if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
									length3b = branchless.Min(lenData - i3, maxTokenLengthWithSpace)
									copy(lilbufStart, data[ i3 : i3 + length3b ])
									index3b, length3b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length3b + lilbufOffset])
									if length3b > length3 + 1 {
										length3b -= lilbufOffset
										second = vocab.info[index3b].alt.data
										branchLength = original.length2 + length3b - forwardDelete
										nextByte = vocab.beginByte[data[i3 + length3b]]
										score3b = (( branchLength + 									// the total length of the branch
											int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
											(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											1 +															// Deduct 1 for using an extra token
											(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
											(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
										maxScore = branchless.Max(maxScore, score3b)
									}
								}
							}
						}
					}

					switch maxScore {
						case -1000000:
							// Do nothing
						case score1:
							buffer = append(buffer, uint8(original.id), uint8(original.id >> 8))
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							buffer = append(buffer, uint8(original.id1), uint8(original.id1 >> 8))
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							buffer = append(buffer, uint8(original.id2), uint8(original.id2 >> 8))
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8))
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							buffer = append(buffer, uint8(original.id1), uint8(original.id1 >> 8), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8))
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							buffer = append(buffer, uint8(original.id2), uint8(original.id2 >> 8), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8))
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				buffer = append(buffer, uint8(original.id), uint8(original.id >> 8))
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.unkToken != DOES_NOT_EXIST {
				index = vocab.unkToken
				buffer = append(buffer, uint8(index), uint8(index >> 8))
			}
			i++
			missing++
			forwardDelete = 0
		}
	}

	return buffer, missing
}

func (vocab Vocab) tokenizeToSerialized24(data []byte, buffer []byte) ([]byte, int) {
	var i, i1, i2, i3, length, length1, length2, length3, length1b, length2b, length3b int
	var index, index1, index2, index3, index1b, index2b, index3b uint32
	var branchLength, missing, nWords int
	var found, found1, found2, found3 bool
	var score1, score2, score3, score1b, score2b, score3b, maxScore int
	var forwardDelete int
	var nextByte uint8
	var original tokenOuter
	var first, second tokenInner

	length = (len(data) / 2) + 6
	if cap(buffer) > length {
		buffer = buffer[0:0]
	} else {
		buffer = make([]byte, 0, length)
	}

	lilbuf := make([]byte, vocab.maxTokenLength)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxTokenLengthWithSpace := vocab.maxTokenLength - lilbufOffset

	// Add 1 extra byte to the end because we look ahead 1 byte
	lenData := len(data)
	if cap(data) > len(data) {
		data = data[0 : len(data) + 1]
	} else {
		data2 := make([]byte, len(data) + 1)
		copy(data2, data)
		data = data2
	}

	for i < lenData {
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxTokenLength) ]); found {
			
			checkpoint:

				original = vocab.info[index].alt
				i1 = i + length

				// Skip checking alternatives if the longest first match is a single whole word of only letters: begins _A + ends A + next_is_space + 1word
				if (i1 < lenData && (original.data.flag & 32 == 0 || vocab.beginByte[data[i1]] != 12)) {
					
					score1 = -1000000
					score2 = -1000000
					score3 = -1000000
					score1b = -1000000
					score2b = -1000000
					score3b = -1000000
					maxScore = -1000000

					// First lookahead to the next token after me
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxTokenLength) ])

					if found1 {
						nWords = int(original.data.nWords) - forwardDelete
						second = vocab.info[index1].alt.data
						nextByte = vocab.beginByte[data[i1 + length1]]

						score1 = ((	length + length1 + 										// the total length of the branch
							int((original.data.flag >> 7) + (second.flag >> 7)) +			// 1 point for each token being either all letters or all punctuation
							branchless.MaxZeroAnd(nWords - 1) + 							// 1 less than the number of word beginnings in the 1st token, min 0									
							branchless.MaxZeroAnd(int(second.nWords) - 1) +					// 1 less than the number of word beginnings in the second token, min 0
							int((second.flag >> 2) & 1) +										// 1 if the second token begins with a space
							int((nextByte >> 2) & 1) +										// 1 if the next character after the 2nd token is a space
							((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -		// 100x the number of whole words covered by this and next token
							( (int(original.data.flag & 1 & (second.flag >> 1)) * 103) + 	// Deduct 103 if the first and second token split a word
							(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
							((int(second.flag & 1 & nextByte) * 3)) )) 						// Deduct 3 if the second token ends inside a word
						maxScore = score1
						
						// Check if we're in the middle of a word
						if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
							length1b = branchless.Min(lenData - i1, maxTokenLengthWithSpace)
							copy(lilbufStart, data[ i1 : i1 + length1b ])
							index1b, length1b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length1b + lilbufOffset])
							if length1b > length1 + 1 {
								length1b -= lilbufOffset
								second = vocab.info[index1b].alt.data
								nextByte = vocab.beginByte[data[i1 + length1b]]
								score1b = ((	length + length1b + 							// the total length of the branch
									int((original.data.flag >> 7) + (second.flag >> 7)) +		// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(original.data.flag & 1) * 103) + 				// Deduct 103 if the first and second token split a word
									(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits capcode markers from each other
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									1 )) 														// Deduct 1 for using an extra token
								maxScore = branchless.Max(maxScore, score1b)
							}
						}
					}

					if original.index != DOES_NOT_EXIST {
						i2 = i + original.length - forwardDelete
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxTokenLength) ])

						if found2 {
							first = vocab.info[original.index].alt.data
							nWords = int(first.nWords) - forwardDelete
							second = vocab.info[index2].alt.data
							nextByte = vocab.beginByte[data[i2 + length2]]
							branchLength = original.length + length2 - forwardDelete

							score2 = ((	branchLength + 										// the total length of the branch
								int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
								branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
								branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
								int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
								int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
								((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
								( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
								(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
								((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
								(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
								(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
							maxScore = branchless.Max(maxScore, score2)

							// Check if we're in the middle of a word
							if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
								length2b = branchless.Min(lenData - i2, maxTokenLengthWithSpace)
								copy(lilbufStart, data[ i2 : i2 + length2b ])
								index2b, length2b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length2b + lilbufOffset])
								if length2b > length2 + 1 {
									length2b -= lilbufOffset
									second = vocab.info[index2b].alt.data
									branchLength = original.length + length2b - forwardDelete
									nextByte = vocab.beginByte[data[i2 + length2b]]
									score2b = (( branchLength + 									// the total length of the branch
										int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
										branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
										branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
										int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
										((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
										( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
										(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
										((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
										1 +															// Deduct 1 for using an extra token
										(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
										(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
									maxScore = branchless.Max(maxScore, score2b)
								}
							}
						}

						if original.index2 != DOES_NOT_EXIST {
							i3 = i + original.length2 - forwardDelete
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxTokenLength) ])

							if found3 {
								first = vocab.info[original.index2].alt.data
								nWords = int(first.nWords) - forwardDelete
								second = vocab.info[index3].alt.data
								nextByte = vocab.beginByte[data[i3 + length3]]
								branchLength = original.length2 + length3 - forwardDelete

								score3 = ((	branchLength + 										// the total length of the branch
									int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((second.flag >> 2) & 1) +									// 1 if the second token begins with a space
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 			// Deduct 103 if the first and second token split a word
									(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
									(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
								maxScore = branchless.Max(maxScore, score3)

								// Check if we're in the middle of a word
								if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
									length3b = branchless.Min(lenData - i3, maxTokenLengthWithSpace)
									copy(lilbufStart, data[ i3 : i3 + length3b ])
									index3b, length3b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length3b + lilbufOffset])
									if length3b > length3 + 1 {
										length3b -= lilbufOffset
										second = vocab.info[index3b].alt.data
										branchLength = original.length2 + length3b - forwardDelete
										nextByte = vocab.beginByte[data[i3 + length3b]]
										score3b = (( branchLength + 									// the total length of the branch
											int((first.flag >> 7) + (second.flag >> 7)) +					// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
											(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											1 +															// Deduct 1 for using an extra token
											(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
											(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
										maxScore = branchless.Max(maxScore, score3b)
									}
								}
							}
						}
					}

					switch maxScore {
						case -1000000:
							// Do nothing
						case score1:
							buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(original.id >> 16))
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							buffer = append(buffer, uint8(original.id1), uint8(original.id1 >> 8), uint8(original.id1 >> 16))
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							buffer = append(buffer, uint8(original.id2), uint8(original.id2 >> 8), uint8(original.id2 >> 16))
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(original.id >> 16), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16))
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							buffer = append(buffer, uint8(original.id1), uint8(original.id1 >> 8), uint8(original.id1 >> 16), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16))
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							buffer = append(buffer, uint8(original.id2), uint8(original.id2 >> 8), uint8(original.id2 >> 16), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16))
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(original.id >> 16))
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.unkToken != DOES_NOT_EXIST {
				index = vocab.unkToken
				buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16))
			}
			i++
			missing++
			forwardDelete = 0
		}
	}
	
	return buffer, missing
}

func (vocab Vocab) tokenizeToSerialized32(data []byte, buffer []byte) ([]byte, int) {
	var i, i1, i2, i3, length, length1, length2, length3, length1b, length2b, length3b int
	var index, index1, index2, index3, index1b, index2b, index3b uint32
	var branchLength, missing, nWords int
	var found, found1, found2, found3 bool
	var score1, score2, score3, score1b, score2b, score3b, maxScore int
	var forwardDelete int
	var nextByte uint8
	var original tokenOuter
	var first, second tokenInner

	length = len(data) + 8
	if cap(buffer) > length {
		buffer = buffer[0:0]
	} else {
		buffer = make([]byte, 0, length)
	}

	lilbuf := make([]byte, vocab.maxTokenLength)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxTokenLengthWithSpace := vocab.maxTokenLength - lilbufOffset

	// Add 1 extra byte to the end because we look ahead 1 byte
	lenData := len(data)
	if cap(data) > len(data) {
		data = data[0 : len(data) + 1]
	} else {
		data2 := make([]byte, len(data) + 1)
		copy(data2, data)
		data = data2
	}

	for i < lenData {
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxTokenLength) ]); found {
			
			checkpoint:

				original = vocab.info[index].alt
				i1 = i + length

				// Skip checking alternatives if the longest first match is a single whole word of only letters: begins _A + ends A + next_is_space + 1word
				if (i1 < lenData && (original.data.flag & 32 == 0 || vocab.beginByte[data[i1]] != 12)) {
					
					score1 = -1000000
					score2 = -1000000
					score3 = -1000000
					score1b = -1000000
					score2b = -1000000
					score3b = -1000000
					maxScore = -1000000

					// First lookahead to the next token after me
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxTokenLength) ])

					if found1 {
						nWords = int(original.data.nWords) - forwardDelete
						second = vocab.info[index1].alt.data
						nextByte = vocab.beginByte[data[i1 + length1]]

						score1 = ((	length + length1 + 										// the total length of the branch
							int((original.data.flag >> 7) + (second.flag >> 7)) +			// 1 point for each token being either all letters or all punctuation
							branchless.MaxZeroAnd(nWords - 1) + 							// 1 less than the number of word beginnings in the 1st token, min 0									
							branchless.MaxZeroAnd(int(second.nWords) - 1) +					// 1 less than the number of word beginnings in the second token, min 0
							int((second.flag >> 2) & 1) +										// 1 if the second token begins with a space
							int((nextByte >> 2) & 1) +										// 1 if the next character after the 2nd token is a space
							((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -		// 100x the number of whole words covered by this and next token
							( (int(original.data.flag & 1 & (second.flag >> 1)) * 103) + 	// Deduct 103 if the first and second token split a word
							(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Deduct 100 if it splits a capcode token
							((int(second.flag & 1 & nextByte) * 3)) )) 						// Deduct 3 if the second token ends inside a word
						maxScore = score1
						
						// Check if we're in the middle of a word
						if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
							length1b = branchless.Min(lenData - i1, maxTokenLengthWithSpace)
							copy(lilbufStart, data[ i1 : i1 + length1b ])
							index1b, length1b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length1b + lilbufOffset])
							if length1b > length1 + 1 {
								length1b -= lilbufOffset
								second = vocab.info[index1b].alt.data
								nextByte = vocab.beginByte[data[i1 + length1b]]
								score1b = ((	length + length1b + 							// the total length of the branch
									int((original.data.flag >> 7) + (second.flag >> 7)) +		// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(original.data.flag & 1) * 103) + 					// Deduct 103 if the first and second token split a word
									(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Deduct 100 if it splits a capcode token
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									1 )) 														// Deduct 1 for using an extra token
								maxScore = branchless.Max(maxScore, score1b)
							}
						}
					}

					if original.index != DOES_NOT_EXIST {
						i2 = i + original.length - forwardDelete
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxTokenLength) ])

						if found2 {
							first = vocab.info[original.index].alt.data
							nWords = int(first.nWords) - forwardDelete
							second = vocab.info[index2].alt.data
							nextByte = vocab.beginByte[data[i2 + length2]]
							branchLength = original.length + length2 - forwardDelete

							score2 = ((	branchLength + 										// the total length of the branch
								int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
								branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
								branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
								int((second.flag >> 2) & 1) +								// 1 if the second token begins with a space
								int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
								((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
								( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 		// Deduct 103 if the first and second token split a word
								(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +	// Deduct 100 if it splits a capcode token
								((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
								(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
								(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
							maxScore = branchless.Max(maxScore, score2)

							// Check if we're in the middle of a word
							if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
								length2b = branchless.Min(lenData - i2, maxTokenLengthWithSpace)
								copy(lilbufStart, data[ i2 : i2 + length2b ])
								index2b, length2b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length2b + lilbufOffset])
								if length2b > length2 + 1 {
									length2b -= lilbufOffset
									second = vocab.info[index2b].alt.data
									branchLength = original.length + length2b - forwardDelete
									nextByte = vocab.beginByte[data[i2 + length2b]]
									score2b = (( branchLength + 									// the total length of the branch
										int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
										branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
										branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
										int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
										((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
										( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
										(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +	// Deduct 100 if it splits a capcode token
										((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
										1 +															// Deduct 1 for using an extra token
										(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
										(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
									maxScore = branchless.Max(maxScore, score2b)
								}
							}
						}

						if original.index2 != DOES_NOT_EXIST {
							i3 = i + original.length2 - forwardDelete
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxTokenLength) ])

							if found3 {
								first = vocab.info[original.index2].alt.data
								nWords = int(first.nWords) - forwardDelete
								second = vocab.info[index3].alt.data
								nextByte = vocab.beginByte[data[i3 + length3]]
								branchLength = original.length2 + length3 - forwardDelete

								score3 = ((	branchLength + 										// the total length of the branch
									int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
									int((second.flag >> 2) & 1) +								// 1 if the second token begins with a space
									int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
									( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 		// Deduct 103 if the first and second token split a word
									(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +	// Deduct 100 if it splits a capcode token
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
									(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
								maxScore = branchless.Max(maxScore, score3)

								// Check if we're in the middle of a word
								if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
									length3b = branchless.Min(lenData - i3, maxTokenLengthWithSpace)
									copy(lilbufStart, data[ i3 : i3 + length3b ])
									index3b, length3b, _ = vocab.dictionary.LongestSubstring(lilbuf[:length3b + lilbufOffset])
									if length3b > length3 + 1 {
										length3b -= lilbufOffset
										second = vocab.info[index3b].alt.data
										branchLength = original.length2 + length3b - forwardDelete
										nextByte = vocab.beginByte[data[i3 + length3b]]
										score3b = (( branchLength + 									// the total length of the branch
											int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(first.flag & 1) * 103) + 							// Deduct 103 if the first and second token split a word
											(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +	// Deduct 100 if it splits a capcode token
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											1 +															// Deduct 1 for using an extra token
											(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
											(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
										maxScore = branchless.Max(maxScore, score3b)
									}
								}
							}
						}
					}

					switch maxScore {
						case -1000000:
							// Do nothing
						case score1:
							buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(original.id >> 16), 0)
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							buffer = append(buffer, uint8(original.id1), uint8(original.id1 >> 8), uint8(original.id1 >> 16), 0)
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							buffer = append(buffer, uint8(original.id2), uint8(original.id2 >> 8), uint8(original.id2 >> 16), 0)
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(original.id >> 16), 0, uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16), 0)
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							buffer = append(buffer, uint8(original.id1), uint8(original.id1 >> 8), uint8(original.id1 >> 16), 0, uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16), 0)
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							buffer = append(buffer, uint8(original.id2), uint8(original.id2 >> 8), uint8(original.id2 >> 16), 0, uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16), 0)
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				buffer = append(buffer, uint8(original.id), uint8(original.id >> 8), uint8(original.id >> 16), 0)
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.unkToken != DOES_NOT_EXIST {
				index = vocab.unkToken
				buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16), 0)
			}
			i++
			missing++
			forwardDelete = 0
		}
	}
	
	return buffer, missing
}

// --------- GENERAL FUNCTIONS ---------

// Info struct allows access to detailed information about each token from TokensDetailed().
// Token is the token still encoded with capcode.
// TokenDecoded is the decoded form of the token, however the token can be modified by a previous token in a sequence so this cannot be used for decoding.
// Type is 0 for regular tokens, 1 for character tokens, 2 for special tokens, 3 for UNK token.
// The Score is the percentage of the training dataset that this token covered and is used for sorting the tokens by their importance.
type Info struct {
	Id uint32
	Token []byte
	TokenDecoded []byte
	Type uint8 // 0 = regular, 1 = character, 2 = special, 3 = unk
	Score float32
}

// Returns a slice of Info struct where the index is the Token ID
func (vocab *Vocab) TokensDetailed() []Info {
	infos := make([]Info, vocab.vocabSize)
	var info Info
	var on int
	vocabinfo := vocab.info
	for i, _ := range vocab.info {
		if vocabinfo[i].score < -0.5 { // skip "duplicate" tokens
			continue
		}
		info.Id = vocabinfo[i].alt.id
		info.Token = unleak(vocabinfo[i].token)
		switch vocab.usingCapcode {
			case 1:
				info.TokenDecoded = capcode.NoCapcodeDecode(unleak(vocabinfo[i].token))
			case 2:
				info.TokenDecoded = capcode.Decode(unleak(vocabinfo[i].token))
			default:
				info.TokenDecoded = info.Token
		}
		info.Type = 0
		if len(info.Token) == 1 {
			info.Type = 1
		} else {
			if vocabinfo[i].alt.data.flag & 64 != 0 {
				info.Type = 2
			}
		}
		info.Score = vocabinfo[i].score
		infos[on] = info
		on++
	}
	if vocab.unkToken != DOES_NOT_EXIST {
		infos[on].Id = vocab.unkToken
		infos[on].Type = 3
	}
	return infos
}

// Returns the token IDs and the corresponding tokens of only the.
// Set `decode` to false to receive the decoded form of the tokens.
func (vocab *Vocab) SpecialTokens() []Info {
	info := vocab.info
	var list []Info
	for i:=0; i<len(info); i++ {
		if info[i].alt.data.flag & 64 != 0 {
			if info[i].score < -0.5 { // skip "duplicate" tokens
				continue
			}
			var special Info
			special.Id = info[i].alt.id
			special.Type = 2
			special.Token = unleak(info[i].token)
			special.Score = info[i].score
			switch vocab.usingCapcode {
				case 1:
					special.TokenDecoded = capcode.NoCapcodeDecode(unleak(info[i].token))
				case 2:
					special.TokenDecoded = capcode.Decode(unleak(info[i].token))
				default:
					special.TokenDecoded = special.Token
			}
			list = append(list, special)
		}
	}
	return list
}

// Returns the number of special tokens in the vocabulary.
func (vocab *Vocab) NumSpecialTokens() int {
	info := vocab.info
	var num int
	for i:=0; i<len(info); i++ {
		if info[i].alt.data.flag & 64 != 0 && info[i].score > -0.5 {
			num++
		}
	}
	return num
}

// Returns a slice of all tokens in the vocabulary (excluding UNK), in their encoded capcode form.
func (vocab *Vocab) Tokens() [][]byte {
	var on int
	list := make([][]byte, vocab.vocabSize)
	for _, v := range vocab.info {
		if v.score > -0.5 { // exclude "duplicate" tokens
			list[on] = unleak(v.token)
			on++
		}
	}
	return list
}

// Returns the encoded token for the token ID, or nil if it does not exist.
func (vocab *Vocab) IdToToken(id uint32) []byte {
	if id >= uint32(len(vocab.reverse)) {
		return nil
	}
	return unleak(vocab.reverse[id])
}

// Returns the ID of the Unk token.
// It will return 16777215 if there is no Unk token. You can use HasUnk() to first check if there is an UNK token.
func (vocab *Vocab) Unk() uint32 {
	return vocab.unkToken
}

// Returns true if the vocabulary is using the UNK token.
// If used, the UNK token ID is used whenever a character being tokenized doesn't exist in the vocabulary.
func (vocab *Vocab) HasUnk() bool {
	return vocab.unkToken != DOES_NOT_EXIST
}

// Decodes capcode from the bytes.
func (vocab *Vocab) Denormalize(b []byte) []byte {
	switch vocab.usingCapcode {
		case 1:
			return capcode.NoCapcodeDecode(unleak(b))
		case 2:
			return capcode.Decode(unleak(b))
		default:
			return b
	}
}

// Returns the ID of the token from bytes.
// This only works for capcode encoded tokens.
// Apply `Normalize` to the bytes first to use this with decoded tokens.
func (vocab *Vocab) TokenToId(b []byte) (uint32, bool) {
	index, found := vocab.dictionary.Find(b)
	if found {
		return vocab.info[index].alt.id, true
	}
	return 0, false
}

// Returns number of tokens in the vocabulary, inluding UNK token if it is used.
func (vocab *Vocab) Len() int {
	return vocab.vocabSize
}

// The length of the longest (encoded) token in the vocabulary.
// This can be lower than that chosen during training if none of the longer tokens were chosen.
func (vocab *Vocab) MaxTokenLength() int {
	return vocab.maxTokenLength
}

// A slice that contains all the single byte tokens in the vocabulary.
// Note that this is returned as only a slice of bytes, not a slice of slice of bytes.
func (vocab *Vocab) SingleByteTokens() []byte {
	info := vocab.info
	var i int
	lst := make([]byte, 256)
	for ; i<len(info); i++ {
		if len(info[i].token) == 1 {
			lst[i] = info[i].token[0]
		} else {
			break
		}
	}
	return lst[0:i]
}

// The number of single byte tokens in the vocabulary.
func (vocab *Vocab) NumSingleByteTokens() int {
	info := vocab.info
	var num int
	for i:=0; i<len(info)-1; i++ {
		if len(info[i].token) == 1 {
			num++
		} else {
			break
		}
	}
	return num
}

// The charset code for the vocabulary.
// 0 = None, 1 = UTF-8, 2 = UTF-16.
func (vocab *Vocab) Charset() uint8 {
	return vocab.charset
}

// The capcode level.
// 0 = disabled, 1 = deleteToken only, 2 = fully enabled.
func (vocab *Vocab) Capcode() uint8 {
	return vocab.usingCapcode
}

// The original filter for training the vocabulary.
// 0 = unfiltered, 1 = clean, 2 = balanced, 3 = consistent, 4 = strict, 5 = not trained with trainvocab.
func (vocab *Vocab) Mode() uint8 {
	return vocab.level
}

// The type of normalization applied automatically when tokenizing.
// Returns a string.
func (vocab *Vocab) Normalization() string {
	return vocab.normalizer.String()
}

// The type of normalization applied automatically when tokenizing.
// Returns a uint8.
func (vocab *Vocab) NormalizationCode() uint8 {
	return vocab.normalizer.Flag
}

// The number of tokens deleted from the vocabulary.
// These can be restored by resizing the vocabulary to be be larger.
func (vocab *Vocab) NumDeletedTokens() int {
	return len(vocab.deleted)
}

// Returns the uint8 code corresponding to the training parameters for single byte tokens.
func (vocab *Vocab) SingleBytesTrainingCode() uint8 {
	return vocab.reserve
}

// Returns the value of the highest token ID.
func (vocab *Vocab) HighestTokenID() int {
	return len(vocab.reverse) - 1
}

// --------- LOADING AND SAVING ---------

// Save the vocabulary to local file.
func (vocab Vocab) Save(outputFilename string) error {
	fi, err := os.Create(outputFilename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewWriter(fi)
	defer w.Close()

	w.WriteByte(vocab.usingCapcode)
	w.WriteByte(vocab.charset)
	w.WriteByte(vocab.normalizer.Flag)
	w.WriteByte(vocab.level)
	w.WriteByte(vocab.reserve)
	w.WriteByte(0) // reserved
	w.WriteByte(0) // reserved
	w.WriteByte(0) // reserved

	w.WriteUint24(vocab.unkToken)
	w.WriteUint24(uint32(vocab.vocabSize))
	w.WriteUint24(uint32(len(vocab.reverse)))
	w.WriteUint24(uint32(len(vocab.info)))
	w.WriteUint24(vocab.deleteToken)
	w.WriteByte(uint8(vocab.maxTokenLength))

	for i, token := range vocab.info {
		w.WriteBytes8(token.token) // a single byte (uint8) specifying length of token bytes, and then that many bytes
		w.WriteByte(token.alt.data.flag)
		w.WriteByte(token.alt.data.nWords)
		// Write the index of the token
		w.WriteUint24(token.alt.index)
		w.WriteUint24(token.alt.index2)
		w.WriteUint24(token.alt.id)
		// The index of the token should always be less than the current index (because the list is sorted), check this is true
		if (token.alt.index > uint32(i) && token.alt.index != DOES_NOT_EXIST) || (token.alt.index2 > uint32(i) && token.alt.index2 != DOES_NOT_EXIST) {
			return errors.New(`Vocabulary is corrupt and cannot be saved`)
		}
		w.WriteFloat32(token.score)
	}

	for i:=0; i<256; i++ {
		w.WriteByte(vocab.beginByte[i])
	}

	w.WriteUint24(uint32(len(vocab.deleted)))
	for _, deleted := range vocab.deleted {
		w.WriteBytes8(deleted.token)
		w.WriteUint24(deleted.id)
		w.WriteFloat32(deleted.score)
	}
	return nil
}

// Load the vocabulary from a local file.
func Load(filename string) (*Vocab, error) {
	var token tokenInfo
	var key []byte
	var res Vocab
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	r := custom.NewReader(fi)
	res.usingCapcode = r.ReadByte()
	res.charset = r.ReadByte()
	res.normalizer.Flag = r.ReadByte()
	res.level = r.ReadByte()
	res.reserve = r.ReadByte()
	r.ReadByte() // reserved byte
	r.ReadByte() // reserved byte
	r.ReadByte() // reserved byte

	if res.charset > 2 || res.usingCapcode > 2 {
		return nil, errors.New(`Not a valid TokenMonster vocabulary.`)
	}

	res.unkToken = r.ReadUint24()
	res.vocabSize = int(r.ReadUint24())
	nReverse := r.ReadUint24()
	nInfo := int(r.ReadUint24())
	res.deleteToken = r.ReadUint24()
	res.maxTokenLength = int(r.ReadByte())

	res.info = make([]tokenInfo, nInfo)
	res.reverse = make([][]byte, nReverse)
	res.dictionary = new(pansearch.Fast)
	lengths := make([]int, nInfo)

	for i:=0; i<nInfo; i++ {
		token = tokenInfo{}
		key = r.ReadBytes8()
		lengths[i] = len(key)
		if len(key) > 40 {
			return nil, errors.New(`Not a valid TokenMonster vocabulary.`)
		}
		token.token = key
		res.dictionary.Add(key)
		token.alt.data.flag = r.ReadByte()
		token.alt.data.nWords = r.ReadByte()
		token.alt.index = r.ReadUint24()
		if token.alt.index != DOES_NOT_EXIST {
			token.alt.length = lengths[token.alt.index]
			token.alt.id1 = res.info[token.alt.index].alt.id
		}
		token.alt.index2 = r.ReadUint24()
		if token.alt.index2 != DOES_NOT_EXIST {
			token.alt.length2 = lengths[token.alt.index2]
			token.alt.id2 = res.info[token.alt.index2].alt.id
		}
		token.alt.id = r.ReadUint24()
		token.score = r.ReadFloat32()
		res.info[i] = token
		res.reverse[token.alt.id] = key
	}

	for i:=0; i<256; i++ {
		res.beginByte[i] = r.ReadByte()
	}

	l := int(r.ReadUint24())
	if l > 0 {
		res.deleted = make([]deletedStruct, l)
		for i:=0; i<l; i++ {
			res.deleted[i].token = r.ReadBytes8()
			res.deleted[i].id = r.ReadUint24()
			res.deleted[i].score = r.ReadFloat32()
		}
	}
	if r.EOF() != nil {
		return nil, errors.New(`Not a valid TokenMonster vocabulary.`)
	}
	res.dictionary.Build()
	return &res, nil
}

// --------- GENERATE & MODIFY ---------

// NewVocab makes a fresh vocabulary from a custom list of tokens.
// If you generated your vocabulary with TokenMonster tools, you will not be using this function but instead using `Load`.
func NewVocab(tokens [][]byte, specialTokens [][]byte, charset uint8, normalization string, usingCapcode uint8, include256bytes bool, include128bytes bool, includeUTF8bytes bool, includeASCIIbytes bool, includeExtendedBytes bool, excludeOtherBytes bool) (*Vocab, error) {
	var reserve uint8
	if include256bytes {
		reserve |= 1 << 0
	}
	if include128bytes {
		reserve |= 1 << 1
	}
	if includeUTF8bytes {
		reserve |= 1 << 2
	}
	if includeASCIIbytes {
		reserve |= 1 << 3
	}
	if includeExtendedBytes {
		reserve |= 1 << 4
	}
	if excludeOtherBytes {
		reserve |= 1 << 5
	}
	vocab := new(Vocab)
	err := vocab.PrivateGenerateVocab(nil, nil, nil, tokens, nil, specialTokens, nil, charset, normalization, usingCapcode, 5, reserve, 0, true)
	return vocab, err
}

// NewVocabFromYAML makes a fresh vocabulary from a YAML file.
func NewVocabFromYAML(yml []byte) (*Vocab, error) {
	vocab := new(Vocab)
	err := vocab.PrivateGenerateVocab(yml, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, 0, false)
	return vocab, err
}

// Adds a single token to the vocabulary.
// Modifying a vocabulary does not change existing token IDs.
// All normalization and capcode is applied automatically.
func (vocab *Vocab) AddToken(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, nil, [][]byte{token}, nil, nil, nil, 0, ``, 0, 0, 0, 0, false)
}

// Adds a single special token to the vocabulary.
// A special token is special because only this token is allowed to tokenize text containing this.
// If any regular tokens contain your special token within them, they will be deleted.
// Modifying a vocabulary does not change existing token IDs.
// All normalization and capcode is applied automatically.
func (vocab *Vocab) AddSpecialToken(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, nil, [][]byte{token}, nil, 0, ``, 0, 0, 0, 0, false)
}

// Deletes a single token from the vocabulary.
// Tokens to delete can be capcoded encoded or not, it will look for both.
// Modifying a vocabulary does not change existing token IDs.
func (vocab *Vocab) DeleteToken(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, [][]byte{token}, nil, nil, 0, ``, 0, 0, 0, 0, false)
}

// Deletes a single token from the vocabulary by specifying the ID.
// Modifying a vocabulary does not change existing token IDs.
func (vocab *Vocab) DeleteTokenID(id uint32) {
	yml := []byte("delete:\n  - id: " + conv.String(int(id)))
	vocab.PrivateGenerateVocab(yml, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, 0, false)
}

// Resets all the IDs of the tokens to be assigned alphabetically, starting from 0, with no gaps.
func (vocab *Vocab) ResetTokenIds(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, 0, true)
}

// Adds multiple regular and optionally special tokens.
// You can use `size` to resize the vocabulary to keep it at a specific size.
// Enter `size` 0 to not resize.
// Modifying a vocabulary does not change existing token IDs.
func (vocab *Vocab) AddTokens(addTokens [][]byte, specialTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, addTokens, nil, specialTokens, nil, 0, ``, 0, 0, 0, size, false)
}

// Add multiple special tokens and optionally resize.
// Enter `size` 0 to not resize.
// Modifying a vocabulary does not change existing token IDs.
func (vocab *Vocab) AddSpecialTokens(specialTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, nil, specialTokens, nil, 0, ``, 0, 0, 0, size, false)
}

// Delete multiple tokens and optionally resize.
// Tokens to delete can be capcoded encoded or not, it will look for both.
// Enter `size` 0 to not resize.
// Modifying a vocabulary does not change existing token IDs.
func (vocab *Vocab) DeleteTokens(deleteTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, deleteTokens, nil, nil, 0, ``, 0, 0, 0, size, false)
}

// Add regular & special tokens, delete tokens and resize, all in one.
// Modifying a vocabulary does not change existing token IDs.
// Pass resetTokenIds = true to ensure there are no gaps in the token IDs.
func (vocab *Vocab) ModifyVocabulary(addTokens [][]byte, specialTokens [][]byte, deleteTokens [][]byte, size int, resetTokenIds bool) {
	vocab.PrivateGenerateVocab(nil, nil, nil, addTokens, deleteTokens, specialTokens, nil, 0, ``, 0, 0, 0, size, resetTokenIds)
}

// Add regular & special tokens, delete tokens and resize, all in one.
// Modifying a vocabulary does not change existing token IDs.
// Pass resetTokenIds = true to ensure there are no gaps in the token IDs.
func (vocab *Vocab) ModifyVocabularyFromYAML(yml []byte, size int, resetTokenIds bool) {
	vocab.PrivateGenerateVocab(yml, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, size, resetTokenIds)
}

// Resize the vocabulary by deleting the worst scoring tokens.
// You can also resize the vocabulary to be larger if any tokens have previously been deleted.
// Modifying a vocabulary does not change existing token IDs.
func (vocab *Vocab) Resize(size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, size, false)
}

// Enables the UNK token.
// Returns true if successful, returns false if an UNK token is not applicable to this vocabulary (all bytes have tokens).
// If enabled, UNK token will be inserted for every character for which there is no token.
// You can resize after this if you want to keep the vocabulary sized as it was before, otherwise it will be 1 larger.
func (vocab *Vocab) EnableUnkToken() bool {
	if len(vocab.reverse) == 0 {
		vocab.unkToken = DOES_NOT_EXIST - 1 // this means it'll be added on the end after vocab is built
	} else {
		if vocab.unkToken != DOES_NOT_EXIST {
			return true
		}
		if !canHaveUnkToken(vocab.NumSingleByteTokens(), vocab.usingCapcode) {
			return false
		}
		vocab.vocabSize++
		// Look for a free ID
		for i, v := range vocab.reverse {
			if v == nil {
				vocab.unkToken = uint32(i)
				return true
			}
		}
		// If no free IDs, add to the end
		vocab.unkToken = uint32(len(vocab.reverse))
		vocab.reverse = append(vocab.reverse, nil)
	}
	return true
}

// Disables the UNK token.
// Without an UNK token, a character that has no token to represent it will be ignored.
func (vocab *Vocab) DisableUnkToken() {
	if vocab.unkToken == DOES_NOT_EXIST {
		return
	}
	if int(vocab.unkToken) == len(vocab.reverse) - 1 {
		vocab.reverse = vocab.reverse[0:vocab.unkToken]
	}
	vocab.unkToken = DOES_NOT_EXIST
	if vocab.vocabSize > 0 {
		vocab.vocabSize--
	}
}

// Don't use this function, it's exported because it's used by the exportvocab tool.
func (vocab *Vocab) PrivateGenerateVocab(yamlData []byte, tokens [][]byte, scores []float32, addTokens [][]byte, deleteTokens [][]byte, specialTokens [][]byte, specialTokensEncoded [][]byte, charset uint8, normalizeString string, usingCapcode uint8, level uint8, reserve uint8, resize int, resetTokenIds bool) error {

	if len(vocab.info) == 0 && vocab.unkToken == 0 {
		vocab.unkToken = DOES_NOT_EXIST
	}

	// Parse YAML data
	var y YamlVocab
	var err error
	var enableUnk bool
	var displayReserve uint8
	if len(yamlData) > 3 {
		y, err = yamlParse(yamlData)
		if err != nil {
			return err
		}
		switch strings.ToLower(y.Charset) {
			case "utf8":
				fallthrough
			case "utf-8":
				charset = 1
			case "utf16":
				fallthrough
			case "utf-16":
				charset = 2
		}
		normalizeString = y.Normalization
		usingCapcode = uint8(branchless.Max(int(usingCapcode), y.Capcode))
		resetTokenIds = resetTokenIds || y.ResetTokenIds
		if y.Include256Bytes {
			reserve |= 1 << 0
		}
		if y.Include128Bytes {
			reserve |= 1 << 1
		}
		if y.IncludeUtf8Bytes {
			reserve |= 1 << 2
		}
		if y.IncludeAsciiBytes {
			reserve |= 1 << 3
		}
		if y.IncludeExtendedBytes {
			reserve |= 1 << 4
		}
		if y.ExcludeOtherBytes {
			reserve |= 1 << 5
		}
		if y.Unk {
			enableUnk = true
			if y.UnkId != nil {
				if *y.UnkId < 0 || *y.UnkId >= DOES_NOT_EXIST {
					return errors.New(`YAML Error: UnkId must be between 0 and 16777213`)
				}
				vocab.unkToken = uint32(*y.UnkId)
			}
		}
		if y.TrainingParam != nil {
			var v uint16 = uint16(*y.TrainingParam)
			if vocab.level == 0 && level == 0 {
				level = uint8(v & 7)
			}
			displayReserve = uint8(v >> 3)
		} else if level == 0 {
			level = 5
		}
	}

	// Note, tokens is assumed already to be capcoded and normalized
	// addTokens and deleteTokens are assumed to be not capcoded or normalized, and so this is applied to them
	// To generate a full vocabulary from all custom tokens, you can leave `tokens` empty and put them all in `addTokens`
	if len(vocab.info) == 0 {
		vocab.charset = charset
		vocab.usingCapcode = usingCapcode
		vocab.level = level
		vocab.normalizer, err = norm.NewNormalizer(normalizeString)
		if err != nil {
			return err
		}
	} else {
		charset = vocab.charset
		usingCapcode = vocab.usingCapcode
	}
	charTable := make([]bool, 256)
	if reserve & 1 != 0 {
		gen256bytes(charTable, usingCapcode)
	}
	if reserve & 2 != 0 {
		gen128bytes(charTable, usingCapcode)
	}
	if reserve & 4 != 0 {
		genUTF8bytes(charTable, usingCapcode)
	}
	if reserve & 8 != 0 {
		genASCIIbytes(charTable, usingCapcode)
	}
	if reserve & 16 != 0 {
		genExtendedbytes(charTable, usingCapcode, vocab.normalizer)
	}
	excludeOtherBytes := (reserve & 32) != 0
	vocab.reserve = vocab.reserve | reserve

	specialMap := make(map[string]bool)
	scoresMap := make(map[string]float32)
	idsMap := make(map[string]uint32)
	used := make(map[uint32]bool)
	deleter := make(map[string]bool)
	deleteById := make(map[uint32]bool)
	originalSpecialTokens := specialTokensEncoded
	var s string
	var tok []byte

	// Parse YAML
	if len(yamlData) > 3 {
		for _, v := range y.Regular {
			if len(v.Token) > 0 {
				v.Token, err = decodeHex(v.Token)
				if err != nil {
					return errors.New(`Invalid TokenMonster hex encoding: ` + v.Token)
				}
				if !v.Encoded {
					tok, err = normalize([]byte(v.Token), usingCapcode, vocab.normalizer)
					if err != nil {
						continue
					}
					s = string(tok)
				} else {
					tok = []byte(v.Token)
					s = v.Token
				}
				tokens = append(tokens, tok)
				if v.Score > 0 {
					scoresMap[s] = float32(v.Score)
				}
				if v.Id != nil {
					if *v.Id < 0 || *v.Id >= DOES_NOT_EXIST-1 {
						return errors.New(`YAML Error: Id must be between 0 and 16777213`)
					}
					idsMap[s] = uint32(*v.Id)
					used[uint32(*v.Id)] = true
				}
			}
		}
		for _, v := range y.Special {
			if len(v.Token) > 0 {
				v.Token, err = decodeHex(v.Token)
				if err != nil {
					return errors.New(`Invalid TokenMonster hex encoding: ` + v.Token)
				}
				if !v.Encoded {
					tok, err = normalize([]byte(v.Token), usingCapcode, vocab.normalizer)
					if err != nil {
						continue
					}
					s = string(tok)
				} else {
					tok = []byte(v.Token)
					s = v.Token
				}
				originalSpecialTokens = append(originalSpecialTokens, tok)
				if v.Score > 0 {
					scoresMap[s] = float32(v.Score)
				}
				if v.Id != nil {
					if *v.Id < 0 || *v.Id >= DOES_NOT_EXIST-1 {
						return errors.New(`YAML Error: Id must be between 0 and 16777213`)
					}
					idsMap[s] = uint32(*v.Id)
					used[uint32(*v.Id)] = true
				}
			}
		}
		for _, v := range y.Delete {
			if len(v.Token) > 0 {
				v.Token, err = decodeHex(v.Token)
				if err != nil {
					return errors.New(`Invalid TokenMonster hex encoding: ` + v.Token)
				}
				if !v.Encoded {
					tok, err = normalize([]byte(v.Token), usingCapcode, vocab.normalizer)
					if err != nil {
						continue
					}
					deleter[string(tok)] = true
				} else {
					deleter[v.Token] = true
				}
			}
			if v.Id != nil {
				if *v.Id < 0 || *v.Id >= DOES_NOT_EXIST-1 {
					return errors.New(`YAML Error: Id must be between 0 and 16777213`)
				}
				deleteById[uint32(*v.Id)] = true
			}
		}
	}

	singleChars := make([]byte, 0, 256)
	deletedTokens := new(pansearch.Counter)
	originalTokens := make([][]byte, 0, vocab.vocabSize)
	var newSpecialTokens [][]byte
	var exists bool
	var index uint32
	if len(vocab.info) > 0 {
		var on uint32
		for _, info := range vocab.info {
			tok = info.token
			s = string(tok)
			if info.score > 0 {
				scoresMap[s] = info.score
			}
			if _, exists = idsMap[s]; !exists {
				if !used[info.alt.id] {
					idsMap[s] = info.alt.id
					used[info.alt.id] = true
				}
			}
			if deleteById[info.alt.id] {
				deletedTokens.Add(info.token, 1)
			} else {
				if len(tok) == 1 {
					if !excludeOtherBytes {
						charTable[tok[0]] = true
					}
				} else if info.alt.data.flag & 64 != 0 {
					if info.score > -0.5 {
						originalSpecialTokens = append(originalSpecialTokens, tok)
					}
				} else {
					if info.score > -0.5 { // negative score is used to indicate that this is a "duplicate" token starting deleteToken space
						originalTokens = append(originalTokens, tok)
						on++
					}
				}
			}
		}
	}
	for i, v := range scores {
		if v > 0 {
			scoresMap[string(tokens[i])] = v
		}
	}
	if len(vocab.deleted) > 0 {
		for _, v := range vocab.deleted {
			s = string(string(v.token))
			if v.score > 0 {
				scoresMap[s] = v.score
			}
			if v.id != DOES_NOT_EXIST {
				if _, exists = idsMap[s]; !exists {
					if !used[v.id] {
						idsMap[s] = v.id
						used[v.id] = true
					}
				}
			}
			deletedTokens.Add(v.token, 1)
		}
	}

	ungreedySuffixes := []string{"'s", "’s"}
	ungreedySuffixesB := make([][]byte, len(ungreedySuffixes))
	if charset < 2 {
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = []byte(suffix)
		}
	} else if charset == 2 {
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = convertStringToUTF16(suffix)
		}
	}

	// Add and delete tokens
	if len(deleteTokens) > 0 {
		for _, v := range deleteTokens {
			if len(v) > 0 && len(v) <= 40  {
				deleter[string(v)] = true
				v, err = normalizeSafe(v, usingCapcode, vocab.normalizer)
				if err != nil {
					deleter[string(v)] = true
				}
			}
		}
	}
	for _, special := range specialTokens {
		if len(special) > 0 && len(special) <= 40  {
			special, err = normalize(special, usingCapcode, vocab.normalizer)
			if err == nil {
				if _, exists = deleter[string(special)]; !exists {
					newSpecialTokens = append(newSpecialTokens, special)
					deleter[string(special)] = true
					specialMap[string(special)] = true
				}
			}
		}
	}
	for _, special := range originalSpecialTokens {
		if len(special) > 0 {
			if _, exists = deleter[string(special)]; !exists {
				newSpecialTokens = append(newSpecialTokens, special)
				deleter[string(special)] = true
				specialMap[string(special)] = true
			}
		}
	}
	counter := new(pansearch.Counter)
	for _, v := range tokens {
		if len(v) > 0 && len(v) <= 40 {
			if _, exists = deleter[string(v)]; !exists {
				for _, special := range newSpecialTokens {
					if bytes.Contains(v, special) {
						exists = true
						break
					}
				}
				if !exists {
					if len(v) == 1 {
						if !excludeOtherBytes {
							charTable[v[0]] = true
						}
					} else {
						counter.Add(v, 1)
					}
				} else {
					deletedTokens.Add(v, 1)
				}
			} else {
				deletedTokens.Add(v, 1)
			}
		}
	}
	for _, v := range originalTokens {
		if len(v) > 0 {
			if _, exists = deleter[string(v)]; !exists {
				for _, special := range newSpecialTokens {
					if bytes.Contains(v, special) {
						exists = true
						break
					}
				}
				if !exists {
					if len(v) == 1 {
						if !excludeOtherBytes {
							charTable[v[0]] = true
						}
					} else {
						counter.Add(v, 1)
					}
				} else {
					deletedTokens.Add(v, 1)
				}
			} else {
				deletedTokens.Add(v, 1)
			}
		}
	}
	for _, v := range addTokens {
		if len(v) > 0 {
			v, err = normalize(v, usingCapcode, vocab.normalizer)
			if err == nil && len(v) <= 40 {
				if _, exists = deleter[string(v)]; !exists {
					for _, special := range newSpecialTokens {
						if bytes.Contains(v, special) {
							exists = true
							break
						}
					}
					if !exists {
						if len(v) == 1 {
							// addTokens is not excluded by exclude-other-bytes
							charTable[v[0]] = true
						} else {
							counter.Add(v, 1)
						}
					}
				}
			}
		}
	}
	counter.Build()
	tokens = counter.Keys()
	for i:=0; i<256; i++ {
		if charTable[i] {
			singleChars = append(singleChars, byte(i))
		}
	}

	total := len(tokens) + len(newSpecialTokens) + len(singleChars)

	// Resize vocabulary (smaller)
	if enableUnk || vocab.unkToken != DOES_NOT_EXIST {
		resize--
	}
	toDelete := total - resize
	if resize > 0 && toDelete > 0 { // Make it smaller
		var on uint32
		scoresList := make([]sortUint32Float32.KeyVal, len(scoresMap))
		scoresK := make([]string, len(scoresMap))
		for k, v := range scoresMap {
			scoresList[on] = sortUint32Float32.KeyVal{on, v}
			scoresK[on] = k
			on++
		}
		sortUint32Float32.Asc(scoresList)
		var deleted int
		var target string
		for _, v := range scoresList {
			target = scoresK[v.K]
			if len(target) == 1 {
				continue
			}
			for ii, v2 := range tokens {
				if string(v2) == target {
					deletedTokens.Add(v2, 1)
					tokens[ii] = nil
					deleted++
					break
				}
			}
			if deleted >= toDelete {
				break
			}
		}
	}

	// Define deleted tokens
	deletedTokens.Build()
	if deletedTokens.Len() > 0 {
		var v []byte
		var score float32
		var on int
		vocab.deleted = make([]deletedStruct, deletedTokens.Len())
		if deletedTokens.Reset() {
			for eof := false; !eof; {
				v, _, eof = deletedTokens.Next()
				s = string(v)
				score = scoresMap[s]
				index, exists = idsMap[s]
				if !exists || resetTokenIds {
					index = DOES_NOT_EXIST
				}
				vocab.deleted[on] = deletedStruct{token:v, id:index, score:score}
				on++
			}
		}
	}

	// Resize vocabulary (larger)
	if resize > 0 && toDelete < 0 { // Make it larger
		toResurrect := 0 - toDelete
		scoresList := make([]sortUint32Float32.KeyVal, len(vocab.deleted))
		for i, v := range vocab.deleted {
			scoresList[i] = sortUint32Float32.KeyVal{uint32(i), v.score}
		}
		sortUint32Float32.Desc(scoresList)
		if toResurrect > len(scoresList) {
			toResurrect = len(scoresList)
		}
		for i:=0; i<toResurrect; i++ {
			b := vocab.deleted[scoresList[i].K].token
			counter.Add(b, 1)
		}
		counter.Build()
		tokens = counter.Keys()
	}

	// Make the list of tokens in the vocabulary
	dic1 := new(pansearch.Light)
	for _, v := range singleChars {
		dic1.AddUnsorted([]byte{v})
	}
	for _, v := range tokens {
		if len(v) > 0 {
			dic1.AddUnsorted(v)
		}
	}
	for _, v := range newSpecialTokens {
		if len(v) > 0 {
			dic1.AddUnsorted(v)
		}
	}
	dic1.Build()

	// Determine vocabulary size and set unkToken
	total = dic1.Len()
	if (resetTokenIds && vocab.unkToken != DOES_NOT_EXIST) || (enableUnk && vocab.unkToken == DOES_NOT_EXIST) || vocab.unkToken == DOES_NOT_EXIST - 1 {
		if !used[uint32(total)] || resetTokenIds {
			vocab.unkToken = uint32(total)
		} else {
			index = 0
			for used[index] {
				index++
			}
			vocab.unkToken = index
		}
	}
	if vocab.unkToken != DOES_NOT_EXIST && !canHaveUnkToken(len(singleChars), usingCapcode) {
		vocab.unkToken = DOES_NOT_EXIST
	}
	if vocab.unkToken != DOES_NOT_EXIST {
		total++ // unk token
	}
	vocab.vocabSize = total

	// Find the highest ID from idsMap
	var maxID uint32 = uint32(vocab.vocabSize)
	if resetTokenIds {
		idsMap = make(map[string]uint32)
		used = make(map[uint32]bool)
	} else {
		for _, index = range idsMap {
			if index + 1 > maxID {
				maxID = index + 1
			}
		}
		if vocab.unkToken != DOES_NOT_EXIST && vocab.unkToken + 1 > maxID {
			maxID = vocab.unkToken + 1
		}
		if vocab.unkToken != DOES_NOT_EXIST {
			used[vocab.unkToken] = true
		}
	}
	
	// Determine the token IDs and build a second dictionary
	// This dictionary includes both variants of tokens beginning with deleteForward then space, and without
	// The result is that there's actually more than vocabSize tokens, but some of them have the same ID
	dictionary := new(pansearch.Fast)
	vocab.reverse = make([][]byte, maxID)
	if dic1.Reset() {
		var token []byte
		var r rune
		var found, inc bool
		index = 0
		for used[index] {
			index++
		}
		var index1 uint32
		add := string(capcode.DeleteToken) + " "
		if usingCapcode == 1 {
			add = string(capcode.NoCapcodeDeleteToken) + " "
		}
		for eof := false; !eof; {
			token, eof = dic1.Next()
			inc = false
			s = string(token)
			if index1, exists = idsMap[s]; !exists { // check if this token has an ID assigned
				index1 = index // if not use the next available ID
				inc = true
			}
			vocab.reverse[index1] = token
			dictionary.Add(token)
			idsMap[s] = index1
			r, _ = decodeRune(token, charset)
			if usingCapcode != 0 && isAlphaNum(r, usingCapcode) {
				if len(newSpecialTokens) > 0 {
					if _, found = specialMap[s]; found {
						specialMap[add + s] = true
					}
				}
				s = add + s
				if len(s) <= 40 {
					dictionary.Add([]byte(s))
					idsMap[s] = index1
					scoresMap[s] = -1 // -1 is used to indicate that this is a "duplicate" token
				}
			}
			if inc {
				index++
				for used[index] {
					index++
				}
			}
		}
	}
	dictionary.Build()

	// Set the deleteToken to the index (later set to the ID)
	vocab.deleteToken = DOES_NOT_EXIST
	if vocab.usingCapcode == 2 {
		if index, found := dictionary.Find([]byte{capcode.DeleteToken}); found {
			vocab.deleteToken = index
		}
	} else if vocab.usingCapcode == 1 {
		if index, found := dictionary.Find([]byte{capcode.NoCapcodeDeleteToken}); found {
			vocab.deleteToken = index
		}
	}

	vocab.maxTokenLength = dictionary.LongestLength()
	vocabList := make([]tokenInfo, dictionary.Len())
	var tokenData tokenInfo
	var beginByte [256][4]uint32
	if dictionary.Reset() {
		var token, subword []byte
		var on, hasSuffix, length, minAltSize int
		var r, r2 rune
		var n, n2 int
		var priority1, priority2, nWords uint8
		var found, onlyLetterSpace, onlyNumberSpace, onlyPunc bool
		var score float32
		for eof := false; !eof; {
			token, eof = dictionary.Next()
			s = string(token)
			index = idsMap[s]
			score = scoresMap[s]
			tokenData = tokenInfo{token:token, score:score, alt:tokenOuter{index:DOES_NOT_EXIST, index2:DOES_NOT_EXIST, id:index}}
			// Check for special tokens
			if len(newSpecialTokens) > 0 {
				if _, found = specialMap[s]; found {
					tokenData.alt.data.flag = 64
					vocabList[on] = tokenData
					on++
					continue
				}
			}
			priority1 = 0
			priority2 = 0
			nWords = 0
			minAltSize = 1
			onlyLetterSpace = false
			onlyNumberSpace = false
			onlyPunc = false
			r, n = decodeRune(token, charset)
			r2, n2 = decodeRune(token[n:], charset)
			// Check beginning of token
			if r == ' ' {
				tokenData.alt.data.flag = 4
				beginByte[token[0]][0]++
				if isAlphaNum(r2, usingCapcode) {
					nWords++
					minAltSize = 2
				}
			} else if isLetter(r, usingCapcode) {
				tokenData.alt.data.flag = 2
				beginByte[token[0]][1]++
			} else if isCapcode(r, usingCapcode) {
				if r == capcode.CharacterToken || r == capcode.WordToken {
					tokenData.alt.data.flag = 4 // count as a space
				}
				tokenData.alt.data.flag |= 16 // begins on capcode
				beginByte[token[0]][3]++
			} else if unicode.IsNumber(r) {
				beginByte[token[0]][2]++
			} else {
				beginByte[token[0]][3]++
			}
			// Count words in token
			if len(token) == 1 {
				onlyPunc = true
			} else {
				if (r == ' ' || isLetter(r, usingCapcode)) && isLetter(r2, usingCapcode) {
					onlyLetterSpace = true
				} else if (r == ' ' || unicode.IsNumber(r)) && unicode.IsNumber(r2) {
					onlyNumberSpace = true
				} else if !isAlphaNum(r, usingCapcode) && !isAlphaNum(r2, usingCapcode) {
					onlyPunc = true
				}
				for i := n + n2; i < len(token); i += n2 {
					r = r2
					n = n2
					r2, n2 = decodeRune(token[i:], charset)
					if r == ' ' && isAlphaNum(r2, usingCapcode) {
						nWords++
					}
					if isLetter(r2, usingCapcode) {
						onlyPunc = false
						onlyNumberSpace = false
					} else if unicode.IsNumber(r2) {
						onlyPunc = false
						onlyLetterSpace = false
					} else if r2 != ' ' {
						onlyLetterSpace = false
						onlyNumberSpace = false
					}
				}
			}	
			tokenData.alt.data.nWords = nWords
			// Now do some precalculations concerning the token
			r = decodeLastRune(token, charset)
			if minAltSize == 2 && isLetter(r, usingCapcode) && onlyLetterSpace { // only letters and full words
				if nWords == 1 {
					tokenData.alt.data.flag |= 32 // 1 word beginning space with only letters
				}
			}
			if minAltSize == 2 && nWords <= 1 { // begins _A and more than 1 word
				minAltSize = 1
			}
			if isCapcode(r, usingCapcode) {
				tokenData.alt.data.flag |= 8
			}
			// Check end of token
			if isLetter(r, usingCapcode) { // token ends with a letter
				tokenData.alt.data.flag |= 1
			}
			if onlyLetterSpace || onlyNumberSpace || onlyPunc {
				tokenData.alt.data.flag |= 128
			}

			hasSuffix = hasSuffixPos(ungreedySuffixesB, token, charset, usingCapcode)

			for length=len(token)-1; length>=minAltSize; length-- { // loop through all possible subwords that would also fit beneath this one
				subword = token[:length] // the subword
				if index, found = dictionary.Find(subword); found { // is this subword in the testVocab?

					// anything | space_letter or space_number
					if length <= len(token) - 2 {
						if token[length] == ' ' {
							r, _ = decodeRune(token[length+1:], charset)
							if isLetter(r, usingCapcode) || unicode.IsNumber(r) { // space then letter or number
								if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
									if priority1 < 10 {
										tokenData.alt.index = index
										tokenData.alt.length = length
										priority1 = 10
									}
								} else {
									if priority2 < 10 {
										tokenData.alt.index2 = index
										tokenData.alt.length2 = length
										priority2 = 10
									}
								}
								continue
							}
						}
					}

					r = decodeLastRune(subword, charset) // last char in subtoken
					r2, _ = decodeRune(token[length:], charset) // the next char

					if usingCapcode == 0 {
						switch {
						case (!isLetter(r, usingCapcode) && r != '_') && (isLetter(r2, usingCapcode) || r2 == '_'):
							fallthrough
						case !unicode.IsNumber(r) && unicode.IsNumber(r2):
							if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
								if priority1 < 9 {
									tokenData.alt.index = index
									tokenData.alt.length = length
									priority1 = 9
								}
							} else {
								if priority2 < 9 {
									tokenData.alt.index2 = index
									tokenData.alt.length2 = length
									priority2 = 9
								}
							}
							continue
						}
					}

					switch {
						// letter | non-letter
						case (isLetter(r, usingCapcode) || r == '_') && (!isLetter(r2, usingCapcode) && r2 != '_'):
							fallthrough
						// number | non-number
						case unicode.IsNumber(r) && !unicode.IsNumber(r2):
							if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
								if priority1 < 9 {
									tokenData.alt.index = index
									tokenData.alt.length = length
									priority1 = 9
								}
							} else {
								if priority2 < 9 {
									tokenData.alt.index2 = index
									tokenData.alt.length2 = length
									priority2 = 9
								}
							}
							continue
						// space | non-space
						case unicode.IsSpace(r) && !unicode.IsSpace(r2):
							if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
								if priority1 < 7 {
									tokenData.alt.index = index
									tokenData.alt.length = length
									priority1 = 7
								}
							} else {
								if priority2 < 7 {
									tokenData.alt.index2 = index
									tokenData.alt.length2 = length
									priority2 = 7
								}
							}
							continue
						// non-space | space
						case !unicode.IsSpace(r) && unicode.IsSpace(r2):
							if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
								if priority1 < 8 {
									tokenData.alt.index = index
									tokenData.alt.length = length
									priority1 = 8
								}
							} else {
								if priority2 < 8 {
									tokenData.alt.index2 = index
									tokenData.alt.length2 = length
									priority2 = 8
								}
							}
							continue
						// everything | capcode
						case isCapcode(r2, usingCapcode):
							if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
								if priority1 < 9 {
									tokenData.alt.index = index
									tokenData.alt.length = length
									priority1 = 9
								}
							} else {
								if priority2 < 9 {
									tokenData.alt.index2 = index
									tokenData.alt.length2 = length
									priority2 = 9
								}
							}
							continue
					}

					// Suffix
					if length == hasSuffix {
						if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
							if priority1 < 8 {
								tokenData.alt.index = index
								tokenData.alt.length = length
								priority1 = 8
							}
						} else {
							if priority2 < 8 {
								tokenData.alt.index2 = index
								tokenData.alt.length2 = length
								priority2 = 8
							}
						}
						break
					}

					// Everything else
					if priority1 < priority2 || (priority1 == priority2 && tokenData.alt.length <= tokenData.alt.length2) {
						if priority1 < 1 {
							tokenData.alt.index = index
							tokenData.alt.length = length
							priority1 = 1
						}
					} else {
						if priority2 < 1 {
							tokenData.alt.index2 = index
							tokenData.alt.length2 = length
							priority2 = 1
						}
					}

				}
			}
			
			// tokenData now contains the index & length of the longest preferred subtoken of this token in the vocabulary
			if tokenData.alt.length == 0 && tokenData.alt.length2 > 0 {
				panic(errors.New(`Sanity check failed`))
			}

			// Make sure the first alternative is the better one
			if tokenData.alt.length2 > 0 && (priority2 > priority1 || (priority2 == priority1 && tokenData.alt.length2 > tokenData.alt.length)) {
				tokenData.alt.index, tokenData.alt.index2 = tokenData.alt.index2, tokenData.alt.index
				tokenData.alt.length, tokenData.alt.length2 = tokenData.alt.length2, tokenData.alt.length
			}

			// Set the IDs for the alternatives, this avoids a lookup during tokenization
			if tokenData.alt.length > 0 {
				tokenData.alt.id1 = vocabList[tokenData.alt.index].alt.id
				if tokenData.alt.length2 > 0 {
					tokenData.alt.id2 = vocabList[tokenData.alt.index2].alt.id
				}
			}

			vocabList[on] = tokenData
			on++
		}
	}

	// Build chartable
	for i:=0; i<256; i++ {
		if beginByte[i][1] > beginByte[i][0] && beginByte[i][1] > beginByte[i][2] && beginByte[i][1] > beginByte[i][3] && beginByte[i][1] > 2 {
			vocab.beginByte[i] = 1 // it's a letter
		} else if beginByte[i][0] > beginByte[i][1] && beginByte[i][0] > beginByte[i][2] && beginByte[i][0] > beginByte[i][3] && beginByte[i][0] > 2 {
			vocab.beginByte[i] = 4 + 8 // it's a space
		} else if beginByte[i][3] > beginByte[i][0] && beginByte[i][3] > beginByte[i][1] && beginByte[i][3] > beginByte[i][2] && beginByte[i][3] > 2 {
			vocab.beginByte[i] = 2 + 8 // it's punctuation or capcode
		}
	}

	// Set the deleteToken to it's ID instead of index
	if vocab.deleteToken != DOES_NOT_EXIST {
		vocab.deleteToken = vocabList[vocab.deleteToken].alt.id
	}

	vocab.info = vocabList
	vocab.dictionary = dictionary
	if vocab.reserve == 0 {
		vocab.reserve = displayReserve
	}
	return nil
}

// -------- YAML parsing --------

type YamlVocab struct {
	Charset              string     `yaml:"charset,omitempty"`
	Normalization        string     `yaml:"normalization,omitempty"`
	Capcode              int        `yaml:"capcode,omitempty"`
	TrainingParam		 *int		`yaml:"training-param,omitempty"`
	ResetTokenIds        bool       `yaml:"reset-token-ids,omitempty"`
	Include256Bytes      bool       `yaml:"include-256-bytes,omitempty"`
	Include128Bytes      bool       `yaml:"include-128-bytes,omitempty"`
	IncludeUtf8Bytes     bool       `yaml:"include-utf8-bytes,omitempty"`
	IncludeAsciiBytes    bool       `yaml:"include-ascii-bytes,omitempty"`
	IncludeExtendedBytes bool       `yaml:"include-extended-bytes,omitempty"`
	ExcludeOtherBytes    bool       `yaml:"exclude-other-bytes,omitempty"`
	Unk                  bool       `yaml:"unk,omitempty"`
	UnkId                *int        `yaml:"unk-id,omitempty"`
	Regular              []YamlItem `yaml:"tokens,omitempty"`
	Special              []YamlItem `yaml:"special,omitempty"`
	Delete               []YamlItem `yaml:"delete,omitempty"`
}

type YamlItem struct {
	Encoded bool   `yaml:"encoded,omitempty"`
	Token   string `yaml:",omitempty"`
	Id      *int    `yaml:"id,omitempty"`
	Score   float32 `yaml:"score,omitempty"`
}

func yamlParse(data []byte) (YamlVocab, error) {
	var parsed YamlVocab
	err := yaml.Unmarshal(data, &parsed)
	if err != nil {
		return parsed, err
	}
	return parsed, nil
}

// Exports the vocabulary to a human-readable YAML file.
// It writes to an io.Writer.
// You can import from YAML with NewVocabFromYAML().
func (vocab *Vocab) ExportYAML(writer io.Writer, orderByScore bool) {
	w := custom.NewWriter(writer)
	defer w.Close()

	switch vocab.charset {
		case 1:
			w.WriteString("charset: utf-8\n")
		case 2:
			w.WriteString("charset: utf-16\n")
		default:
			w.WriteString("charset: none\n")
	}
	w.WriteString(`normalization: "` + strings.ToLower(vocab.normalizer.String()) + "\"\n")
	switch vocab.usingCapcode {
		case 0:
			w.WriteString("capcode: 0\n")
		case 1:
			w.WriteString("capcode: 1\n")
		case 2:
			w.WriteString("capcode: 2\n")
	}
	if vocab.level < 5 {
		w.WriteString("training-param: ")
		w.WriteInt(int((uint16(vocab.reserve) << 3) | uint16(vocab.level)))
		w.WriteByte('\n')
	}
	if vocab.unkToken != DOES_NOT_EXIST {
		w.WriteString("unk: true\n")
		w.WriteString("unk-id: ")
		w.WriteInt(int(vocab.unkToken))
		w.WriteByte('\n')
	}
	w.WriteString("tokens:\n")

	b := bytes.NewBuffer(make([]byte, 0, 42))
	if orderByScore {
		listRegular := make([]sortUint32Float32.KeyVal, 0, vocab.vocabSize)
		listSpecial := make([]sortUint32Float32.KeyVal, 0, 1)
		for i, v := range vocab.info {
			if v.score > -0.5 {
				if v.alt.data.flag & 64 != 0 {
					listSpecial = append(listSpecial, sortUint32Float32.KeyVal{uint32(i), v.score})
				} else {
					listRegular = append(listRegular, sortUint32Float32.KeyVal{uint32(i), v.score})
				}
			}
		}
		sortUint32Float32.Desc(listRegular)
		sortUint32Float32.Desc(listSpecial)
		var tok tokenInfo
		
		for _, v := range listRegular {
			tok = vocab.info[v.K]
			w.WriteString("    - token:   ")
			escapeYAML(b, tok.token)
			w.Write(b.Bytes())
			w.WriteString("\n      id:      ")
			w.WriteInt(int(tok.alt.id))
			w.WriteByte('\n')
			if tok.score > 0 {
				w.WriteString("      score:   ")
				writeFloatPrintable(w, tok.score)
				w.WriteByte('\n')
			}
			w.WriteString("      encoded: true\n")
		}

		if len(listSpecial) > 0 {
			w.WriteString("special:\n")
			for _, v := range listSpecial {
				tok = vocab.info[v.K]
				w.WriteString("    - token:   ")
				escapeYAML(b, tok.token)
				w.Write(b.Bytes())
				w.WriteString("\n      id:      ")
				w.WriteInt(int(tok.alt.id))
				w.WriteByte('\n')
				if tok.score > 0 {
					w.WriteString("      score:   ")
					writeFloatPrintable(w, tok.score)
					w.WriteByte('\n')
				}
				w.WriteString("      encoded: true\n")
			}
		}
	} else {
		buf := bytes.NewBuffer(nil)
		for _, tok := range vocab.info {
			if tok.score > -0.5 {
				if tok.alt.data.flag & 64 != 0 {
					// Special
					buf.WriteString("    - token:   ")
					escapeYAML(b, tok.token)
					buf.Write(b.Bytes())
					buf.WriteString("\n      id:      ")
					buf.Write(conv.Bytes(int(tok.alt.id)))
					buf.WriteByte('\n')
					if tok.score > 0 {
						buf.WriteString("      score:   ")
						writeFloatPrintable(buf, tok.score)
						buf.WriteByte('\n')
					}
					buf.WriteString("      encoded: true\n")
				} else {
					// Regular
					w.WriteString("    - token:   ")
					escapeYAML(b, tok.token)
					w.Write(b.Bytes())
					w.WriteString("\n      id:      ")
					w.WriteInt(int(tok.alt.id))
					w.WriteByte('\n')
					if tok.score > 0 {
						w.WriteString("      score:   ")
						writeFloatPrintable(w, tok.score)
						w.WriteByte('\n')
					}
					w.WriteString("      encoded: true\n")
				}
			}
		}
		if buf.Len() > 0 {
			w.WriteString("special:\n")
			w.Write(buf.Bytes())
		}
	}
}

func escapeYAML(b *bytes.Buffer, s []byte) {
	var r rune
	var n int
	b.Reset()
	b.WriteByte('"')
	for i:=0; i < len(s); i += n {
		r, n = utf8.DecodeRune(s[i:]) // get the next rune
		if r == runeError {
			b.Reset()
			b.WriteString("\"TokenMonsterHexEncode{")
			b.WriteString(hex.EncodeToString(s))
			b.WriteString("}\"")
			return
		}
		switch {
			case r == '\x00':
				b.Write([]byte("\\0"))
			case r == '\x08':
				b.Write([]byte("\\b"))
			case r == '\x09':
				b.Write([]byte("\\t"))
			case r == '\x0A':
				b.Write([]byte("\\n"))
			case r == '\x0B':
				b.Write([]byte("\\v"))
			case r == '\x0C':
				b.Write([]byte("\\f"))
			case r == '\x0D':
				b.Write([]byte("\\r"))
			case r == '\\':
				b.Write([]byte("\\\\"))
			case r == '"':
				b.Write([]byte("\\\""))
			default:
				b.Write(s[i:i+n])
			}
	}
	b.WriteByte('"')
}

func writeFloatPrintable(writer io.Writer, value float32) {
	str := strconv.FormatFloat(float64(value), 'f', -1, 32)
	writer.Write([]byte(str))
}

func decodeHex(str string) (string, error) {
	if strings.HasPrefix(str, "TokenMonsterHexEncode{") {
		if strings.HasSuffix(str, "}") {
			result := strings.TrimPrefix(strings.TrimSuffix(str, "}"), "TokenMonsterHexEncode{")
			decoded, err := hex.DecodeString(result)
			if err != nil {
				return str, err
			}
			return string(decoded), nil
		}
	}
	return str, nil
}
