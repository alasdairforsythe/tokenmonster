package main

/*
	Filter testing: https://goplay.tools/snippet/a0iuvwvgL-n
*/

import (
	"os"
	"log"
	"fmt"
	"flag"
	"sync"
	"time"
	"bytes"
	"errors"
	"strings"
	"runtime"
	"reflect"
	"unicode"
	"io/ioutil"
	"unicode/utf8"
	"unicode/utf16"
	"encoding/binary"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	uni "golang.org/x/text/encoding/unicode"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Conv"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/capcode/go"
)

const (
	minHighSurrogate = 0xD800 // Start of high surrogate range
	maxHighSurrogate = 0xDBFF // End of high surrogate range
	minLowSurrogate  = 0xDC00 // Start of low surrogate range
	maxLowSurrogate  = 0xDFFF // End of low surrogate range
	runeError 		 = '\uFFFD'
	apostrophe	   	 = '\''
	apostrophe2      = '’'
)

var delimiterPairs = map[rune]rune{
	'(': ')',
	'[': ']',
	'{': '}',
	'\'': '\'',
	'"': '"',
	'‘': '’',
	'“': '”',
	'«': '»',
	'‹': '›',
	'‛': '’',
	'`': '`',
	'„': '”',
	'″': '″',
	'〝': '〞',
	'「': '」',
	'『': '』',
	'｢': '｣',
	'〈': '〉',
	'《': '》',
	'‟': '”',
	'❛': '❜',
	'❝': '❞',
	'❮': '❯',
	'〔': '〕',
	'⸨': '⸩',
}

var delimiters = map[rune]bool{
	'(': true, ')': true, '[': true, ']': true, '{': true, '}': true, '\'': true, 
    '"': true, '‘': true, '’': true, '“': true, '”': true, '«': true, '»': true, 
    '‹': true, '›': true, '‛': true, '`': true, '„': true, '″': true, '〝': true, 
    '〞': true, '「': true, '」': true, '『': true, '』': true, '｢': true, '｣': true, 
    '〈': true, '〉': true, '《': true, '》': true, '‟': true, '❛': true, '❜': true, 
    '❝': true, '❞': true, '❮': true, '❯': true, '〔': true, '〕': true, '⸨': true, '⸩': true,
}

/*

The defaults are good for an 840MB dataset with peak RAM usage around 200GB.
microChunks can be increased to reduce memory usage, but at a massive cost of performance.

*/

var (
	datasetFilename string
	saveFilename string
	maxTokenLength int = 40
	minOccurPerChunk int = 4
	minOccurTotal int = 0
	minOccurSingles int = 0
	chunkSize int = 100000000
	chunkSizeString string
	microChunks int = 5
	minOccurPerMicroChunk int = 2
	usingCapcode bool = true
	disableCapcode bool = false
	charset string
	charsetFlag uint8
	multithreaded bool
	levelFlag string
	level uint8
	charTable [256]int
	numWorkers int = 8
)

type workStruct struct {
	chunkId int
	data [][]byte
	tokens *pansearch.Counter
}

func flagRequired(name string, value interface{}) {
    switch v := reflect.ValueOf(value); v.Kind() {
    case reflect.String:
        if v.String() == "" {
            fmt.Fprintf(os.Stderr, "%s is required\n", name)
            flag.Usage()
            os.Exit(1)
        }
    case reflect.Int:
        if v.Int() == 0 {
            fmt.Fprintf(os.Stderr, "%s is required\n", name)
            flag.Usage()
            os.Exit(1)
        }
    }
}

func formatInt(v int) string {
	return string(conv.FormatThousands(conv.Bytes(v), ','))
}

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
	// Assume LittleEndian if not specified
	endian := uni.LittleEndian
	bomPolicy := uni.IgnoreBOM
	// Check for BOM
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

func saveTokensToFile(filename string, obj *pansearch.Counter, specifyLevel uint8) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	defer w.Close()

	w.WriteBool(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteByte(specifyLevel)
	w.WriteByte(0) // reserve
	w.WriteByte(0) // custom

	singleChars := make([]byte, 256)
	var on int
	for i, v := range charTable[:] {
		if v >= minOccurSingles {
			singleChars[on] = byte(i)
			on++
		}
	}
	singleChars = singleChars[0:on]

	w.WriteUint64(uint64(obj.Len() + len(singleChars)))
	for _, b := range singleChars {
		w.WriteByte(1) // length
		w.WriteByte(b) // the single character
	}
	if obj.Reset() {
		var b []byte
		var eof bool
		for !eof {
			b, _, eof = obj.Next()
			w.WriteBytes8(b)
		}
	}
	return nil
}

