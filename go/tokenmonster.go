package tokenmonster

import (
	"os"
	"fmt"
	"bytes"
	"unsafe"
	"errors"
	"unicode"
	"io/ioutil"
	"unicode/utf8"
	"unicode/utf16"
	"encoding/binary"
	uni "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Sort/Uint32Float32"
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

var isLittleEndian = *(*byte)(unsafe.Pointer(&[]uint16{0x0100}[0])) == 0x00

// The main struct for the vocabulary
type Vocab struct {
	dictionary *pansearch.Fast
	info []tokenInfo
	deleted []deletedStruct // deleted tokens are stored here and can later be restored
	beginByte [256]byte
	index2id []uint32
	id2index []uint32
	deleteToken uint32
	maxlen int
	usingCapcode bool
	useUnk bool
	charset uint8
	level uint8
	reserve uint8
	customIDs bool
}

// A decoder object for sequential decoding.
// Use the NewDecoder function of the Vocab struct.
type Decoder struct {
	vocab Vocab
	remainder []byte
	capcodeDecoder *capcode.Decoder
}

type tokenInfo struct {
	token	[]byte
	score	float32
	alt		tokenOuter
}

type tokenOuter struct {
	index	uint32		// the index of the token I'm willing to alternative because I'm not greedy
	index2  uint32
	length	int
	length2 int
	data	tokenInner
}

type tokenInner struct {
	flag	uint8
	nWords 	uint8
}

type deletedStruct struct {
	token []byte
	score float32
}

// --------- HELPER FUNCTIONS ---------

func norm_UTF8_NFD(input []byte) (output []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			// Convert panic into error
			err = errors.New(`UTF-8 NFD normalization panicked`)
		}
	}()
	normalized := bytes.NewBuffer(make([]byte, 0, len(input) + (len(input) / 3) + 4))
	normalizer := norm.NFD.Writer(normalized)
	_, err = normalizer.Write(input)
	if err != nil {
		return nil, err
	}
	err = normalizer.Close()
	if err != nil {
		return nil, err
	}
	output = normalized.Bytes()
	return output, err
}

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
	// Create a transformer to decode to UTF-8 and normalize the text to NFD
	transformer := transform.Chain(utf16Decoder.NewDecoder(), norm.NFD)
	// Create a reader with the transformer
	reader := transform.NewReader(bytes.NewReader(input), transformer)
	// Read normalized NFD UTF-8 bytes
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

