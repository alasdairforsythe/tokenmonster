package main

import (
	"os"
	"log"
	"fmt"
	"flag"
	"reflect"
	"io/ioutil"
	"github.com/alasdairforsythe/pansearch"
	"github.com/AlasdairF/Custom"
)

var (
	datasetFilename string
	saveFilename string
	maxTokenLength int = 30
	minOccurPerChuk int = 3
	minOccurTotal int = 50
	chunkSize int = 100000000
)

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

func main() {
	flag.StringVar(&datasetFilename, "dataset", datasetFilename, "filename of the dataset plain-text (required)")
	flag.StringVar(&saveFilename, "output", saveFilename, "output filename for the dictionary(required)")
	flag.IntVar(&maxTokenLength, "max-token-length", maxTokenLength, "the maximum length of a token")
	flag.IntVar(&minOccurPerChuk, "min-occur-chunk", minOccurPerChuk, "tokens will be trimmed if they occur less frequently than this per chunk")
	flag.IntVar(&minOccurTotal, "min-occur", minOccurTotal, "tokens will be trimmed if they occur less frequently than this in the dataset")
	flag.IntVar(&chunkSize, "chunk-size", chunkSize, "the number of bytes processed at a time, you need around 1000x this much RAM, so 10GB of RAM for 10MB chunk-size")
	flag.Parse()
	flagRequired("dataset", datasetFilename)
	flagRequired("output", saveFilename)

	// Load the text
	filedata, err := ioutil.ReadFile(datasetFilename)
    if err != nil {
		panic(err)
    }

	obj := new(pansearch.CounterBytes)
	chunks := (len(filedata) / chunkSize)
	if (chunks * chunkSize) < len(filedata) {
		chunks++
	}
	var i, length, from, to int
	var data []byte

	// Split the data into chunks
	data_chunk := make([][]byte, chunks)
	for i=0; i<chunks; i++ {
		from = i * chunkSize
		to = (i + 1) * chunkSize
		if len(filedata) < to {
			to = len(filedata)
		}
		data_chunk[i] = filedata[from:to]
	}

	// Loop through all the chunks
	for run:=0; run<chunks; run++ {
		log.Println(`Finding tokens in`, (run+1), `of`, chunks)
		data = data_chunk[run]
		// Move forward one character at a time capturing all possible combinations of characters from 2 to maxTokenLength
		for i = 0; i < len(data); i++ {
			to = maxTokenLength
			if len(data) - i < maxTokenLength {
				to = len(data) - i
			}
			for length = 2; length <= to; length++ {
				obj.Add([]byte(data[i:i+length]), 1)
			}
		}
		log.Println(`Trimming`, (run+1), `of`, chunks)
		if (run+1 < chunks) { // if this is not the final chunk
			if minOccurPerChuk > 1 {
				obj.Build_With_Min(minOccurPerChuk) // aggregate and delete
			} else {
				obj.Build() // aggregate
			}
		} else { // final chunk
			if minOccurTotal > 1 {
				obj.Build_With_Min(minOccurTotal)  // aggregate and delete
			} else {
				obj.Build() // aggregate
			}
		}
		log.Println(`Tokens`, obj.Len())
	}

	log.Println(`Saving final`)
	if err := save_tokens(saveFilename, obj.Keys()); err != nil {
		panic(err)
	}
	log.Println(`Done`)
}