func isLetter(r rune) bool {
	return (unicode.IsLetter(r) && (!usingCapcode || (r != 'W' && r != 'C' && r != 'D'))) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isCapcode(r rune) bool {
	return r == '\x7F' || (usingCapcode && (r == 'C' || r == 'W' || r == 'D'))
}

func isOther(r rune) bool {
	return !isAlphaNum(r)
}

func isAlphaNum(r rune) bool {
	return (unicode.IsLetter(r) && (!usingCapcode || (r != 'W' && r != 'C' && r != 'D'))) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

/*
func isDelimiter(r rune) bool {
	if r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' || r == '\'' || r == '"' || r == '‘' || r == '’' || r == '“' || r == '”' || r == '«' || r == '»' || r == '‹' || r == '›' || r == '‛' || r == '`' || r == '„' || r == '″' || r == '〝' || r == '〞' || r == '「' || r == '」' || r == '『' || r == '』' || r == '｢' || r == '｣' || r == '〈' || r == '〉' || r == '《' || r == '》' || r == '‟' || r == '❛' || r == '❜' || r == '❝' || r == '❞' || r == '❮' || r == '❯' || r == '〔' || r == '〕' || r == '⸨' || r == '⸩' {
		return true
	}
	return false
}
*/
func isDelimiter(r rune) bool {
	return delimiters[r]
}

func decodeRune(b []byte) (rune, int) {
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

func decodeLastRune(b []byte) (rune, int) {
	switch charsetFlag {
		case 1: // UTF-8
			return utf8.DecodeLastRune(b)
		case 2: // UTF-16
			if len(b) < 2 {
				return runeError, 0
			}
			u := binary.LittleEndian.Uint16(b[len(b)-2:])
			if u >= minLowSurrogate && u <= maxLowSurrogate {
				// This is a surrogate pair. We need another two bytes.
				if len(b) < 4 {
					return runeError, 0
				}
				u2 := binary.LittleEndian.Uint16(b[len(b)-4:])
				if u2 < minHighSurrogate || u2 > maxHighSurrogate {
					return runeError, 0
				}
				r := utf16.Decode([]uint16{u2, u})
				if len(r) == 0 {
					return runeError, 0
				}
				return r[0], 4
			}
			return rune(u), 2
		default:
			return -1, 0
	}
}

func trim(b []byte) []byte {
	_, n := decodeLastRune(b)
	return b[:len(b) - n]
}

/*
func stripLastPunc(tok []byte) []byte {
	rlast, nlast := decodeLastRune(tok)
	if isOther(rlast) {
		if unicode.IsSpace(rlast) || isDelimiter(rlast) {
			return tok
		}
		if isCapcode(rlast) {
			rlast2, nlast2 := decodeLastRune(tok[0 : len(tok)-nlast])
			if isOther(rlast2) {
				if unicode.IsSpace(rlast2) || isDelimiter(rlast2) {
					return tok[0 : len(tok)-nlast]
				}
			}
			if isCapcode(rlast2) {
				rlast3, nlast3 := decodeLastRune(tok[0 : len(tok)-(nlast+nlast2)])
				if isOther(rlast3) {
					if unicode.IsSpace(rlast3) || isDelimiter(rlast3) || isCapcode(rlast3) {
						return tok[0 : len(tok)-(nlast+nlast2)]
					}
				}
				return tok[0 : len(tok)-(nlast+nlast2+nlast3)]
			}
			return tok[0 : len(tok)-(nlast+nlast2)]
		}
		return tok[0 : len(tok)-nlast]
	}
	return tok
}*/

func stripLastPunc(tok []byte) []byte {
	rlast, nlast := decodeLastRune(tok)
	if isOther(rlast) {
		if unicode.IsSpace(rlast) || isDelimiter(rlast) || isCapcode(rlast) {
			return tok
		}
		return tok[0 : len(tok)-nlast]
	}
	return tok
}

func stripOpenClose(tok []byte, r rune, n int) ([]byte, bool) {
	if len(tok) <= n {
		return tok, false
	}
	if r == ' ' {
		var nx int
		r, nx = decodeRune(tok[1:])
		n += nx
		if len(tok) <= n {
			return tok, false
		}
	}
	closer, exists := delimiterPairs[r]
	if !exists {
		return tok, false
	}
	lastRune, n2 := decodeLastRune(tok)
	if lastRune == closer {
		if len(tok)-n2 >= n {
			return tok[n : len(tok)-n2], true
		}
	}
	return tok, false
}

func filterClean(tok []byte) ([]byte, bool) {
	// If it contains any letters, numbers, capcode or delimiters, it may not contain more than 1 space in a row, except 2 new lines in a row
	var r rune
	var n int
	var nSpace, nNewLines, spaceRuns, spaceChar uint8
	var hasAlpha, hasCapcode, exists, lastSpace, doubleSpace, other, firstSpace bool

	rnext, nnext := decodeLastRune(tok)
	tok = tok[0 : len(tok)-nnext]
	if len(tok) < 2 {
		return tok, false
	}
	trimmed := tok

	// Remove /n & /r from the end as these are always okay
	var removed bool
	for n = len(tok) - 1; n > 0; n-- {
		if tok[n] != '\n' && tok[n] != '\r' {
			tok = tok[0 : n+1]
			break
		} else {
			removed = true
		}
	}

	for i := 0; i < len(tok); i += n {
		r, n = decodeRune(tok[i:])
		if isLetter(r) || unicode.IsNumber(r) { // has letter or has number
			exists = true
			hasAlpha = true
			lastSpace = false
		} else if isCapcode(r) {
			hasCapcode = true
		} else if isDelimiter(r) {
			exists = true
			lastSpace = false
		} else if unicode.IsSpace(r) {
			if i == 0 {
				firstSpace = true
				if r == ' ' {
					spaceChar = 1
				}
			} else if i == 1 {
				spaceChar = 0
			}
			nSpace++
			if r == '\n' || r == '\r' || r == '\t' {
				nNewLines++
			}
			if lastSpace {
				doubleSpace = true
				if hasAlpha && nSpace != nNewLines {
					return trimmed, false
				}
			} else {
				spaceRuns++
			}
			lastSpace = true
		} else {
			other = true
			lastSpace = false
		}
	}
	spaceRuns -= spaceChar
	nSpace -= spaceChar
	if doubleSpace && (exists || (other && spaceRuns > 1)) {
		if (r == ' ' && other && !removed) || (!lastSpace && !firstSpace && nSpace > 3) || (!(nSpace == nNewLines && spaceRuns <= 1) && !(nSpace >= uint8(len(tok)-1) && (!lastSpace || !firstSpace))) {
			return trimmed, false
		}
	}
	// If it contains letters or numbers or capcode it may not end with a space
	if (hasAlpha || hasCapcode || exists || (other && isAlphaNum(rnext))) && r == ' ' && !removed {
		return trimmed, false
	}
	// If it contains letters and spaces, except the first space, if may not
	return trimmed, true
}

func filterBalanced(tok []byte) ([]byte, bool) {
	// If it contains any letters, numbers, capcode or delimiters, it may not contain more than 1 space in a row, except 2 new lines in a row
	var r rune
	var n int
	var nSpace, nNewLines, spaceRuns uint8
	var hasAlpha, hasCapcode, exists, lastSpace, doubleSpace, other, firstSpace, hasLetter, spaceChar bool

	rnext, nnext := decodeLastRune(tok)
	tok = tok[0 : len(tok)-nnext]
	if len(tok) < 2 {
		return tok, false
	}

	for i := 0; i < len(tok); i += n {
		r, n = decodeRune(tok[i:])
		if isLetter(r) { // has letter or has number
			exists = true
			hasAlpha = true
			lastSpace = false
			hasLetter = true
		} else if unicode.IsNumber(r) {
			exists = true
			hasAlpha = true
			lastSpace = false
		} else if isCapcode(r) {
			hasCapcode = true
		} else if isDelimiter(r) {
			exists = true
			lastSpace = false
		} else if unicode.IsSpace(r) {
			if i == 0 {
				firstSpace = true
				if r == ' ' {
					spaceChar = true
				}
			} else if i == 1 {
				spaceChar = false
			}
			nSpace++
			if r == '\n' || r == '\r' || r == '\t' {
				nNewLines++
			}
			if lastSpace {
				doubleSpace = true
				if hasAlpha {
					return tok, false
				}
			} else {
				spaceRuns++
			}
			lastSpace = true
		} else {
			other = true
			lastSpace = false
		}
	}
	if spaceChar {
		firstSpace = false
		spaceRuns--
		nSpace--
	}
	if r == '\n' || r == '\r' {
		spaceRuns--
	}
	if doubleSpace && (exists || (other && spaceRuns > 1)) {
		if hasAlpha || (r == ' ' && other) || (exists && (nSpace > 5 || (nSpace > 3 && nSpace != nNewLines))) || (!(nSpace == nNewLines && spaceRuns <= 1) && !(nSpace >= uint8(len(tok)-1) && (!lastSpace || !firstSpace))) {
			return tok, false
		}
	}
	// If it contains letters or numbers, don't end on capcode wordToken or CharacterToken unless preceded by a .
	if hasAlpha && usingCapcode && isCapcode(r) {
		if len(tok) < 3 {
			return tok, false
		}
		if !((tok[len(tok)-2] == '.' || tok[len(tok)-2] == '-') || ((tok[len(tok)-2] == 'D' || tok[len(tok)-2] == 127) && (tok[len(tok)-3] == '.' || tok[len(tok)-3] == '-'))) {
			return tok, false
		}
	}
	// If it contains letters or numbers or capcode it may not end with any kind of space
	if (hasAlpha || hasCapcode) && unicode.IsSpace(r) {
		return tok, false
	}
	// If it contains punctuation it may not end with a space
	if (other || exists) && r == ' ' {
		return tok, false
	}
	// Don't do a full word, then half of the next word
	if hasLetter && isLetter(rnext) && (nSpace >= 2 || (spaceChar && nSpace >= 1) || ((nSpace == 1 || (nSpace == 0 && spaceChar)) && tok[0] != ' ')) {
		return tok, false
	}
	return tok, true
}

func filterConsistent(tok []byte) ([]byte, bool) {
	var r1, r2, r3 rune
	var n1, n2, n3 int

	rnext, nnext := decodeLastRune(tok)
	tok = tok[0 : len(tok)-nnext]
	trimmed := tok
	if len(tok) < 2 {
		return trimmed, false
	}

	r1, n1 = decodeRune(tok)
	if len(tok) > n1 {
		r2, n2 = decodeRune(tok[n1:])
	} else {
		return trimmed, true // it's a single multi-byte character
	}

	// Strip open and closers
	var openCloseStripped bool
	tok, openCloseStripped = stripOpenClose(tok, r1, n1)
	if openCloseStripped {
		if len(tok) == 0 {
			return trimmed, true
		}
		r1, n1 = decodeRune(tok)
		if len(tok) > n1 {
			r2, n2 = decodeRune(tok[n1:])
		} else {
			return trimmed, true
		}
	}

	// Doubles of anything are allowed
	if len(tok) == 2 {
		if r1 == r2 {
			return trimmed, true
		}
	}

	// Setup for allowing capcode marker beginnings
	if isCapcode(r1) && (isCapcode(r2) || r2 == ' ') {
		if r2 == ' ' {
			tok = tok[1:]
			r1 = ' '
			n1 = 1
		} else {
			tok = tok[2:]
			r1, n1 = decodeRune(tok)
		}
		r2, n2 = decodeRune(tok)
		if len(tok) > n1 {
			r2, n2 = decodeRune(tok[n1:])
		} else {
			return trimmed, true // it's a single multi-byte character
		}
	}

	// If it begins _A it may not contain anything other than letter or [apostrophes or space, no more than 1 in a row]
	// It may contain more than one word
	// It may not end on a space
	// It may contain capcode markers, but not end on characterToken or wordToken
	// It may end on a single punctuation that is not a delimiter or space
	// It may contain "-D " inside it or end on -D
	if r1 == ' ' && isLetter(r2) {
		var apos, space, hasSpace bool
		var hyphenok uint8
		tok = stripLastPunc(tok)
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if isLetter(r3) {
				space = false
				apos = false
				hyphenok = 0
			} else {
				if r3 == ' ' {
					if space {
						return trimmed, false
					}
					space = true
					apos = false
					hasSpace = true
					hyphenok = 0
					continue
				} else {
					if r3 == apostrophe || r3 == apostrophe2 {
						if apos {
							return trimmed, false
						}
						apos = true
						space = false
						continue
					}
					space = false
					apos = false
					if r3 == '-' || r3 == '.' {
						if hyphenok == 0 {
							hyphenok = 1
							continue
						}
					} else if isCapcode(r3) {
						if hyphenok == 1 {
							hyphenok = 2
							continue
						} else if hyphenok == 2 {
							hyphenok = 3
							continue
						}
					}
					return trimmed, false
				}
			}
		}
		if r3 == ' ' || r3 == '.' || r3 == '-' || (isCapcode(r3) && hyphenok <= 1) {
			return trimmed, false
		}
		if isLetter(rnext) && hasSpace {
			return trimmed, false
		}
		return trimmed, true
	}

	// If it begins with _1, it may not contain anything other than numbers
	// But it may end on a punctuation (non-delimiter)
	if r1 == ' ' && unicode.IsNumber(r2) {
		tok = stripLastPunc(tok)
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if !unicode.IsNumber(r3) {
				return trimmed, false
			}
		}
		return trimmed, true
	}

	// If it begins with a number, it may not contain anything other than numbers
	if unicode.IsNumber(r1) {
		if !unicode.IsNumber(r2) {
			return trimmed, false
		}
		tok = stripLastPunc(tok)
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if !unicode.IsNumber(r3) {
				return trimmed, false
			}
		}
		return trimmed, true
	}

	// If it begins with an apostrophe or letter, it may not contain anything other than letters
	if isLetter(r1) && !isLetter(r2) {
		return trimmed, false
	}
	if ((r1 == apostrophe || r1 == apostrophe2) && isLetter(r2)) || isLetter(r1) {
		tok = stripLastPunc(tok)
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if !isLetter(r3) {
				return trimmed, false
			}
		}
		return trimmed, true
	}

	var numDelim, numCapcode, numSpace, numNewline, numOther, spacesRun, maxSpacesRun, nSpaceRuns uint8
	var lastSpace bool
	for i := 0; i < len(tok); i += n3 {
		r3, n3 = decodeRune(tok[i:])
		switch {
		case isAlphaNum(r3):
			return trimmed, false
		case isDelimiter(r3):
			numDelim++
			lastSpace = false
		case isCapcode(r3):
			numCapcode++
		case unicode.IsSpace(r3):
			numSpace++
			if r3 == '\n' || r3 == '\r' || r3 == '\t' {
				numNewline++
			}
			if lastSpace {
				spacesRun++
			} else {
				if spacesRun > maxSpacesRun {
					maxSpacesRun = spacesRun
				}
				nSpaceRuns++
				spacesRun = 1
				lastSpace = true
			}
		default:
			numOther++
			lastSpace = false
		}
	}
	if spacesRun > maxSpacesRun {
		maxSpacesRun = spacesRun
	}
	// It can't end with a space, unless it contains all spaces
	if r3 == ' ' && (numOther > 0 || numCapcode > 0 || numDelim > 0) {
		return trimmed, false
	}
	// Any single or double char followed by a capcode marker or two is okay
	if isCapcode(r3) { // last is capcode
		if len(tok) == 2 || (len(tok) <= 4 && numCapcode == 2) || (numOther == 0 && numDelim == 0) {
			return trimmed, true
		}
	}
	// If 2 or more spaces in a row and not all spaces, disallow, except if all spaces are newlines, unless there are more than 3 puncs
	if maxSpacesRun > 1 && (numDelim != 0 || numCapcode != 0 || numOther != 0) && ((numSpace != numNewline || (numOther+numDelim) > 3) || nSpaceRuns > 1) {
		return trimmed, false
	}
	otherAndSpace := numOther + numSpace + numCapcode
	if (numDelim > 1 && len(tok) > 2) || (numDelim > 0 && openCloseStripped) { // 2 delimiters are not allowed (with above exceptions)
		return trimmed, false
	}
	if numDelim == 1 {
		if isDelimiter(r1) {
			if len(tok) <= 3 && numSpace <= 1 {
				return trimmed, true
			}
		} else {
			if (otherAndSpace <= 1) || (len(tok) == 3 && (r1 == ' ' || r2 == ' ')) {
				return trimmed, true // a delimiter may be preceded by 1 punctuation or space
			}
		}
		return trimmed, false // anything else is not okay
	}

	return trimmed, true
}