func convertStringToUTF16WithNFDNormalization(s string) []byte {
	s = norm.NFD.String(s)
	b := []byte(s)
	buf := &bytes.Buffer{}
	w := transform.NewWriter(buf, uni.UTF16(uni.LittleEndian, uni.IgnoreBOM).NewEncoder())
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func normalizeTokenBytes(b []byte, usingCapcode bool, charsetFlag uint8) ([]byte, error) {
	if charsetFlag == 1 {
		var b2 []byte
		var err error
		if usingCapcode {
			b2 = capcode.Encode(b)
		} else {
			b2 = capcode.NoCapcodeEncode(b)
		}
		b2, err = norm_UTF8_NFD(b2)
		if err != nil {
			b2, err = norm_UTF8_NFD(b)
			if usingCapcode {
				b2 = capcode.Encode(b2)
			} else {
				b2 = capcode.NoCapcodeEncode(b2)
			}
		}
		return b2, err
	} else if charsetFlag == 2 {
		b, _ = uni.UTF16(uni.LittleEndian, uni.IgnoreBOM).NewEncoder().Bytes(b)
		return norm_UTF16_NFD(b)
	}
	return b, nil
}

// normalizes but avoids double encoding with capcode
func normalizeTokenBytesSafe(b []byte, usingCapcode bool, charsetFlag uint8) ([]byte, error) {
	if charsetFlag == 1 {
		var b2 []byte
		var err error
		if usingCapcode {
			var okay bool = true
			for _, v := range b {
				if v == capcode.DeleteToken || v == capcode.CharacterToken || v == capcode.WordToken {
					okay = false
					break
				}
			}
			if okay {
				b2 = capcode.Encode(b)
			}
		} else {
			var okay bool = true
			for _, v := range b {
				if v == capcode.NoCapcodeDeleteToken {
					okay = false
					break
				}
			}
			if okay {
				b2 = capcode.NoCapcodeEncode(b)
			}
		}
		b2, err = norm_UTF8_NFD(b2)
		if err != nil {
			b2, err = norm_UTF8_NFD(b)
			if usingCapcode {
				b2 = capcode.Encode(b2)
			} else {
				b2 = capcode.NoCapcodeEncode(b2)
			}
		}
		return b2, err
	} else if charsetFlag == 2 {
		b, _ = uni.UTF16(uni.LittleEndian, uni.IgnoreBOM).NewEncoder().Bytes(b)
		return norm_UTF16_NFD(b)
	}
	return b, nil
}

func hasSuffixPos(ungreedySuffixesB [][]byte, key []byte, charset uint8, usingCapcode bool) int {
	if charset == 0 {
		return -1
	}
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

func genUTF8bytes(list []bool, usingCapcode bool, charsetFlag uint8) {
	genASCIIbytes(list, usingCapcode, charsetFlag)
    // Continuation bytes in multi-byte characters
    for i := 0x80; i <= 0xBF; i++ {
		list[i] = true
    }
    // Starting bytes of multi-byte characters excluding overlongs
    for i := 0xC2; i <= 0xF4; i++ {
		list[i] = true
    }
}

func genASCIIbytes(list []bool, usingCapcode bool, charsetFlag uint8) {
	for i:=32; i<127; i++ {
		if !usingCapcode || (!(i >= 'A' && i <= 'Z' && i != 'C' && i != 'W' && i != 'D')) {
			list[i] = true
		}
	}
	list[9] = true
	list[10] = true
	list[13] = true
	if charsetFlag == 1 && !usingCapcode {
		list[127] = true
	}
}

func genExtendedbytes(list []bool, usingCapcode bool, charsetFlag uint8) {
	s := `£€©®™°%¢¥—–•‘’“”áéíóúýàèìòùâêîôûäëïöüñãõçåæœ`
	if !usingCapcode {
		s += `ÁÉÍÓÚÝÀÈÌÒÙÂÊÎÔÛÄËÏÖÜÑÃÕÇÅÆŒ`
	}
	s2, _ := norm_UTF8_NFD([]byte(s))
	for _, b := range s2 {
		list[b] = true
	}
	genASCIIbytes(list, usingCapcode, charsetFlag)
}

func gen128bytes(list []bool, usingCapcode bool, charsetFlag uint8) {
	var b byte
	for i:=0; i<128; i++ {
		b = byte(i)
		if !usingCapcode || (!(b >= 'A' && b <= 'Z' && b != 'C' && b != 'W' && b != 'D')) {
			list[i] = true
		}
	}
}

func gen256bytes(list []bool, usingCapcode bool, charsetFlag uint8) {
	var b byte
	for i:=0; i<256; i++ {
		b = byte(i)
		if !usingCapcode || (!(b >= 'A' && b <= 'Z' && b != 'C' && b != 'W' && b != 'D')) {
			list[i] = true
		}
	}
}

func isLetter(r rune, usingCapcode bool) bool {
	return (unicode.IsLetter(r) && (!usingCapcode || (r != 'W' && r != 'C' && r != 'D'))) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isAlphaNum(r rune, usingCapcode bool) bool {
	return (unicode.IsLetter(r) && (!usingCapcode || (r != 'W' && r != 'C' && r != 'D'))) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isCapcode(r rune, charset uint8, usingCapcode bool) bool {
	if usingCapcode {
		return r == 'W' || r == 'D' || r == 'C'
	} else if charset == 1 {
		return r == 127
	}
	return false
}

func decodeRune(b []byte, charsetFlag uint8) (rune, int) {
	switch charsetFlag {
		case 1: // UTF-8
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
		case 1: // UTF-8
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

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func unleak(b []byte) []byte {
	new := make([]byte, len(b))
	copy(new, b)
	return new
}

func canHaveUnkToken(i int, usingCapcode bool) bool {
	if (i < 256 && !usingCapcode) || i < 233 {
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
		if len(d.vocab.info) <= 65536 {
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
		info := d.vocab.info
		nTokens := uint16(len(info))
		var i int
		if d.vocab.charset == 0 {
			for _, v := range tokens {
				if v < nTokens {
					i += len(info[v].token)
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
					copy(buffer[i:], info[v].token)
					i += len(info[v].token)
				}
			}
			return buffer
		}
		// Get the size
		i = len(d.remainder)
		for _, v := range tokens {
			if v < nTokens {
				i += len(info[v].token)
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
				copy(buffer[i:], info[v].token)
				i += len(info[v].token)
			}
		}
		if d.vocab.charset == 1 { // UTF-8
			remaining := len(buffer) - incompleteUTF8Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
			if (d.vocab.usingCapcode) {
				buffer = d.capcodeDecoder.Decode(buffer)
			} else {
				buffer = d.capcodeDecoder.NoCapcodeDecode(buffer)
			}
		} else { // UTF-16
			remaining := len(buffer) - incompleteUTF16Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
		}
		return buffer
	} else if encodingLength == 3 {
		var on uint64
		var to uint64 = uint64(len(b))
		var v uint32
		info := d.vocab.info
		nTokens := uint32(len(info))
		var i int
		if d.vocab.charset == 0 {
			for on=0; on<to; on+=3 {
				v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
				if v < nTokens {
					i += len(info[v].token)
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
					copy(buffer[i:], info[v].token)
					i += len(info[v].token)
				}
			}
			return buffer
		}
		// Get the size
		i = len(d.remainder)
		for on=0; on<to; on+=3 {
			v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
			if v < nTokens {
				i += len(info[v].token)
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
				copy(buffer[i:], info[v].token)
				i += len(info[v].token)
			}
		}
		if d.vocab.charset == 1 { // UTF-8
			remaining := len(buffer) - incompleteUTF8Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
			if (d.vocab.usingCapcode) {
				buffer = d.capcodeDecoder.Decode(buffer)
			} else {
				buffer = d.capcodeDecoder.NoCapcodeDecode(buffer)
			}
		} else { // UTF-16
			remaining := len(buffer) - incompleteUTF16Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
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
		info := d.vocab.info
		nTokens := uint32(len(info))
		var i int
		if d.vocab.charset == 0 {
			for _, v := range tokens {
				if v < nTokens {
					i += len(info[v].token)
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
					copy(buffer[i:], info[v].token)
					i += len(info[v].token)
				}
			}
			return buffer
		}
		// Get the size
		i = len(d.remainder)
		for _, v := range tokens {
			if v < nTokens {
				i += len(info[v].token)
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
				copy(buffer[i:], info[v].token)
				i += len(info[v].token)
			}
		}
		if d.vocab.charset == 1 { // UTF-8
			remaining := len(buffer) - incompleteUTF8Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
			if (d.vocab.usingCapcode) {
				buffer = d.capcodeDecoder.Decode(buffer)
			} else {
				buffer = d.capcodeDecoder.NoCapcodeDecode(buffer)
			}
		} else { // UTF-16
			remaining := len(buffer) - incompleteUTF16Bytes(buffer)
			d.remainder = buffer[remaining:]
			buffer = buffer[:remaining]
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
	info := d.vocab.info
	nTokens := uint32(len(info))
	var i int = len(d.remainder)
	for _, v := range tokens {
		if v < nTokens {
			i += len(info[v].token)
		}
	}
	// Make the exact size array
	data := make([]byte, i)
	// Copy the keys into it
	copy(data, d.remainder)
	i = len(d.remainder)
	for _, v := range tokens {
		if v < nTokens {
			copy(data[i:], info[v].token)
			i += len(info[v].token)
		}
	}
	if d.vocab.charset == 1 { // UTF-8
		remaining := len(data) - incompleteUTF8Bytes(data)
		d.remainder = data[remaining:]
		data = data[:remaining]
		if (d.vocab.usingCapcode) {
			data = d.capcodeDecoder.Decode(data)
		} else {
			data = d.capcodeDecoder.NoCapcodeDecode(data)
		}
	} else { // UTF-16
		remaining := len(data) - incompleteUTF16Bytes(data)
		d.remainder = data[remaining:]
		data = data[:remaining]
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
		if len(vocab.info) <= 65536 {
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
	if vocab.charset == 1 {
		if (vocab.usingCapcode) {
			return capcode.Decode(data)
		} else {
			return capcode.NoCapcodeDecode(data)
		}
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
	if vocab.charset == 1 {
		if (vocab.usingCapcode) {
			return capcode.Decode(data)
		} else {
			return capcode.NoCapcodeDecode(data)
		}
	}
	return data
}

func (vocab *Vocab) decode(tokens []uint32) []byte {
	// Get the size
	info := vocab.info
	nTokens := uint32(len(info))
	var i int
	for _, v := range tokens {
		if v < nTokens {
			i += len(info[v].token)
		}
	}
	// Make the exact size array
	data := make([]byte, i)
	// Copy the keys into it
	i = 0
	for _, v := range tokens {
		if v < nTokens {
			copy(data[i:], info[v].token)
			i += len(info[v].token)
		}
	}
	return data
}

func (vocab *Vocab) decodeSerialized(b []byte, encodingLength uint8, buffer []byte) []byte {
	info := vocab.info
	if encodingLength <= 1 {
		if len(info) <= 65536 {
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
		nTokens := uint16(len(info))
		var i int
		for _, v := range tokens {
			if v < nTokens {
				i += len(info[v].token)
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
				copy(buffer[i:], info[v].token)
				i += len(info[v].token)
			}
		}
		return buffer
	} else if encodingLength == 3 {
		var on uint64
		var to uint64 = uint64(len(b))
		var v uint32
		nTokens := uint32(len(info))
		var i int
		for on=0; on<to; on+=3 {
			v = uint32(b[on]) | (uint32(b[on+1]) << 8) | (uint32(b[on+2]) << 16)
			if v < nTokens {
				i += len(info[v].token)
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
				copy(buffer[i:], info[v].token)
				i += len(info[v].token)
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
		nTokens := uint32(len(info))
		var i int
		for _, v := range tokens {
			if v < nTokens {
				i += len(info[v].token)
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
				copy(buffer[i:], info[v].token)
				i += len(info[v].token)
			}
		}
		return buffer
	}
	return nil
}

// --------- TOKENIZE ---------

// Applies all normalizations to the bytes, including capcode and NFD.
func (vocab *Vocab) Normalize(data []byte) ([]byte, error) {
	if vocab.charset == 1 {
		var temp []byte
		var err error
		if vocab.usingCapcode {
			temp = capcode.Encode(data)
		} else {
			temp = capcode.NoCapcodeEncode(data)
		}
		temp, err = norm_UTF8_NFD(temp)
		if err != nil {
			temp, err = norm_UTF8_NFD(data)
			if vocab.usingCapcode {
				temp = capcode.Encode(temp)
			} else {
				temp = capcode.NoCapcodeEncode(temp)
			}
		}
		return temp, err
	} else if vocab.charset == 2 {
		return norm_UTF16_NFD(data)
	}
	return data, nil
}

// Tokenizes text from bytes slice to token IDs.
// The 2nd returned value (int) is the number of characters for which there were no tokens and were replaced with Unk token.
func (vocab *Vocab) Tokenize(data []byte) ([]uint32, int, error) {
	normalized, err := vocab.Normalize(data)
	if err != nil {
		return nil, 0, err
	}
	return vocab.tokenize(normalized)
}

// Tokenizes directly into serialized bytes with either 16-bit, 24-bit or 32-bit encoded unsigned integers depending on the vocabulary size.
// Set encodingLength to 0 for it to be chosen automatically, or set `encodingLength` to 2, 3 or 4.
// The 2rd return value is the encodingLength that was used, and the 3rd is the number of characters for which there were no tokens.
// `buffer` is an optional reusable buffer, you can send nil.
func (vocab *Vocab) TokenizeToSerialized(data []byte, encodingLength uint8, buffer []byte) ([]byte, uint8, int, error) {
	if encodingLength <= 1 {
		if len(vocab.info) <= 65536 {
			encodingLength = 2
		} else {
			encodingLength = 3
		}
	}
	normalized, err := vocab.Normalize(data)
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

	lilbuf := make([]byte, vocab.maxlen)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxlenWithSpace := vocab.maxlen - lilbufOffset

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
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxlen) ]); found {
			
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
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxlen) ])

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
							length1b = branchless.Min(lenData - i1, maxlenWithSpace)
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
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxlen) ])

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
								length2b = branchless.Min(lenData - i2, maxlenWithSpace)
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
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxlen) ])

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
									length3b = branchless.Min(lenData - i3, maxlenWithSpace)
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
							tokens = append(tokens, index)
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							tokens = append(tokens, original.index)
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							tokens = append(tokens, original.index2)
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							tokens = append(tokens, index, vocab.deleteToken)
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							tokens = append(tokens, original.index, vocab.deleteToken)
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							tokens = append(tokens, original.index2, vocab.deleteToken)
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				tokens = append(tokens, index)
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.useUnk {
				tokens = append(tokens, vocab.Unk())
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

	lilbuf := make([]byte, vocab.maxlen)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxlenWithSpace := vocab.maxlen - lilbufOffset

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
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxlen) ]); found {
			
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
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxlen) ])

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
							length1b = branchless.Min(lenData - i1, maxlenWithSpace)
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
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxlen) ])

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
								length2b = branchless.Min(lenData - i2, maxlenWithSpace)
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
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxlen) ])

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
									length3b = branchless.Min(lenData - i3, maxlenWithSpace)
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
							buffer = append(buffer, uint8(index), uint8(index >> 8))
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							buffer = append(buffer, uint8(original.index), uint8(original.index >> 8))
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							buffer = append(buffer, uint8(original.index2), uint8(original.index2 >> 8))
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8))
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							buffer = append(buffer, uint8(original.index), uint8(original.index >> 8), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8))
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							buffer = append(buffer, uint8(original.index2), uint8(original.index2 >> 8), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8))
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				buffer = append(buffer, uint8(index), uint8(index >> 8))
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.useUnk {
				index = vocab.Unk()
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

	lilbuf := make([]byte, vocab.maxlen)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxlenWithSpace := vocab.maxlen - lilbufOffset

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
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxlen) ]); found {
			
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
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxlen) ])

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
							length1b = branchless.Min(lenData - i1, maxlenWithSpace)
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
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxlen) ])

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
								length2b = branchless.Min(lenData - i2, maxlenWithSpace)
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
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxlen) ])

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
									length3b = branchless.Min(lenData - i3, maxlenWithSpace)
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
							buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16))
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							buffer = append(buffer, uint8(original.index), uint8(original.index >> 8), uint8(original.index >> 16))
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							buffer = append(buffer, uint8(original.index2), uint8(original.index2 >> 8), uint8(original.index2 >> 16))
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16))
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							buffer = append(buffer, uint8(original.index), uint8(original.index >> 8), uint8(original.index >> 16), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16))
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							buffer = append(buffer, uint8(original.index2), uint8(original.index2 >> 8), uint8(original.index2 >> 16), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16))
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16))
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.useUnk {
				index = vocab.Unk()
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

	lilbuf := make([]byte, vocab.maxlen)
	lilbuf[0] = 32
	lilbufOffset := 1
	if vocab.charset == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]
	maxlenWithSpace := vocab.maxlen - lilbufOffset

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
		if index, length, found = vocab.dictionary.LongestSubstring(data[ i : i + branchless.Min(lenData - i, vocab.maxlen) ]); found {
			
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
					index1, length1, found1 = vocab.dictionary.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, vocab.maxlen) ])

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
							(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
							((int(second.flag & 1 & nextByte) * 3)) )) 						// Deduct 3 if the second token ends inside a word
						maxScore = score1
						
						// Check if we're in the middle of a word
						if vocab.deleteToken != DOES_NOT_EXIST && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
							length1b = branchless.Min(lenData - i1, maxlenWithSpace)
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
									(int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
									((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
									1 )) 														// Deduct 1 for using an extra token
								maxScore = branchless.Max(maxScore, score1b)
							}
						}
					}

					if original.index != DOES_NOT_EXIST {
						i2 = i + original.length - forwardDelete
						index2, length2, found2 = vocab.dictionary.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, vocab.maxlen) ])

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
								length2b = branchless.Min(lenData - i2, maxlenWithSpace)
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
							index3, length3, found3 = vocab.dictionary.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, vocab.maxlen) ])

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
									length3b = branchless.Min(lenData - i3, maxlenWithSpace)
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
							buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16), uint8(index >> 24))
							i += length // forwardDelete is already applied to length
							length = length1
							index = index1
							forwardDelete = 0
							goto checkpoint
						case score2:
							buffer = append(buffer, uint8(original.index), uint8(original.index >> 8), uint8(original.index >> 16), uint8(original.index >> 24))
							i += original.length - forwardDelete
							length = length2
							index = index2
							forwardDelete = 0
							goto checkpoint
						case score3:
							buffer = append(buffer, uint8(original.index2), uint8(original.index2 >> 8), uint8(original.index2 >> 16), uint8(original.index2 >> 24))
							i += original.length2 - forwardDelete
							length = length3
							index = index3
							forwardDelete = 0
							goto checkpoint
						case score1b:
							buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16), uint8(index >> 24), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16), uint8(vocab.deleteToken >> 24))
							i += length
							length = length1b
							index = index1b
							forwardDelete = 1
							goto checkpoint
						case score2b:
							buffer = append(buffer, uint8(original.index), uint8(original.index >> 8), uint8(original.index >> 16), uint8(original.index >> 24), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16), uint8(vocab.deleteToken >> 24))
							i += original.length - forwardDelete
							length = length2b
							index = index2b
							forwardDelete = 1
							goto checkpoint
						case score3b:
							buffer = append(buffer, uint8(original.index2), uint8(original.index2 >> 8), uint8(original.index2 >> 16), uint8(original.index2 >> 24), uint8(vocab.deleteToken), uint8(vocab.deleteToken >> 8), uint8(vocab.deleteToken >> 16), uint8(vocab.deleteToken >> 24))
							i += original.length2 - forwardDelete
							length = length3b
							index = index3b
							forwardDelete = 1
							goto checkpoint
					}
				}
				// Skipped this branch (or case -1000000 from scores)
				buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16), uint8(index >> 24))
				i += length // forwardDelete is already applied to length
				forwardDelete = 0

		} else { // !found
			if vocab.useUnk {
				index = vocab.Unk()
				buffer = append(buffer, uint8(index), uint8(index >> 8), uint8(index >> 16), uint8(index >> 24))
			}
			i++
			missing++
			forwardDelete = 0
		}
	}
	
	return buffer, missing
}

