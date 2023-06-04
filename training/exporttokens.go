package main

import (
	"os"
	"fmt"
	"errors"
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
	"unicode/utf16"
	"encoding/binary"
	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/transform"
	uni "golang.org/x/text/encoding/unicode"
	"github.com/AlasdairF/Custom"
	"github.com/alasdairforsythe/pansearch"
)

const (
	noSacrifice = 16777215
	minHighSurrogate = 0xD800 // Start of high surrogate range
	maxHighSurrogate = 0xDBFF // End of high surrogate range
	minLowSurrogate  = 0xDC00 // Start of low surrogate range
	maxLowSurrogate  = 0xDFFF // End of low surrogate range
	runeError = '\uFFFD'
)

var (
	ungreedyCapcode =	   []rune{'B', 'E', 'W', 'C', 'T'}
	ungreedyHighPriority = []rune{'‘', '“', '"', '`', '(', '[', ' ', '_', '/', '@', '\r', '\n', '\t', '\x00', '\x01', '\x02', '\x03', '\x04'}
	ungreedyMidPriority  = []rune{'-', ':', '{', ';', '#', '$', '~', '.', '}', '*', '&', '>', '<', '+', '='}
	ungreedyLowPriority  = []rune{'!', '%', '^', '?', '|', ',', '\\', ']', ')', '\'', '’', '”'}
	ungreedySuffixes     = []string{"'s", "'re", "'ll", "'t", "’s", "’re", "’ll", "’t"}
	ungreedySuffixesB [][]byte
	ungreedyLookupTable [256]uint8
	charsetFlag uint8
)

type sacrificeStruct struct {
	index	int		// the index of the token I'm willing to sacrifice because I'm not greedy (16777215 = no sacrifice)
	length	int		// that token is this many bytes long (0 = no sacrifice)
	// The following refer to the parent, not the child referenced by index
	begin	bool	// does it begin with a letter?
	end		bool	// does it end with a letter?
}

func hasSuffixPos(key []byte) int {
	if charsetFlag == 0 {
		return -1
	}
	for _, suffix := range ungreedySuffixesB {
		if bytes.HasSuffix(key, suffix) {
			if len(suffix) < len(key) {
				r := decodeLastRune(key[:len(key)-len(suffix)])
				if unicode.IsLetter(r) {
					return len(key) - len(suffix)
				}
			}
		}
	}
	return -1
}

