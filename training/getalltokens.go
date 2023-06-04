package main

import (
	"os"
	"log"
	"fmt"
	"flag"
	"bytes"
	"strings"
	"reflect"
	"io/ioutil"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
	uni "golang.org/x/text/encoding/unicode"
	"github.com/AlasdairF/Custom"
	"github.com/alasdairforsythe/pansearch"
	"github.com/alasdairforsythe/capcode/go"
)

/*

The defaults are good for an 840MB dataset with peak RAM usage around 200GB.
Increasing the workers will increase memory requirements considerably, 2x the workers means 3x the memory.
Using only 1 worker is much faster because it uses only 1 dictionary instead of having multiple dictionaries that all need to be sorted and aggregated.
The benefits would only be seen with more than 4 workers, but that would require perhaps 800 GB RAM.
microChunks can be increased to reduce memory usage, but at a massive cost of performance.
Long story short: unless you have 512GB or more RAM, it's better to use 1 worker.

*/

var (
	datasetFilename string
	saveFilename string
	maxTokenLength int = 32
	minOccurPerChunk int = 3
	minOccurTotal int = 100
	chunkSize int = 100000000
	microChunks int = 1
	workers int = 1
	includeSingleBytes bool = false
	usingCapcode bool = false
	charset string
	charsetFlag uint8
)

type workStruct struct {
	chunkId int
	data [][]byte
	tokens *pansearch.CounterBytes
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


func processChunk(asset workStruct, numChunks int, trim bool) *pansearch.CounterBytes {
	log.Println(`Finding tokens in chunk`, asset.chunkId, `of`, numChunks)
	tokens := asset.tokens
	lastMicroChunk := len(asset.data) - 1
	var i, to, l, length int
	var minLength = 2
	if includeSingleBytes {
		minLength = 1
	} 
	// Process microchunks
	for onMicroChunk, data := range asset.data {
		l = len(data) - maxTokenLength // the data has been split into chunks anyway, so we can just ignore the last maxTokenLength character and save bound checking in the main loop
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		for i = 0; i < l; i++ {
			to = maxTokenLength
			for length = minLength; length <= to; length++ {
				_ = data[i : i+length] // Eliminate bounds check
				tokens.Add([]byte(data[i:i+length]), 1)
			}
		}
		// Optimize the micro chunk to save memory
		if onMicroChunk < lastMicroChunk {
			tokens.Build()
			tokens.Optimize()
		}
	}
	// Trim the chunk
	if trim {
		log.Println(`Trimming chunk`, asset.chunkId, `of`, numChunks)
		tokens.Build_With_Min(minOccurPerChunk)
		tokens.Optimize()
	}
	log.Println(`Completed chunk`, asset.chunkId, `of`, numChunks)
	return tokens
}

func worker(channelWork chan workStruct, channelResult chan *pansearch.CounterBytes, numChunks int) {
	for asset := range channelWork {
		channelResult <- processChunk(asset, numChunks, true)
	}
}

func main() {
	flag.StringVar(&datasetFilename, "dataset", datasetFilename, "filename of the dataset plain-text (required)")
	flag.StringVar(&saveFilename, "output", saveFilename, "output filename for the dictionary (required)")
	flag.StringVar(&charset, "charset", charset, "One of: UTF-8, UTF-16, binary (required)")
	flag.IntVar(&maxTokenLength, "max-token-length", maxTokenLength, "the maximum length of a token")
	flag.IntVar(&minOccurPerChunk, "min-occur-chunk", minOccurPerChunk, "tokens will be trimmed if they occur less frequently than this per chunk")
	flag.IntVar(&minOccurTotal, "min-occur", minOccurTotal, "tokens will be trimmed if they occur less frequently than this in the dataset")
	flag.IntVar(&chunkSize, "chunk-size", chunkSize, "the number of bytes processed at a time, higher is faster but it means more RAM requirements")
	flag.IntVar(&microChunks, "micro-chunks", microChunks, "The higher this number, the slower it is but it will reduce peak memory usage")
	flag.IntVar(&workers, "workers", workers, "Multi-threading, also multiplies RAM requirements, you can't have more workers than chunks")
	flag.BoolVar(&includeSingleBytes, "include-single-bytes", includeSingleBytes, "If you enable this single byte tokens will also be recorded (default false)")
	flag.BoolVar(&usingCapcode, "capcode", usingCapcode, "use alternative uppercase encoding (default false)")
	flag.Parse()
	flagRequired("dataset", datasetFilename)
	flagRequired("output", saveFilename)
	flagRequired("charset", charset)

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

	// Load the text & normalize
	var err error
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

	// Get the results
	tokens := new(pansearch.CounterBytes)
	if workers == 1 { // only 1 worker, no need for goroutines or channels
		to = numChunks - 1
		for i=0; i<to; i++ {
			tokens = processChunk(workStruct{i+1, data_chunk[i], tokens}, numChunks, true)
		}
		tokens = processChunk(workStruct{i+1, data_chunk[i], tokens}, numChunks, false)
	} else {
		// Launch the worker threads
		var channelWork = make(chan workStruct, numChunks)
		var channelResult = make(chan *pansearch.CounterBytes, numChunks)
		for i=0; i<workers; i++ {
			go worker(channelWork, channelResult, numChunks)
		}
		// Send the chunks
		for i=0; i<numChunks; i++ {
			channelWork <- workStruct{i+1, data_chunk[i], new(pansearch.CounterBytes)} // each chunk has its own dictionary
		}
		i = 0
		var received bool
		var tok []byte
		var val int
		var eof bool
		for result := range channelResult {
			if received {
				// Iterate over the tokens returned from that chunk and add them to the base dictionary of everything
				if result.Reset() {
					for eof = false; !eof; {
						tok, val, eof = result.Next()
						tokens.Add(tok, val)
					}
				}
				result = nil // free
				tokens.Build()
			} else {
				tokens = result // the first one back becomes our base dictionary
				received = true
			}
			// Stop once all chunks are received
			if i++; i == numChunks {
				break
			}
		}
	}

	log.Println(`Tokens before trimming:`, tokens.Len())
	log.Println(`Trimming final tokens for min`, minOccurTotal)
	tokens.Build_With_Min(minOccurTotal)
	log.Println(`Tokens after trimming:`, tokens.Len())
	log.Println(`Saving tokens list`)
	if err := save_tokens(saveFilename, tokens.Keys()); err != nil {
		panic(err)
	}
	log.Println(`Done`)
}