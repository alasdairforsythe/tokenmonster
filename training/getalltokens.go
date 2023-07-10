package main

/*
	Filter testing: https://goplay.tools/snippet/Bmuh4tAUuup
*/

import (
	"os"
	"log"
	"fmt"
	"flag"
	"sync"
	"time"
	"strings"
	"runtime"
	"reflect"
	"unicode"
	"io/ioutil"
	"unicode/utf8"
	"unicode/utf16"
	"encoding/binary"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Conv"
	"github.com/alasdairforsythe/norm"
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
	usingCapcode uint8
	capcodeFlag int = 2
	disableCapcode bool = false
	charset string
	charsetFlag uint8
	normalizer norm.Normalizer
	multithreaded bool
	levelFlag string
	level uint8
	charTable [256]int
	numWorkers int = 8
	onlyLatin bool
	onlyValid bool
	normFlag string
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

func applyCapcode(data []byte) []byte {
	if usingCapcode == 2 {
		return capcode.Encode(data)
	} else if usingCapcode == 1 {
		return capcode.NoCapcodeEncode(data)
	}
	return data
}

func normalize(data []byte) []byte {
	processed, err := normalizer.Normalize(data)
	if err == nil {
		return applyCapcode(processed)
	} else { // if failed try it the other way around
		if !normalizer.SpecifiedLowercase() {
			processed = applyCapcode(data)
			processed, err = normalizer.Normalize(processed)
			if err != nil {
				panic(err)
			}
		} else {
			panic(err)
		}
	}
	return processed
}

func saveTokensToFile(filename string, obj *pansearch.Counter) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	defer w.Close()

	w.WriteByte(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteByte(normalizer.Flag)
	w.WriteByte(level)
	w.WriteByte(0) // reserve
	w.WriteByte(0) // reserve
	w.WriteByte(0) // reserve
	w.WriteByte(0) // reserve

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

func isLatin(b []byte) bool {
	for len(b) > 0 {
		r, n := decodeRune(b)
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return false
		}
		b = b[n:]
	}
	return true
}

func isValid(b []byte) bool {
	if charsetFlag != 2 {
		return utf8.Valid(b)
	}
	for len(b) > 0 {
		r, n := decodeRune(b)
		if r == runeError {
			return false
		}
		b = b[n:]
	}
	return true
}

func isValidLatin(b []byte) bool {
	for len(b) > 0 {
		r, n := decodeRune(b)
		if r == runeError || (unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r)) {
			return false
		}
		b = b[n:]
	}
	return true
}