// --------- GENERAL FUNCTIONS ---------

// Returns all tokens.
// The ID of the token is the index in the slice.
// The tokens are "raw" encoded with capcode.
// A token can be modified by a previous token in a sequence so this cannot be used for decoding.
func (vocab *Vocab) Tokens() [][]byte {
	info := vocab.info
	tokens := make([][]byte, len(info))
	for i, _ := range info {
		tokens[i] = unleak(info[i].token)
	}
	return tokens
}

// Info struct allows access to detailed information about each token from TokensDetailed().
// Token is the token still encoded with capcode.
// TokenDecoded is the decoded form of the token, however the token can be modified by a previous token in a sequence so this cannot be used for decoding.
// Type is 0 for regular tokens, 1 for character tokens, and 3 for special tokens.
// The Score is the percentage of the training dataset that this token covered and is used for sorting the tokens by their importance.
type Info struct {
	Token []byte
	TokenDecoded []byte
	Type uint8 // 0 = regular, 1 = character, 2 = special, 3 = unk
	Score float32
}

// Returns a slice of Info struct where the index is the Token ID
func (vocab *Vocab) TokensDetailed() []Info {
	infos := make([]Info, len(vocab.info))
	var info Info
	vocabinfo := vocab.info
	for i, _ := range vocab.info {
		info.Token = unleak(vocabinfo[i].token)
		if vocab.charset == 1 {
			if vocab.usingCapcode {
				info.TokenDecoded = capcode.Decode(unleak(vocabinfo[i].token))
			} else {
				info.TokenDecoded = capcode.NoCapcodeDecode(unleak(vocabinfo[i].token))
			}
		} else {
			info.TokenDecoded = unleak(info.Token)
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
		infos[i] = info
	}
	if vocab.useUnk {
		infos[vocab.Unk()].Type = 3
	}
	return infos
}

// Special struct is for accessing information about special tokens from SpecialTokens().
type Special struct {
	ID uint32
	Token []byte
	TokenDecoded []byte
}

// Returns the token IDs and the corresponding tokens of only the.
// Set `decode` to false to receive the decoded form of the tokens.
func (vocab *Vocab) SpecialTokens() []Special {
	info := vocab.info
	var list []Special
	for i:=0; i<len(info); i++ {
		if info[i].alt.data.flag & 64 != 0 {
			var special Special
			special.ID = uint32(i)
			special.Token = unleak(info[i].token)
			if vocab.charset == 1 {
				if vocab.usingCapcode {
					special.TokenDecoded = capcode.Decode(unleak(info[i].token))
				} else {
					special.TokenDecoded = capcode.NoCapcodeDecode(unleak(info[i].token))
				}
			} else {
				special.TokenDecoded = unleak(special.Token)
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
		if info[i].alt.data.flag & 64 != 0 {
			num++
		}
	}
	return num
}

// Returns the encoded token for the token ID.
func (vocab *Vocab) Token(id uint32) []byte {
	if id >= uint32(len(vocab.info)) {
		return nil
	}
	return unleak(vocab.info[id].token)
}

// Returns the score of the token ID.
func (vocab *Vocab) Score(id uint32) float32 {
	if id >= uint32(len(vocab.info)) {
		return 0
	}
	return vocab.info[id].score
}

// Returns the ID of the Unk token.
// This will return an invalid ID if there is no Unk token.
func (vocab *Vocab) Unk() uint32 {
	return uint32(vocab.dictionary.Len())
}

// Returns true if the vocabulary is using the UNK token.
// If used, the UNK token ID is used whenever a character being tokenized doesn't exist in the vocabulary.
func (vocab *Vocab) HasUnk() bool {
	return vocab.useUnk
}

// Decodes capcode from the bytes.
func (vocab *Vocab) Denormalize(b []byte) []byte {
	if vocab.charset == 1 {
		if (vocab.usingCapcode) {
			return capcode.Decode(unleak(b))
		} else {
			return capcode.NoCapcodeDecode(unleak(b))
		}
	}
	return b
}

// Returns the ID of the token from bytes.
// This only works for "raw" encoded tokens.
// Apply `Normalize` to the bytes first to use this with decoded tokens.
func (vocab *Vocab) ID(b []byte) (uint32, bool) {
	return vocab.dictionary.Find(b)
}

// Returns number of tokens in the vocabulary, inluding UNK token if it is used.
func (vocab *Vocab) Len() int {
	return len(vocab.info)
}

// The length of the longest (encoded) token in the vocabulary.
// This can be lower than that chosen during training if none of the longer tokens were chosen.
func (vocab *Vocab) MaxTokenLength() int {
	return vocab.maxlen
}

// A slice that contains all the single byte tokens in the vocabulary.
// Note that this is returned as only a slice of bytes, not a slice of slice of bytes.
func (vocab *Vocab) ReservedTokens() []byte {
	info := vocab.info
	var i int
	lst := make([]byte, 256)
	for ; i<len(info); i++ {
		if len(info[i].token) <= 1 {
			lst[i] = info[i].token[0]
		} else {
			break
		}
	}
	return lst[0:i]
}

// The number of single byte tokens in the vocabulary.
func (vocab *Vocab) NumReservedTokens() int {
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
// 0 = Binary, 1 = UTF-8, 2 = UTF-16.
func (vocab *Vocab) Charset() uint8 {
	return vocab.charset
}

// True if the vocabulary is using capcode.
// Even if it's not using capcode the tokens are still normalized with a forward delete token if charset is UTF-8.
func (vocab *Vocab) Capcode() bool {
	return vocab.usingCapcode
}

// The original filter for training the vocabulary.
// 0 = unfiltered, 1 = clean, 2 = balanced, 3 = consistent, 4 = strict, 5 = custom.
func (vocab *Vocab) Mode() uint8 {
	return vocab.level
}

// True is the vocabulary uses custom token IDs.
func (vocab *Vocab) HasCustomIDs() bool {
	return vocab.customIDs
}

// The number of tokens deleted from the vocabulary.
// These can be restored by resizing the vocabulary to be be larger.
func (vocab *Vocab) DeletedTokens() int {
	return len(vocab.deleted)
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

	w.WriteBool(vocab.usingCapcode)
	w.WriteBool(vocab.useUnk)
	w.WriteByte(vocab.charset)
	w.WriteByte(vocab.level)
	w.WriteByte(vocab.reserve)
	w.WriteBool(vocab.customIDs)

	w.WriteUint24(uint32(len(vocab.info)))
	w.WriteUint24(vocab.deleteToken)
	for i, token := range vocab.info {
		w.WriteBytes8(token.token) // a single byte (uint8) specifying length of token bytes, and then that many bytes
		w.WriteByte(token.alt.data.flag)
		w.WriteByte(token.alt.data.nWords)
		// Write the index of the token
		w.WriteUint24(token.alt.index)
		w.WriteUint24(token.alt.index2)
		// The index of the token should always be less than the current index (because the list is sorted), check this is true
		if (token.alt.index > uint32(i) && token.alt.index != DOES_NOT_EXIST) || (token.alt.index2 > uint32(i) && token.alt.index2 != DOES_NOT_EXIST) {
			return errors.New(`Vocabulary is corrupt and cannot be saved`)
		}
		w.WriteFloat32(token.score)
	}

	for i:=0; i<256; i++ {
		w.WriteByte(vocab.beginByte[i])
	}

	if vocab.customIDs {
		for _, v := range vocab.index2id {
			w.WriteUint24(v)
		}
	}

	w.WriteUint24(uint32(len(vocab.deleted)))
	for _, deleted := range vocab.deleted {
		w.WriteBytes8(deleted.token)
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
	res.usingCapcode = r.ReadBool()
	res.useUnk = r.ReadBool()
	res.charset = r.ReadByte()
	res.level = r.ReadByte()
	res.reserve = r.ReadByte()
	res.customIDs = r.ReadBool()

	if res.charset > 2 {
		return nil, errors.New(`Not a valid TokenMonster vocabulary.`)
	}
	l := int(r.ReadUint24())
	res.deleteToken = r.ReadUint24()
	res.info = make([]tokenInfo, l)
	res.dictionary = new(pansearch.Fast)
	lengths := make([]int, l)
	for i:=0; i<l; i++ {
		token = tokenInfo{}
		key = r.ReadBytes8()
		lengths[i] = len(key)
		if len(key) > res.maxlen {
			if len(key) > 40 {
				return nil, errors.New(`Not a valid TokenMonster vocabulary.`)
			}
			res.maxlen = len(key)
		}
		token.token = key
		res.dictionary.Add(key)
		token.alt.data.flag = r.ReadByte()
		token.alt.data.nWords = r.ReadByte()
		token.alt.index = r.ReadUint24()
		if token.alt.index != DOES_NOT_EXIST {
			token.alt.length = lengths[token.alt.index]
		}
		token.alt.index2 = r.ReadUint24()
		if token.alt.index2 != DOES_NOT_EXIST {
			token.alt.length2 = lengths[token.alt.index2]
		}
		token.score = r.ReadFloat32()
		res.info[i] = token
	}

	for i:=0; i<256; i++ {
		res.beginByte[i] = r.ReadByte()
	}

	if res.customIDs {
		if res.useUnk {
			l++
		}
		var max int
		var v uint32
		res.index2id = make([]uint32, l)
		for i:=0; i<l; i++ {
			v = r.ReadUint24()
			res.index2id[i] = v
			max = branchless.Max(max, int(v))
		}
		res.id2index = make([]uint32, max + 1)
		for i:=0; i<l; i++ {
			res.id2index[res.index2id[i]] = uint32(i)
		}
	}

	if r.EOF() != nil {
		l = int(r.ReadUint24())
		if l > 0 {
			res.deleted = make([]deletedStruct, l)
			for i:=0; i<l; i++ {
				res.deleted[i].token = r.ReadBytes8()
				res.deleted[i].score = r.ReadFloat32()
			}
		}
		if r.EOF() != nil {
			return nil, errors.New(`Not a valid TokenMonster vocabulary.`)
		}
	}
	res.dictionary.Build()
	return &res, nil
}

// --------- GENERATE & MODIFY ---------

// NewVocab makes a fresh vocabulary from a custom list of tokens.
// If you generated your vocabulary with TokenMonster tools, you will not be using this function but instead using `Load`.
func NewVocab(tokens [][]byte, specialTokens [][]byte, charset uint8, usingCapcode bool, include256bytes bool, include128bytes bool, includeUTF8bytes bool, includeASCIIbytes bool, includeExtendedBytes bool, excludeOtherBytes bool) *Vocab {
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
	return vocab.PrivateGenerateVocab(nil, nil, tokens, nil, specialTokens, charset, usingCapcode, 4, reserve, 0)
}

// Adds a single token to the vocabulary.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
// All normalization and capcode is applied automatically.
func (vocab *Vocab) AddToken(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, [][]byte{token}, nil, nil, 0, false, 0, 0, 0)
}

// Adds a single special token to the vocabulary.
// A special token is special because only this token is allowed to tokenize text containing this.
// If any regular tokens contain your special token within them, they will be deleted.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
// All normalization and capcode is applied automatically.
func (vocab *Vocab) AddSpecialToken(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, [][]byte{token}, 0, false, 0, 0, 0)
}

// Deletes a single token from the vocabulary.
// Tokens to delete can be capcoded encoded or not, it will look for both.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
// All normalization and capcode is applied automatically.
func (vocab *Vocab) DeleteToken(token []byte) {
	vocab.PrivateGenerateVocab(nil, nil, nil, [][]byte{token}, nil, 0, false, 0, 0, 0)
}

// Deletes a single token from the vocabulary by specifying the ID.
// This changes all the token IDs, all higher than this one will shift 1 token ID down to fill the gap.
func (vocab *Vocab) DeleteTokenID(id uint32) {
	if id >= uint32(len(vocab.info)) {
		return
	}
	vocab.PrivateGenerateVocab(nil, nil, nil, [][]byte{vocab.info[id].token}, nil, 0, false, 0, 0, 0)
}

// Adds multiple regular and optionally special tokens.
// You can use `size` to resize the vocabulary to keep it at a specific size.
// Enter `size` 0 to not resize.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
func (vocab *Vocab) AddTokens(addTokens [][]byte, specialTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, addTokens, nil, specialTokens, 0, false, 0, 0, size)
}

// Add multiple special tokens and optionally resize.
// Enter `size` 0 to not resize.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
func (vocab *Vocab) AddSpecialTokens(specialTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, specialTokens, 0, false, 0, 0, size)
}

// Delete multiple tokens and optionally resize.
// Tokens to delete can be capcoded encoded or not, it will look for both.
// Enter `size` 0 to not resize.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
func (vocab *Vocab) DeleteTokens(deleteTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, deleteTokens, nil, 0, false, 0, 0, size)
}