func filterStrict(tok []byte) ([]byte, bool) {
	var r1, r2, r3 rune
	var n1, n2, n3 int

	rnext, nnext := decodeLastRune(tok)
	tok = tok[0 : len(tok)-nnext]
	trimmed := tok
	if len(tok) < 2 {
		return trimmed, false
	}

	r1, n1 = decodeRune(tok)
	if len(tok) > n1 {
		r2, n2 = decodeRune(tok[n1:])
	} else {
		return trimmed, true // it's a single multi-byte character
	}

	// If it begins _A it may not contain anything other than letter or [apostrophes or space, no more than 1 in a row]
	// It may contain more than one word
	// It may not end on a space
	if r1 == ' ' && isLetter(r2) {
		var apos, space, hasSpace bool
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if isLetter(r3) {
				space = false
				apos = false
			} else {
				if r3 == ' ' {
					if space {
						return trimmed, false
					}
					hasSpace = true
					space = true
					apos = false
					continue
				} else {
					if r3 == apostrophe || r3 == apostrophe2 {
						if apos {
							return trimmed, false
						}
						apos = true
						space = false
						continue
					}
					return trimmed, false
				}
			}
		}
		if r3 == ' ' {
			return trimmed, false
		}
		if isLetter(rnext) && hasSpace {
			return trimmed, false
		}
		return trimmed, true
	}

	// If it begins with _1, it may not contain anything else
	if r1 == ' ' && unicode.IsNumber(r2) {
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if !unicode.IsNumber(r3) {
				return trimmed, false
			}
		}
		return trimmed, true
	}

	// If it begins with a number, it may not contain anything other than numbers
	if unicode.IsNumber(r1) {
		if !unicode.IsNumber(r2) {
			return trimmed, false
		}
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if !unicode.IsNumber(r3) {
				return trimmed, false
			}
		}
		return trimmed, true
	}

	// If it begins with an apostrophe or letter, it may not contain anything other than letters
	if isLetter(r1) && !isLetter(r2) {
		return trimmed, false
	}
	if ((r1 == apostrophe || r1 == apostrophe2) && isLetter(r2)) || isLetter(r1) {
		for i := n1 + n2; i < len(tok); i += n3 {
			r3, n3 = decodeRune(tok[i:])
			if !isLetter(r3) {
				return trimmed, false
			}
		}
		return trimmed, true
	}

	// It may have only have openclosers if it has both of them (singles are always allowed)
	if len(tok) == 2 {
		if (r1 == '(' && r2 == ')') || (r1 == '[' && r2 == ']') || (r1 == '{' && r2 == '}') || (r1 == '"' && r2 == '"') || (r1 == '\'' && r2 == '\'') {
			return trimmed, true
		}
		// It may have 2 characters if one of them is a comma
		if (r1 == ',' || r2 == ',' || r1 == '.') && !unicode.IsSpace(r2) {
			return trimmed, true
		}
	}

	var numDelim, numCapcode, numSpace, numNewline, numOther, spacesRun, maxSpacesRun, nSpaceRuns uint8
	var lastSpace bool
	for i := 0; i < len(tok); i += n3 {
		r3, n3 = decodeRune(tok[i:])
		switch {
		case isAlphaNum(r3):
			return trimmed, false
		case isDelimiter(r3):
			numDelim++
			lastSpace = false
		case isCapcode(r3):
			numCapcode++
		case unicode.IsSpace(r3):
			numSpace++
			if r3 == '\n' || r3 == '\r' {
				numNewline++
			}
			if lastSpace {
				spacesRun++
			} else {
				if spacesRun > maxSpacesRun {
					maxSpacesRun = spacesRun
				}
				nSpaceRuns++
				spacesRun = 1
				lastSpace = true
			}
		default:
			numOther++
			lastSpace = false
		}
	}
	if spacesRun > maxSpacesRun {
		maxSpacesRun = spacesRun
	}

	// Any single char followed by a capcode marker or two is okay
	if isCapcode(r3) && (len(tok) == 2 || (len(tok) == 3 && numCapcode == 2)) {
		return trimmed, true
	}

	if numSpace != uint8(len(tok)) && r3 == ' ' {
		return trimmed, false
	}

	// If 2 or more spaces in a row and not all spaces, disallow, except if all spaces are newlines
	if maxSpacesRun > 1 && (numDelim != 0 || numCapcode != 0 || numOther != 0) && ((numSpace != numNewline || numOther > 1 || nSpaceRuns > 1) || (r3 != '\n' && r3 != '\r' && !isCapcode(r3))) {
		return trimmed, false
	}

	otherAndSpace := numOther + numSpace + numCapcode
	if numDelim > 1 { // 2 delimiters are not allowed (with above exceptions)
		return trimmed, false
	}
	if numDelim == 1 {
		if otherAndSpace == 0 || (otherAndSpace-numCapcode == 1 && r1 == ' ') {
			return trimmed, true // a delimiter may be combined with capcode markers
		}
		if otherAndSpace == 1 && unicode.IsSpace(r1) {
			return trimmed, true // a delimiter may be preceded by 1 space
		}
		return trimmed, false // anything else is not okay
	}

	return trimmed, true
}