func isLetter(r rune) bool {
	return (unicode.IsLetter(r) && (usingCapcode!=2 || (r != 'W' && r != 'C' && r != 'D'))) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isCapcode(r rune) bool {
	return (usingCapcode == 1 && r == '\x7F') || (usingCapcode==2 && (r == 'C' || r == 'W' || r == 'D'))
}

func isOther(r rune) bool {
	return !isAlphaNum(r)
}

func isAlphaNum(r rune) bool {
	return (unicode.IsLetter(r) && (usingCapcode!=2 || (r != 'W' && r != 'C' && r != 'D'))) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
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

func isDelimiterConsistent(r rune) bool {
	return delimiters2[r]
}

func decodeRune(b []byte) (rune, int) {
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

func decodeLastRune(b []byte) (rune, int) {
	switch charsetFlag {
		case 0, 1: // UTF-8
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
	if usingCapcode != 0 && ((hasAlpha || hasCapcode || exists || (other && isAlphaNum(rnext))) && r == ' ' && !removed) {
		return trimmed, false
	}
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
	// If it contains letters or numbers, don't end on capcode wordToken or CharacterToken unless preceded by .
	if hasAlpha && usingCapcode==2 && isCapcode(r) {
		if len(tok) < 3 {
			return tok, false
		}
		if !((tok[len(tok)-2] == '.' || tok[len(tok)-2] == '-') || ((tok[len(tok)-2] == 'D' || tok[len(tok)-2] == 127) && (tok[len(tok)-3] == '.' || tok[len(tok)-3] == '-'))) {
			return tok, false
		}
	}
	// If it contains letters or numbers or capcode it may not end with any kind of space
	if usingCapcode != 0 && ((hasAlpha || hasCapcode) && unicode.IsSpace(r)) {
		return tok, false
	}
	// If it contains punctuation it may not end with a space
	if usingCapcode != 0 && (other || exists) && r == ' ' {
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
			if isLetter(r3) || unicode.IsNumber(r3) {
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
					if r3 == '-' || r3 == '.' || r3 == '_' {
						if hyphenok == 0 {
							hyphenok = 1
							continue
						}
					} else if isCapcode(r3) {
						if hyphenok == 1 {
							hyphenok = 2

						} else if hyphenok == 2 {
							hyphenok = 3
						}
						continue
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
	var lastSpace, gt bool
	var delim rune
	var delimPos int
	for i := 0; i < len(tok); i += n3 {
		r3, n3 = decodeRune(tok[i:])
		switch {
		case isAlphaNum(r3):
			return trimmed, false
		case isDelimiterConsistent(r3):
			numDelim++
			delim = r3
			delimPos = i
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
			if gt && r3 == '<' {
				return trimmed, false
			}
			if r3 == '>' {
				gt = true
			}
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
	// Don't allow more than 1 delimeter (pairs on the front and back are allowed earlier)
	if numDelim > 1 || (numDelim > 0 && openCloseStripped) {
		return trimmed, false
	}
	otherAndSpace := numOther + numSpace + numCapcode
	if numDelim == 1 {
		var b byte
		switch delim {
		case '(', '[', '{':
			for i := 0; i < delimPos; i++ {
				b = tok[i]
				if b != ',' && b != '.' && b != ' ' && b != '\r' && b != '\n' {
					return trimmed, false
				}
			}
		case ')', ']', '}':
			for i := delimPos + 1; i < len(tok); i++ {
				b = tok[i]
				if b != ',' && b != '.' && b != ' ' && b != '\r' && b != '\n' {
					return trimmed, false
				}
			}
		}
		if isDelimiter(r1) {
			if len(tok) <= 3 && numSpace <= 1 {
				return trimmed, true
			}
		} else {
			if (otherAndSpace <= 1) || (len(tok) == 3 && (r1 == ' ' || r2 == ' ')) || r1 == '\t' {
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
	var lastSpace, gt bool
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
			if gt && r3 == '<' {
				return trimmed, false
			}
			if r3 == '>' {
				gt = true
			}
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
		if otherAndSpace == 1 && unicode.IsSpace(r1) && r1 != '\t' {
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
	flag.StringVar(&charset, "charset", charset, "one of: UTF-8, none (default UTF-8)")
	flag.StringVar(&normFlag, "norm", normFlag, "combine any of the following: NFD, lowercase, accents, quotemarks, collapse, trim, leadingspace, newlines (default NFD)")
	flag.IntVar(&numWorkers, "workers", numWorkers, "number of worker threads to run")
	flag.IntVar(&maxTokenLength, "max-token-length", maxTokenLength, "the maximum length of a token")
	flag.IntVar(&minOccurPerChunk, "min-occur-chunk", minOccurPerChunk, "tokens will be trimmed if they occur less frequently than this per chunk")
	flag.IntVar(&minOccurPerMicroChunk, "min-occur-micro-chunk", minOccurPerMicroChunk, "tokens will be trimmed if they occur less frequently than this per micro-chunk")
	flag.IntVar(&minOccurTotal, "min-occur", minOccurTotal, "tokens will be trimmed if they occur less frequently than this in the dataset (default 1 per 10MB)")
	flag.StringVar(&chunkSizeString, "chunk-size", chunkSizeString, "the number of bytes processed at a time, higher is faster but requires more RAM (default 100MB)")
	flag.IntVar(&microChunks, "micro-chunks", microChunks, "the higher this number, the slower it is but it will reduce peak memory usage")
	flag.IntVar(&capcodeFlag, "capcode", capcodeFlag, "0 = disabled, 1 = deleteToken only, 2 = enabled")
	flag.BoolVar(&onlyLatin, "only-latin", onlyLatin, "if enabled, tokens that contains letters must be in Latin script (default false)")
	flag.BoolVar(&onlyValid, "only-valid", onlyValid, "if enabled, tokens must contain full and valid characters, except single byte tokens (default false)")
	flag.IntVar(&minOccurSingles, "min-occur-byte", minOccurSingles, "single bytes will be trimmed if they occur less frequently than this in the dataset (default min-occur)")
	flag.StringVar(&levelFlag, "mode", levelFlag, "0 = unfiltered, 1 = clean, 2 = balanced, 3 = consistent, 4 = strict (required)")
	flag.Parse()
	flagRequired("dataset", datasetFilename)
	flagRequired("output", saveFilename)
	flagRequired("mode", levelFlag)

	usingCapcode = uint8(capcodeFlag)
	var err error
	normalizer, err = norm.NewNormalizer(normFlag)
	if err != nil {
		fmt.Fprintln(os.Stdout, err)
		os.Exit(1)
	}
	if normalizer.SpecifiedLowercase() && usingCapcode == 2 {
		fmt.Fprintf(os.Stderr, "You cannot normalize to lowercase and also encode uppercase with capcode level 2.\nChoose either capcode level 1 (deleteToken), or remove 'lowercase' from the -norm flag.\n")
		os.Exit(1)
	}

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
	if microChunks < 1 {
		microChunks = 1
	}
	if len(charset) == 0 {
		charset = `utf8`
	}
	switch strings.ToLower(charset) {
		case "utf8":
			fallthrough
		case "utf-8":
			charsetFlag = 1
			if len(normFlag) == 0 { // default for UTF-8 is NFD normalization
				normalizer, _ = norm.NewNormalizer(`nfd`)
			}
			fmt.Println(`Charset: UTF-8`)
		case "utf16":
			fallthrough
		case "utf-16":
			fmt.Fprintf(os.Stderr, "UTF-16 support is not yet fully implemented\n")
            os.Exit(0)
			/*
			charsetFlag = 2
			if usingCapcode != 0 {
				fmt.Fprintf(os.Stderr, "capcode is not supported with UTF-16 encoding\n")
				flag.Usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: UTF-16`)
			*/
		case "none":
			fallthrough
		case "binary":
			if normalizer.SpecifiedNFD() {
				fmt.Fprintf(os.Stderr, "To use NFD normalization, choose charset UTF-8\n")
            	os.Exit(1)
			}
			if onlyValid {
				fmt.Fprintf(os.Stderr, "To use -only-valid, you must select a charset\n")
            	os.Exit(1)
			}
			charsetFlag = 0
			fmt.Println(`Charset: None`)
		default:
			fmt.Fprintf(os.Stderr, "-charset must be one of: UTF-8, UTF-16, none\n")
            flag.Usage()
            os.Exit(1)
	}
	fmt.Println(`Normalization: ` + normalizer.String())
	switch usingCapcode {
		case 0:
			fmt.Println(`Capcode: 0 (disabled)`)
		case 1:
			fmt.Println(`Capcode: 1 (deleteToken)`)
		case 2:
			fmt.Println(`Capcode: 2 (enabled)`)
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
	if level >= 3 && usingCapcode == 0 {
		fmt.Fprintf(os.Stderr, "EXITING: Optimization modes 'consistent' and 'strict' require capcode level 1 or 2\n")
		os.Exit(1)
	}
	if onlyLatin {
		fmt.Println(`Only Latin script allowed`)
	}
	if onlyValid {
		if charsetFlag == 2 {
			fmt.Println(`Only valid UTF-16 allowed`)
		} else {
			fmt.Println(`Only valid UTF-8 allowed`)
		}
	}

	// Load the text & normalize
	log.Println(`Loading`, datasetFilename)
	filedata, err := ioutil.ReadFile(datasetFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Dataset file does not exist or cannot be opened: " + datasetFilename + "\n")
		os.Exit(1)
	}
	filedata = normalize(filedata)

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

	// Sort and filter the final list
	switch {
		case onlyLatin && onlyValid:
			if multithreaded {
				tokens.Build_With_Min_Filter_Multithreaded(minOccurTotal, isValidLatin)
			} else {
				tokens.Build_With_Min_Filter(minOccurTotal, isValidLatin)
			}
		case onlyLatin:
			if multithreaded {
				tokens.Build_With_Min_Filter_Multithreaded(minOccurTotal, isLatin)
			} else {
				tokens.Build_With_Min_Filter(minOccurTotal, isLatin)
			}
		case onlyValid:
			if multithreaded {
				tokens.Build_With_Min_Filter_Multithreaded(minOccurTotal, isValid)
			} else {
				tokens.Build_With_Min_Filter(minOccurTotal, isValid)
			}
		default:
			if multithreaded {
				tokens.Build_With_Min_Multithreaded(minOccurTotal)
			} else {
				tokens.Build_With_Min(minOccurTotal)
			}
	}

	// Unless strict mode, add code-related tokens that would otherwise be denied
	// This in no way harms datasets that don't contain any code, they'll just be immediately pruned during training
	if level < 4 {
		for _, v := range extraTokens {
			tokens.Add(normalize([]byte(v)), 1)
			tokens.Add(normalize([]byte(" " + string(v))), 1)
			if v[len(v)-1] == '/' {
				tokens.Add([]byte(string(v) + "D"), 1)
			}
		}
		if multithreaded {
			tokens.Build_Multithreaded()
		} else {
			tokens.Build()
		}
	}

	// Use the charTable to count the total tokens counted
	// It's exactly the number of single characters counted, multiplied by the token length (the tail of each chunk is skipped for efficiency)
	var total int
	for i:=0; i<256; i++ {
		total += charTable[i]
	}
	total *= maxTokenLength

	log.Println(`Tokens after trimming:`, formatInt(tokens.Len()))
	log.Println(`Filtered`, formatInt(total), `tokens in`, time.Now().Sub(startTime).Round(time.Millisecond))
	
	log.Println(`Saving tokens...`)
	if err = saveTokensToFile(saveFilename, tokens); err != nil {
		panic(err)
	}
	log.Println(`Saved:`, saveFilename)
}

// ------------------------------------------------------

var delimiters = map[rune]bool{
	'(': true, ')': true, '[': true, ']': true, '{': true, '}': true, '\'': true, 
    '"': true, '‘': true, '’': true, '“': true, '”': true, '«': true, '»': true, 
    '‹': true, '›': true, '‛': true, '`': true, '„': true, '″': true, '〝': true, 
    '〞': true, '「': true, '」': true, '『': true, '』': true, '｢': true, '｣': true, 
    '〈': true, '〉': true, '《': true, '》': true, '‟': true, '❛': true, '❜': true, 
    '❝': true, '❞': true, '❮': true, '❯': true, '〔': true, '〕': true, '⸨': true, '⸩': true,
}

var delimiters2 = map[rune]bool{
	'<': true, '>': true,
	'(': true, ')': true, '[': true, ']': true, '{': true, '}': true, '\'': true, 
    '"': true, '‘': true, '’': true, '“': true, '”': true, '«': true, '»': true, 
    '‹': true, '›': true, '‛': true, '`': true, '„': true, '″': true, '〝': true, 
    '〞': true, '「': true, '」': true, '『': true, '』': true, '｢': true, '｣': true, 
    '〈': true, '〉': true, '《': true, '》': true, '‟': true, '❛': true, '❜': true, 
    '❝': true, '❞': true, '❮': true, '❯': true, '〔': true, '〕': true, '⸨': true, '⸩': true,
}

var extraTokens = []string{
	`#define`, `#elif`, `#else`, `#endif`, `#error`, `#if`, `#ifdef`, `#ifndef`, `#include`, `#line`, 
	`#pragma`, `#undef`, `$GLOBALS`, `$HTTP_RAW_POST_DATA`, `$_COOKIE`, `$_ENV`, `$_FILES`, `$_GET`, `$_POST`, `$_REQUEST`, 
	`$_SERVER`, `$_SESSION`, `$argc`, `$argv`, `$http_response_header`, `$this`, `&#10;`, `&#123;`, `&#124;`, `&#125;`, 
	`&#160;`, `&#161;`, `&#162;`, `&#163;`, `&#164;`, `&#165;`, `&#166;`, `&#167;`, `&#168;`, `&#169;`, 
	`&#170;`, `&#171;`, `&#172;`, `&#173;`, `&#174;`, `&#175;`, `&#176;`, `&#177;`, `&#178;`, `&#179;`, 
	`&#180;`, `&#181;`, `&#182;`, `&#183;`, `&#184;`, `&#185;`, `&#186;`, `&#187;`, `&#188;`, `&#189;`, 
	`&#190;`, `&#191;`, `&#192;`, `&#193;`, `&#194;`, `&#195;`, `&#196;`, `&#197;`, `&#198;`, `&#199;`, 
	`&#200;`, `&#201;`, `&#202;`, `&#203;`, `&#204;`, `&#205;`, `&#206;`, `&#207;`, `&#208;`, `&#209;`, 
	`&#210;`, `&#211;`, `&#212;`, `&#213;`, `&#214;`, `&#215;`, `&#216;`, `&#217;`, `&#218;`, `&#219;`, 
	`&#220;`, `&#221;`, `&#222;`, `&#223;`, `&#224;`, `&#225;`, `&#226;`, `&#227;`, `&#228;`, `&#229;`, 
	`&#230;`, `&#231;`, `&#232;`, `&#233;`, `&#234;`, `&#235;`, `&#236;`, `&#237;`, `&#238;`, `&#239;`, 
	`&#240;`, `&#241;`, `&#242;`, `&#243;`, `&#244;`, `&#245;`, `&#246;`, `&#247;`, `&#248;`, `&#249;`, 
	`&#250;`, `&#251;`, `&#252;`, `&#253;`, `&#254;`, `&#255;`, `&#256;`, `&#257;`, `&#258;`, `&#259;`, 
	`&#260;`, `&#261;`, `&#262;`, `&#263;`, `&#264;`, `&#265;`, `&#266;`, `&#267;`, `&#268;`, `&#269;`, 
	`&#270;`, `&#271;`, `&#272;`, `&#273;`, `&#274;`, `&#275;`, `&#276;`, `&#277;`, `&#278;`, `&#279;`, 
	`&#280;`, `&#281;`, `&#284;`, `&#285;`, `&#286;`, `&#287;`, `&#288;`, `&#289;`, `&#290;`, `&#291;`, 
	`&#292;`, `&#293;`, `&#294;`, `&#295;`, `&#296;`, `&#297;`, `&#298;`, `&#299;`, `&#300;`, `&#301;`, 
	`&#302;`, `&#303;`, `&#304;`, `&#305;`, `&#306;`, `&#307;`, `&#308;`, `&#309`, `&#309;`, `&#310;`, 
	`&#311;`, `&#321;`, `&#322;`, `&#336;`, `&#337;`, `&#33;`, `&#342;`, `&#343;`, `&#346;`, `&#347;`, 
	`&#34;`, `&#350;`, `&#351;`, `&#354;`, `&#355;`, `&#35;`, `&#360;`, `&#361;`, `&#368;`, `&#369;`, 
	`&#36;`, `&#372;`, `&#373;`, `&#374;`, `&#375;`, `&#37;`, `&#38;`, `&#39;`, `&#40;`, `&#41;`, 
	`&#42;`, `&#43;`, `&#44;`, `&#46;`, `&#47;`, `&#58;`, `&#59;`, `&#60;`, `&#61;`, `&#62;`, 
	`&#63;`, `&#64;`, `&#7922;`, `&#7923;`, `&#91;`, `&#92;`, `&#93;`, `&#94;`, `&#95;`, `&#96;`, 
	`&#9;`, `&AElig;`, `&AMP;`, `&Aacute;`, `&Abreve;`, `&Acirc;`, `&Agrave;`, `&Amacr;`, `&Aogon;`, `&Aring;`, 
	`&Atilde;`, `&Auml;`, `&COPY;`, `&Cacute;`, `&Ccaron;`, `&Ccedil;`, `&Ccirc;`, `&Cdot;`, `&Dcaron;`, `&Dogon;`, 
	`&Dot;`, `&Dstrok;`, `&ETH;`, `&Eacute;`, `&Ebreve;`, `&Ecaron;`, `&Ecirc;`, `&Egrave;`, `&Emacr;`, `&Eogon;`, 
	`&Etilde;`, `&Euml;`, `&GT;`, `&Gbreve;`, `&Gcedil;`, `&Gcirc;`, `&Gdot;`, `&Gogon;`, `&Hat;`, `&Hcirc;`, 
	`&Hstrok;`, `&IJlig;`, `&Iacute;`, `&Ibreve;`, `&Icirc;`, `&Idot;`, `&Igrave;`, `&Imacr;`, `&Iogon;`, `&Itilde;`, 
	`&Iuml;`, `&Jcirc;`, `&Kcedil;`, `&LCub;`, `&LT;`, `&Lstrok;`, `&Mcirc;`, `&Mdot;`, `&NewLine;`, `&Ntilde;`, 
	`&Oacute;`, `&Obreve;`, `&Ocirc;`, `&Odblac;`, `&Ograve;`, `&Oslash;`, `&Otilde;`, `&Ouml;`, `&QUOT;`, `&RCub;`, 
	`&REG;`, `&Rcedil;`, `&Sacute;`, `&Scedil;`, `&THORN;`, `&Tab;`, `&Tcedil;`, `&Uacute;`, `&Ucirc;`, `&Udblac;`, 
	`&Ugrave;`, `&Utilde;`, `&Uuml;`, `&VerticalLine;`, `&Wcirc;`, `&Yacute;`, `&Ycirc;`, `&Ytilde;`, `&aacute;`, `&abreve;`, 
	`&acirc;`, `&acute;`, `&aelig;`, `&agrave;`, `&amacr;`, `&amp;`, `&aogon;`, `&apos;`, `&aring;`, `&ast;`, 
	`&atilde;`, `&auml;`, `&brvbar;`, `&bsol;`, `&cacute;`, `&ccaron;`, `&ccedil;`, `&ccirc;`, `&cdot;`, `&cedil;`, 
	`&cent;`, `&circledR;`, `&colon;`, `&comma;`, `&commat;`, `&copy;`, `&curren;`, `&dcaron;`, `&deg;`, `&die;`, 
	`&divide;`, `&dollar;`, `&dot;`, `&dstrok;`, `&eacute;`, `&ebreve;`, `&ecaron;`, `&ecirc;`, `&egrave;`, `&emacr;`, 
	`&eogon;`, `&equals;`, `&eth;`, `&etilde;`, `&euml;`, `&excl;`, `&frac12;`, `&frac14;`, `&frac34;`, `&gbreve;`, 
	`&gcirc;`, `&gdot;`, `&grave;`, `&gt;`, `&hcirc;`, `&hstrok;`, `&iacute;`, `&ibreve;`, `&icirc;`, `&iexcl;`, 
	`&igrave;`, `&ijlig;`, `&imacr;`, `&imath;`, `&inodot;`, `&iogon;`, `&iquest;`, `&itilde;`, `&iuml;`, `&jcirc;`, 
	`&kcedil;`, `&laquo;`, `&lbrace;`, `&lbrack;`, `&lowbar;`, `&lpar;`, `&lsqb;`, `&lstrok;`, `&lt;`, `&macr;`, 
	`&mcirc;`, `&mdot;`, `&micro;`, `&middot;`, `&nbsp;`, `&not;`, `&ntilde;`, `&num;`, `&oacute;`, `&obreve;`, 
	`&ocirc;`, `&odblac;`, `&ograve;`, `&ordf;`, `&ordm;`, `&oslash;`, `&otilde;`, `&ouml;`, `&para;`, `&percnt;`, 
	`&period;`, `&plus;`, `&plusmn;`, `&pound;`, `&quest;`, `&quot;`, `&raquo;`, `&rbrace;`, `&rbrack;`, `&rcedil;`, 
	`&reg;`, `&rpar;`, `&rsqb;`, `&sacute;`, `&scedil;`, `&sect;`, `&semi;`, `&shy;`, `&sol;`, `&sup1;`, 
	`&sup2;`, `&sup3;`, `&szlig;`, `&tcedil;`, `&thorn;`, `&times;`, `&uacute;`, `&ucirc;`, `&udblac;`, `&ugrave;`, 
	`&uml;`, `&utilde;`, `&uuml;`, `&vert;`, `&wcirc;`, `&yacute;`, `&ycirc;`, `&yen;`, `&ytilde;`, `&yuml;`, 
	`(const T& arg)`, `--%>`, `-->`, `.h>`, `<!--#`, `<!--`, `<!--#include -->`, `<!---->`, `<!--[if IE ]>`, `<!DOCTYPE>`, `<![endif]-->`, 
	`<%--`, `</A>`, `</ABBR>`, `</ACRONYM>`, `</ADDRESS>`, `</ANNOTATION>`, `</APP>`, `</APPINFO>`, `</APPLET>`, `</AREA>`, 
	`</ARTICLE>`, `</ASIDE>`, `</AUDIO>`, `</B>`, `</BASE>`, `</BASEFONT>`, `</BDI>`, `</BDO>`, `</BGSOUND>`, `</BIG>`, 
	`</BINDING>`, `</BLINK>`, `</BLOCKQUOTE>`, `</BODY>`, `</BR>`, `</BUTTON>`, `</CANVAS>`, `</CAPTION>`, `</CENTER>`, `</CITE>`, 
	`</CODE>`, `</COL>`, `</COLGROUP>`, `</COMMAND>`, `</COMMENT>`, `</CONTAINER>`, `</CONTENT>`, `</DATA>`, `</DATALIST>`, `</DD>`, 
	`</DECORATOR>`, `</DEL>`, `</DETAILS>`, `</DFN>`, `</DIALOG>`, `</DIR>`, `</DIV>`, `</DL>`, `</DOCUMENTATION>`, `</DT>`, 
	`</ELEMENT>`, `</EM>`, `</EMBED>`, `</FETCH>`, `</FIELDSET>`, `</FIGCAPTION>`, `</FIGURE><FOOTER>`, `</FIGURECAPTION>`, `</FONT>`, `</FOOTER>`, 
	`</FORM>`, `</FRAME>`, `</FRAMESET>`, `</H1><H2>`, `</H2>`, `</H3>`, `</H4>`, `</H5>`, `</H6>`, `</HEAD>`, 
	`</HEADER>`, `</HGROUP>`, `</HR>`, `</HTML>`, `</I>`, `</IFRAME>`, `</ILAYER>`, `</IMAGE>`, `</IMG>`, `</IMPORT>`, 
	`</INCLUDE>`, `</INPUT>`, `</INS>`, `</ISINDEX>`, `</KBD>`, `</KEYGEN>`, `</LABEL>`, `</LAYER>`, `</LEGEND>`, `</LI>`, 
	`</LINK>`, `</LISTING>`, `</MAIN>`, `</MAP>`, `</MARK>`, `</MARQUEE>`, `</MENU>`, `</META>`, `</METER>`, `</MIXIN>`, 
	`</MULTICOL>`, `</NAV>`, `</NEXTID>`, `</NOEMBED>`, `</NOFRAMES>`, `</NOINDEX>`, `</NOLAYER>`, `</NOSCRIPT>`, `</NXTID>`, `</OBJECT>`, 
	`</OL>`, `</OPTGROUP>`, `</OPTION>`, `</OUTPUT>`, `</P>`, `</PARAM>`, `</PICTURE>`, `</PLAINTEXT>`, `</PRE>`, `</PROCESS>`, 
	`</PROGRESS>`, `</Q>`, `</REDEFINE>`, `</REPEATER>`, `</RP>`, `</RT>`, `</RUBY>`, `</React.Fragment>`, `</S>`, `</SAMP>`, 
	`</SCRIPT>`, `</SECTION>`, `</SELECT>`, `</SERVER>`, `</SERVICE>`, `</SHADOW>`, `</SIMPLETYPE>`, `</SMALL>`, `</SOUND>`, `</SOURCE>`, 
	`</SPACER>`, `</SPAN>`, `</SPOT>`, `</STRIKE>`, `</STRONG>`, `</STYLE>`, `</SUB>`, `</SUMMARY>`, `</SUP>`, `</TABLE>`, 
	`</TBODY>`, `</TD>`, `</TEMPLATE>`, `</TEXTAREA>`, `</TFOOT>`, `</TH>`, `</THEAD>`, `</TIME><TITLE>`, `</TITLE></TR>`, `</TRACK>`, 
	`</U>`, `</UL>`, `</UNION>`, `</VAR>`, `</VIDEO>`, `</WBR>`, `</XMP>`, `</XTAGS>`, `</a>`, `</abbr>`, 
	`</acronym>`, `</address>`, `</annotation>`, `</app>`, `</appinfo>`, `</applet>`, `</area>`, `</article>`, `</aside>`, `</audio>`, 
	`</b>`, `</base>`, `</basefont>`, `</bdi>`, `</bdo>`, `</bgsound>`, `</big>`, `</binding>`, `</blink>`, `</blockquote>`, 
	`</body>`, `</br>`, `</button>`, `</canvas>`, `</caption>`, `</center>`, `</cite>`, `</code>`, `</col>`, `</colgroup>`, 
	`</command>`, `</comment>`, `</container>`, `</content>`, `</data>`, `</datalist>`, `</dd>`, `</decorator>`, `</del>`, `</details>`, 
	`</dfn>`, `</dialog>`, `</dir>`, `</div>`, `</dl>`, `</documentation>`, `</dt>`, `</element>`, `</em>`, `</embed>`, 
	`</fetch>`, `</fieldset>`, `</figcaption>`, `</figure><footer>`, `</figurecaption>`, `</font>`, `</footer>`, `</form>`, `</frame>`, `</frameset>`, 
	`</h1>`, `</h2>`, `</h3>`, `</h4>`, `</h5>`, `</h6>`, `</head>`, `</header>`, `</hgroup>`, `</hr>`, 
	`</html>`, `</i>`, `</iframe>`, `</ilayer>`, `</image>`, `</img>`, `</import>`, `</include>`, `</input>`, `</ins>`, 
	`</isindex>`, `</kbd>`, `</keygen>`, `</label>`, `</layer>`, `</legend>`, `</li>`, `</link>`, `</listing>`, `</main>`, 
	`</map>`, `</mark>`, `</marquee>`, `</menu>`, `</menuitem>`, `</meta>`, `</meter>`, `</mixin>`, `</multicol>`, `</nav>`, 
	`</nextid>`, `</ng-template>`, `</nobr>`, `</noembed>`, `</noframes>`, `</noindex>`, `</nolayer>`, `</noscript>`, `</nxtid>`, `</object>`, 
	`</ol>`, `</optgroup>`, `</option>`, `</output>`, `</p>`, `</param>`, `</picture>`, `</plaintext>`, `</pre>`, `</process>`, 
	`</progress>`, `</q>`, `</redefine>`, `</repeater>`, `</rp>`, `</rt>`, `</ruby>`, `</s>`, `</samp>`, `</script>`, 
	`</section>`, `</select>`, `</server>`, `</service>`, `</shadow>`, `</simpleType>`, `</sound>`, `</source>`, `</spacer>`, `</span>`, 
	`</spot>`, `</strike>`, `</strong>`, `</style>`, `</sub>`, `</summary>`, `</sup>`, `</table>`, `</tbody>`, `</td>`, 
	`</template>`, `</textarea>`, `</tfoot>`, `</th>`, `</thead>`, `</time>`, `</title>`, `</tr>`, `</track>`, `</tt>`, 
	`</u>`, `</ul>`, `</union>`, `</var>`, `</video>`, `</wbr>`, `</xmp>`, `</xtags>`, `<?`, `<?=`, 
	`<?php`, `<?xml`, `<A>`, `<ABBR>`, `<ACRONYM>`, `<ADDRESS>`, `<ANNOTATION>`, `<APP>`, `<APPINFO>`, `<APPLET>`, 
	`<AREA />`, `<AREA/>`, `<AREA>`, `<ARTICLE>`, `<ASIDE>`, `<AUDIO>`, `<B>`, `<BASE />`, `<BASE/>`, `<BASE>`, 
	`<BASEFONT>`, `<BDI>`, `<BDO>`, `<BGSOUND>`, `<BIG>`, `<BINDING>`, `<BLINK>`, `<BLOCKQUOTE>`, `<BODY>`, `<BR />`, 
	`<BR/>`, `<BR>`, `<BUTTON>`, `<CANVAS>`, `<CAPTION>`, `<CENTER>`, `<CITE>`, `<CODE>`, `<COL />`, `<COL/>`, 
	`<COL>`, `<COLGROUP>`, `<COMMAND>`, `<COMMENT>`, `<CONTAINER>`, `<CONTENT>`, `<DATA>`, `<DATALIST>`, `<DD>`, `<DECORATOR>`, 
	`<DEL>`, `<DETAILS>`, `<DFN>`, `<DIALOG>`, `<DIR>`, `<DIV>`, `<DL>`, `<DOCUMENTATION>`, `<DT>`, `<ELEMENT>`, 
	`<EM>`, `<EMBED />`, `<EMBED/>`, `<EMBED>`, `<FETCH>`, `<FIELDSET>`, `<FIGCAPTION>`, `<FIGURE>`, `<FIGURECAPTION>`, `<FONT>`, 
	`<FORM>`, `<FRAME>`, `<FRAMESET>`, `<H1>`, `<H3>`, `<H4>`, `<H5>`, `<H6>`, `<HEAD>`, `<HEADER>`, 
	`<HGROUP>`, `<HR />`, `<HR/>`, `<HR>`, `<HTML>`, `<I>`, `<IFRAME>`, `<ILAYER>`, `<IMAGE>`, `<IMG />`, 
	`<IMG/>`, `<IMG>`, `<IMPORT>`, `<INCLUDE>`, `<INPUT />`, `<INPUT/>`, `<INPUT>`, `<INS>`, `<ISINDEX>`, `<KBD>`, 
	`<KEYGEN />`, `<KEYGEN/>`, `<KEYGEN>`, `<LABEL>`, `<LAYER>`, `<LEGEND>`, `<LI>`, `<LINK />`, `<LINK/>`, `<LINK>`, 
	`<LISTING>`, `<MAIN>`, `<MAP>`, `<MARK>`, `<MARQUEE>`, `<MENU>`, `<META />`, `<META/>`, `<META>`, `<METER>`, 
	`<MIXIN>`, `<MULTICOL>`, `<NAV>`, `<NEXTID>`, `<NOEMBED>`, `<NOFRAMES>`, `<NOINDEX>`, `<NOLAYER>`, `<NOSCRIPT>`, `<NXTID>`, 
	`<OBJECT>`, `<OL>`, `<OPTGROUP>`, `<OPTION>`, `<OUTPUT>`, `<P>`, `<PARAM />`, `<PARAM/>`, `<PARAM>`, `<PICTURE>`, 
	`<PLAINTEXT>`, `<PRE>`, `<PROCESS>`, `<PROGRESS>`, `<Q>`, `<REDEFINE>`, `<REPEATER>`, `<RP>`, `<RT>`, `<RUBY>`, 
	`<React.Fragment>`, `<S>`, `<SAMP>`, `<SCRIPT>`, `<SECTION>`, `<SELECT>`, `<SERVER>`, `<SERVICE>`, `<SHADOW>`, `<SIMPLETYPE>`, 
	`<SMALL>`, `<SOUND>`, `<SOURCE />`, `<SOURCE/>`, `<SOURCE>`, `<SPACER>`, `<SPAN>`, `<SPOT>`, `<STRIKE>`, `<STRONG>`, 
	`<STYLE>`, `<SUB>`, `<SUMMARY>`, `<SUP>`, `<TABLE>`, `<TBODY>`, `<TD>`, `<TEMPLATE>`, `<TEXTAREA>`, `<TFOOT>`, 
	`<TH>`, `<THEAD>`, `<TIME>`, `<TR>`, `<TRACK />`, `<TRACK/>`, `<TRACK>`, `<U>`, `<UL>`, `<UNION>`, 
	`<VAR>`, `<VIDEO>`, `<WBR />`, `<WBR/>`, `<WBR>`, `<XMP>`, `<XTAGS>`, `<a>`, `<abbr>`, `<acronym>`, 
	`<address>`, `<algorithm>`, `<annotation>`, `<app>`, `<appinfo>`, `<applet>`, `<area />`, `<area/>`, `<area>`, `<array>`, 
	`<article>`, `<aside>`, `<assert.h>`, `<atomic>`, `<audio>`, `<b>`, `<base />`, `<base/>`, `<base>`, `<basefont>`, 
	`<baseurl>`, `<bdi>`, `<bdo>`, `<bgsound>`, `<big>`, `<binding>`, `<bitset>`, `<blink>`, `<blockquote>`, `<body>`, 
	`<br />`, `<br/>`, `<br>`, `<button>`, `<canvas>`, `<caption>`, `<cassert>`, `<ccomplex>`, `<cctype>`, `<center>`, 
	`<cfloat>`, `<chrono>`, `<cinttypes>`, `<ciso646>`, `<cite>`, `<climits>`, `<clocale>`, `<cmath>`, `<code>`, `<codecvt>`, 
	`<col />`, `<col/>`, `<col>`, `<colgroup>`, `<command>`, `<comment>`, `<complex>`, `<condition_variable>`, `<container>`, `<content>`, 
	`<csetjmp>`, `<csignal>`, `<cstdarg>`, `<cstdbool>`, `<cstddef>`, `<cstdint>`, `<cstdio>`, `<cstdlib>`, `<cstring>`, `<ctime>`, 
	`<ctype.h>`, `<cwchar>`, `<cwctype>`, `<data>`, `<datalist>`, `<dd>`, `<decorator>`, `<del>`, `<deque>`, `<details>`, 
	`<dfn>`, `<dialog>`, `<dir>`, `<div>`, `<dl>`, `<documentation>`, `<dom-module>`, `<dt>`, `<element>`, `<em>`, 
	`<embed />`, `<embed/>`, `<embed>`, `<errno.h>`, `<exception>`, `<fetch>`, `<field>`, `<fieldset>`, `<figcaption>`, `<figure>`, 
	`<figurecaption>`, `<filesystem>`, `<font>`, `<form>`, `<frame>`, `<frameset>`, `<fstream>`, `<functional>`, `<future>`, `<h1>`, 
	`<h2>`, `<h3>`, `<h4>`, `<h5>`, `<h6>`, `<head>`, `<header>`, `<hgroup>`, `<hr />`, `<hr/>`, 
	`<hr>`, `<html>`, `<i>`, `<iframe>`, `<ilayer>`, `<image>`, `<img />`, `<img/>`, `<img>`, `<import>`, 
	`<include>`, `<initializer_list>`, `<input />`, `<input/>`, `<input>`, `<ins>`, `<iomanip>`, `<ios>`, `<iostream>`, `<isindex>`, 
	`<iterator>`, `<kbd>`, `<keygen />`, `<keygen/>`, `<keygen>`, `<label>`, `<layer>`, `<legend>`, `<li>`, `<limits.h>`, 
	`<link />`, `<link/>`, `<link>`, `<list>`, `<listing>`, `<locale.h>`, `<locale>`, `<main>`, `<map>`, `<mark>`, 
	`<marquee>`, `<math.h>`, `<menu>`, `<menuitem>`, `<meta />`, `<meta/>`, `<meta>`, `<meter>`, `<mixin>`, `<multicol>`, 
	`<mutex>`, `<nav>`, `<nextid>`, `<ng-template>`, `<nobr>`, `<noembed>`, `<noframes>`, `<noindex>`, `<nolayer>`, `<noscript>`, 
	`<numeric>`, `<nxtid>`, `<object>`, `<ol>`, `<optgroup>`, `<option>`, `<output>`, `<p>`, `<param />`, `<param/>`, 
	`<param>`, `<picture>`, `<plaintext>`, `<pre>`, `<process>`, `<progress>`, `<q>`, `<queue>`, `<random>`, `<ratio>`, 
	`<redefine>`, `<regex>`, `<repeater>`, `<rp>`, `<rt>`, `<ruby>`, `<s>`, `<samp>`, `<script>`, `<section>`, 
	`<select>`, `<server>`, `<service>`, `<set>`, `<setjmp.h>`, `<shadow>`, `<signal.h>`, `<simpleType>`, `<small></small>`, `<sound>`, 
	`<source />`, `<source/>`, `<source>`, `<spacer>`, `<span>`, `<spot>`, `<sstream>`, `<stack>`, `<stdarg.h>`, `<stddef.h>`, 
	`<stdexcept>`, `<stdint.h>`, `<stdio.h>`, `<stdlib.h>`, `<streambuf>`, `<strike>`, `<string.h>`, `<string>`, `<strong>`, `<style>`, 
	`<sub>`, `<summary>`, `<sup>`, `<table>`, `<tbody>`, `<td>`, `<template>`, `<textarea>`, `<tfoot>`, `<th>`, 
	`<thead>`, `<thread>`, `<time.h>`, `<time>`, `<title>`, `<tr>`, `<track />`, `<track/>`, `<track>`, `<tt>`, 
	`<tuple>`, `<typeinfo>`, `<typename T>`, `<u>`, `<ul>`, `<union>`, `<utility>`, `<valarray>`, `<var>`, `<vector>`, 
	`<video>`, `<wbr />`, `<wbr/>`, `<wbr>`, `<wchar.h>`, `<wctype.h>`, `<xf:case>`, `<xf:group>`, `<xf:input>`, `<xf:instance>`, 
	`<xf:model>`, `<xf:namespace>`, `<xf:output>`, `<xf:repeat>`, `<xf:submission>`, `<xf:switch>`, `<xf:trigger>`, `<xmp><xtags>`, `=begin `, `=end`, 
	`?>`, `Array.isArray`, `Array.prototype`, `Console.WriteLine`, `Dir.glob`, `DispatchQueue.main.async`, `File.open`, `File.read`, `JSON.parse`, `JSON.stringify`, 
	`Kernel.rand`, `Object()`, `Object.keys`, `System.Collections.`, `System.Collections.Generic.`, `System.Collections.Generic.Dictionary`, `System.Collections.Generic.List`, `System.Data.DataSet`, `System.Data.SqlClient.SqlConnection`, `System.IO.File`, 
	`System.Linq.Enumerable.Range`, `System.Net.Http.HttpClient`, `System.Net.WebClient`, `System.Text.RegularExpressions.Regex`, `System.Text.StringBuilder`, `System.Threading.Thread.Sleep`, `System.Xml.XmlDocument`, `System.out.println`, `Time.now`, `[DEFINE]`, 
	`[ELIF]`, `[ENDIF]`, `[ERROR]`, `[IFNOT]`, `[IF]`, `[INCLUDE]`, `[LINE]`, `[PRAGMA]`, `[UNDEF]`, `__add__()`, 
	`__construct`, `__construct()`, `__dirname`, `__eq__()`, `__file__`, `__filename`, `__init__`, `__init__()`, `__len__`, `__len__()`, 
	`__main__`, `__name__`, `__str__()`, `boost::`, `bufio.NewScanner`, `console.error`, `console.log`, `console.warn`, `constructor()`, `date_default_timezone_set`, 
	`date_time`, `document.createAttribute`, `document.createComment`, `document.createDocumentFragment`, `document.createElement`, `document.createTextNode`, `document.getElementById`, `document.getElementsByClassName`, `document.getElementsByName`, `document.getElementsByTagName`, 
	`document.querySelector`, `document.querySelectorAll`, `fmt.Printf`, `fmt.Println`, `gc_collect_cycles()`, `gc_disable()`, `gc_enable()`, `gc_enabled()`, `getenv()`, `getopt()`, 
	`http.Get`, `http.Post`, `io.Reader`, `io.Writer`, `java.io.File`, `java.lang.String`, `java.net.Socket`, `java.sql.Connection`, `java.util.ArrayList`, `java.util.Calendar`, 
	`java.util.Date`, `java.util.Enumeration`, `java.util.GregorianCalendar`, `java.util.HashMap`, `java.util.Iterator`, `java.util.List`, `java.util.Locale`, `java.util.Map`, `java.util.Observable`, `java.util.Observer`, 
	`java.util.Properties`, `java.util.ResourceBundle`, `java.util.Scanner`, `java.util.Set`, `java.util.SimpleTimeZone`, `java.util.TimeZone`, `json.Marshal`, `json.Unmarshal`, `main()`, `math.Sqrt`, 
	`math.sqrt`, `matplotlib.pyplot.plot`, `memory_get_peak_usage()`, `memory_get_usage()`, `module.exports`, `new Date`, `new Promise`, `numpy.array`, `os.Create`, `os.Open`, 
	`os.Remove`, `os.chdir`, `os.environ`, `os.getcwd`, `os.listdir`, `os.mkdir`, `os.path`, `os.path.`, `os.path.exists`, `os.path.getatime`, 
	`os.path.getctime`, `os.path.getmtime`, `os.path.getsize`, `os.path.isdir`, `os.path.isfile`, `os.path.join`, `os.path.split`, `os.path.splitext`, `os.popen`, `os.rename`, 
	`os.rmdir`, `os.startfile`, `os.system`, `os.walk`, `pandas.DataFrame`, `parent::__construct`, `print`, `printf`, `printf()`, `process.env`, 
	`process.exit`, `putenv()`, `require_once`, `scanf()`, `smart_ptr`, `sort.Ints`, `sort.Strings`, `sql.Open`, `sql.Query`, `std::`, 
	`std::accumulate`, `std::acos`, `std::acosh`, `std::adjacent_difference`, `std::adjacent_find`, `std::advance`, `std::array`, `std::asin`, `std::asinh`, `std::async`, 
	`std::atan`, `std::atan2`, `std::atanh`, `std::atomic`, `std::atomic_`, `std::atomic_bool`, `std::atomic_char`, `std::atomic_double`, `std::atomic_flag`, `std::atomic_float`, 
	`std::atomic_int`, `std::atomic_int16_t`, `std::atomic_int32_t`, `std::atomic_int64_t`, `std::atomic_int8_t`, `std::atomic_int_fast16_t`, `std::atomic_int_fast32_t`, `std::atomic_int_fast64_t`, `std::atomic_int_fast8_t`, `std::atomic_int_least16_t`, 
	`std::atomic_int_least32_t`, `std::atomic_int_least64_t`, `std::atomic_int_least8_t`, `std::atomic_intmax_t`, `std::atomic_intptr_t`, `std::atomic_long`, `std::atomic_ptrdiff_t`, `std::atomic_schar`, `std::atomic_short`, `std::atomic_size_t`, 
	`std::atomic_uchar`, `std::atomic_uint`, `std::atomic_uint16_t`, `std::atomic_uint32_t`, `std::atomic_uint64_t`, `std::atomic_uint8_t`, `std::atomic_uint_fast16_t`, `std::atomic_uint_fast32_t`, `std::atomic_uint_fast64_t`, `std::atomic_uint_fast8_t`, 
	`std::atomic_uint_least16_t`, `std::atomic_uint_least32_t`, `std::atomic_uint_least64_t`, `std::atomic_uint_least8_t`, `std::atomic_uintmax_t`, `std::atomic_uintptr_t`, `std::atomic_ulong`, `std::atomic_ushort`, `std::atomic_wchar_t`, `std::bad_function_call`, 
	`std::bad_future`, `std::bad_promise`, `std::begin`, `std::binary_search`, `std::bitset`, `std::cbegin`, `std::cbrt`, `std::ceil`, `std::cend`, `std::chrono::duration`, 
	`std::chrono::high_resolution_clock`, `std::chrono::hours`, `std::chrono::microseconds`, `std::chrono::milliseconds`, `std::chrono::minutes`, `std::chrono::nanoseconds`, `std::chrono::seconds`, `std::chrono::steady_clock`, `std::chrono::system_clock`, `std::chrono::time_point`, 
	`std::cin`, `std::cin.ignore`, `std::cin.peek`, `std::clamp`, `std::condition_variable`, `std::copy`, `std::copy_backward`, `std::copy_if`, `std::copy_n`, `std::cos`, 
	`std::cosh`, `std::cout`, `std::cout.put`, `std::crbegin`, `std::crend`, `std::current_exception`, `std::defaultfloat`, `std::deque`, `std::distance`, `std::end`, 
	`std::endl`, `std::equal_range`, `std::erf`, `std::erfc`, `std::exception_ptr`, `std::exchange`, `std::exp`, `std::exp2`, `std::expm1`, `std::fill`, 
	`std::fill_n`, `std::find`, `std::find_end`, `std::find_first_of`, `std::find_if`, `std::find_if_not`, `std::fixed`, `std::floor`, `std::forward`, `std::forward_list`, 
	`std::fstream`, `std::future`, `std::future_error`, `std::gcd`, `std::generate`, `std::generate_n`, `std::hexfloat`, `std::hypot`, `std::ifstream`, `std::includes`, 
	`std::inner_product`, `std::inplace_merge`, `std::internal`, `std::ios_base::sync_with_stdio`, `std::iota`, `std::is_heap`, `std::is_heap_until`, `std::is_partitioned`, `std::is_sorted`, `std::is_sorted_until`, 
	`std::istream::get`, `std::launch`, `std::lcm`, `std::left`, `std::lexicographical_compare`, `std::lgamma`, `std::list`, `std::log`, `std::log10`, `std::log1p`, 
	`std::log2`, `std::lower_bound`, `std::make_exception_ptr`, `std::make_heap`, `std::map`, `std::map<std::string, int>`, `std::max`, `std::max_element`, `std::merge`, `std::min`, 
	`std::min_element`, `std::minmax_element`, `std::move`, `std::move_backward`, `std::move_if_noexcept`, `std::mutex`, `std::nested_exception`, `std::next`, `std::next_permutation`, `std::nth_element`, 
	`std::ofstream`, `std::ostream::put`, `std::packaged_task`, `std::pair`, `std::partial_sort`, `std::partial_sort_copy`, `std::partial_sum`, `std::partition`, `std::partition_copy`, `std::partition_point`, 
	`std::pop_heap`, `std::pow`, `std::prev`, `std::prev_permutation`, `std::priority_queue`, `std::promise`, `std::push_heap`, `std::queue`, `std::random_shuffle`, `std::rbegin`, 
	`std::recursive_mutex`, `std::remove`, `std::remove_copy`, `std::remove_copy_if`, `std::remove_if`, `std::rend`, `std::replace`, `std::replace_copy`, `std::replace_copy_if`, `std::replace_if`, 
	`std::resetiosflags`, `std::rethrow_exception`, `std::rethrow_if_nested`, `std::reverse`, `std::reverse_copy`, `std::right`, `std::rotate`, `std::rotate_copy`, `std::scientific`, `std::search`, 
	`std::search_n`, `std::set`, `std::set_difference`, `std::set_intersection`, `std::set_symmetric_difference`, `std::set_union`, `std::setbase`, `std::setfill`, `std::setiosflags`, `std::setprecision`, 
	`std::setw`, `std::shared_future`, `std::shared_mutex`, `std::showpos`, `std::shuffle`, `std::sin`, `std::sinh`, `std::sort`, `std::sort_heap`, `std::sqrt`, 
	`std::stable_partition`, `std::stable_sort`, `std::stack`, `std::stod`, `std::stof`, `std::stoi`, `std::stol`, `std::stold`, `std::stoll`, `std::stoul`, 
	`std::stoull`, `std::string`, `std::swap`, `std::swap_ranges`, `std::tan`, `std::tanh`, `std::tgamma`, `std::this_thread::sleep_for`, `std::this_thread::sleep_until`, `std::thread`, 
	`std::throw_with_nested`, `std::to_string`, `std::to_wstring`, `std::tr`, `std::transform`, `std::uncaught_exception`, `std::uncaught_exceptions`, `std::unique`, `std::unique_copy`, `std::unordered_map`, 
	`std::unordered_multimap`, `std::unordered_multiset`, `std::unordered_set`, `std::upper_bound`, `std::uppercase`, `std::vector`, `std::vector<bool>`, `std::vector<int>`, `strings.Contains`, `strings.Join`, 
	`strings.Split`, `sync.Mutex`, `sync.WaitGroup`, `sys.argv`, `sys_getloadavg()`, `template`, `template <typename T>`, `time.Now`, `time.Sleep`, `window.addEventListener`, 
	`window.alert`, `window.clearInterval`, `window.clearTimeout`, `window.close`, `window.confirm`, `window.onload`, `window.open`, `window.prompt`, `window.setInterval`, `window.setTimeout`,
	`http://`, `https://`, `ftp://`, `.D com`, `.D org`, `.D net`}