// Add regular & special tokens, delete tokens and resize, all in one.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
func (vocab *Vocab) ModifyVocabulary(addTokens [][]byte, specialTokens [][]byte, deleteTokens [][]byte, size int) {
	vocab.PrivateGenerateVocab(nil, nil, addTokens, deleteTokens, specialTokens, 0, false, 0, 0, size)
}

// Resize the vocabulary by deleting the worst scoring tokens.
// You can also resize the vocabulary if any tokens have previously been deleted.
// Modifying a vocabulary changes all the token IDs, it does not add the token at the end, the tokens are gives IDs alphabetically.
func (vocab *Vocab) Resize(size int) {
	vocab.PrivateGenerateVocab(nil, nil, nil, nil, nil, 0, false, 0, 0, size)
}

// Enables the UNK token.
// The UNK token will be inserted for every character for which there is no token.
// The UNK token takes the last token ID in the vocabulary, therefore it can be enabled or disabled without affecting the rest of the vocabulary.
// This function returns true if an UNK token is added, it will return false if all characters already have tokens and therefore there is no use for an UNK token.
// You can resize after this if you want to keep the vocabulary sized as it was before.
func (vocab *Vocab) EnableUnkToken() bool {
	if vocab.useUnk {
		return true
	}
	if !canHaveUnkToken(vocab.NumReservedTokens(), vocab.usingCapcode) {
		return false
	}
	vocab.useUnk = true
	if len(vocab.info) != 0 && len(vocab.info) == vocab.dictionary.Len() {
		vocab.info = append(vocab.info, tokenInfo{token:nil, alt:tokenOuter{index:DOES_NOT_EXIST, index2:DOES_NOT_EXIST}})
	}
	return true
}

