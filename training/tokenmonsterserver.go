package main

import (
	"io"
	"os"
	"fmt"
	"sync"
	"time"
	"math"
	"bytes"
	"os/exec"
	"syscall"
	"runtime"
	"strings"
	"github.com/AlasdairF/Conv"
	"github.com/alasdairforsythe/tokenmonster/go"
)

/*

	This application operates as a tokenization server that communicates via stdin & stdout.
	It's used by the Python library and cannot be run standalone.

	STATUS CODES:
		0 = 8 bytes length
		1 = 4 bytes ID
		2 = Nothing
		10 = Received corrupt data
		11 = Channel has been closed, app closing down
		12 = Unable to open vocabulary file
		13 = Error normalizing text
		14 = Decoder has not been initialized
		15 = Invalid job ID
		16 = YAML is invalid

*/

const ( // status codes
	HEADER_IS_LENGTH = 0
	HEADER_IS_ID = 1
	HEADER_IS_EMPTY = 2
	ERROR_ID_DOES_NOT_EXIST = 10
	ERROR_ID_IS_UNLOADED = 11
	ERROR_FILE_CANNOT_OPEN = 12
	ERROR_NORMALIZATION_FAILED = 13
	ERROR_READ_FAILED = 14
	ERROR_INVALID_JOB = 15
	ERROR_YAML_INVALID = 16
	VERSION = 2
)

type work struct {
	data []byte
	err error
}

type workCount struct {
	data int
	err error
}

var lastAccess int64

func readUint64(buf []byte) uint64 {
	return uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 | uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56
}

func readUint32(buf []byte) uint32 {
	return uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
}

func writeUint64(buf []byte, v uint64) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
	buf[4] = byte(v >> 32)
	buf[5] = byte(v >> 40)
	buf[6] = byte(v >> 48)
	buf[7] = byte(v >> 56)
}

func readUint56(buf []byte) uint64 {
	return uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 | uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48
}

func writeUint32(buf []byte, v uint32) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
}

func writeFloat32(buf []byte, v float32) {
	writeUint32(buf, math.Float32bits(v))
}

func readString8(data []byte) (string, []byte) {
	l := uintptr(data[0]) + 1
	return string(data[1:l]), data[l:]
}

func sendError(statusCode byte) {
	io.Copy(io.Discard, os.Stdin)
	header := make([]byte, 9)
	header[0] = statusCode
	_, err := os.Stdout.Write(header)
	if err != nil {
		os.Exit(1)
	}
	os.Stdout.Sync()
}

func isProcessRunning(processID string) bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/C", "tasklist /FI \"PID eq "+processID+"\"").Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), processID)
	} else { // for "linux" and "darwin"
		out, err := exec.Command("ps", "-p", processID).Output()
		if err != nil {
			exitError, ok := err.(*exec.ExitError)
			if ok {
				// The program has exited with an exit code != 0
				// This works on both Unix and Windows. Although package
				// syscall is generally platform dependent, WaitStatus is
				// defined for both Unix and Windows and in both cases has
				// an ExitStatus() method with the same signature.
				if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
					if status.ExitStatus() == 1 {
						return false
					}
				}
			}
			return false
		}
		return strings.Contains(string(out), processID)
	}
}

func zombieController(parentPID string) {
	// In rare cases it's possible for the parent process to have been killed without this child process being closed
	// Therefore, after 6 hours of inactivity the PID of the parent is assessed every 2 hours
	for {
		time.Sleep(time.Hour * 2)
		sixHoursAgo := time.Now().Unix() - 21600
		if lastAccess < sixHoursAgo { // lastAccess does not need to be atomic in this very specific circumstance
			if !isProcessRunning(parentPID) {
				os.Exit(0)
			}
		}
	}
}

func readBlock(buf []byte) {
	var n, offset int
	var retries uint8
	var err error
	for {
		n, err = io.ReadFull(os.Stdin, buf[offset:])
		offset += n

		if err == io.ErrUnexpectedEOF {
			retries++
			if retries == 10 {
				sendError(ERROR_READ_FAILED)
				os.Exit(1)
			}
			time.Sleep(time.Second / 2)
			continue
		} else if err != nil {
			sendError(ERROR_READ_FAILED)
			os.Exit(1)
		}

		lastAccess = time.Now().Unix()
		return
	}
}

