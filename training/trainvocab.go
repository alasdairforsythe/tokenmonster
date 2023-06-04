package main

import (
	"os"
	"log"
	"fmt"
	"time"
	"flag"
	"math"
	"bytes"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"reflect"
	"runtime"
	"math/rand"
	"io/ioutil"
	"sync/atomic"
	"unicode/utf8"
	"unicode/utf16"
	"path/filepath"
	"encoding/binary"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	uni "golang.org/x/text/encoding/unicode"
	"github.com/AlasdairF/Conv"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Sort/IntInt"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/capcode/go"
)

const (
	minHighSurrogate = 0xD800 // Start of high surrogate range
	maxHighSurrogate = 0xDBFF // End of high surrogate range
	minLowSurrogate  = 0xDC00 // Start of low surrogate range
	maxLowSurrogate  = 0xDFFF // End of low surrogate range
	runeError = '\uFFFD'
)

var (
	vocabSize int // common: 30000, 30522, 32000, 50265, 65535
	maxTokenLength int // 30
	workers int = runtime.GOMAXPROCS(0) - 1
	strips int = 100
	percentage int = 15
	midwayTarget int = 500000
	datasetFilename string
	dictionaryFilename string
	resultsDir string
	keepTrying int = 1000
	reserve256bytes bool = true
	noReserve256bytes bool = false
	usingCapcode bool = false
	charset string
	charsetFlag uint8 = 0

	ungreedyCapcode =	   []rune{'B', 'E', 'W', 'C', 'T'}
	ungreedyHighPriority = []rune{'‘', '“', '"', '`', '(', '[', ' ', '_', '/', '@', '\r', '\n', '\t', '\f', '\v', '\x00', '\x01', '\x02', '\x03', '\x04'}
	ungreedyMidPriority  = []rune{'-', ':', '{', ';', '#', '$', '~', '.', '}', '*', '&', '>', '<', '+', '='}
	ungreedyLowPriority  = []rune{'!', '%', '^', '?', '|', ',', '\\', ']', ')', '\'', '’', '”'}
	ungreedySuffixes     = []string{"'s", "'re", "'ll", "'t", "’s", "’re", "’ll", "’t"}
	ungreedySuffixesB [][]byte
	ungreedyLookupTable [256]uint8

	remainingTokens_atomic int64
)

type resultStruct struct {
	testVocab *pansearch.KeyBytes
	tokensInText int
	tokensToRemove [][]byte
}

type bestStruct struct {
    tokens    int
    filename  string
}

type sacrificeStruct struct {
	index	int		// the index of the token I'm willing to sacrifice because I'm not greedy
	length	int		// that token is this many bytes long (0 = no sacrifice)
	// The following refer to the parent, not the child referenced by index
	begin	bool	// does it begin with a letter?
	end		bool	// does it end with a letter?
}

// Channels that holds the various random dictionaries
var channelWork = make(chan *pansearch.KeyBytes, 2)
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

