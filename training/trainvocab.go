package main

import (
	"os"
	"log"
	"fmt"
	"time"
	"flag"
	"bytes"
	"errors"
	"regexp"
	"unicode"
	"reflect"
	"math/rand"
	"io/ioutil"
	"sync/atomic"
	"unicode/utf8"
	"unicode/utf16"
	"path/filepath"
	"encoding/json"
	"encoding/binary"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	uni "golang.org/x/text/encoding/unicode"
	"github.com/AlasdairF/Conv"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Sort/Uint32Uint32"
	"github.com/alasdairforsythe/branchless"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/capcode/go"
)

const (
	minHighSurrogate = 0xD800 // Start of high surrogate range
	maxHighSurrogate = 0xDBFF // End of high surrogate range
	minLowSurrogate  = 0xDC00 // Start of low surrogate range
	maxLowSurrogate  = 0xDFFF // End of low surrogate range
	runeError = '\uFFFD'
	apostrophe	   = '\''
	apostrophe2    = '’'
	noAlternative = 16777215
	MAXINT = 9223372036854775807
)

var (
	vocabSize int // common: 30000, 30522, 32000, 50265, 65535
	workers int = 8
	strips int = 100
	percentage int = 15
	midwayTarget int = 0
	datasetFilename string
	dictionaryFilename string
	resultsDir string
	keepTrying int = 1000
	include256bytes bool
	includeUTF8bytes bool
	include128bytes bool
	includeASCIIbytes bool
	includeExtendedbytes bool
	excludeOtherBytes bool
	reserve uint8
	usingCapcode bool = false
	charsetFlag uint8 = 0
	level uint8 = 0
	fast bool
	specialTokensFilename string
	dictionary2 string
	hasSpecial bool
	includeMissingBytes bool

	ungreedySuffixes = []string{"'s", "’s"}
	ungreedySuffixesB [][]byte

	specialMap map[string]bool

	remainingTokens_atomic int64
)

type resultStruct struct {
	testVocab *pansearch.Fast
	tokensInText int
	tokensToRemove [][]byte
	missing []byte
	scores []uint32
	usingFullDataset bool
}

type bestStruct struct {
    tokens    int
    filename  string
}

type tokenInfo struct {
	alt		tokenOuter
}

type tokenOuter struct {
	index	uint32		// the index of the alternative token
	index2  uint32		// the index of the 2nd alternative token
	length	int			// that token is this many bytes long
	length2 int
	data	tokenInner
}

type tokenInner struct {
	flag	uint8	
	nWords 	uint8	// the number of word beginnings
}

// Channels that holds the various random dictionaries
var channelWork = make(chan *pansearch.Fast, 2)
var channelResult = make(chan resultStruct, workers * 4)
var regx = regexp.MustCompile("^[0-9]+_[0-9]+\\.[a-zA-Z0-9]+$")

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

func flagIsSet(flagName string) bool {
	var set bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == flagName {
			set = true
		}
	})
	return set
}

func formatInt(v int) string {
	return string(conv.FormatThousands(conv.Bytes(v), ','))
}

func hasSuffixPos(key []byte) int {
	if charsetFlag == 0 {
		return -1
	}
	for _, suffix := range ungreedySuffixesB {
		if bytes.HasSuffix(key, suffix) {
			if len(suffix) < len(key) {
				r := decodeLastRune(key[:len(key)-len(suffix)])
				if isLetter(r) {
					return len(key) - len(suffix)
				}
			}
		}
	}
	return -1
}

func genUTF8bytes(list []bool) {
	genASCIIbytes(list)
    // Continuation bytes in multi-byte characters
    for i := 0x80; i <= 0xBF; i++ {
		list[i] = true
    }
    // Starting bytes of multi-byte characters excluding overlongs
    for i := 0xC2; i <= 0xF4; i++ {
		list[i] = true
    }
}