func main() {

/*

	All requests begin with a 12 byte header
	All responses begin with a 9 byte header

	Header receive:
		1 byte = job_type
		4 bytes = ID
		7 bytes = 56-bit unsigned integer length of payload

	Header send:
		1 byte = statusCode (0 is no error)
		8 bytes = length of payload

	job_type 0
		Returns VERSION
			Only responds header with version number

	job_type 1
		Tokenize
			4 bytes = number of batches
			Then for each batch:
				8 bytes = length
				data * length

	job_type 2, 3, 4 (the number is the encoding_length)
		Decode
			4 bytes = number of batches
			Then for each batch:
				8 bytes = length
				data * length

	job_type 5
		New Decoder
			No payload

	job_type 6
		Delete Decoder
			No payload

	job_type 7, 8, 9 (minus 5 to get encoding_length)
		Decoder decode
			(no batches, length is as the header so:)
			data * length

	job_type 10
		Load vocab
			1 byte = filename length
			Then filename
			Only responds header with ID

	job_type 11
		Unload vocab
			Only responds header

	job_type 12
		Save vocab
			1 byte = filename bytes length
			Then filename bytes

	job_type 14
		Change tokenizer
			1 byte reset token ids
			1 byte change unk
			4 bytes number to add, then 1bytelength + each
			4 bytes number to add special, then 1bytelength + each
			4 bytes number to delete, then 1bytelength + each
			4 bytes number to resize the vocabulary

	job_type 15
		List all tokens
			No payload
		Response is:
			4 bytes number of tokens
			For each token:
				4 bytes ID
				1 byte length raw form
				1 byte length decoded form
				1 byte type: 0 = regular, 1 = character, 2 = special
				4 bytes float32 score
				Raw form bytes
				Decoded form bytes

	job_type 16
		Delete 1 token by ID
			4 bytes = ID of token to delete
			Only responds header

	job_type 17
		Modify vocab by YAML
			The YAML file
			Only responds header

	job_type 18
		New vocab from YAML
			The YAML file
			Only responds header with ID

	job_type 19
		Export YAML from vocab
			No payload

*/

	if len(os.Args) < 2 {
		fmt.Println(`This application is a subprocess to accelerate the TokenMonster python library.`)
		fmt.Println(`Exiting`)
		os.Exit(0)
	}
	lastAccess = time.Now().Unix()
	go zombieController(strings.TrimSpace(os.Args[1]))

	header13 := make([]byte, 13)
	header12 := header13[0:12]
	header9 := header13[0:9]
	header8 := header13[0:8]
	var err error
	var jobType, statusCode, encodingLength uint8
	var length uint64
	var i, id, numBatches uint32
	var vocab *tokenmonster.Vocab
	var decoder *tokenmonster.Decoder
	var vocabs []*tokenmonster.Vocab
	var deletedVocabs []uint32
	var decoders []*tokenmonster.Decoder
	var deletedDecoders []uint32
	readBuffer := make([]byte, 1024 * 1024)
	writeBuffer := make([]byte, 1024 * 1024)
	var readBufferLen uint64 = uint64(len(readBuffer))
	var data []byte

	for {
		readBlock(header12)
		
		jobType = header12[0]
		id = readUint32(header12[1:])
		length = readUint56(header12[5:])
		if length > 0 {
			if length > readBufferLen {
				data = make([]byte, length)
			} else {
				data = readBuffer[0:length]
			}
			readBlock(data)
		}

		switch jobType {
			case 0: // Get VERSION
				header9[0] = HEADER_IS_ID
				writeUint32(header9[1:], VERSION)
				os.Stdout.Write(header9)
			
			case 1: // Tokenize
				statusCode = HEADER_IS_LENGTH
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST)
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED)
					continue
				}
				encodingLength = 2
				if vocab.Len() > 65536 {
					encodingLength = 4
				}
				numBatches = readUint32(data)
				data = data[4:]
				results := make([]work, numBatches)
				if numBatches == 1 {
					length = readUint64(data)
					data = data[8:]
					body := data[0:length]
					encodedTokens, _, _, err := vocab.TokenizeToSerialized(body, encodingLength, writeBuffer)
					results[0] = work{encodedTokens, err}
				} else {
					var wg sync.WaitGroup
					wg.Add(int(numBatches))
					for i=0; i<numBatches; i++ {
						length = readUint64(data)
						data = data[8:]
						body := data[0:length]
						data = data[length:]
						go func(i uint32, body []byte, encodingLength uint8) {
							encodedTokens, _, _, err := vocab.TokenizeToSerialized(body, encodingLength, nil)
							results[i] = work{encodedTokens, err}
							wg.Done()
						}(i, body, encodingLength)
					}
					wg.Wait()
				}
				if results[0].err != nil {
					statusCode = ERROR_NORMALIZATION_FAILED
				}
				length = 4
				for i, _ := range results {
					length += 8 + uint64(len(results[i].data))
				}
				header13[0] = statusCode
				writeUint64(header13[1:], length)
				writeUint32(header13[9:], numBatches)
				os.Stdout.Write(header13)
				for i:=0; i<len(results); i++ {
					writeUint64(header8, uint64(len(results[i].data)))
					os.Stdout.Write(header8)
					os.Stdout.Write(results[i].data)
				}

			case 2: // Decode 2 bytes encoding length
				fallthrough
			case 3: // Decode 3 bytes encoding length
				fallthrough
			case 4: // Decode 4 bytes encoding length
				statusCode = HEADER_IS_LENGTH
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				encodingLength = jobType
				numBatches = readUint32(data)
				data = data[4:]
				results := make([][]byte, numBatches)
				if numBatches == 1 {
					length = readUint64(data)
					data = data[8:]
					body := data[0:length]
					results[0] = vocab.DecodeSerialized(body, encodingLength, writeBuffer)
				} else {
					var wg sync.WaitGroup
					wg.Add(int(numBatches))
					for i=0; i<numBatches; i++ {
						length = readUint64(data)
						data = data[8:]
						body := data[0:length]
						data = data[length:]
						go func(i uint32, body []byte, encodingLength uint8) {
							results[i] = vocab.DecodeSerialized(data, encodingLength, nil)
							wg.Done()
						}(i, body, encodingLength)
					}
					wg.Wait()
				}
				length = 4
				for i, _ := range results {
					length += 8 + uint64(len(results[i]))
				}
				header13[0] = statusCode
				writeUint64(header13[1:], length)
				writeUint32(header13[9:], numBatches)
				os.Stdout.Write(header13)
				for i:=0; i<len(results); i++ {
					writeUint64(header8, uint64(len(results[i])))
					os.Stdout.Write(header8)
					os.Stdout.Write(results[i])
				}

			case 5: // New Decoder
				statusCode = HEADER_IS_ID
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				decoder = vocab.NewDecoder()
				if len(deletedDecoders) == 0 {
					id = uint32(len(decoders))
					decoders = append(decoders, decoder)
				} else {
					id = deletedDecoders[len(deletedDecoders) - 1]
					deletedDecoders = deletedDecoders[0 : len(deletedDecoders) - 1]
					decoders[id] = decoder
				}
				header9[0] = statusCode
				writeUint32(header9[1:], id)
				os.Stdout.Write(header9)

			case 6: // Unload Decoder
				statusCode = HEADER_IS_EMPTY
				if id >= uint32(len(decoders)) {
					sendError(4) // vocab ID does not exist
					continue
				}
				decoders[id] = nil
				deletedDecoders = append(deletedDecoders, id)
				header9[0] = statusCode
				os.Stdout.Write(header9)

			case 7: // Decoder: Decode 2 bytes encoding length
				fallthrough
			case 8: // Decoder: Decode 3 bytes encoding length
				fallthrough
			case 9: // Decoder: Decode 4 bytes encoding length
				statusCode = HEADER_IS_LENGTH
				encodingLength = jobType - 5
				if id >= uint32(len(decoders)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				decoder = decoders[id]
				if decoder == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				result := decoder.DecodeSerialized(data, encodingLength, writeBuffer)
				header9[0] = statusCode
				writeUint64(header9[1:], uint64(len(result)))
				os.Stdout.Write(header9)
				os.Stdout.Write(result)

			case 10: // Load vocab
				statusCode = HEADER_IS_ID
				filename, _ := readString8(data)
				vocab, err = tokenmonster.Load(filename)
				if err == nil {
					if len(deletedVocabs) == 0 {
						id = uint32(len(vocabs))
						vocabs = append(vocabs, vocab)
					} else {
						id = deletedVocabs[len(deletedVocabs) - 1]
						deletedVocabs = deletedVocabs[0 : len(deletedVocabs) - 1]
						vocabs[id] = vocab
					}
				} else {
					statusCode = ERROR_FILE_CANNOT_OPEN
				}
				header9[0] = statusCode
				writeUint32(header9[1:], id)
				os.Stdout.Write(header9)

			case 11: // Unload vocab
				statusCode = HEADER_IS_EMPTY
				if id < uint32(len(vocabs)) {
					vocabs[id] = nil
					deletedVocabs = append(deletedVocabs, id)
				} else {
					statusCode = ERROR_ID_DOES_NOT_EXIST
				}
				header9[0] = statusCode
				os.Stdout.Write(header9)

			case 12: // Save vocab
				statusCode = HEADER_IS_EMPTY
				filename, _ := readString8(data)
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				err = vocab.Save(filename)
				if err != nil {
					statusCode = ERROR_FILE_CANNOT_OPEN
				}
				header9[0] = statusCode
				os.Stdout.Write(header9)

			case 14: // Modify vocab
				statusCode = HEADER_IS_ID
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				var l uintptr
				// Read reset tokenIDs
				var resetTokenIds bool = data[0] == 1
				// Read "change_unk"
				switch data[1] {
					case 1:
						vocab.DisableUnkToken()
					case 2:
						vocab.EnableUnkToken()
				}
				data = data[2:]
				// Read "add"
				var toAdd [][]byte
				numBatches = readUint32(data)
				data = data[4:]
				if numBatches > 0 {
					toAdd = make([][]byte, numBatches)
					for i=0; i<numBatches; i++ {
						l = uintptr(data[0])
						toAdd[i] = data[1:l+1]
						data = data[l+1:]
					}
				}
				// Read "delete"
				var toDelete [][]byte
				numBatches = readUint32(data)
				data = data[4:]
				if numBatches > 0 {
					toDelete = make([][]byte, numBatches)
					for i=0; i<numBatches; i++ {
						l = uintptr(data[0])
						toDelete[i] = data[1:l+1]
						data = data[l+1:]
					}
				}
				// Read "add" special
				var toAddSpecial [][]byte
				numBatches = readUint32(data)
				data = data[4:]
				if numBatches > 0 {
					toAddSpecial = make([][]byte, numBatches)
					for i=0; i<numBatches; i++ {
						l = uintptr(data[0])
						toAddSpecial[i] = data[1:l+1]
						data = data[l+1:]
					}
				}
				// Read "resize"
				resize := int(readUint32(data))
				// Do the modification
				vocab.PrivateGenerateVocab(nil, nil, nil, toAdd, toDelete, toAddSpecial, nil, 0, ``, 0, 0, 0, resize, resetTokenIds)
				header9[0] = statusCode
				writeUint32(header9[1:], uint32(vocab.Len()))
				os.Stdout.Write(header9)

			case 15: // Get detailed info
				statusCode = HEADER_IS_LENGTH
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				info := vocab.TokensDetailed()
				// Get total length
				length = 4
				for _, v := range info {
					length += 11 + uint64(len(v.Token)) + uint64(len(v.TokenDecoded))
				}
				header13[0] = statusCode
				writeUint64(header13[1:], length)
				writeUint32(header13[9:], uint32(len(info)))
				os.Stdout.Write(header13)
				for _, v := range info {
					writeUint32(header12, v.Id)
					header12[4] = uint8(len(v.Token))
					header12[5] = uint8(len(v.TokenDecoded))
					header12[6] = v.Type
					writeFloat32(header12[7:], v.Score)
					os.Stdout.Write(header12[0:11])
					os.Stdout.Write(v.Token)
					os.Stdout.Write(v.TokenDecoded)
				}

			case 16: // Delete tokens by ID
				statusCode = HEADER_IS_ID
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST)
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED)
					continue
				}
				var yml string = "delete:\n"
				numBatches = readUint32(data)
				for i=0; i<numBatches; i++ {
					data = data[4:]
					yml += "  - id: " + conv.String(int(readUint32(data))) + "\n"
				}
				vocab.PrivateGenerateVocab([]byte(yml), nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, 0, false)
				vocabs[id] = vocab
				header9[0] = statusCode
				writeUint32(header9[1:], uint32(vocab.Len()))
				os.Stdout.Write(header9)

			case 17: // Modify by YAML
				statusCode = HEADER_IS_ID
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST)
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED)
					continue
				}
				err = vocab.PrivateGenerateVocab(data, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, 0, false)
				if err == nil {
					vocabs[id] = vocab
				} else {
					statusCode = ERROR_YAML_INVALID
				}
				header9[0] = statusCode
				writeUint32(header9[1:], uint32(vocab.Len()))
				os.Stdout.Write(header9)

			case 18: // New Vocab From YAML
				statusCode = HEADER_IS_LENGTH
				vocab = new(tokenmonster.Vocab)
				err = vocab.PrivateGenerateVocab(data, nil, nil, nil, nil, nil, nil, 0, ``, 0, 0, 0, 0, false)
				if err != nil {
					header9[0] = ERROR_YAML_INVALID
					os.Stdout.Write(header9)
				} else {
					if len(deletedVocabs) == 0 {
						id = uint32(len(vocabs))
						vocabs = append(vocabs, vocab)
					} else {
						id = deletedVocabs[len(deletedVocabs) - 1]
						deletedVocabs = deletedVocabs[0 : len(deletedVocabs) - 1]
						vocabs[id] = vocab
					}
					header9[0] = statusCode
					writeUint64(header9[1:], 16)
					os.Stdout.Write(header9)
					temp := make([]byte, 16)
					temp[0] = vocab.Capcode()
					temp[1] = vocab.Charset()
					temp[2] = vocab.NormalizationCode()
					temp[3] = vocab.Mode()
					writeUint32(temp[4:], uint32(vocab.Len()))
					writeUint32(temp[8:], id)
					writeUint32(temp[12:], vocab.Unk())
					os.Stdout.Write(temp)
				}

			case 19: // Export YAML from vocab
				statusCode = HEADER_IS_LENGTH
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST) // vocab ID does not exist
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED) // vocab ID already closed
					continue
				}
				w := bytes.NewBuffer(writeBuffer)
				vocab.ExportYAML(w, data[0] == 1)
				header9[0] = statusCode
				writeUint64(header9[1:], uint64(w.Len()))
				os.Stdout.Write(header9)
				w.WriteTo(os.Stdout)

			case 20: // Tokenize Count
				statusCode = HEADER_IS_LENGTH
				if id >= uint32(len(vocabs)) {
					sendError(ERROR_ID_DOES_NOT_EXIST)
					continue
				}
				vocab = vocabs[id]
				if vocab == nil {
					sendError(ERROR_ID_IS_UNLOADED)
					continue
				}
				numBatches = readUint32(data)
				data = data[4:]
				results := make([]workCount, numBatches)
				if numBatches == 1 {
					length = readUint64(data)
					data = data[8:]
					body := data[0:length]
					ntokens, _, err := vocab.Count(body)
					results[0] = workCount{ntokens, err}
				} else {
					var wg sync.WaitGroup
					wg.Add(int(numBatches))
					for i=0; i<numBatches; i++ {
						length = readUint64(data)
						data = data[8:]
						body := data[0:length]
						data = data[length:]
						go func(i uint32, body []byte) {
							ntokens, _, err := vocab.Count(body)
							results[i] = workCount{ntokens, err}
							wg.Done()
						}(i, body)
					}
					wg.Wait()
				}
				if results[0].err != nil {
					statusCode = ERROR_NORMALIZATION_FAILED
				}
				length = 4 + (uint64(len(results)) * 8)
				header13[0] = statusCode
				writeUint64(header13[1:], length)
				writeUint32(header13[9:], numBatches)
				os.Stdout.Write(header13)
				for i:=0; i<len(results); i++ {
					writeUint64(header8, uint64(results[i].data))
					os.Stdout.Write(header8)
				}

			default: // Invalid job type
				header9[0] = ERROR_INVALID_JOB
				os.Stdout.Write(header9)
		}

		os.Stdout.Sync()
		data = nil
		lastAccess = time.Now().Unix()
	}

}