// Disables the UNK token.
// The UNK token will be inserted for every character for which there is no token.
// The UNK token takes the last token ID in the vocabulary, therefore it can be enabled or disabled without affecting the rest of the vocabulary.
func (vocab *Vocab) DisableUnkToken() {
	if !vocab.useUnk {
		return
	}
	vocab.useUnk = false
	if len(vocab.info) != 0 {
		if len(vocab.info) > vocab.dictionary.Len() {
			vocab.info = vocab.info[0 : vocab.dictionary.Len()]
		}
	}
}

// Don't use this function, it's exported because it's used by the exportvocab tool.
func (vocab *Vocab) PrivateGenerateVocab(tokens [][]byte, scores []float32, addTokens [][]byte, deleteTokens [][]byte, specialTokens [][]byte, charset uint8, usingCapcode bool, level uint8, reserve uint8, resize int) *Vocab {

	// Note, tokens is assumed already to be capcoded and normalized
	// addTokens and deleteTokens are assumed to be not capcoded or normalized, and so this is applied to them
	// To generate a full vocabulary from all custom tokens, you can leave `tokens` empty and put them all in `addTokens`
	if len(vocab.info) == 0 {
		vocab.charset = charset
		vocab.usingCapcode = usingCapcode
		vocab.level = level
	} else {
		charset = vocab.charset
		usingCapcode = vocab.usingCapcode
	}
	charTable := make([]bool, 256)
	if reserve & 1 != 0 {
		gen256bytes(charTable, usingCapcode, charset)
	}
	if reserve & 2 != 0 {
		gen128bytes(charTable, usingCapcode, charset)
	}
	if reserve & 4 != 0 {
		genUTF8bytes(charTable, usingCapcode, charset)
	}
	if reserve & 8 != 0 {
		genASCIIbytes(charTable, usingCapcode, charset)
	}
	if reserve & 16 != 0 {
		genExtendedbytes(charTable, usingCapcode, charset)
	}
	excludeOtherBytes := (reserve & 32) != 0
	vocab.reserve = vocab.reserve | reserve
	specialMap := make(map[string]bool)
	scoresMap := make(map[string]float32)
	singleChars := make([]byte, 0, 256)
	deletedTokens := new(pansearch.Counter)
	var originalTokens [][]byte
	var originalSpecialTokens [][]byte
	var newSpecialTokens [][]byte
	if len(vocab.info) > 0 {
		var on uint32
		for _, info := range vocab.info {
			if info.score > 0 {
				scoresMap[string(info.token)] = info.score
			}
			if len(info.token) == 1 {
				if !excludeOtherBytes {
					charTable[info.token[0]] = true
				}
			} else if info.alt.data.flag & 64 != 0 {
				originalSpecialTokens = append(originalSpecialTokens, info.token)
			} else {
				originalTokens = append(originalTokens, info.token)
				on++
			}
		}
	}
	for i, v := range tokens {
		if scores[i] > 0 {
			scoresMap[string(v)] = scores[i]
		}
	}
	if len(vocab.deleted) > 0 {
		for _, v := range vocab.deleted {
			if v.score > 0 {
				scoresMap[string(v.token)] = v.score
			}
			deletedTokens.Add(v.token, 1)
		}
	}

	ungreedySuffixes := []string{"'s", "’s"}
	ungreedySuffixesB := make([][]byte, len(ungreedySuffixes))
	if charset == 1 {
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = []byte(suffix)
		}
	} else if charset == 2 {
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = convertStringToUTF16WithNFDNormalization(suffix)
		}
	}

	// Add and delete tokens
	var err error
	var exists bool
	deleter := make(map[string]bool)
	if len(deleteTokens) > 0 {
		for _, v := range deleteTokens {
			if len(v) > 0 && len(v) <= 40  {
				deleter[string(v)] = true
				v, err = normalizeTokenBytesSafe(v, usingCapcode, charset)
				if err != nil {
					deleter[string(v)] = true
				}
			}
		}
	}
	for _, special := range specialTokens {
		if len(special) > 0 && len(special) <= 40  {
			special, err = normalizeTokenBytes(special, usingCapcode, charset)
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
		if len(v) > 0 && len(v) <= 40  {
			v, err = normalizeTokenBytes(v, usingCapcode, charset)
			if err == nil {
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
	if vocab.useUnk && !canHaveUnkToken(len(singleChars), usingCapcode) {
		vocab.useUnk = false
	}
	if vocab.useUnk {
		total++ // unk token
	}

	// Resize vocabulary (smaller)
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
				score = scoresMap[string(v)]
				vocab.deleted[on] = deletedStruct{v, score}
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

	dictionary := new(pansearch.Fast)
	for _, v := range singleChars {
		dictionary.Add([]byte{v})
	}
	for _, v := range tokens {
		if len(v) > 0 {
			dictionary.Add(v)
		}
	}
	for _, v := range newSpecialTokens {
		if len(v) > 0 {
			dictionary.Add(v)
		}
	}
	dictionary.Build()

	vocab.maxlen = dictionary.LongestLength()
	l := dictionary.Len()
	if vocab.useUnk {
		l++
	}

	// Set the deleteToken
	if vocab.charset == 1 {
		if vocab.usingCapcode {
			if index, found := dictionary.Find([]byte{capcode.DeleteToken}); found {
				vocab.deleteToken = index
			}
		} else {
			if index, found := dictionary.Find([]byte{capcode.NoCapcodeDeleteToken}); found {
				vocab.deleteToken = index
			}
		}
	} else {
		vocab.deleteToken = DOES_NOT_EXIST
	}

	vocabList := make([]tokenInfo, l)
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
		var index uint32
		for eof := false; !eof; {
			token, eof = dictionary.Next()
			score = scoresMap[string(token)]
			tokenData = tokenInfo{token:token, score:score, alt:tokenOuter{index:DOES_NOT_EXIST, index2:DOES_NOT_EXIST}}
			// Check for special tokens
			if len(newSpecialTokens) > 0 {
				if _, found = specialMap[string(token)]; found {
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
			} else if isCapcode(r, charset, usingCapcode) {
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
			if isCapcode(r, charset, usingCapcode) {
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
					if !usingCapcode {
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
						case isCapcode(r2, charset, usingCapcode):
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
			vocabList[on] = tokenData
			on++
		}
	}

	// Add "unk" token
	if vocab.useUnk {
		vocabList[dictionary.Len()] = tokenInfo{token:nil, alt:tokenOuter{index:DOES_NOT_EXIST, index2:DOES_NOT_EXIST}}
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

	vocab.info = vocabList
	vocab.dictionary = dictionary
	return vocab
}