func formatInt(v int) string {
	return string(conv.FormatThousands(conv.Bytes(v), ','))
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

func norm_UTF8_NFD(input []byte) ([]byte, error) {
	normalized := bytes.NewBuffer(make([]byte, 0, len(input) + (len(input) / 3) + 4))
	normalizer := norm.NFD.Writer(normalized)
	_, err := normalizer.Write(input)
	if err != nil {
		return nil, err
	}
	err = normalizer.Close()
	if err != nil {
		return nil, err
	}
	return normalized.Bytes(), nil
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

func save_tokens(filename string, data [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	defer w.Close()
	w.WriteUint64(uint64(len(data)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	return nil
}

func load_saved(filename string) ([][]byte, error) {
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
	// Make sure we're at the end
	if r.EOF() != nil {
		return nil, errors.New(filename + ` not valid.`)
	}
	return data, nil
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func worker(id int, datastrips [][]byte, filedata []byte) {
	var i, i2, i3, index, index2, index3, length, length2, length3, branch1, branch2, divider, remainingTokens, tokensInText, maxlen, missing int
	var run int = 1
	var exists, reachedMidway, found, found2, found3 bool
	var data []byte
	var sacrifice sacrificeStruct
	scores := make([]sortIntInt.KeyVal, vocabSize)

	for testVocab := range channelWork {
		log.Println(`Worker`, id, `starting run`, run)

		// Reset vars this round's total and scores
		tokensInText = 0
		missing = 0
		for i, _ = range scores { // Reset scores to index & zero
			scores[i] = sortIntInt.KeyVal{i, 0}
		}

		// Finish building the testVocab
		maxlen = testVocab.LongestLength() // the longest token length in this testVocab

		// Sanity check, this should never happen
		if testVocab.Len() != vocabSize {
			panic(errors.New(`testVocab contains ` + conv.String(testVocab.Len()) + ` not the target ` + conv.String(vocabSize)))
		}

		// Loop through all tokens in the testVocab and try to find other tokens that have the same beginning, these are potential ungreedy alternatives
		sacrificeTo := make([]sacrificeStruct, vocabSize)
		if testVocab.Reset() {
			var key []byte
			var on, preferred, hasSuffix int
			var r rune
			var boundary bool
			for eof := false; !eof; {
				key, eof = testVocab.Next()
				sacrifice = sacrificeStruct{0, 0, false, false}
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
		
		// If midwayTarget has been reached, check the full dataset
		if !reachedMidway {
			remainingTokens = int(atomic.LoadInt64(&remainingTokens_atomic))
			if remainingTokens <= midwayTarget {
				datastrips[0] = filedata // replace the datastrips with the whole dataset
				datastrips = datastrips[0:1]
				reachedMidway = true
			}
		}

		// This is the main tokenization loop
		if reserve256bytes { // we've added all single-character bytes, which means it's impossible to not have a match
			for _, data = range datastrips {
				i = 0
				for i < len(data) {
					for length = min(len(data) - i, maxlen); length > 0; length-- {
						if index, exists = testVocab.Find(data[i:i+length]); exists {
							checkpoint:
								sacrifice = sacrificeTo[index]
								i2 = i + length
								if sacrifice.length != 0 && i2 < len(data) { // if there is a potential alternative token do a lookahead
									// First lookahead to the next token after me
									for length2 = min(len(data) - i2, maxlen); length2 > 0; length2-- {
										if index2, exists = testVocab.Find(data[i2:i2+length2]); exists {
											break
										}
									}
									// Now check the potential token that would be next if sacrificed
									i3 = i + sacrifice.length // the length of the token sacrificed to
									for length3 = min(len(data) - i3, maxlen); length3 > 0; length3-- {
										if index3, exists = testVocab.Find(data[i3:i3+length3]); exists {
											break
										}
									}
									// Now we have the next token looking ahead from both me and the sacrified to token, which one is longer?
									branch1 = length + length2
									branch2 = sacrifice.length + length3
									if branch1 > branch2 || (branch1 == branch2 && sacrifice.end != sacrificeTo[index2].begin) { // if they're equal check whether it begins with an ungreedy preference, if so prefer that one, if not then prefer the original
										// Go with original token
										scores[index].V += length // this token saved this many characters (its length)
										i += length
										tokensInText++
										// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
										length = length2
										index = index2
										goto checkpoint
									} else {
										// Sacrifice and go with alternative
										scores[sacrifice.index].V += sacrifice.length // this token saved this many characters (its length)
										i += sacrifice.length
										tokensInText++
										// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
										length = length3
										index = index3
										goto checkpoint
									}
								}
							// there is no alternative "sacrifice" option for this token
							scores[index].V += length // this token saved this many characters (its length)
							i += length
							tokensInText++
							break
						}
					}
				}
			}
		} else { // without reserve256bytes, it's possible to not match a token, which means I have to check for that
			for _, data = range datastrips {
				i = 0
				for i < len(data) {
					found = false
					for length = min(len(data) - i, maxlen); length > 0; length-- {
						if index, exists = testVocab.Find(data[i:i+length]); exists {
							checkpoint2:
								sacrifice = sacrificeTo[index]
								i2 = i + length
								if sacrifice.length != 0 && i2 < len(data) { // if there is a potential alternative token do a lookahead
									found2 = false
									found3 = false
									// First lookahead to the next token after me
									for length2 = min(len(data) - i2, maxlen); length2 > 0; length2-- {
										if index2, exists = testVocab.Find(data[i2:i2+length2]); exists {
											found2 = true
											break
										}
									}
									// Now check the potential token that would be next if sacrificed
									i3 = i + sacrifice.length // the length of the token sacrificed to
									for length3 = min(len(data) - i3, maxlen); length3 > 0; length3-- {
										if index3, exists = testVocab.Find(data[i3:i3+length3]); exists {
											found3 = true
											break
										}
									}
									// Now we have the next token looking ahead from both me and the sacrified to token, which one is longer?
									branch1 = length + length2
									branch2 = sacrifice.length + length3
									if (branch1 > branch2 || (branch1 == branch2 && sacrifice.end != sacrificeTo[index2].begin)) && found2 { // if they're equal check whether it begins with an ungreedy preference, if so prefer that one, if not then prefer the original
										// Go with original token
										scores[index].V += length // this token saved this many characters (its length)
										i += length
										tokensInText++
										found = true
										// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
										length = length2
										index = index2
										goto checkpoint2
									} else if found3 {
										// Sacrifice and go with alternative
										scores[sacrifice.index].V += sacrifice.length // this token saved this many characters (its length)
										i += sacrifice.length
										tokensInText++
										found = true
										// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
										length = length3
										index = index3
										goto checkpoint2
									}
								}
							// there is no alternative "sacrifice" option for this token
							scores[index].V += length // this token saved this many characters (its length)
							i += length
							tokensInText++
							found = true
							break
						}
					}
					if !found {
						missing++
						i++
					}
				}
			}
		}

		// What to do if the tokens didn't cover all of the characters?
		// We're just going to act like normal but make the score so bad that this vocabulary will never be chosen
		if missing != 0 {
			tokensInText *= 100
		}

		// Determine tokens to delete
		remainingTokens = int(atomic.LoadInt64(&remainingTokens_atomic))
		keys := testVocab.Keys()
		sortIntInt.Asc(scores) // sort all the tokens by the number of characters they saved (their length * occurences)
		switch {
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
		length = vocabSize / divider
		tokensToRemove := make([][]byte, length)
		index = 0
		for i=0; i<length; i++ {
			if reserve256bytes && len(keys[scores[i].K]) == 1 { // this is a 1 byte token
				length++
				continue
			}
			tokensToRemove[index] = keys[scores[i].K]
			index++
		}
		// Now check if these are still at 0 and if so includes all zeros
		if scores[i].V == 0 {
			for ; i<vocabSize; i++ {
				if scores[i].V > 0 {
					break
				}
				if reserve256bytes && len(keys[scores[i].K]) == 1 { // this is a 1 byte token
					continue
				}
				tokensToRemove = append(tokensToRemove, keys[scores[i].K])
			}
		}
		// Return the result back to the master thread
		channelResult <- resultStruct{testVocab, tokensInText, tokensToRemove}
		log.Println(`Worker`, id, `completed run`, run, ` Tokens:`, formatInt(tokensInText))
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

func main() {

	flag.IntVar(&maxTokenLength, "max-token-length", maxTokenLength, "the maximum length of a token (required)")
	flag.IntVar(&vocabSize, "vocab", vocabSize, "vocabulary size, e.g. 65535 (required)")
	flag.StringVar(&datasetFilename, "dataset", datasetFilename, "filename of the dataset plain-text (required)")
	flag.StringVar(&dictionaryFilename, "dictionary", dictionaryFilename, "filename of the dictionary generated by makedictionary or any of the saved output files from this app (required)")
	flag.StringVar(&resultsDir, "dir", resultsDir, "The directory to save the results within (required)")
	flag.IntVar(&workers, "workers", workers, "number of worker threads to run, excluding main thread")
	flag.IntVar(&strips, "strips", strips, "number of strips to distribute to the workers")
	flag.IntVar(&percentage, "percentage", percentage, "percentage of the dataset given to each worker before midway")
	flag.IntVar(&midwayTarget, "midway-target", midwayTarget, "aggressive until this point, beneath this the full dataset is used for every worker")
	flag.IntVar(&keepTrying, "keep-trying", keepTrying, "program will exit when unable to find a better match this many times in a row")
	flag.BoolVar(&noReserve256bytes, "no-reserve-256", noReserve256bytes, "disable default behavior of including 256 tokens representing every single byte (default false)")
	flag.BoolVar(&usingCapcode, "capcode", usingCapcode, "expect capcode encoding, which modifies ungreedy behavior (default false)")
	flag.StringVar(&charset, "charset", charset, "One of: UTF-8, binary (required)")
	flag.Parse()
    flagRequired("max-token-length", maxTokenLength)
    flagRequired("vocab", vocabSize)
    flagRequired("dataset", datasetFilename)
    flagRequired("dictionary", dictionaryFilename)
    flagRequired("dir", resultsDir)
	flagRequired("charset", charset)

	if noReserve256bytes {
		reserve256bytes = false
	}

	switch strings.ToLower(charset) {
		case "utf8":
			fallthrough
		case "utf-8":
			charsetFlag = 1
			if usingCapcode {
				fmt.Println(`Charset: UTF-8, capcode enabled`)
			} else {
				fmt.Println(`Charset: UTF-8, capcode disabled`)
			}
		case "utf16":
			fallthrough
		case "utf-16":
			charsetFlag = 2
			if usingCapcode {
				fmt.Fprintf(os.Stderr, "capcode is currently only supported with UTF-8 encoding")
				flag.Usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: UTF-16, capcode disabled`)
		case "none":
			fallthrough
		case "binary":
			charsetFlag = 0
			if usingCapcode {
				fmt.Fprintf(os.Stderr, "capcode is currently only supported with UTF-8 encoding")
				flag.Usage()
				os.Exit(1)
			}
			fmt.Println(`Charset: none, binary mode enabled`)
		default:
			fmt.Fprintf(os.Stderr, "-charset must be one of: UTF-8, binary")
			flag.Usage()
			os.Exit(1)
	}

	// Trim trailing slashes from resultsDir and create it if it does not exist
	for len(resultsDir) > 0 && os.IsPathSeparator(resultsDir[len(resultsDir)-1]) {
		resultsDir = resultsDir[:len(resultsDir)-1]
	}
	if _, err := os.Stat(resultsDir); os.IsNotExist(err) {
		os.MkdirAll(resultsDir, 0755)
	}
	resultsDir = resultsDir + string(filepath.Separator)

	// Vars
	rand.Seed(time.Now().UnixNano())
	var i, i2, to, remainingTokens, best1percent, uniqueFileNumber, noNewBest, interval10, removed, shuffles, zeroRemoved int
	var exists, hasTokensToRemove, reachedMidway, withinVocabX2, reachedVocab, atLeast1UniqueVocab bool
	var lastIntervalFileName string
	var key []byte
	var hash uint64
	var c byte
	var err error
	tokensToRemove := new(pansearch.CounterBytes)
	dictsWithin1percent := make([]bestStruct, 0, 100)
	best := math.MaxInt64
	var vocabSizeEffective = vocabSize
	if reserve256bytes {
		vocabSizeEffective -= 256
	}
	
	// Build the ungreedy preference lookup table
	// If there are multiple options of ungreedy sacrifice, these are the preferred points
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

	// Load the text & normalize UTF8
	var filedata []byte
	{
		var temp []byte
		temp, err = ioutil.ReadFile(datasetFilename)
		if err != nil {
			panic(err)
		}
		switch charsetFlag {
			case 0: // binary
				filedata = temp
			case 1: // utf-8
				if usingCapcode {
					temp = capcode.Encode(temp)
				}
				filedata, err = norm_UTF8_NFD(temp)
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

	// Distribute the text randomly but evenly to each worker has x strips each from a different part of filedata
	bytesPerWorker := (len(filedata) * percentage) / 100
	bytesPerStrip := bytesPerWorker / strips
	bytesPerStrip += 4 - (bytesPerStrip % 4) // ensure it's divisible by 4 to avoid splitting glyphs
	offset := len(filedata) / strips
	data := make([][][]byte, workers)
	if offset + bytesPerStrip > len(filedata) || percentage >= 100 {
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
				if from + bytesPerStrip > len(filedata) {
					from = (from + bytesPerStrip) - len(filedata)
				}
				data[i][i2] = filedata[from:from+bytesPerStrip]
				from += offset
			}
		}
	}

	// Load the big dictionary of all the tokens from the dataset
	var tokens [][]byte
	tokens, err = load_saved(dictionaryFilename)
	if err != nil {
		panic(err)
	}

	// This section resumes the final run given one of the final run files, it's only here because I needed to do that when testing
	// Usually you would redo the final run from the finalrun file but you can use this to make it continue checking from the be
	if len(tokens) == vocabSize {
		if nscore, is := detectSavedFinal(dictionaryFilename); is {
			best = int(nscore)
			nscore += nscore / 100
			best1percent = int(nscore)
			reachedMidway = true
			withinVocabX2 = true
			reachedVocab = true
			// Recreate dictsWithin1percent from the files in the directory
			uniqueTokens := new(pansearch.CounterBytes)
			for _, b := range tokens {
				if (len(b) > 1) {
					uniqueTokens.Add(b, 1)
				}
			}
			dir := filepath.Dir(dictionaryFilename)
			files, err := ioutil.ReadDir(dir)
			if err != nil {
				panic(err)
			}
			for _, file := range files {
				fpath := filepath.Join(dir, file.Name())
				if nscore2, is := detectSavedFinal(file.Name()); is && nscore2 <= nscore && nscore2 > 0 {
					dictsWithin1percent = append(dictsWithin1percent, bestStruct{int(nscore2), fpath})
					toks, err := load_saved(fpath)
					if err != nil {
						continue
					}
					for _, b := range toks {
						if (len(b) > 1) {
							uniqueTokens.Add(b, 1)
						}
					}
				}
			}
			uniqueTokens.Build()
			tokens = uniqueTokens.Keys() // this is all the tokens that are present in those within 1% of the best score
			log.Println(`Resuming final run from score`, best)
		}
	}
	
	// How many tokens are there?
	remainingTokens = len(tokens)
	remainingTokens_atomic = int64(remainingTokens) // still single-threaded here
	vocabsTried := make(map[uint64]bool)

	// Launch the worker threads
	for i=0; i<workers; i++ {
		go worker(i, data[i], filedata)
	}

	// Master loop
	for {
		select {
		case result, ok := <- channelResult: // this channel delivers the results
			if !ok { // channel is closed
				break
			}

			// Save all dictionaries within 10% of the best performing one
			if withinVocabX2 { // if we're within 2x the vocabSize
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
					filename := resultsDir + conv.String(result.tokensInText) + "_" + conv.String(uniqueFileNumber) + ".zlib"
					uniqueFileNumber++
					err = save_tokens(filename, result.testVocab.Keys())
					dictsWithin1percent = append(dictsWithin1percent, bestStruct{result.tokensInText, filename})
				}
			}

			if reachedVocab {
				if noNewBest >= keepTrying {
					log.Println(`-- Exiting --`)
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
					i2 = min(i2 + zeroRemoved, len(result.tokensToRemove))
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
					zeroRemoved++
				} else {
					zeroRemoved = 0
				}
				tokens = tokens[0:remainingTokens]
				atomic.StoreInt64(&remainingTokens_atomic, int64(remainingTokens))
				debugStr := ``
				if reachedMidway {
					debugStr += ` reachedMidway`
				}
				if withinVocabX2 {
					debugStr += ` withinVocabX2`
				}
				if reachedVocab {
					debugStr += ` reachedVocab`
				}
				if noNewBest > 0 {
					debugStr += ` noNewBest ` + formatInt(noNewBest)
				}
				if best < math.MaxInt64 {
					debugStr += ` best ` + formatInt(best)
				}
				log.Println(`Deleted`, formatInt(removed), `of`, formatInt(tokensToRemove.Len()), `tokens; Remaining`, formatInt(remainingTokens), `tokens;`, debugStr)
				if remainingTokens <= midwayTarget && !reachedMidway {
					save_tokens(resultsDir + `midwaypoint_` + conv.String(remainingTokens) + `.zlib`, tokens)
					log.Println(`Reached midwayTarget`)
					reachedMidway = true
				}
				if remainingTokens <= vocabSize * 2 && !withinVocabX2  {
					save_tokens(resultsDir + `doublevocab_` + conv.String(remainingTokens) + `.zlib`, tokens)
					log.Println(`Reached 2x vocabSize`)
					withinVocabX2 = true
				}
				if remainingTokens < vocabSizeEffective || shuffles == 10000 { // its okay to do this multiple times
					log.Println(`Reached vocabSize`)
					// Now make the the final tokens, from all the tokens that are present in all tokensets that are within 1% of the best score
					uniqueTokens := new(pansearch.CounterBytes)
					targetPercentage := best1percent
					if reachedVocab { // this is the 2nd time, target 0.5% within best score instead of 1%
						targetPercentage = best + (best / 200)
					} else { // reset noNewBest the first time
						noNewBest = 0
					}
					for _, v := range dictsWithin1percent {
						if v.tokens < targetPercentage {
							toks, err := load_saved(v.filename)
							if err != nil {
								panic(err)
							}
							for _, b := range toks {
								if (len(b) > 1 || !reserve256bytes) {
									uniqueTokens.Add(b, 1)
								}
							}
						}
					}
					uniqueTokens.Build()
					tokens = uniqueTokens.Keys() // this is all the tokens that are present in those within 10% of the best score
					remainingTokens = len(tokens)
					if !reachedVocab { // only first time
						save_tokens(resultsDir + `finalrun_` + conv.String(remainingTokens) + `.zlib`, tokens)
					}
					reachedVocab = true
					log.Println(`Determining best combination of`, formatInt(len(tokens)), `tokens`)
				}
				tokensToRemove = new(pansearch.CounterBytes) // empty tokensToRemove for next round
				hasTokensToRemove = false
				// Save the tokens every 10, useful for stopping and starting
				if interval10++; interval10 == 10 {
					if len(lastIntervalFileName) > 0 { // delete the last interval file
						os.Remove(lastIntervalFileName)
					}
					lastIntervalFileName = resultsDir + `interval_` + conv.String(remainingTokens) + `.zlib`
					save_tokens(lastIntervalFileName, tokens) // save interval file
					interval10 = 0
				}
			}
			// Shuffle the dictionary and send it out to the workers
			shuffles = 0
			for atLeast1UniqueVocab = false; !atLeast1UniqueVocab; { // keep trying until at least 1 vocabulary is generated
				if shuffles == 10000 { // stuck in a loop because all vocabs have been tried already
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
					testVocab := new(pansearch.KeyBytes)
					// Add single "reserved" bytes
					if reserve256bytes {
						for i3:=0; i3<256; i3++ {
							testVocab.AddUnsorted([]byte{byte(i3)})
						}
					}
					for ; i<to; i++ {
						testVocab.AddUnsorted(tokens[i])
					}
					testVocab.Build()
					// If withinVocabX2, make FNV-1a 64-bit hash out of the vocabulary and use this to determine whether its unique
					exists = false
					if withinVocabX2 {
						if testVocab.Reset() {
							// Calculate the FNV-1a hash value of this vocabulary
							hash = 14695981039346656037
							for eof := false; !eof; {
								key, eof = testVocab.Next()
								if len(key) > 1 || !reserve256bytes {
									for _, c = range key {
										hash = (hash ^ uint64(c)) * 1099511628211
									}
								}
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