func genASCIIbytes(list []bool) {
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

func genExtendedbytes(list []bool) {
	s := `£€©®™°%¢¥—–•‘’“”áéíóúýàèìòùâêîôûäëïöüñãõçåæœ`
	if !usingCapcode {
		s += `ÁÉÍÓÚÝÀÈÌÒÙÂÊÎÔÛÄËÏÖÜÑÃÕÇÅÆŒ`
	}
	s2, _ := norm_UTF8_NFD([]byte(s))
	for _, b := range s2 {
		list[b] = true
	}
	genASCIIbytes(list)
}

func gen128bytes(list []bool) {
	var b byte
	for i:=0; i<128; i++ {
		b = byte(i)
		if !usingCapcode || (!(b >= 'A' && b <= 'Z' && b != 'C' && b != 'W' && b != 'D')) {
			list[i] = true
		}
	}
}

func gen256bytes(list []bool) {
	var b byte
	for i:=0; i<256; i++ {
		b = byte(i)
		if !usingCapcode || (!(b >= 'A' && b <= 'Z' && b != 'C' && b != 'W' && b != 'D')) {
			list[i] = true
		}
	}
}

func mergeBytes(list [][]byte, new []byte) ([][]byte, int) {
	var num int
	for _, b1 := range new {
		exists := false
		for _, b2 := range list {
			if b1 == b2[0] {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, []byte{byte(b1)})
			num++
		}
	}
	return list, num
}

func isLetter(r rune) bool {
	return (unicode.IsLetter(r) && (!usingCapcode || (r != 'W' && r != 'C' && r != 'D'))) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isAlphaNum(r rune) bool {
	return (unicode.IsLetter(r) && (!usingCapcode || (r != 'W' && r != 'C' && r != 'D'))) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r)
}

func isCapcode(r rune) bool {
	if usingCapcode {
		return r == 'W' || r == 'D' || r == 'C'
	} else if charsetFlag == 1 {
		return r == 127
	}
	return false
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

func decodeLastRune(b []byte) rune {
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
			return runeError
	}
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

func convertStringToUTF16WithNFDNormalization(s string) []byte {
	s = norm.NFD.String(s)
	b := []byte(s)
	buf := &bytes.Buffer{}
	w := transform.NewWriter(buf, uni.UTF16(uni.LittleEndian, uni.IgnoreBOM).NewEncoder())
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func saveTokensToFile(filename string, data [][]byte, data2 [][]byte, data3 [][]byte, scores []uint32, datasize int) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	defer w.Close()
	w.WriteBool(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteByte(level)
	w.WriteByte(reserve)
	w.WriteByte(0)
	w.WriteUint64(uint64(len(data) + len(data2) + len(data3)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	for _, b := range data2 {
		w.WriteBytes8(b)
	}
	for _, b := range data3 {
		w.WriteBytes8(b)
	}
	if len(scores) > 0 {
		var divider float64 = float64(datasize)
		for _, v := range scores {
			w.WriteFloat32(float32(float64(v) / divider))
		}
	}
	return nil
}

func loadTokensFromFile(filename string) (bool, uint8, uint8, uint8, uint8, [][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return false, 0, 0, 0, 0, nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	_usingCapcode := r.ReadBool()
	_charsetFlag := r.ReadByte()
	_level := r.ReadByte()
	_reserve := r.ReadByte()
	_custom := r.ReadByte()
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	// Make sure we're at the end
	if r.EOF() != nil { // it can be longer if it includes scores, so just do a quick sanity check
		if _charsetFlag > 2 || _level > 5 || len(data[0]) > 40 || len(data[1]) > 40 || len(data[len(data)-1]) > 40 {
			return false, 0, 0, 0, 0, nil, errors.New(filename + ` not valid.`)
		}
	}
	return _usingCapcode, _charsetFlag, _level, _reserve, _custom, data, nil
}



/*

Bitwise stuff:

Things that I need:

1	ends with a letter
2	begins with a letter
4 	begins with a space OR characterToken OR wordToken
8 	ends on capcode
16	begins on capcode
32 	a single straight word
64 	is special
128 is either all letters or no letters

beginByte
	1 = letter
	10 = anything else
	12 = space >>2 & 1 == 1
	>>3 means not a letter

*/

func worker(id int, datastrips [][]byte, filedata []byte) {
	var i, i1, i2, i3, length, length1, length2, length3, length1b, length2b, length3b int
	var score1, score2, score3, score1b, score2b, score3b, nWords, branchLength int
	var index, index1, index2, index3, index1b, index2b, index3b, deleteToken uint32
	var divider, remainingTokens, tokensInText, maxlen, lenData, maxlenWithSpace int
	var run int = 1
	var reachedMidway, hasDeleteToken bool
	var found, found1, found2, found3 bool
	var firstRun bool = true
	var data []byte
	var tokenData, original tokenInfo
	var first, second tokenInner
	var forwardDelete, maxScore int
	var nextByte uint8
	keys := make([][]byte, vocabSize)
	scores := make([]sortUint32Uint32.KeyVal, vocabSize)
	lilbuf := make([]byte, 40)
	lilbuf[0] = 32
	lilbufOffset := 1
	if charsetFlag == 2 {
		lilbufOffset = 2
	}
	lilbufStart := lilbuf[lilbufOffset:]

	for testVocab := range channelWork {
		if firstRun {
			log.Println(`Worker`, id, `starting run`, run)
			firstRun = false
		}

		// Reset vars this round's total and scores
		tokensInText = 0
		missingList := []byte{}
		for i, _ = range scores {
			scores[i] = sortUint32Uint32.KeyVal{uint32(i), 0}
		} 

		// Finish building the testVocab
		maxlen = testVocab.LongestLength() // the longest token length in this testVocab
		maxlenWithSpace = maxlen - lilbufOffset

		// Sanity check, this should never happen
		if testVocab.Len() != vocabSize {
			panic(errors.New(`testVocab contains ` + conv.String(testVocab.Len()) + ` not the target ` + conv.String(vocabSize)))
		}

		// Loop through all tokens in the testVocab and try to find other tokens that have the same beginning, these are potential ungreedy alternatives
		var charTable [256][4]uint32
		vocabList := make([]tokenInfo, vocabSize)
		if testVocab.Reset() {
			var token, subword []byte
			var on, hasSuffix, minAltSize int
			var r, r2 rune
			var n, n2 int
			var priority1, priority2, nWords uint8
			var onlyLetterSpace, onlyPunc, onlyNumberSpace bool
			for eof := false; !eof; {
				token, eof = testVocab.Next()
				keys[on] = token
				tokenData = tokenInfo{alt:tokenOuter{index:noAlternative, index2:noAlternative}}
				// Check for special tokens
				if hasSpecial {
					if _, found = specialMap[string(token)]; found {
						tokenData.alt.data.flag = 64
						vocabList[on] = tokenData
						on++
						// special tokens aren't allowed to have tokenDatas
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
				r, n = decodeRune(token)
				r2, n2 = decodeRune(token[n:])
				// Check beginning of token
				if r == ' ' {
					tokenData.alt.data.flag = 4
					charTable[token[0]][0]++
					if isAlphaNum(r2) {
						nWords++
						minAltSize = 2
					}
				} else if isLetter(r) {
					tokenData.alt.data.flag = 2
					charTable[token[0]][1]++
				} else if isCapcode(r) {
					if r == capcode.CharacterToken || r == capcode.WordToken {
						tokenData.alt.data.flag = 4 // count as a space
					}
					tokenData.alt.data.flag |= 16 // begins on capcode
					charTable[token[0]][3]++
				} else if unicode.IsNumber(r) {
					charTable[token[0]][2]++
				} else {
					charTable[token[0]][3]++
				}
				// Count words in token
				if len(token) == 1 {
					onlyPunc = true
				} else {
					if (r == ' ' || isLetter(r)) && isLetter(r2) {
						onlyLetterSpace = true
					} else if (r == ' ' || unicode.IsNumber(r)) && unicode.IsNumber(r2) {
						onlyNumberSpace = true
					} else if !isAlphaNum(r) && !isAlphaNum(r2) {
						onlyPunc = true
					}
					for i = n + n2; i < len(token); i += n2 {
						r = r2
						n = n2
						r2, n2 = decodeRune(token[i:])
						if r == ' ' && isAlphaNum(r2) {
							nWords++
						}
						if isLetter(r2) {
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
				r = decodeLastRune(token)
				if minAltSize == 2 && isLetter(r) && onlyLetterSpace { // only letters and full words
					if nWords == 1 {
						tokenData.alt.data.flag |= 32 // 1 word beginning space with only letters
					}
				}
				if minAltSize == 2 && nWords <= 1 { // begins _A and more than 1 word
					minAltSize = 1
				}
				if isCapcode(r) {
					tokenData.alt.data.flag |= 8
				}
				// Check end of token
				if isLetter(r) { // token ends with a letter
					tokenData.alt.data.flag |= 1
				}
				if onlyLetterSpace || onlyPunc || onlyNumberSpace {
					tokenData.alt.data.flag |= 128
				}

				hasSuffix = hasSuffixPos(token)

				for length=len(token)-1; length>=minAltSize; length-- { // loop through all possible subwords that would also fit beneath this one
					subword = token[:length] // the subword
					if index, found = testVocab.Find(subword); found { // is this subword in the testVocab?

						// anything | space_letter or space_number
						if length <= len(token) - 2 {
							if token[length] == ' ' {
								r, _ = decodeRune(token[length+1:])
								if isLetter(r) || unicode.IsNumber(r) { // space then letter or number
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

						r = decodeLastRune(subword) // last char in subtoken
						r2, _ = decodeRune(token[length:]) // the next char
						if !usingCapcode {
							switch {
							case (!isLetter(r) && r != '_') && (isLetter(r2) || r2 == '_'):
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
							case (isLetter(r) || r == '_') && (!isLetter(r2) && r2 != '_'):
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
							case isCapcode(r2):
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

		// Build chartable
		var beginByte [256]uint8
		for i=0; i<256; i++ {
			if charTable[i][1] > charTable[i][0] && charTable[i][1] > charTable[i][2] && charTable[i][1] > charTable[i][3] && charTable[i][1] > 2 {
				beginByte[i] = 1 // it's a letter
			} else if charTable[i][0] > charTable[i][1] && charTable[i][0] > charTable[i][2] && charTable[i][0] > charTable[i][3] && charTable[i][0] > 2 {
				beginByte[i] = 4 + 8 // it's a space
			} else if charTable[i][3] > charTable[i][0] && charTable[i][3] > charTable[i][1] && charTable[i][3] > charTable[i][2] && charTable[i][3] > 2 {
				beginByte[i] = 2 + 8 // it's punctuation or capcode
			}
		}

		// Find the deleteToken
		hasDeleteToken = false
		if charsetFlag == 1 {
			if usingCapcode {
				if index, found = testVocab.Find([]byte{capcode.DeleteToken}); found {
					deleteToken = index
					hasDeleteToken = true
				}
			} else {
				if index, found = testVocab.Find([]byte{capcode.NoCapcodeDeleteToken}); found {
					deleteToken = index
					hasDeleteToken = true
				}
			}
		}
		
		// If midwayTarget has been reached, check the full dataset
		if !reachedMidway {
			remainingTokens = int(atomic.LoadInt64(&remainingTokens_atomic))
			if remainingTokens <= midwayTarget {
				datastrips[0] = filedata // replace the datastrips with the whole dataset
				datastrips = datastrips[0:1]
				reachedMidway = true
			}
		}

		// Main tokenization loop
		for _, data = range datastrips {
			// We increase the data length by 1 because we're always checking the next byte
			lenData = len(data) // remember the true length
			if cap(data) > len(data) { // this should be true because capcode copies it originally
				data = data[0:len(data)+1]
			} else {
				data2 := make([]byte, len(data) + 1)
				copy(data2, data)
				data = data2
			}
			i = 0
			for i < lenData {
				if index, length, found = testVocab.LongestSubstring(data[ i : i + branchless.Min(lenData - i, maxlen) ]); found {
					
					checkpoint:

						original = vocabList[index]
						i1 = i + length

						// Skip checking alternatives if the longest first match is a single whole word of only letters: begins _A + ends A + next_is_space + 1word
						if (i1 < lenData && (original.alt.data.flag & 32 == 0 || beginByte[data[i1]] != 12)) {
							
							score1 = -1000000
							score2 = -1000000
							score3 = -1000000
							score1b = -1000000
							score2b = -1000000
							score3b = -1000000
							maxScore = -1000000

							// First lookahead to the next token after me
							index1, length1, found1 = testVocab.LongestSubstring(data[ i1 : i1 + branchless.Min(lenData - i1, maxlen) ])

							if found1 {
								nWords = int(original.alt.data.nWords) - forwardDelete
								second = vocabList[index1].alt.data
								nextByte = beginByte[data[i1 + length1]]

								score1 = ((	length + length1 + 											// the total length of the branch
									int((original.alt.data.flag >> 7) + (second.flag >> 7)) +			// 1 point for each token being either all letters or all punctuation
									branchless.MaxZeroAnd(nWords - 1) + 								// 1 less than the number of word beginnings in the 1st token, min 0									
									branchless.MaxZeroAnd(int(second.nWords) - 1) +						// 1 less than the number of word beginnings in the second token, min 0
									int((second.flag >> 2) & 1) +										// 1 if the second token begins with a space
									int((nextByte >> 2) & 1) +											// 1 if the next character after the 2nd token is a space
									((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -			// 100x the number of whole words covered by this and next token
									( (int(original.alt.data.flag & 1 & (second.flag >> 1)) * 103) + 	// Deduct 103 if the first and second token split a word
									(int((original.alt.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +	// Decuct 100 if it splits a capcode token
									((int(second.flag & 1 & nextByte) * 3)) )) 							// Deduct 3 if the second token ends inside a word
								maxScore = score1
								
								// Check if we're in the middle of a word
								if hasDeleteToken && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
									length1b = branchless.Min(lenData - i1, maxlenWithSpace)
									copy(lilbufStart, data[ i1 : i1 + length1b ])
									index1b, length1b, _ = testVocab.LongestSubstring(lilbuf[:length1b + lilbufOffset])
									if length1b > length1 + 1 {
										length1b -= lilbufOffset
										second = vocabList[index1b].alt.data
										nextByte = beginByte[data[i1 + length1b]]
										score1b = ((	length + length1b + 							// the total length of the branch
											int((original.alt.data.flag >> 7) + (second.flag >> 7)) +	// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(original.alt.data.flag & 1) * 103) + 				// Deduct 103 if the first and second token split a word
											(int((original.alt.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +	// Decuct 100 if it splits a capcode token
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											1 )) 														// Deduct 1 for using an extra token
										maxScore = branchless.Max(maxScore, score1b)
									}
								}
							}

							if original.alt.index != noAlternative {
								i2 = i + original.alt.length - forwardDelete
								index2, length2, found2 = testVocab.LongestSubstring(data[ i2 : i2 + branchless.Min(lenData - i2, maxlen) ])

								if found2 {
									first = vocabList[original.alt.index].alt.data
									nWords = int(first.nWords) - forwardDelete
									second = vocabList[index2].alt.data
									nextByte = beginByte[data[i2 + length2]]
									branchLength = original.alt.length + length2 - forwardDelete
	
									score2 = ((	branchLength + 										// the total length of the branch
										int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
										branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
										branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
										int((second.flag >> 2) & 1) +								// 1 if the second token begins with a space
										int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
										((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
										( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 		// Deduct 103 if the first and second token split a word
										(int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) + // Decuct 100 if it splits a capcode token
										((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
										(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
										(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
									maxScore = branchless.Max(maxScore, score2)

									// Check if we're in the middle of a word
									if hasDeleteToken && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
										length2b = branchless.Min(lenData - i2, maxlenWithSpace)
										copy(lilbufStart, data[ i2 : i2 + length2b ])
										index2b, length2b, _ = testVocab.LongestSubstring(lilbuf[:length2b + lilbufOffset])
										if length2b > length2 + 1 {
											length2b -= lilbufOffset
											second = vocabList[index2b].alt.data
											branchLength = original.alt.length + length2b - forwardDelete
											nextByte = beginByte[data[i2 + length2b]]
											score2b = (( branchLength + 									// the total length of the branch
												int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
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

								if original.alt.index2 != noAlternative {
									i3 = i + original.alt.length2 - forwardDelete
									index3, length3, found3 = testVocab.LongestSubstring(data[ i3 : i3 + branchless.Min(lenData - i3, maxlen) ])
	
									if found3 {
										first = vocabList[original.alt.index2].alt.data
										nWords = int(first.nWords) - forwardDelete
										second = vocabList[index3].alt.data
										nextByte = beginByte[data[i3 + length3]]
										branchLength = original.alt.length2 + length3 - forwardDelete
		
										score3 = ((	branchLength + 										// the total length of the branch
											int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
											branchless.MaxZeroAnd(nWords - 1) + 						// 1 less than the number of word beginnings in the 1st token, min 0									
											branchless.MaxZeroAnd(int(second.nWords) - 1) +				// 1 less than the number of word beginnings in the second token, min 0
											int((second.flag >> 2) & 1) +								// 1 if the second token begins with a space
											int((nextByte >> 2) & 1) +									// 1 if the next character after the 2nd token is a space
											((nWords + int(second.nWords + (nextByte >> 3))) * 100)) -	// 100x the number of whole words covered by this and next token
											( (int(first.flag & 1 & (second.flag >> 1)) * 103) + 		// Deduct 103 if the first and second token split a word
											((int(second.flag & 1 & nextByte) * 3)) +					// Deduct 3 if the second token ends inside a word
											(branchless.LessThan(branchLength, length) * 100) + 		// Deduct 100 if the entire branch is shorter than the longest first token
											(branchless.Equal(branchLength, length) * 10000) )) 		// Deduct 10,000 if the entire branch is the same size as the original first token
										maxScore = branchless.Max(maxScore, score3)

										// Check if we're in the middle of a word
										if hasDeleteToken && second.flag & 2 != 0 && nextByte == 1 && second.nWords == 0 {
											length3b = branchless.Min(lenData - i3, maxlenWithSpace)
											copy(lilbufStart, data[ i3 : i3 + length3b ])
											index3b, length3b, _ = testVocab.LongestSubstring(lilbuf[:length3b + lilbufOffset])
											if length3b > length3 + 1 {
												length3b -= lilbufOffset
												second = vocabList[index3b].alt.data
												branchLength = original.alt.length2 + length3b - forwardDelete
												nextByte = beginByte[data[i3 + length3b]]
												score3b = (( branchLength + 									// the total length of the branch
													int((first.flag >> 7) + (second.flag >> 7)) +				// 1 point for each token being either all letters or all punctuation
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
									scores[index].V += uint32(length) // forwardDelete is already applied to length
									i += length
									tokensInText++
									length = length1
									index = index1
									forwardDelete = 0
									goto checkpoint
								case score2:
									scores[original.alt.index].V += uint32(original.alt.length - forwardDelete)
									i += original.alt.length - forwardDelete
									tokensInText++
									length = length2
									index = index2
									forwardDelete = 0
									goto checkpoint
								case score3:
									scores[original.alt.index2].V += uint32(original.alt.length2 - forwardDelete)
									i += original.alt.length2 - forwardDelete
									tokensInText++
									length = length3
									index = index3
									forwardDelete = 0
									goto checkpoint
								case score1b:
									scores[index].V += uint32(length)
									scores[deleteToken].V++
									i += length
									tokensInText += 2
									length = length1b
									index = index1b
									forwardDelete = 1
									goto checkpoint
								case score2b:
									scores[original.alt.index].V += uint32(original.alt.length - forwardDelete)
									scores[deleteToken].V++
									i += original.alt.length - forwardDelete
									tokensInText += 2
									length = length2b
									index = index2b
									forwardDelete = 1
									goto checkpoint
								case score3b:
									scores[original.alt.index2].V += uint32(original.alt.length2 - forwardDelete)
									scores[deleteToken].V++
									i += original.alt.length2 - forwardDelete
									tokensInText += 2
									length = length3b
									index = index3b
									forwardDelete = 1
									goto checkpoint
							}
						}
						// Skipped this branch (or case -1000000 from scores)
						scores[index].V += uint32(length) // this token saved this many characters (its length)
						i += length
						tokensInText++
						forwardDelete = 0

				} else { // !found
					if includeMissingBytes {
						missingList = append(missingList, data[i])
					}
					tokensInText++
					i++
					forwardDelete = 0
				}
			}
		}

		// Copy the scores
		scoresCopy := make([]uint32, vocabSize)
		for i, _ = range scores {
			scoresCopy[i] = scores[i].V
		}

		// Determine tokens to delete
		remainingTokens = int(atomic.LoadInt64(&remainingTokens_atomic))
		sortUint32Uint32.Asc(scores) // sort all the tokens by the number of characters they saved (their length * occurences)
		if fast {
			switch {
				case remainingTokens == 0: // reachedVocab, it'll be decided by master
					divider = 10
				case remainingTokens < vocabSize + (vocabSize / 4):
					divider = 200
				case remainingTokens < vocabSize + (vocabSize / 2):
					divider = 150
				case remainingTokens < vocabSize * 2:
					divider = 100 	
				case remainingTokens < midwayTarget / 6: 	// < 83,333
					divider = 100 								
				case remainingTokens < midwayTarget / 4: 	// < 125,000
					divider = 100 								
				case remainingTokens < midwayTarget / 2: 	// < 250,000
					divider = 100 								
				case remainingTokens < midwayTarget: 		// < 500,000 (below midwayTarget, the entire dataset is used for each run)
					divider = 50 								
				case remainingTokens < (midwayTarget*3)/2: // < 750,000
					divider = 40 								
				case remainingTokens < midwayTarget * 2: 	// < 1,000,000
					divider = 30 								
				case remainingTokens < midwayTarget * 4: 	// < 2,000,000
					divider = 20 								
				case remainingTokens < midwayTarget * 10: 	// < 5,000,000
					divider = 10 							
				default:										
					divider = 10							// 10%
			}
		} else {
			switch {
				case remainingTokens == 0: // reachedVocab, it'll be decided by master
					divider = 10
				case remainingTokens < vocabSize + (vocabSize / 4):
					divider = 2000 	
				case remainingTokens < vocabSize + (vocabSize / 2):
					divider = 1500
				case remainingTokens < vocabSize * 2:
					divider = 1000 	
				case remainingTokens < midwayTarget / 6: 	// < 83,333
					divider = 400 								
				case remainingTokens < midwayTarget / 4: 	// < 125,000
					divider = 300 								
				case remainingTokens < midwayTarget / 2: 	// < 250,000
					divider = 200 								
				case remainingTokens < midwayTarget: 		// < 500,000 (below midwayTarget, the entire dataset is used for each run)
					divider = 150 								
				case remainingTokens < (midwayTarget*3)/2: // < 750,000
					divider = 100 								
				case remainingTokens < midwayTarget * 2: 	// < 1,000,000
					divider = 80 								
				case remainingTokens < midwayTarget * 4: 	// < 2,000,000
					divider = 40 								
				case remainingTokens < midwayTarget * 10: 	// < 5,000,000
					divider = 20 							
				default:										
					divider = 10							// 10%
			}
		}
		length = vocabSize / divider
		if length < 2 {
			length = 2
		}
		if length > vocabSize - 1 {
			length = vocabSize - 1
		}
		tokensToRemove := make([][]byte, length)
		index = 0
		for i=0; i<length && i<len(scores); i++ {
			if len(keys[scores[i].K]) == 1 { // don't try to remove single bytes
				length++
				continue
			}
			tokensToRemove[index] = keys[scores[i].K]
			index++
		}
		tokensToRemove = tokensToRemove[0:index]
		// Now check if these are still at 0 and if so includes all zeros
		if i < len(scores) {
			if scores[i].V == 0 {
				for ; i < len(scores); i++ {
					if scores[i].V > 0 {
						break
					}
					if len(keys[scores[i].K]) == 1 { // don't try to remove single bytes
						continue
					}
					tokensToRemove = append(tokensToRemove, keys[scores[i].K])
				}
			}
		}
		// Return the result back to the master thread
		channelResult <- resultStruct{testVocab, tokensInText, tokensToRemove, missingList, scoresCopy, reachedMidway}
		log.Println(`Worker`, id, `completed run`, run, ` Score:`, formatInt(tokensInText))
		run++
    }
}

func shuffle(original [][]byte) {
	var i, j int
	for i = len(original) - 1; i > 0; i-- {
		j = rand.Intn(i + 1)
		original[i], original[j] = original[j], original[i]
	}
}

// This is a helper function to allow for resuming the progress from a final dictionary
// It returns the score and true if the filename is score_numbers.whatever
func detectSavedFinal(path string) (uint, bool) {
	f := filepath.Base(path)
	if regx.MatchString(f) {
		bs := []byte(f)
		for i, b := range bs {
			if b == '_' {
				return conv.Uint(bs[0:i]), true
			}
		}
	}
	return 0, false
}

func normalizeToken(b []byte) ([]byte, error) {
	if charsetFlag == 1 {
		if usingCapcode {
			b = capcode.Encode(b)
		}
		return norm_UTF8_NFD(b)
	} else if charsetFlag == 2 {
		b, _ = uni.UTF16(uni.LittleEndian, uni.IgnoreBOM).NewEncoder().Bytes(b)
		return norm_UTF16_NFD(b)
	}
	return b, nil
}

func main() {

	flag.IntVar(&vocabSize, "vocab-size", vocabSize, "vocabulary size, e.g. 32000 (required)")
	flag.StringVar(&datasetFilename, "dataset", datasetFilename, "filename of the dataset plain-text (required)")
	flag.StringVar(&dictionaryFilename, "dictionary", dictionaryFilename, "filename of the dictionary generated by getalltokens or any of the saved output files from this app (required)")
	flag.StringVar(&dictionary2, "dictionary2", dictionary2, "a second dictionary that will be merged with the first (optional)")
	flag.StringVar(&resultsDir, "dir", resultsDir, "directory to save the results within (required)")
	flag.IntVar(&workers, "workers", workers, "number of worker threads to run, excluding main thread")
	flag.IntVar(&percentage, "percentage", percentage, "percentage of the dataset given to each worker before midway-target")
	flag.IntVar(&midwayTarget, "midway-target", midwayTarget, "beneath this the full dataset is used for every worker (default 6x vocab-size)")
	flag.IntVar(&keepTrying, "keep-trying", keepTrying, "program will exit when unable to find a better match this many times in a row")
	flag.StringVar(&specialTokensFilename, "special", specialTokensFilename, "filename of a JSON file containing special tokens (optional)")
	flag.BoolVar(&include256bytes, "include-256-bytes", include256bytes, "include tokens representing every possible byte (default false)")
	flag.BoolVar(&include128bytes, "include-128-bytes", include128bytes, "include tokens representing every ASCII character inc. control characters (default false)")
	flag.BoolVar(&includeUTF8bytes, "include-utf8-bytes", includeUTF8bytes, "include tokens for every byte that can occur in UTF-8 text (default false)")
	flag.BoolVar(&includeASCIIbytes, "include-ascii-bytes", includeASCIIbytes, "include tokens for every printable ASCII character, inc. \\r\\n\\t (default false)")
	flag.BoolVar(&includeExtendedbytes, "include-extended-bytes", includeExtendedbytes, "include tokens for ASCII & UTF-8 chars used in English, e.g. “£©áê (default false)")
	flag.BoolVar(&includeMissingBytes, "include-missing-bytes", includeMissingBytes, "add tokens for any single bytes found in the dataset that are not tokens already (default false)")
	flag.BoolVar(&excludeOtherBytes, "exclude-other-bytes", excludeOtherBytes, "any single bytes not specifically included will not receive tokens, even if they were in the training dataset (default false)")
	flag.BoolVar(&fast, "fast", fast, "runs 10x faster but the vocabulary might not be as optimal (default false)")
	flag.Parse()
    flagRequired("vocab", vocabSize)
    flagRequired("dataset", datasetFilename)
    flagRequired("dictionary", dictionaryFilename)
    flagRequired("dir", resultsDir)

	if excludeOtherBytes && !include256bytes && !include128bytes && !includeASCIIbytes && !includeUTF8bytes {
		fmt.Fprintln(os.Stderr, "To exclude-other-bytes you need to have included some bytes.")
		os.Exit(1)
	}

	if fast {
		fmt.Println(`Fast mode enabled`)
		if !flagIsSet("percentage") {
			percentage = 10
		}
		if midwayTarget == 0 {
			midwayTarget = (vocabSize * 2) + (vocabSize / 4)
		}
		if !flagIsSet("keep-trying") {
			keepTrying = 275
		}
	} else if midwayTarget == 0 {
		midwayTarget = vocabSize * 6
	}
	if midwayTarget < vocabSize + (vocabSize / 10) {
		fmt.Fprintln(os.Stderr, "midway-target must be at least 10% higher than vocab-size")
		os.Exit(1)
	}

	fmt.Println(`Loading`, dictionaryFilename)

	// Load the big dictionary of all the tokens from the dataset
	var tokens [][]byte
	var err error
	usingCapcode, charsetFlag, level, _, _, tokens, err = loadTokensFromFile(dictionaryFilename)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to open the file:", dictionaryFilename)
		os.Exit(1)
	}
	// Load the second dictionary (if exists) and remove duplicates
	if len(dictionary2) > 0 {
		var tokens2 [][]byte
		_, _, _, _, _, tokens2, err = loadTokensFromFile(dictionary2)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Unable to open the file:", dictionary2)
			os.Exit(1)
		}
		counter := new(pansearch.Counter)
		for _, b := range tokens {
			counter.Add(b, 1)
		}
		for _, b := range tokens2 {
			counter.Add(b, 1)
		}
		counter.Build()
		tokens = counter.Keys()
	}

	// Parse the special tokens file
	var specialTokens [][]byte
	if len(specialTokensFilename) > 0 {
		log.Println(`Parsing`, specialTokensFilename)
		file, err := os.Open(specialTokensFilename)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Unable to open the file:", specialTokensFilename)
			os.Exit(1)
		}
		defer file.Close()
		
		// Read file
		data, err := ioutil.ReadAll(file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading", specialTokensFilename, err)
			os.Exit(1)
		}
		
		// Parse JSON
		type JsonData struct {
			Special []string `json:"special,omitempty"`
		}
		var jd JsonData
		err = json.Unmarshal(data, &jd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "There is an error in the JSON formatting of the 'special' JSON file:", err)
			fmt.Fprintln(os.Stderr, "Example of correct formatting: { \"special\": [ \"TOKEN1\", \"TOKEN2\", \"TOKEN3\" ] }")
			os.Exit(1)
		}
		if len(jd.Special) == 0 {
			fmt.Fprintln(os.Stderr, "Error: the special tokens file does not appear to contain any tokens")
			fmt.Fprintln(os.Stderr, "If you do not want to include special tokens, please omit including the file in the command line arguments")
			os.Exit(1)
		}
		specialTokens = make([][]byte, len(jd.Special))
		var on int
		for _, s := range jd.Special {
			if len(s) > 0 {
				b, err := normalizeToken([]byte(s))
				if err != nil {
					fmt.Fprintln(os.Stderr, "Error parsing tokens in special file. Please check the encoding is correct")
					os.Exit(1)
				}
				if len(b) == 1 {
					fmt.Fprintln(os.Stderr, "Error: A special token cannot be only 1 character")
					os.Exit(1)
				}
				specialTokens[on] = b
				on++
			}
		}
		specialTokens = specialTokens[0:on]
		hasSpecial = true
		specialMap = make(map[string]bool)
	}

	switch charsetFlag {
		case 0:
			fmt.Println(`Charset: None`)
		case 1:
			if usingCapcode {
				fmt.Println(`Charset: UTF-8, Capcode Enabled`)
			} else {
				fmt.Println(`Charset: UTF-8, Capcode Disabled`)
			}
		case 2:
			fmt.Println(`Charset: UTF-16`)
		default:
			fmt.Fprintf(os.Stderr, "Input file appears to be corrupt")
			os.Exit(1)
	}
	switch level {
		case 0:
			fmt.Println(`Optimization mode: 0 (unfiltered)`)
		case 1:
			fmt.Println(`Optimization mode: 1 (clean)`)
		case 2:
			fmt.Println(`Optimization mode: 2 (balanced)`)
		case 3:
			fmt.Println(`Optimization mode: 3 (consistent)`)
		case 4:
			fmt.Println(`Optimization mode: 4 (strict)`)
		default:
			fmt.Println(`Optimization mode: undefined`)
	}

	reserve = 0
	includeBytes := make([]bool, 256)
	if include256bytes {
		reserve |= 1 << 0
		gen256bytes(includeBytes)
	}
	if include128bytes {
		reserve |= 1 << 1
		gen128bytes(includeBytes)
	}
	if includeUTF8bytes {
		reserve |= 1 << 2
		genUTF8bytes(includeBytes)
	}
	if includeASCIIbytes {
		reserve |= 1 << 3
		genASCIIbytes(includeBytes)
	}
	if includeExtendedbytes {
		reserve |= 1 << 4
		genExtendedbytes(includeBytes)
	}
	fmt.Println(`Vocabulary size:`, vocabSize)
	if len(includeBytes) > 0 {
		var n int
		for i:=0; i<256; i++ {
			if includeBytes[i] {
				n++
			}
		}
		fmt.Println(`Single byte tokens:`, n)
		if excludeOtherBytes {
			reserve |= 1 << 5
			fmt.Println(`All other single byte tokens excluded`)
		}
	}

	fmt.Println(`Loading`, datasetFilename)

	// Trim trailing slashes from resultsDir and create it if it does not exist
	for len(resultsDir) > 0 && os.IsPathSeparator(resultsDir[len(resultsDir)-1]) {
		resultsDir = resultsDir[:len(resultsDir)-1]
	}
	if _, err = os.Stat(resultsDir); os.IsNotExist(err) {
		os.MkdirAll(resultsDir, 0755)
	}
	resultsDir = resultsDir + string(filepath.Separator)

	// Vars
	rand.Seed(time.Now().UnixNano())
	var i, i2, to, remainingTokens, best1percent, uniqueFileNumber, noNewBest, interval10, removed, shuffles, zeroRemoved int
	var exists, hasTokensToRemove, reachedMidway, withinVocabX2, reachedVocab, justReset bool
	var lastIntervalFileName, debugStr, finalRunFilename string
	var key []byte
	var hash uint64
	var c byte
	tokensToRemove := new(pansearch.Counter)
	dictsWithin1percent := make([]bestStruct, 0, 100)
	var best int = MAXINT

	// Build the ungreedy preference lookup table
	// If there are multiple options of ungreedy alternative, these are the preferred points
	ungreedySuffixesB = make([][]byte, len(ungreedySuffixes))
	if charsetFlag == 1 {
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = []byte(suffix)
		}
	} else if charsetFlag == 2 {
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = convertStringToUTF16WithNFDNormalization(suffix)
		}
	}

	// Load the text & normalize UTF8
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
				if usingCapcode {
					filedata = capcode.Encode(temp)
				} else {
					filedata = capcode.NoCapcodeEncode(temp)
				}
				filedata, err = norm_UTF8_NFD(filedata)
				if err != nil { // if it fails try the other way around
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
	dataLen := len(filedata)

	// Distribute the text randomly but evenly to each worker has x strips each from a different part of filedata
	if dataLen < 10 * 1024 * 1024 {
		strips = 20
	}
	bytesPerWorker := (dataLen * percentage) / 100
	bytesPerStrip := bytesPerWorker / strips
	bytesPerStrip += 4 - (bytesPerStrip % 4) // ensure it's divisible by 4 to avoid splitting glyphs
	offset := dataLen / strips
	data := make([][][]byte, workers)
	if offset + bytesPerStrip > dataLen || percentage >= 100 || dataLen < 24000 { // give the whole dataset to each worker in any of these conditions
		for i=0; i<workers; i++ {
			data[i] = make([][]byte, 1)
			data[i][0] = filedata
		}
	} else {
		var from int
		for i=0; i<workers; i++ {
			data[i] = make([][]byte, strips)
			from = rand.Intn(offset) // initial position
			for i2=0; i2<strips; i2++ {
				if from + bytesPerStrip > dataLen {
					from = (from + bytesPerStrip) - dataLen
				}
				data[i][i2] = filedata[from:from+bytesPerStrip]
				from += offset
			}
		}
	}

	// This section resumes the final run given one of the final run files, it's only here because I needed to do that when testing
	// Usually you would redo the final run from the finalrun file but you can use this to make it continue checking from the be
	if len(tokens) <= vocabSize {
		if nscore, is := detectSavedFinal(dictionaryFilename); is {
			best = int(nscore)
			nscore += nscore / 100
			best1percent = int(nscore)
			reachedMidway = true
			withinVocabX2 = true
			reachedVocab = true
			// Recreate dictsWithin1percent from the files in the directory
			uniqueTokens := new(pansearch.Counter)
			for _, b := range tokens {
				uniqueTokens.Add(b, 1)
			}
			dir := filepath.Dir(dictionaryFilename)
			files, err := ioutil.ReadDir(dir)
			if err != nil {
				panic(err)
			}
			for _, file := range files {
				fpath := filepath.Join(dir, file.Name())
				if nscore2, is := detectSavedFinal(file.Name()); is && nscore2 > 0 && nscore2 <= nscore {
					dictsWithin1percent = append(dictsWithin1percent, bestStruct{int(nscore2), fpath})
					_, _, _, _, _, toks, err := loadTokensFromFile(fpath)
					if err != nil {
						continue
					}
					for _, b := range toks {
						uniqueTokens.Add(b, 1)
					}
				}
			}
			uniqueTokens.Build()
			tokens = uniqueTokens.Keys() // this is all the tokens that are present in those within 1% of the best score
			log.Println(`Resuming final run from score`, best)
		}
	}

	// Remove tokens that include any of the special tokens inside of them
	// This stops the tokenizer from ever skipping a special token
	// Don't let any normal tokens contain special tokens
	for _, special := range specialTokens {
		specialMap[string(special)] = true
		i++
		for i, tok := range tokens {
			if bytes.Contains(tok, special) {
				tokens[i] = nil
			}
		}
	}

	// Remove deleted and separate single byte tokens (they are added to every vocabulary)
	i2 = 0
	for _, tok := range tokens {
		if len(tok) == 0 {
			continue
		}
		if len(tok) == 1 {
			if !excludeOtherBytes {
				includeBytes[tok[0]] = true
			}
		} else {
			tokens[i2] = tok
			i2++
		}
	}
	if usingCapcode {
		includeBytes[capcode.DeleteToken] = true
	} else {
		if charsetFlag == 1 {
			includeBytes[capcode.NoCapcodeDeleteToken] = true
		}
	}
	tokens = tokens[0:i2]
	var singleChars [][]byte
	for i=0; i<256; i++ {
		if includeBytes[i] {
			singleChars = append(singleChars, []byte{byte(i)})
		}
	}
	
	// How many tokens are there?
	vocabsTried := make(map[uint64]bool)
	//if (!usingCapcode && len(singleChars) < 256) || len(singleChars) < 233 {
	//	nUnk = 1
	//}
	vocabDiff := len(singleChars) + len(specialTokens) // not including nUnk on purpose
	vocabSizeEffective := vocabSize - vocabDiff
	remainingTokens = len(tokens)
	remainingTokens_atomic = int64(remainingTokens + vocabDiff) // still single-threaded here

	// Launch the worker threads
	for i=0; i<workers; i++ {
		go worker(i, data[i], filedata)
	}

	// Master loop
	for {
		select {
		case result, ok := <- channelResult: // this channel delivers the results
			if !ok { // channel is closed, never happens
				break
			}

			// If there are any missing characters, add them to the list
			if len(result.missing) != 0 {
				singleChars, i = mergeBytes(singleChars, result.missing)
				if i > 0 {
					//nUnk = 0
					//if (!usingCapcode && len(singleChars) < 256) || len(singleChars) < 233 {
					//	nUnk = 1
					//}
					vocabDiff = len(singleChars) + len(specialTokens)
					vocabSizeEffective = vocabSize - vocabDiff
					log.Println(i, `missing character(s) found and added to reserved tokens`)
				}
			}

			// Save all dictionaries within 10% of the best performing one
			if withinVocabX2 && result.usingFullDataset { // if we're within 2x the vocabSize
				if result.tokensInText < best {
					best = result.tokensInText
					best1percent = best + (best / 100)
					noNewBest = 0
					log.Println(`New best score`, formatInt(best))
					i = 0
					for _, v := range dictsWithin1percent {
						if v.tokens > best1percent {
							os.Remove(v.filename)
						} else {
							dictsWithin1percent[i] = v
							i++
						}
					}
					dictsWithin1percent = dictsWithin1percent[0:i]
				} else {
					noNewBest++
				}
				if result.tokensInText < best1percent {
					filename := resultsDir + conv.String(result.tokensInText) + "_" + conv.String(uniqueFileNumber) + ".tok"
					uniqueFileNumber++
					err = saveTokensToFile(filename, result.testVocab.Keys(), nil, nil, result.scores, len(filedata))
					if err != nil {
						panic(err)
					}
					dictsWithin1percent = append(dictsWithin1percent, bestStruct{result.tokensInText, filename})
				}
			}

			if reachedVocab {
				if noNewBest >= keepTrying {
					log.Println(`-- FINISHED --`)
					fmt.Println(`No new best score in`, noNewBest, `runs`)
					fmt.Println(`Best result tokenized`, formatInt(len(filedata)), `bytes with`, formatInt(best), `tokens`)
					fmt.Println(`Average`, string(conv.FloatBytes(float64(len(filedata)) / float64(best), 3)), `characters/token`)
					fmt.Println(`Best result:`)
					for _, v := range dictsWithin1percent {
						if v.tokens > best1percent {
							os.Remove(v.filename) // delete everything not in the top 1%
						} else {
							if v.tokens == best {
								fmt.Println(` `, v.filename) // output the filesnames of all those that are the best, which may be more than 1
							}
						}
					}
					os.Exit(0)
				}
				if best != result.tokensInText && len(result.tokensToRemove) > 0 {
					temp := remainingTokens - vocabSizeEffective
					switch  {
						case temp < 50:
							i2 = 1
						case temp < 100:
							i2 = 2
						case temp < 200:
							i2 = 3
						case temp < 300:
							i2 = 4
						case temp < 400:
							i2 = 6
						case temp < 500:
							i2 = 8
						case temp < 750:
							i2 = 10
						case temp < 1000:
							i2 = 20
						case temp < 2000:
							i2 = 30
						case temp < 2500:
							i2 = 40
						case temp < 3000:
							i2 = 50
						default:
							i2 = 100
					}
					if result.tokensInText > best1percent {
						i2 += 2
					}
					if fast {
						i2 *= 2
					}
					i2 = branchless.Min(i2 + zeroRemoved, len(result.tokensToRemove))
					for i=0; i<i2; i++ {
						tokensToRemove.Add(result.tokensToRemove[i], 1)
					}
					hasTokensToRemove = true
				}
			} else { // add tokens to remove
				if best != result.tokensInText {
					for _, v := range result.tokensToRemove {
						tokensToRemove.Add(v, 1)
					}
					hasTokensToRemove = true
				}
			}

		default:
			// no values left in the channel
			if hasTokensToRemove { // if there are any tokens to cull
				tokensToRemove.Build()
				remainingTokens = 0
				removed = 0
				for i=0; i<len(tokens); i++ {
					if _, exists = tokensToRemove.Find(tokens[i]); !exists {
						tokens[remainingTokens] = tokens[i]
						remainingTokens++
					} else {
						removed++
					}
				}
				if removed == 0 {
					zeroRemoved++ // if zero tokens are removed, remove 1 more next round
				} else {
					zeroRemoved = 0
				}
				tokens = tokens[0:remainingTokens]
				if reachedVocab {
					debugStr = ` reached_vocab`
				} else if withinVocabX2 {
					debugStr = ` within_vocab_x2`
				} else if reachedMidway {
					debugStr = ` reached_midway`
				} else {
					debugStr = ``
				}
				if best != MAXINT {
					debugStr += ` Best: ` + formatInt(best)
				}
				if noNewBest > 0 {
					debugStr += `; Tries:` + formatInt(noNewBest)
				}
				log.Println(`Deleted`, formatInt(removed), `of`, formatInt(tokensToRemove.Len()), `tokens; Remaining`, formatInt(remainingTokens + vocabDiff), `tokens;`, debugStr)
				if remainingTokens <= midwayTarget && !reachedMidway {
					saveTokensToFile(resultsDir + `midwaypoint_` + conv.String(remainingTokens + vocabDiff) + `.tok`, tokens, specialTokens, singleChars, nil, len(filedata))
					log.Println(`Reached midway target`)
					reachedMidway = true
				}
				if remainingTokens <= vocabSize * 2 && !withinVocabX2  {
					saveTokensToFile(resultsDir + `doublevocab_` + conv.String(remainingTokens + vocabDiff) + `.tok`, tokens, specialTokens, singleChars, nil, len(filedata))
					log.Println(`Reached 2x vocab size`)
					withinVocabX2 = true
				}
				justReset = false
				if remainingTokens < vocabSizeEffective || shuffles == 10000 { // its okay to do this multiple times
					log.Println(`Reached vocab size`)
					// Now make the the final tokens, from all the tokens that are present in all tokensets that are within 1% of the best score
					if reachedVocab { // second time
						uniqueTokens := new(pansearch.Counter)
						_, _, _, _, _, toks, err := loadTokensFromFile(finalRunFilename)
						if err != nil {
							panic(err)
						}
						if hasSpecial {
							for _, b := range toks {
								if len(b) > 1 {
									if _, exists = specialMap[string(b)]; !exists {
										uniqueTokens.Add(b, 1)
									}
								}
							}
						} else {
							for _, b := range toks {
								if len(b) > 1 {
									uniqueTokens.Add(b, 1)
								}
							}
						}
						uniqueTokens.Build()
						tokens = uniqueTokens.Keys()
					} else { // first time
						uniqueTokens := new(pansearch.Counter)
						for _, v := range dictsWithin1percent {
							if v.tokens < best1percent {
								_, _, _, _, _, toks, err := loadTokensFromFile(v.filename)
								if err != nil {
									panic(err)
								}
								if hasSpecial {
									for _, b := range toks {
										if len(b) > 1 {
											if _, exists = specialMap[string(b)]; !exists {
												uniqueTokens.Add(b, 1)
											}
										}
									}
								} else {
									for _, b := range toks {
										if len(b) > 1 {
											uniqueTokens.Add(b, 1)
										}
									}
								}
							}
						}
						uniqueTokens.Build()
						tokens = uniqueTokens.Keys() // this is all the tokens that are present in those within 1% of the best score
						noNewBest = 0
						finalRunFilename = resultsDir + `finalrun_` + conv.String(len(tokens) + vocabDiff) + `.tok`
						saveTokensToFile(finalRunFilename, tokens, specialTokens, singleChars, nil, len(filedata))
						reachedVocab = true
						atomic.StoreInt64(&remainingTokens_atomic, 0)
					}
					log.Println(`Determining best combination of`, formatInt(len(tokens) + vocabDiff), `tokens`)
					justReset = true
				}
				remainingTokens = len(tokens)
				if !reachedVocab {
					atomic.StoreInt64(&remainingTokens_atomic, int64(remainingTokens + vocabDiff))
				}
				tokensToRemove = new(pansearch.Counter) // empty tokensToRemove for next round
				hasTokensToRemove = false
				// Save the tokens every 10 steps, useful for stopping and starting
				if !reachedVocab && remainingTokens > vocabSizeEffective + (vocabSizeEffective / 50) {
					if interval10++; interval10 == 10 {
						if len(lastIntervalFileName) > 0 { // delete the last interval file
							os.Remove(lastIntervalFileName)
						}
						lastIntervalFileName = resultsDir + `interval_` + conv.String(remainingTokens + vocabDiff) + `.tok`
						saveTokensToFile(lastIntervalFileName, tokens, specialTokens, singleChars, nil, len(filedata)) // save interval file
						interval10 = 0
					}
				}
			}
			// Shuffle the dictionary and send it out to the workers
			shuffles = 0
			for atLeast1UniqueVocab := false; !atLeast1UniqueVocab; { // keep trying until at least 1 vocabulary is generated
				if shuffles == 10000 { // stuck in a loop because all vocabs have been tried already
					if justReset { // every possibility has been tried
						log.Println(`-- FINISHED --`)
						fmt.Println(`All near vocabularies have been tested`)
						fmt.Println(`Best result tokenized`, formatInt(len(filedata)), `bytes in`, formatInt(best), `tokens`)
						fmt.Println(`Average`, string(conv.FloatBytes(float64(len(filedata)) / float64(best), 3)), `characters/token`)
						fmt.Println(`Best result:`)
						for _, v := range dictsWithin1percent {
							if v.tokens > best1percent {
								os.Remove(v.filename) // delete everything not in the top 1%
							} else {
								if v.tokens == best {
									fmt.Println(` `, v.filename) // output the filesnames of all those that are the best, which may be more than 1
								}
							}
						}
						os.Exit(0)
					}
					hasTokensToRemove = true
					break
				}
				shuffle(tokens)
				shuffles++
				i = 0
				for i2 = 0; i2 < 10; i2++ { // I let it make up to 10 dictionaries out of 1 shuffle because shuffles are expensive
					to = i + vocabSizeEffective
					if to > len(tokens) {
						break
					}
					testVocab := new(pansearch.Fast)
					// Add single character tokens to every vocabulary
					for _, v := range singleChars {
						testVocab.Add(v)
					}
					// Add regular tokens
					for ; i<to; i++ {
						testVocab.Add(tokens[i])
					}
					// Add special tokens
					for _, v := range specialTokens {
						testVocab.Add(v)
					}
					testVocab.Build()
					// If withinVocabX2, make FNV-1a 64-bit hash out of the vocabulary and use this to determine whether its unique
					exists = false
					if withinVocabX2 {
						if testVocab.Reset() {
							// Calculate the [modified] FNV-1a hash value of this vocabulary
							// Don't want to test the same vocabulary twice
							hash = 14695981039346656037
							for eof := false; !eof; {
								key, eof = testVocab.Next()
								for _, c = range key {
									hash = (hash ^ (uint64(c) + 11)) * 1099511628211
								}
								hash = (hash ^ 11400714819323198485) * 1099511628211 // end of string hash
							}
							if _, exists = vocabsTried[hash]; !exists {
								vocabsTried[hash] = true
							}
						}
					}
					if !exists { // if not already seen
						channelWork <- testVocab // send the dictionary to the worker channel
						atLeast1UniqueVocab = true
					}
				}
			}
			break
		}
	}
}