func decodeRune(b []byte) rune {
	switch charsetFlag {
		case 1: // UTF-8
			r, _ := utf8.DecodeRune(b)
			return r
		case 2: // UTF-16
			if len(b) < 2 {
				return runeError
			}
			u := binary.LittleEndian.Uint16(b)
			if u >= minHighSurrogate && u <= maxHighSurrogate {
				// This is a surrogate pair. We need another two bytes.
				if len(b) < 4 {
					return runeError
				}
				u2 := binary.LittleEndian.Uint16(b[2:])
				if u2 < minLowSurrogate || u2 > maxLowSurrogate {
					return runeError
				}
				r := utf16.Decode([]uint16{u, u2})
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
			return -1
	}
}

func lastByteUTF8(r rune) byte {
	utf8Bytes := []byte(string(r))
	return utf8Bytes[len(utf8Bytes)-1]
}

func lastByteUTF16(r rune) byte {
	// Check if the rune is within the BMP (Basic Multilingual Plane)
	if r <= 0xFFFF {
		return byte(r)
	}
	// Calculate the low surrogate pair
	lo := 0xDC00 + ((r - 0x10000) & 0x3FF)
	return byte(lo)
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

func saveAsTxt(filename string, data [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewWriter(fi)
	defer w.Close()
	for _, b := range data {
		w.Write(b)
		w.WriteByte('\n')
	}
	return nil
}

func loadTokensFromFile(filename string) ([][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	if r.EOF() != nil {
		return nil, errors.New(`Not a valid structure.`)
	}
	return data, nil
}

func usage() {
	fmt.Println(`Usage:  ./exporttokens tokensfilename outputfilename -capcode -charset UTF-8`)
	fmt.Println(`        -charset (required) must be one of: UTF-8, UTF-16, binary`)
	fmt.Println(`        -capcode (optional) flag must be used if capcode were used during training`)
	fmt.Println(`        -txt (optional) saves the tokens in a text file separated one per line`)
	fmt.Println(`Output: outputfilename.vocab : the vocabulary for tokenization`)
	fmt.Println(`        outputfilename.txt : the tokens in a text file for curiosity`)
	os.Exit(0)
}

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	var usingCapcode, saveTxt bool
	var inputFilename, outputFilename string
	charsetFlag = 255

	for i:=1; i<len(os.Args); i++ {
		v := os.Args[i]
		switch {
			case v == `-charset`:
				if i+1 == len(os.Args) {
					fmt.Println(`-charset must be followed by one of: binary, UTF-8, UTF-16`)
					usage()
				}
				i++
				switch strings.ToLower(os.Args[i]) {
					case "utf8":
						fallthrough
					case "utf-8":
						charsetFlag = 1
					case "utf16":
						fallthrough
					case "utf-16":
						charsetFlag = 2
					case "none":
						fallthrough
					case "binary":
						charsetFlag = 0
					default:
						fmt.Fprintf(os.Stderr, "-charset must be one of: UTF-8, binary")
						usage()
						os.Exit(1)
				}
			case v == `-capcode`:
				fallthrough
			case v == `--capcode`:
				usingCapcode = true
			case v == `-txt`:
				fallthrough
			case v == `--txt`:
				saveTxt = true
			case len(inputFilename) == 0:
				inputFilename = v
			case len(outputFilename) == 0:
				outputFilename = v
			default:
				usage()
				os.Exit(1)
		}
	}

	switch charsetFlag {
		case 0:
			if usingCapcode {
				fmt.Fprintf(os.Stderr, "capcode is currently only supported with UTF-8 encoding")
				usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: none, binary mode enabled`)
		case 1:
			if usingCapcode {
				fmt.Println(`Charset: UTF-8, capcode enabled`)
			} else {
				fmt.Println(`Charset: UTF-8, capcode disabled`)
			}
		case 2:
			if usingCapcode {
				fmt.Fprintf(os.Stderr, "capcode is currently only supported with UTF-8 encoding")
				usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: UTF-16, capcode disabled`)
		default:
			fmt.Fprintf(os.Stderr, "-charset is required")
			usage()
			os.Exit(1)
	}

	tokens, err := loadTokensFromFile(inputFilename)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(tokens), `tokens`)

	ungreedySuffixesB = make([][]byte, len(ungreedySuffixes))
	if charsetFlag == 1 {
		for _, r := range ungreedyLowPriority {
			ungreedyLookupTable[lastByteUTF8(r)] = 1
		}
		for _, r := range ungreedyMidPriority {
			ungreedyLookupTable[lastByteUTF8(r)] = 2
		}
		for _, r := range ungreedyHighPriority {
			ungreedyLookupTable[lastByteUTF8(r)] = 3
		}
		if usingCapcode {
			for _, r := range ungreedyCapcode {
				ungreedyLookupTable[lastByteUTF8(r)] = 3
			}
		}
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = []byte(suffix)
		}
	} else if charsetFlag == 2 {
		for _, r := range ungreedyLowPriority {
			ungreedyLookupTable[lastByteUTF16(r)] = 1
		}
		for _, r := range ungreedyMidPriority {
			ungreedyLookupTable[lastByteUTF16(r)] = 2
		}
		for _, r := range ungreedyHighPriority {
			ungreedyLookupTable[lastByteUTF16(r)] = 3
		}
		if usingCapcode {
			for _, r := range ungreedyCapcode {
				ungreedyLookupTable[lastByteUTF16(r)] = 3
			}
		}
		for i, suffix := range ungreedySuffixes {
			ungreedySuffixesB[i] = convertStringToUTF16WithNFDNormalization(suffix)
		}
	}

	testVocab := new(pansearch.KeyBytes)
	for _, k := range tokens {
		testVocab.AddUnsorted(k)
	}
	testVocab.Build()

	var sacrifice sacrificeStruct
	// Loop through all tokens in the testVocab and try to find other tokens that have the same beginning, these are potential ungreedy alternatives
		sacrificeTo := make([]sacrificeStruct, testVocab.Len())
		if testVocab.Reset() {
			var key, data []byte
			var on, preferred, hasSuffix, length, index int
			var r rune
			var boundary, exists bool
			for eof := false; !eof; {
				key, eof = testVocab.Next()
				sacrifice = sacrificeStruct{noSacrifice, 0, false, false}
				preferred = 0
				r = decodeRune(key)
				if unicode.IsLetter(r) {
					sacrifice.begin = true
				}
				r = decodeLastRune(key)
				if unicode.IsLetter(r) {
					sacrifice.end = true
				}
				hasSuffix = hasSuffixPos(key)
				outer:
				for length=len(key)-1; length>0; length-- { // loop through all possible subwords that would also fit beneath this one
					data = key[:length] // the subword
					if index, exists = testVocab.Find(data); exists { // is this subword in the testVocab?

						// Check first if this is a suffix
						if length == hasSuffix {
							sacrifice.index = index
							sacrifice.length = length
							break
						}

						// Check whether the next character is part of a word
						r = decodeRune(key[length:]) // it's no problem if this is not a full UTF-8 sequence, it'll just come out to false below
						if unicode.IsLetter(r) || unicode.IsNumber(r) { // if the next character is a letter or number
							boundary = true
						} else {
							boundary = false
						}

						/*
						Preference:
			                suffix					9
							boundary & ungreedy3	8
							boundary & ungreedy2	7
							boundary & ungreedy1	6
							!boundary & ungreedy0	5
							!boundary & ungreedy3	4
							!boundary & ungreedy2	3
							!boundary & ungreedy1	2
							boundary & ungreedy0	1
						*/

						switch ungreedyLookupTable[data[length-1]] { // is this a preferred ungreedy point (the last character is a space or something like that)
							case 0: // not even a priority
								if boundary {
									if preferred < 1 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 1
									}
								} else {
									if preferred < 5 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 5
									}
								}
							case 1: // low-priority
								if boundary {
									if preferred < 6 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 6
									}
								} else {
									if preferred < 2 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 2
									}
								}
							case 2: // mid-priority
								if boundary {
									if preferred < 7 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 7
									}
								} else {
									if preferred < 3 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 3
									}
								}
							case 3: // high-priority
								if boundary {
									if preferred < 8 {
										sacrifice.index = index
										sacrifice.length = length
										break outer // end here because we've found the longest preference
									}
								} else {
									if preferred < 4 {
										sacrifice.index = index
										sacrifice.length = length
										preferred = 4
									}
								}
						}
					}
				}
				// sacrifice now contains the index & length of the longest preferred subtoken of this token in the vocabulary
				sacrificeTo[on] = sacrifice
				on++
			}
		}


	// Save the tokens as a text file for viewing
	tokens = testVocab.Keys()
	if saveTxt {
		saveAsTxt(outputFilename + `.txt`, tokens)
		fmt.Println(`Exported:`, outputFilename + `.txt`)
	}

	// Create the vocabulary file
	if !strings.HasSuffix(outputFilename, `.vocab`) {
		outputFilename += `.vocab`
	}
	fi, err := os.Create(outputFilename)
	if err != nil {
		fmt.Println(`Error: ` + err.Error())
		os.Exit(1)
	}
	defer fi.Close()
	w := custom.NewWriter(fi)
	defer w.Close()
	var flag byte
	w.WriteBool(usingCapcode)
	w.WriteByte(charsetFlag)
	w.WriteUint24(uint32(len(tokens)))
	for i, b := range tokens {
		sacrifice = sacrificeTo[i]
		w.WriteBytes8(b) // a single byte (uint8) specifying length of token bytes, and then that many bytes
		// Encode 2 booleans in 1 byte
		flag = 0
		if sacrifice.begin {
			flag = 1
		}
		if sacrifice.end {
			flag += 2
		}
		w.WriteByte(flag)
		// Write the index of the sacrifice
		w.WriteUint24(uint32(sacrifice.index))
		// The index of the sacrifice should always be less than the current index (because the list is sorted), check this is true
		if sacrifice.index > i && sacrifice.index != noSacrifice {
			fmt.Println(`Error: Sanity check failed`)
		}
	}
	fmt.Println(`Exported:`, outputFilename)
}