func processChunkUnfiltered(asset workStruct, numChunks int, trim bool) *pansearch.Counter {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, l, length int
	var max int = maxTokenLength

	// Process microchunks
	for onMicroChunk, data := range asset.data {
		l = len(data) - max // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		
		_ = data[0 : l + max] // infer to the optimizer that we don't access beyond this
		for i = 0; i < l; i++ {
			charTable[data[i]]++ // single characters recorded separately
			for length = max; length >= 2; length-- {
				tokens.Add(data[i:i+length], 1)
			}
		}
		
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			if multithreaded {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min_Multithreaded(minOccurPerMicroChunk)
				} else {
					tokens.Build_Multithreaded()
				}
			} else {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min(minOccurPerMicroChunk)
				} else {
					tokens.Build()
				}
			}
			tokens.Optimize_With_Space()
			runtime.GC()
		}
	}

	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		if multithreaded {
			tokens.Build_With_Min_Multithreaded(minOccurPerChunk)
		} else {
			tokens.Build_With_Min(minOccurPerChunk)
		}
		tokens.Optimize_With_Space() // free memory but reserve some for growth
		runtime.GC()
	}

	//log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func workerClean(max int, jobs <-chan [][]byte, ret chan<- [][]byte) {
	var okay bool
	var clean []byte
	var on int
	for job := range jobs {
		on = 0
        for _, b := range job {
			if clean, okay = filterClean(b); okay {
				if len(clean) >= 2 && len(clean) <= max {
					job[on] = clean
					on++
				}
			}
		}
        ret <- job[0:on]
    }
}

