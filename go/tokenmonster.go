package tokenmonster

import (
	"os"
	"bytes"
	"errors"
	"io/ioutil"
	uni "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	"github.com/AlasdairF/Custom"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/capcode/go"
)

const noSacrifice = 16777215

type Vocab struct {
	dictionary *pansearch.KeyBytes
	info []sacrificeStruct
	reserve256bytes bool
	charset uint8
	usingCapcode bool
	maxlen int
}

type Decoder struct {
	vocab Vocab
	remainder []byte
	capcodeDecoder capcode.Decoder
}

type sacrificeStruct struct {
	index	int		// the index of the token I'm willing to sacrifice because I'm not greedy (16777215 = no sacrifice)
	length	int		// that token is this many bytes long (0 = no sacrifice)
	// The following refer to the parent, not the child referenced by index
	begin	bool	// does it begin with a letter?
	end		bool	// does it end with a letter?
	token	[]byte
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

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func grow(ar []int) []int {
	newar := make([]int, len(ar) + (len(ar)/2) + 2)
	copy(newar, ar)
	return newar
}

func (vocab Vocab) NewDecoder() *Decoder {
	return &Decoder{vocab:vocab}
}

func (d *Decoder) Detokenize(tokens []int) []byte {
	if d.vocab.charset == 0 {
		return d.vocab.DetokenizeBytes(tokens)
	}
	// Get the size
	info := d.vocab.info
	var i int = len(d.remainder)
	for _, v := range tokens {
		if v >= 0 && v < len(info) {
			i += len(info[v].token)
		}
	}
	// Make the exact size array
	data := make([]byte, i)
	// Copy the keys into it
	copy(data, d.remainder)
	i = len(d.remainder)
	for _, v := range tokens {
		if v >= 0 && v < len(info) {
			copy(data[i:], info[v].token)
			i += len(info[v].token)
		}
	}
	if d.vocab.charset == 1 { // UTF-8
		remaining := len(data) - capcode.IncompleteUTF8Bytes(data)
		d.remainder = data[remaining:]
		data = data[:remaining]
		if (d.vocab.usingCapcode) {
			data = d.capcodeDecoder.Decode(data)
		}
	} else { // UTF-16
		remaining := len(data) - capcode.IncompleteUTF16Bytes(data)
		d.remainder = data[remaining:]
		data = data[:remaining]
	}
	return data
}

func (vocab Vocab) DetokenizeBytes(tokens []int) []byte {
	// Get the size
	var i int
	for _, v := range tokens {
		if v >= 0 && v < len(vocab.info) {
			i += len(vocab.info[v].token)
		}
	}
	// Make the exact size array
	data := make([]byte, i)
	// Copy the keys into it
	i = 0
	for _, v := range tokens {
		if v >= 0 && v < len(vocab.info) {
			copy(data[i:], vocab.info[v].token)
			i += len(vocab.info[v].token)
		}
	}
	return data
}

func (vocab Vocab) Tokenize(data []byte) ([]int, int, error) {
	var err error
	switch vocab.charset {
		case 1: // UTF-8
			if vocab.usingCapcode {
				data = capcode.Encode(data)
			}
			data, err = norm_UTF8_NFD(data)
			if err != nil {
				return nil, 0, err
			}
		case 2: // UTF-16
			data, err = norm_UTF16_NFD(data)
			if err != nil {
				return nil, 0, err
			}
	}
	return vocab.TokenizeBytes(data)
}

func (vocab Vocab) TokenizeBytes(data []byte) ([]int, int, error) {
	var i, i2, i3, length, length2, length3, index, index2, index3, branch1, branch2, tokensInText, missing int
	var exists bool
	tokens := make([]int, (len(data) / 4) + 4)
	maxlen := vocab.maxlen
	var sacrifice sacrificeStruct
	// This is the main tokenization loop
	if vocab.reserve256bytes { // all single byte characters exist in the file, which means it's impossible to not have a match
			for i < len(data) {
				for length = min(len(data) - i, maxlen); length > 0; length-- {
					if index, exists = vocab.dictionary.Find(data[i:i+length]); exists {
						checkpoint:
							sacrifice = vocab.info[index]
							i2 = i + length
							if sacrifice.length != 0 && i2 < len(data) { // if there is a potential alternative token do a lookahead
								// First lookahead to the next token after me
								for length2 = min(len(data) - i2, maxlen); length2 > 0; length2-- {
									if index2, exists = vocab.dictionary.Find(data[i2:i2+length2]); exists {
										break
									}
								}
								// Now check the potential token that would be next if sacrificed
								i3 = i + sacrifice.length // the length of the token sacrificed to
								for length3 = min(len(data) - i3, maxlen); length3 > 0; length3-- {
									if index3, exists = vocab.dictionary.Find(data[i3:i3+length3]); exists {
										break
									}
								}
								// Now we have the next token looking ahead from both me and the sacrified to token, which one is longer?
								branch1 = length + length2
								branch2 = sacrifice.length + length3
								if branch1 > branch2 || (branch1 == branch2 && sacrifice.end != vocab.info[index2].begin) { // if they're equal check whether it begins with an ungreedy preference, if so prefer that one, if not then prefer the original
									// Go with original token
									i += length
									if tokensInText == len(tokens) {
										tokens = grow(tokens)
									}
									tokens[tokensInText] = index
									tokensInText++
									// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
									length = length2
									index = index2
									goto checkpoint
								} else {
									// Sacrifice and go with alternative
									i += sacrifice.length
									if tokensInText == len(tokens) {
										tokens = grow(tokens)
									}
									tokens[tokensInText] = sacrifice.index
									tokensInText++
									// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
									length = length3
									index = index3
									goto checkpoint
								}
							}
							// there is no alternative "sacrifice" option for this token
							i += length
							if tokensInText == len(tokens) {
								tokens = grow(tokens)
							}
							tokens[tokensInText] = index
							tokensInText++
							break
					}
				}
			}
	} else { // without reserve256bytes, it's possible to not match a token, which means I have to check for that
			var found, found2, found3 bool
			for i < len(data) {
				found = false
				for length = min(len(data) - i, maxlen); length > 0; length-- {
					if index, exists = vocab.dictionary.Find(data[i:i+length]); exists {
						found = true
						checkpoint2:
							sacrifice = vocab.info[index]
							i2 = i + length
							if sacrifice.length != 0 && i2 < len(data) { // if there is a potential alternative token do a lookahead
								found2 = false
								found3 = false
								// First lookahead to the next token after me
								for length2 = min(len(data) - i2, maxlen); length2 > 0; length2-- {
									if index2, exists = vocab.dictionary.Find(data[i2:i2+length2]); exists {
										found2 = true
										break
									}
								}
								// Now check the potential token that would be next if sacrificed
								i3 = i + sacrifice.length // the length of the token sacrificed to
								for length3 = min(len(data) - i3, maxlen); length3 > 0; length3-- {
									if index3, exists = vocab.dictionary.Find(data[i3:i3+length3]); exists {
										found3 = true
										break
									}
								}
								// Now we have the next token looking ahead from both me and the sacrified to token, which one is longer?
								branch1 = length + length2
								branch2 = sacrifice.length + length3
								if (branch1 > branch2 || (branch1 == branch2 && sacrifice.end != vocab.info[index2].begin)) && found2 { // if they're equal check whether it begins with an ungreedy preference, if so prefer that one, if not then prefer the original
									// Go with original token
									i += length
									if tokensInText == len(tokens) {
										tokens = grow(tokens)
									}
									tokens[tokensInText] = index
									tokensInText++
									// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
									length = length2
									index = index2
									goto checkpoint2
								} else if found3 {
									// Sacrifice and go with alternative
									i += sacrifice.length
									if tokensInText == len(tokens) {
										tokens = grow(tokens)
									}
									tokens[tokensInText] = sacrifice.index
									tokensInText++
									// now set the lookahead as if it were chosen by the loop and goto the correct point in the code to continue from here
									length = length3
									index = index3
									goto checkpoint2
								}
							}
							// there is no alternative "sacrifice" option for this token
							i += length
							if tokensInText == len(tokens) {
								tokens = grow(tokens)
							}
							tokens[tokensInText] = index
							tokensInText++
							break
					}
				}
				if !found {
					missing++
					i++
				}
			}
	}

	return tokens[0:tokensInText], missing, nil
}

func LoadVocabFromFile(filename string) (Vocab, error) {
	var sacrifice sacrificeStruct
	var reserved int
	var key []byte
	var res Vocab
	fi, err := os.Open(filename)
	if err != nil {
		return res, err
	}
	defer fi.Close()
	r := custom.NewReader(fi)
	res.usingCapcode = r.ReadBool()
	res.charset = r.ReadByte()
	if res.charset > 2 {
		return res, errors.New(`Not a valid TokenMonster vocabulary.`)
	}
	l := int(r.ReadUint24())
	res.info = make([]sacrificeStruct, l)
	res.dictionary = new(pansearch.KeyBytes)
	lengths := make([]int, l)
	for i:=0; i<l; i++ {
		key = r.ReadBytes8()
		lengths[i] = len(key)
		if len(key) == 1 {
			reserved++
		} else if len(key) > res.maxlen {
			res.maxlen = len(key)
		}
		res.dictionary.AddUnsorted(key) // Because they're already in order, no need to build it
		switch r.ReadByte() {
			case 0:
				sacrifice = sacrificeStruct{0, 0, false, false, key}
			case 1:
				sacrifice = sacrificeStruct{0, 0, true, false, key}
			case 2:
				sacrifice = sacrificeStruct{0, 0, false, true, key}
			case 3:
				sacrifice = sacrificeStruct{0, 0, true, true, key}
			default:
				return res, errors.New(`Not a valid TokenMonster vocabulary.`)
		}
		sacrifice.index = int(r.ReadUint24())
		if sacrifice.index != noSacrifice {
			sacrifice.length = lengths[sacrifice.index]
		}
		res.info[i] = sacrifice
	}
	if reserved == 256 {
		res.reserve256bytes = true
	}
	if r.EOF() != nil {
		return res, errors.New(`Not a valid TokenMonster vocabulary.`)
	}
	res.dictionary.Build()
	return res, nil
}