func workerBalanced(max int, jobs <-chan [][]byte, ret chan<- [][]byte) {
	var okay bool
	var clean []byte
	var on int
	for job := range jobs {
		on = 0
        for _, b := range job {
			if clean, okay = filterBalanced(b); okay {
				if len(clean) >= 2 && len(clean) <= max {
					job[on] = clean
					on++
				}
			}
		}
        ret <- job[0:on]
    }
}

func workerConsistent(max int, jobs <-chan [][]byte, ret chan<- [][]byte) {
	var okay bool
	var clean []byte
	var on int
	for job := range jobs {
		on = 0
        for _, b := range job {
			if clean, okay = filterConsistent(b); okay {
				if len(clean) >= 2 && len(clean) <= max {
					job[on] = clean
					on++
				}
			}
		}
        ret <- job[0:on]
    }
}

func workerStrict(max int, jobs <-chan [][]byte, ret chan<- [][]byte) {
	var okay bool
	var clean []byte
	var on int
	for job := range jobs {
		on = 0
        for _, b := range job {
			if clean, okay = filterStrict(b); okay {
				if len(clean) >= 2 && len(clean) <= max {
					job[on] = clean
					on++
				}
			}
		}
        ret <- job[0:on]
    }

}

func processChunkMulti(asset workStruct, numChunks int, trim bool, level uint8) *pansearch.Counter {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, l, length, on int
	var maxTokenLengthEffective int = maxTokenLength + 1
	lenJob := (maxTokenLengthEffective - 3) + 1
	lenJob1000 := lenJob * 2500 // 100,000
	lenJob1000safe := lenJob1000 - (maxTokenLengthEffective + 1)

	// Process microchunks
	for onMicroChunk, data := range asset.data {

		var jobs = make(chan [][]byte, 120)
		var ret = make(chan [][]byte, 240)
		var wg sync.WaitGroup
		var wg2 sync.WaitGroup
	
		// Start workers.
		wg.Add(numWorkers)
		wg2.Add(1)
		for w := 0; w < numWorkers; w++ {
			switch level {
				case 1:
					go func() {
						defer wg.Done()
						workerClean(maxTokenLength, jobs, ret)
					}()
				case 2:
					go func() {
						defer wg.Done()
						workerBalanced(maxTokenLength, jobs, ret)
					}()
				case 3:
					go func() {
						defer wg.Done()
						workerConsistent(maxTokenLength, jobs, ret)
					}()
				case 4:
					go func() {
						defer wg.Done()
						workerStrict(maxTokenLength, jobs, ret)
					}()
			}
		}
	
		go func() {
			for r := range ret {
				for _, b := range r {
					tokens.Add(b, 1)
				}
			}
			wg2.Done()
		}()

		l = len(data) - maxTokenLengthEffective // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		
		_ = data[0 : l + maxTokenLengthEffective] // infer to the optimizer that we don't access beyond this

		on = 0
		job := make([][]byte, lenJob1000)
		for i = 0; i < l; i++ {
			charTable[data[i]]++ // single characters recorded separately
			for length = maxTokenLengthEffective; length >= 3; length-- {
				job[on] = data[i:i+length]
				on++
			}
			if on >= lenJob1000safe {
				jobs <- job[0:on]
				job = make([][]byte, lenJob1000)
				on = 0
			}
		}
		jobs <- job[0:on]
		close(jobs)
		wg.Wait()
		close(ret)
		wg2.Wait()
		
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			if multithreaded {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min_Multithreaded(minOccurPerMicroChunk)
				} else {
					tokens.Build_Multithreaded()
				}
			} else {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min(minOccurPerMicroChunk)
				} else {
					tokens.Build()
				}
			}
			tokens.Optimize_With_Space()
			runtime.GC()
		}
	}

	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		if multithreaded {
			tokens.Build_With_Min_Multithreaded(minOccurPerChunk)
		} else {
			tokens.Build_With_Min(minOccurPerChunk)
		}
		tokens.Optimize_With_Space() // free memory but reserve some for growth
		runtime.GC()
	}

	//log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func processChunkClean(asset workStruct, numChunks int, trim bool) *pansearch.Counter {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, l, length int
	var maxTokenLengthEffective int = maxTokenLength + 1
	var max = maxTokenLength
	var okay bool
	var clean []byte

	// Process microchunks
	for onMicroChunk, data := range asset.data {
		l = len(data) - maxTokenLengthEffective // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		
		_ = data[0 : l + maxTokenLengthEffective] // infer to the optimizer that we don't access beyond this
		for i = 0; i < l; i++ {
			charTable[data[i]]++ // single characters recorded separately
			for length = maxTokenLengthEffective; length >= 3; length-- {
				if clean, okay = filterClean(data[i:i+length]); okay {
					if len(clean) >= 2 && len(clean) <= max {
						tokens.Add(clean, 1)
					}
				}
			}
		}
		
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			if multithreaded {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min_Multithreaded(minOccurPerMicroChunk)
				} else {
					tokens.Build_Multithreaded()
				}
			} else {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min(minOccurPerMicroChunk)
				} else {
					tokens.Build()
				}
			}
			tokens.Optimize_With_Space()
			runtime.GC()
		}
	}

	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		if multithreaded {
			tokens.Build_With_Min_Multithreaded(minOccurPerChunk)
		} else {
			tokens.Build_With_Min(minOccurPerChunk)
		}
		tokens.Optimize_With_Space() // free memory but reserve some for growth
		runtime.GC()
	}

	//log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func processChunkBalanced(asset workStruct, numChunks int, trim bool) *pansearch.Counter {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, l, length int
	var maxTokenLengthEffective int = maxTokenLength + 1
	var max = maxTokenLength
	var okay bool
	var clean []byte

	// Process microchunks
	for onMicroChunk, data := range asset.data {
		l = len(data) - maxTokenLengthEffective // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		
		_ = data[0 : l + maxTokenLengthEffective] // infer to the optimizer that we don't access beyond this
		for i = 0; i < l; i++ {
			charTable[data[i]]++ // single characters recorded separately
			for length = maxTokenLengthEffective; length >= 3; length-- {
				if clean, okay = filterBalanced(data[i:i+length]); okay {
					if len(clean) >= 2 && len(clean) <= max {
						tokens.Add(clean, 1)
					}
				}
			}
		}
		
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			if multithreaded {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min_Multithreaded(minOccurPerMicroChunk)
				} else {
					tokens.Build_Multithreaded()
				}
			} else {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min(minOccurPerMicroChunk)
				} else {
					tokens.Build()
				}
			}
			tokens.Optimize_With_Space()
			runtime.GC()
		}
	}

	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		if multithreaded {
			tokens.Build_With_Min_Multithreaded(minOccurPerChunk)
		} else {
			tokens.Build_With_Min(minOccurPerChunk)
		}
		tokens.Optimize_With_Space() // free memory but reserve some for growth
		runtime.GC()
	}

	//log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func processChunkConsistent(asset workStruct, numChunks int, trim bool) *pansearch.Counter {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, l, length int
	var max int = maxTokenLength
	var maxTokenLengthEffective int = maxTokenLength + 1
	var okay bool
	var clean []byte

	// Process microchunks
	for onMicroChunk, data := range asset.data {
		l = len(data) - maxTokenLengthEffective // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		
		_ = data[0 : l + maxTokenLengthEffective] // infer to the optimizer that we don't access beyond this
		for i = 0; i < l; i++ {
			charTable[data[i]]++ // single characters recorded separately
			for length = maxTokenLengthEffective; length >= 3; length-- {
				if clean, okay = filterConsistent(data[i:i+length]); okay {
					if len(clean) >= 2 && len(clean) <= max {
						tokens.Add(clean, 1)
					}
				}
			}
		}
		
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			if multithreaded {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min_Multithreaded(minOccurPerMicroChunk)
				} else {
					tokens.Build_Multithreaded()
				}
			} else {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min(minOccurPerMicroChunk)
				} else {
					tokens.Build()
				}
			}
			tokens.Optimize_With_Space()
			runtime.GC()
		}
	}

	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		if multithreaded {
			tokens.Build_With_Min_Multithreaded(minOccurPerChunk)
		} else {
			tokens.Build_With_Min(minOccurPerChunk)
		}
		tokens.Optimize_With_Space() // free memory but reserve some for growth
		runtime.GC()
	}

	//log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func processChunkStrict(asset workStruct, numChunks int, trim bool) *pansearch.Counter {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, l, length int
	var max int = maxTokenLength
	var maxTokenLengthEffective int = maxTokenLength + 1
	var okay bool
	var clean []byte

	// Process microchunks
	for onMicroChunk, data := range asset.data {
		l = len(data) - maxTokenLengthEffective // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		
		_ = data[0 : l + maxTokenLengthEffective] // infer to the optimizer that we don't access beyond this
		for i = 0; i < l; i++ {
			charTable[data[i]]++ // single characters recorded separately
			for length = maxTokenLengthEffective; length >= 3; length-- {
				if clean, okay = filterStrict(data[i:i+length]); okay {
					if len(clean) >= 2 && len(clean) <= max {
						tokens.Add(clean, 1)
					}
				}
			}
		}
		
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			if multithreaded {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min_Multithreaded(minOccurPerMicroChunk)
				} else {
					tokens.Build_Multithreaded()
				}
			} else {
				if minOccurPerMicroChunk > 1 {
					tokens.Build_With_Min(minOccurPerMicroChunk)
				} else {
					tokens.Build()
				}
			}
			tokens.Optimize_With_Space()
			runtime.GC()
		}
	}

	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		if multithreaded {
			tokens.Build_With_Min_Multithreaded(minOccurPerChunk)
		} else {
			tokens.Build_With_Min(minOccurPerChunk)
		}
		tokens.Optimize_With_Space() // free memory but reserve some for growth
		runtime.GC()
	}

	//log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func containsOnlyNumbers(input string) bool {
	for _, char := range input {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func main() {
	flag.StringVar(&datasetFilename, "dataset", datasetFilename, "filename of the dataset plain-text (required)")
	flag.StringVar(&saveFilename, "output", saveFilename, "output filename for the dictionary (required)")
	flag.StringVar(&charset, "charset", charset, "one of: UTF-8, UTF-16, none (required)")
	flag.IntVar(&numWorkers, "workers", numWorkers, "number of worker threads to run")
	flag.IntVar(&maxTokenLength, "max-token-length", maxTokenLength, "the maximum length of a token")
	flag.IntVar(&minOccurPerChunk, "min-occur-chunk", minOccurPerChunk, "tokens will be trimmed if they occur less frequently than this per chunk")
	flag.IntVar(&minOccurPerMicroChunk, "min-occur-micro-chunk", minOccurPerMicroChunk, "tokens will be trimmed if they occur less frequently than this per micro-chunk")
	flag.IntVar(&minOccurTotal, "min-occur", minOccurTotal, "tokens will be trimmed if they occur less frequently than this in the dataset (default 1 per 10MB)")
	flag.StringVar(&chunkSizeString, "chunk-size", chunkSizeString, "the number of bytes processed at a time, higher is faster but requires more RAM (default 100MB)")
	flag.IntVar(&microChunks, "micro-chunks", microChunks, "the higher this number, the slower it is but it will reduce peak memory usage")
	flag.BoolVar(&disableCapcode, "disable-capcode", disableCapcode, "disables capcode normalizations (default false)")
	flag.IntVar(&minOccurSingles, "min-occur-byte", minOccurSingles, "single bytes will be trimmed if they occur less frequently than this in the dataset (default min-occur)")
	flag.StringVar(&levelFlag, "mode", levelFlag, "0 = unfiltered, 1 = clean, 2 = balanced, 3 = consistent, 4 = strict (required)")
	flag.Parse()
	flagRequired("dataset", datasetFilename)
	flagRequired("output", saveFilename)
	flagRequired("charset", charset)
	flagRequired("mode", levelFlag)
	if len(chunkSizeString) > 0 {
		chunkSizeString = strings.ToLower(chunkSizeString)
		l := len(chunkSizeString)
		if chunkSizeString[l-1] == 'b' {
			chunkSizeString = chunkSizeString[0:l-1]
		}
		l--
		if l == 0 {
			fmt.Fprintf(os.Stderr, "chunk-size input is invalid\n")
			os.Exit(1)
		}
		switch chunkSizeString[l-1] {
			case 'k':
				chunkSizeString = chunkSizeString[0:l-1]
				if !containsOnlyNumbers(chunkSizeString) {
					fmt.Fprintf(os.Stderr, "chunk-size input is invalid\n")
					os.Exit(1)
				}
				chunkSize = conv.Int([]byte(chunkSizeString)) * 1000
			case 'm':
				chunkSizeString = chunkSizeString[0:l-1]
				if !containsOnlyNumbers(chunkSizeString) {
					fmt.Fprintf(os.Stderr, "chunk-size input is invalid\n")
					os.Exit(1)
				}
				chunkSize = conv.Int([]byte(chunkSizeString)) * 1000000
			case 'g':
				chunkSizeString = chunkSizeString[0:l-1]
				if !containsOnlyNumbers(chunkSizeString) {
					fmt.Fprintf(os.Stderr, "chunk-size input is invalid\n")
					os.Exit(1)
				}
				chunkSize = conv.Int([]byte(chunkSizeString)) * 1000000000
			case 't':
				chunkSizeString = chunkSizeString[0:l-1]
				if !containsOnlyNumbers(chunkSizeString) {
					fmt.Fprintf(os.Stderr, "chunk-size input is invalid\n")
					os.Exit(1)
				}
				chunkSize = conv.Int([]byte(chunkSizeString)) * 1000000000000
			default:
				if !containsOnlyNumbers(chunkSizeString) {
					fmt.Fprintf(os.Stderr, "chunk-size input is invalid\n")
					os.Exit(1)
				}
				chunkSize = conv.Int([]byte(chunkSizeString))
		}
	}
	if numWorkers > 1 {
		multithreaded = true
		numWorkers--
	}
	if maxTokenLength < 2 || maxTokenLength > 40 {
		fmt.Fprintf(os.Stderr, "max-token-length must be between 2 and 40\n")
		os.Exit(1)
	}
	if disableCapcode {
		usingCapcode = false
	}
	if microChunks <= 1 {
		microChunks = 1
	}
	switch strings.ToLower(charset) {
		case "utf8":
			fallthrough
		case "utf-8":
			charsetFlag = 1
			if usingCapcode {
				fmt.Println(`Charset: UTF-8, Capcode Enabled`)
			} else {
				fmt.Println(`Charset: UTF-8, Capcode Disabled`)
			}
		case "utf16":
			fallthrough
		case "utf-16":
			charsetFlag = 2
			if usingCapcode {
				fmt.Fprintf(os.Stderr, "capcode is currently only supported with UTF-8 encoding\n")
				flag.Usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: UTF-16, Capcode Disabled`)
		case "none":
			fallthrough
		case "binary":
			charsetFlag = 0
			if usingCapcode {
				fmt.Fprintf(os.Stderr, "capcode is currently only supported with UTF-8 encoding\n")
				flag.Usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: None`)
		default:
			fmt.Fprintf(os.Stderr, "-charset must be one of: UTF-8, UTF-16, none\n")
            flag.Usage()
            os.Exit(1)
	}
	switch strings.ToLower(levelFlag) {
		case "0":
			fallthrough
		case "unfiltered":
			level = 0
			fmt.Println(`Optimization mode: 0 (unfiltered)`)
		case "1":
			fallthrough
		case "clean":
			level = 1
			fmt.Println(`Optimization mode: 1 (clean)`)
		case "2":
			fallthrough
		case "balanced":
			level = 2
			fmt.Println(`Optimization mode: 2 (balanced)`)
		case "3":
			fallthrough
		case "consistent":
			level = 3
			fmt.Println(`Optimization mode: 3 (consistent)`)
		case "4":
			fallthrough
		case "strict":
			level = 4
			fmt.Println(`Optimization mode: 4 (strict)`)
		default:
			fmt.Fprintf(os.Stderr, "mode must be one of: unfiltered, balanced, consistent, strict, all\n")
			os.Exit(1)
	}

	// Load the text & normalize
	log.Println(`Loading`, datasetFilename)
	var err error
	var filedata []byte
	{
		var temp []byte
		temp, err = ioutil.ReadFile(datasetFilename)
		if err != nil {
			panic(err)
		}
		switch charsetFlag {
			case 0: // none
				filedata = temp
			case 1: // utf-8
				// The normalization function has a bug which I'm working around with the catch
				if usingCapcode {
					filedata = capcode.Encode(temp)
				} else {
					filedata = capcode.NoCapcodeEncode(temp)
				}
				filedata, err = norm_UTF8_NFD(filedata)
				if err != nil {
					filedata, err = norm_UTF8_NFD(temp)
					if usingCapcode {
						filedata = capcode.Encode(filedata)
					} else {
						filedata = capcode.NoCapcodeEncode(filedata)
					}
				}
				if err != nil {
					panic(err)
				}
			case 2:
				filedata, err = norm_UTF16_NFD(temp)
				if err != nil {
					panic(err)
				}
		}
	}

	if minOccurTotal == 0 {
		minOccurTotal = len(filedata) / 10000000
		if minOccurTotal < 1 {
			minOccurTotal = 1
		}
		fmt.Println(`-min-occur set to`, minOccurTotal)
	}
	if minOccurSingles == 0 {
		minOccurSingles = minOccurTotal
	}

	chunkSize += 4 - (chunkSize % 4) // ensure it's divisible by 4 to avoid splitting glyphs
	numChunks := (len(filedata) / chunkSize)
	if (numChunks * chunkSize) < len(filedata) {
		numChunks++
	}
	microChunkSize := chunkSize / microChunks
	microChunkSize += 4 - (microChunkSize % 4) // ensure it's divisible by 4 to avoid splitting glyphs

	var i, i2, thisto int

	// Split the data into chunks & microchunks
	var from = 0
	var to = microChunkSize
	data_chunk := make([][][]byte, numChunks)
	for i=0; i<numChunks; i++ {
		data_chunk[i] = make([][]byte, microChunks)
		thisto = from + chunkSize
		if len(filedata) < thisto {
			thisto = len(filedata)
		}
		for i2=0; i2<microChunks; i2++ {
			to = from + microChunkSize
			if thisto < to {
				to = thisto
			}
			data_chunk[i][i2] = filedata[from:to]
			from = to
		}
	}
	filedata = nil

	// Get the results
	tokens := new(pansearch.Counter)
	startTime := time.Now()
	to = numChunks - 1
	for i=0; i<to; i++ {
		switch level {
			case 0:
				tokens = processChunkUnfiltered(workStruct{i+1, data_chunk[i], tokens}, numChunks, true)
			case 1:
				if multithreaded {
					tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, true, level)
				} else {
					tokens = processChunkClean(workStruct{i+1, data_chunk[i], tokens}, numChunks, true)
				}
			case 2:
				if multithreaded {
					tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, true, level)
				} else {
					tokens = processChunkBalanced(workStruct{i+1, data_chunk[i], tokens}, numChunks, true)
				}
			case 3:
				if multithreaded {
					tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, true, level)
				} else {
					tokens = processChunkConsistent(workStruct{i+1, data_chunk[i], tokens}, numChunks, true)
				}
			case 4:
				if multithreaded {
					tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, true, level)
				} else {
					tokens = processChunkStrict(workStruct{i+1, data_chunk[i], tokens}, numChunks, true)
				}
		}
		data_chunk[i] = nil // it can be freed
	}
	switch level {
		case 0:
			tokens = processChunkUnfiltered(workStruct{i+1, data_chunk[i], tokens}, numChunks, false)
		case 1:
			if multithreaded {
				tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, false, level)
			} else {
				tokens = processChunkClean(workStruct{i+1, data_chunk[i], tokens}, numChunks, false)
			}
		case 2:
			if multithreaded {
				tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, false, level)
			} else {
				tokens = processChunkBalanced(workStruct{i+1, data_chunk[i], tokens}, numChunks, false)
			}
		case 3:
			if multithreaded {
				tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, false, level)
			} else {
				tokens = processChunkConsistent(workStruct{i+1, data_chunk[i], tokens}, numChunks, false)
			}
			
		case 4:
			if multithreaded {
				tokens = processChunkMulti(workStruct{i+1, data_chunk[i], tokens}, numChunks, false, level)
			} else {
				tokens = processChunkStrict(workStruct{i+1, data_chunk[i], tokens}, numChunks, false)
			}
	}
	data_chunk = nil // it can be freed

	log.Println(`Tokens before final trim:`, formatInt(tokens.Len()))
	log.Println(`Trimming final tokens for min`, minOccurTotal)

	if multithreaded {
		tokens.Build_With_Min_Multithreaded(minOccurTotal)
	} else {
		tokens.Build_With_Min(minOccurTotal)
	}

	// Use the charTable to count the total tokens counted
	// It's exactly the number of single characters counted, times the token length (the tail of each chunk is skipped for efficiency)
	var total int
	for i:=0; i<256; i++ {
		total += charTable[i]
	}
	total *= maxTokenLength

	log.Println(`Tokens after trimming:`, formatInt(tokens.Len()))
	log.Println(`Filtered`, formatInt(total), `tokens in`, time.Now().Sub(startTime).Round(time.Millisecond))
	
	log.Println(`Saving tokens...`)
	if err = saveTokensToFile(saveFilename, tokens, level); err != nil {
		panic(err)
	}
	log.Println(`Saved:`, saveFilename)
}