class Uint8ArrayHashMap {
  constructor() {
    this.buckets = new Array(65536);
    for (let i = 0; i < this.buckets.length; i++) {
      this.buckets[i] = [];
    }
  }

  _hash(key) {
    let hash = 0;
    for (let i = 0; i < key.length; i++) {
      hash = (hash + key[i]) % 65536;
    }
    return hash;
  }

  _equals(a, b) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (a[i] !== b[i]) return false;
    }
    return true;
  }

  set(key, value) {
    const hash = this._hash(key);
    const bucket = this.buckets[hash];
    for (const pair of bucket) {
      if (this._equals(pair[0], key)) {
        pair[1] = value;
        return;
      }
    }
    bucket.push([key, value]);
  }

  get(key) {
    const hash = this._hash(key);
    const bucket = this.buckets[hash];
    for (const pair of bucket) {
      if (this._equals(pair[0], key)) {
        return pair[1];
      }
    }
    return -1;
  }
}

class TokenMonster {
  constructor() {
    this.word2id = new Uint8ArrayHashMap();
    this.id2word = [];
    this.id2sacrifice = [];
    this.id2sacrifice_length = [];
    this.id2begin = [];
    this.id2end = [];
    this.max_token_len = 0;
    this.charset = 0;
    this.capcode = false;
  }

  Decoder() {
    const parent = this;
    return new class Decoder {
        constructor() {
            this.remainder = new Uint8Array();
            this.capcodeDecoder = new CapcodeDecoder();
        }

        detokenize(tokens) {
          if (parent.charset == 0) {
            return parent.detokenize_bytes(tokens)
          }
          let id;
          let len = this.remainder.length;
          const maxid = parent.id2word.length;
          // Calculate the length of the Uint8Array
          for (let i = 0; i < tokens.length; i++) {
            id = tokens[i];
            if (id >= 0 && id < maxid) {
              len += parent.id2word[id].length;
            }
          }
          // Create a new Uint8Array with the calculated length
          const decoded = new Uint8Array(len);
          decoded.set(this.remainder, 0);
          let offset = this.remainder.length;
          // Fill the new Uint8Array with the appropriate values
          for (let i = 0; i < tokens.length; i++) {
            id = tokens[i];
            if (id >= 0 && id < maxid) {
              decoded.set(parent.id2word[id], offset);
              offset += parent.id2word[id].length;
            }
          }
          let invalidBytes = 0;
          let decoder;
          if (parent.charset == 1) { // UTF-8
            invalidBytes = incompleteUTF8Bytes(decoded)
            decoder = new TextDecoder('utf-8');
          } else { // UTF-16
            invalidBytes = incompleteUTF16Bytes(decoded)
            decoder = new TextDecoder('utf-16');
          }
          this.remainder = decoded.slice(decoded.length-invalidBytes, decoded.length);
          let decodedString = decoder.decode(decoded.slice(0, decoded.length-invalidBytes));
          if (parent.capcode) {
            decodedString = this.capcodeDecoder.decode(decodedString)
          }
          return decodedString;
        }
    };
  }

  async load(url) {
    let buffer = new ArrayBuffer(0);
    if (typeof process !== 'undefined' && process.versions != null && process.versions.node != null) {
      // We are in a Node.js environment
      let URL;
      try {
        URL = require('url').URL;
        new URL(url);
        // The URL is valid, fetch the data from it
        let http = require('http');
        let https = require('https');
        const protocol = url.startsWith('https') ? https : http;

        buffer = await new Promise((resolve, reject) => {
          protocol
            .get(url, (response) => {
              if (response.statusCode < 200 || response.statusCode >= 300) {
                reject(new Error(`Failed to fetch the URL (${response.statusCode}): ${url}`));
              }
              const chunks = [];
              response.on('data', (chunk) => chunks.push(chunk));
              response.on('end', () => {
                const buffer = Buffer.concat(chunks);
                resolve(buffer);
              });
            })
            .on('error', reject);
        });
      } catch (error) {
        // The URL is not valid, try to read the data from a local file
        const fs = require('fs');
        buffer = await new Promise((resolve, reject) => {
          fs.readFile(url, (error, data) => {
            if (error) {
              reject(error);
            } else {
              resolve(data);
            }
          });
        });
      }
    } else {
      // We are in a browser environment, use the Fetch API to get the data
      const response = await fetch(url);
      if (!response.ok) {
        throw new Error('Failed to fetch the file.');
      }
      buffer = await response.arrayBuffer();
    }
    const dataView = new DataView(buffer);

    // Read capcode var
    switch (dataView.getUint8(0)) {
      case 0:
        this.capcode = false;
        break;
      case 1:
        this.capcode = true;
        break;
      default:
        throw new Error('Invalid MonsterToken vocabulary file.');
    }

    // Read charset var
    this.charset = dataView.getUint8(1)
    if (this.charset > 2) {
      throw new Error('Invalid MonsterToken vocabulary file.');
    }

    // Read the first 8 bytes as an encoded integer
    const n = dataView.getUint8(2) | (dataView.getUint8(3) << 8) | (dataView.getUint8(4) << 16);

    // Iterate n times
    let offset = 5;
    let max_token_len = 0;
    for (let i = 0; i < n; i++) {
      // Read 1 byte and convert it to an integer
      const len = dataView.getUint8(offset);
      offset += 1;

      // Read len bytes as a string
      const key = new Uint8Array(dataView.buffer, dataView.byteOffset + offset, len);
      offset += len;
      max_token_len = Math.max(max_token_len, len);

      // Set the key in the map to the corresponding index
      this.word2id.set(key, i);
      this.id2word[i] = key;

      // Get the begin and end flags for this
      switch (dataView.getUint8(offset)) {
        case 0:
            this.id2begin[i] = false;
            this.id2end[i] = false;
            break;
        case 1:
            this.id2begin[i] = true;
            this.id2end[i] = false;
            break
        case 2:
            this.id2begin[i] = false;
            this.id2end[i] = true;
            break;
        case 3:
            this.id2begin[i] = true;
            this.id2end[i] = true;
            break;
        default:
            throw new Error('Invalid MonsterToken vocabulary file.');
      }
      offset += 1;

      const sacrifice_index = dataView.getUint8(offset) | (dataView.getUint8(offset + 1) << 8) | (dataView.getUint8(offset + 2) << 16)
      this.id2sacrifice[i] = sacrifice_index;
      if (sacrifice_index == 16777215) { // index 16777215 means no sacrifice
        this.id2sacrifice_length[i] = 0;
      } else {
        const sac = this.id2word[sacrifice_index];
        this.id2sacrifice_length[i] = sac.length; // save javascript length, not byte length
      }

      offset += 3;
    }
    this.max_token_len = max_token_len;

    // Check if there are remaining bytes in the buffer
    if (offset < buffer.byteLength) {
      throw new Error('Invalid MonsterToken vocabulary file.');
    }
  }

  tokenize(text) {
    let encoder = new TextEncoder();
    switch (this.charset) {
      case 1: // UTF-8
        if (text instanceof Uint8Array) {
          const decoder = new TextDecoder('utf-8');
          text = decoder.decode(text);
        }
        if (this.capcode) {
          text = capcode_encode(text);
        }
        text = text.normalize("NFD");
        text = encoder.encode(text);
        break;
      case 2: // UTF-16
        if (text instanceof Uint8Array) {
          const decoder = new TextDecoder('utf-16');
          text = decoder.decode(text);
        }
        if (this.capcode) {
          text = capcode_encode(text);
        }
        text = text.normalize("NFD");
        encoder = new TextEncoder('utf-16le');
        text = encoder.encode(text);
        break;
    }
    if (!(text instanceof Uint8Array)) {
      text = Uint8Array.from(text);
    }
    return this.tokenize_bytes(text);
  }

  tokenize_bytes(text) {
    let tokens = [];
    const textLen = text.length;
    let i = 0;
    let i2 = 0;
    let i3 = 0;
    let id = 0
    let id2 = 0;
    let id3 = 0;
    let len = 0;
    let len2 = 0;
    let len3 = 0;
    let slen = 0;
    let branch1 = 0;
    let branch2 = 0;
    let found = false;
    let found2 = false;
    let found3 = false;

    while (i < textLen) {
      found = false;

      // Check for tokens starting from the maximum token length
      outerLoop:
      for (len = Math.min(textLen - i, this.max_token_len); len > 0; len--) {

          id = this.word2id.get(text.subarray(i, i+len));

          if (id !== -1) {
            
            found = true;
            while (i < textLen) {
              slen = this.id2sacrifice_length[id];
              i2 = i + len;
              if (slen > 0 && i2 < textLen) {
                found2 = false;
                found3 = false;
                // lookforward original
                for (len2 = Math.min(textLen - i2, this.max_token_len); len2 > 0; len2--) {
                  id2 = this.word2id.get(text.subarray(i2, i2+len2));
                  if (id2 !== -1) {
                    found2 = true;
                    break;
                  }
                }
                // lookforward alternative
                i3 = i + slen;
                for (len3 = Math.min(textLen - i3, this.max_token_len); len3 > 0; len3--) {
                  id3 = this.word2id.get(text.subarray(i3, i3+len3));
                  if (id3 !== -1) {
                    found3 = true;
                    break;
                  }
                }

                branch1 = len + len2;
                branch2 = slen + len3;

                // Decide
                if (branch1 > branch2 || (branch1 == branch2 && this.id2end[id] != this.id2begin[id2])) {
                  tokens.push(id);
                  i += len;
                  if (!found2) {
                    break outerLoop;
                  }
                  id = id2;
                  len = len2;
                } else {
                  tokens.push(this.id2sacrifice[id]);
                  i += slen;
                  if (!found3) {
                    break outerLoop;
                  }
                  id = id3;
                  len = len3;
                }
              } else {
                tokens.push(id);
                i += len;
                break outerLoop;
              }
            }
          }
      }
      if (!found) {
        i++;
      }
    }
    return tokens;
  }

  detokenize_bytes(tokens) {
    let id;
    let len = 0;
    const maxid = this.id2word.length;
    // Calculate the length of the Uint8Array
    for (let i = 0; i < tokens.length; i++) {
      id = tokens[i];
      if (id >= 0 && id < maxid) {
        len += this.id2word[id].length;
      }
    }
    // Create a new Uint8Array with the calculated length
    const decoded = new Uint8Array(len);
    let offset = 0;
    // Fill the new Uint8Array with the appropriate values
    for (let i = 0; i < tokens.length; i++) {
      id = tokens[i];
      if (id >= 0 && id < maxid) {
        decoded.set(this.id2word[id], offset);
        offset += this.id2word[id].length;
      }
    }
    return decoded;
  }
  
}

function incompleteUTF8Bytes(bytes) {
  let bytesLen = bytes.length;
  // Single byte or empty string
  if (bytesLen === 0)
      return 0;
  if (bytes[bytesLen - 1] & 0b10000000 === 0)
      return 0;
  // Find the start of the last character sequence
  let seqStart = bytesLen - 1;
  while (seqStart >= 0 && (bytes[seqStart] & 0b11000000) === 0b10000000)
      seqStart--;
  // If no sequence start found, all bytes are continuation bytes and thus are all incomplete
  if (seqStart === -1)
      return bytesLen;
  // Determine expected sequence length from leading byte
  let seqLen = 0;
  while ((bytes[seqStart] & (0b10000000 >> seqLen)) !== 0)
      seqLen++;
  // If sequence length is larger than the remaining bytes, it's incomplete
  if (bytesLen - seqStart < seqLen)
      return seqLen - (bytesLen - seqStart);
  return 0;
}

function incompleteUTF16Bytes(bytes) {
  let bytesLen = bytes.length;
  // Single byte or empty array
  if (bytesLen === 0)
      return 0;
  // Check if bytesLen is divisible by 2
  if (bytesLen % 2 !== 0) {
    let lastThreeBytes = bytesLen >= 3 ? (bytes[bytesLen - 2] | (bytes[bytesLen - 3] << 8)) : null;
    return lastThreeBytes >= 0xD800 && lastThreeBytes <= 0xDBFF ? 3 : 1;
  }
  // Check if last 16-bit unit is a high surrogate
  let lastTwoBytes = (bytes[bytesLen - 1] | (bytes[bytesLen - 2] << 8));
  if ((lastTwoBytes >= 0xD800 && lastTwoBytes <= 0xDBFF) && bytesLen < 4)
      return 2; // High surrogate without a following low surrogate
  return 0; // All bytes form complete UTF-16 characters
}

// ---- capcode.js ----

const characterToken = 'C';
const wordToken = 'W';
const beginToken = 'B';
const endToken = 'E';
const apostrophe = '\'';
const apostrophe2 = '’';

function isUpper(r) {
  return /\p{Lu}/u.test(r);
}

function isLower(r) {
  return /\p{Ll}/u.test(r);
}

function isLetter(r) {
  return /\p{L}/u.test(r);
}

function isNumber(r) {
  return /\p{Nd}/u.test(r);
}

function isModifier(r) {
  return /\p{M}/u.test(r);
}

function capcode_encode(data) {
  let buf = new Array(Math.ceil(data.length + (data.length / 4) + 8));
  let pos = 0;
  let capStartPos = 0;
  let capEndPos = 0;
  let secondCapStartPos = 0;
  let lastWordCapEndPos = 0;
  let nWords = 0;
  let inCaps = false;
  let singleLetter = false;
  let inWord = false;

  for (let r of data) {

    if (inCaps) {
      if (isLetter(r)) {
        if (isUpper(r)) {
          if (!inWord) {
            inWord = true;
            if (nWords === 0) {
              secondCapStartPos = pos;
            }
            lastWordCapEndPos = capEndPos;
            nWords++;
          }
          buf[pos++] = r.toLowerCase();
          capEndPos = pos;
          singleLetter = false;
        } else {
          if (singleLetter && inWord) {
            buf[capStartPos] = characterToken;
          } else {
            switch (nWords) {
              case 0:
                if (!inWord) {
                  buf[capStartPos] = wordToken;
                } else {
                  buf[capStartPos] = characterToken;
                  for (let i2 = capStartPos + 1; i2 < capEndPos; i2++) {
                    let r2 = buf[i2];
                    if (isLetter(r2)) {
                        for (let j = pos; j > i2; j--) {
                            buf[j] = buf[j - 1];
                        }
                        buf[i2] = characterToken;
                        pos++;
                        capEndPos++;
                        i2++;
                    }
                  }
                }
                break;
              case 1:
                buf[capStartPos] = wordToken;
                if (!inWord) {
                  buf.splice(secondCapStartPos, 0, wordToken);
                  pos++;
                } else {
                  for (let i2 = secondCapStartPos; i2 < capEndPos; i2++) {
                    let r2 = buf[i2];
                    if (isLetter(r2)) {
                        for (let j = pos; j > i2; j--) {
                            buf[j] = buf[j - 1];
                        }
                        buf[i2] = characterToken;
                        pos++;
                        capEndPos++;
                        i2++;
                    }
                  }
                }
                break;
              case 2:
                if (!inWord) {
                  buf.splice(capEndPos, 0, endToken);
                  pos++;
                } else {
                  buf[capStartPos] = wordToken;
                  buf.splice(secondCapStartPos, 0, wordToken);
                  pos++;
                  capEndPos++;
                  for (let i2 = lastWordCapEndPos + 1; i2 < capEndPos; i2++) {
                    let r2 = buf[i2];
                    if (isLetter(r2)) {
                        for (let j = pos; j > i2; j--) {
                            buf[j] = buf[j - 1];
                        }
                        buf[i2] = characterToken;
                        pos++;
                        capEndPos++;
                        i2++;
                    }
                  }
                }
                break;
              default:
                if (!inWord) {
                  buf.splice(capEndPos, 0, endToken);
                  pos++;
                } else {
                  buf.splice(lastWordCapEndPos, 0, endToken);
                  pos++;
                  capEndPos++;
                  for (let i2 = lastWordCapEndPos + 1; i2 < capEndPos; i2++) {
                    let r2 = buf[i2];
                    if (isLetter(r2)) {
                        for (let j = pos; j > i2; j--) {
                            buf[j] = buf[j - 1];
                        }
                        buf[i2] = characterToken;
                        pos++;
                        capEndPos++;
                        i2++;
                    }
                  }
                }
            }
          }
          buf[pos++] = r;
          inCaps = false;
          capStartPos = pos;
        }
      } else {
        buf[pos++] = r;
        if (isModifier(r)) {
          capEndPos = pos
        } else if (r !== apostrophe && r !== apostrophe2 && !isNumber(r)) {
          inWord = false;
        }
      }
    } else {
      if (isUpper(r)) {
        capStartPos = pos;
        buf[pos++] = beginToken;
        buf[pos++] = r.toLowerCase();
        capEndPos = pos;
        nWords = 0;
        inCaps = true;
        inWord = true;
        singleLetter = true;
      } else {
        buf[pos++] = r;
        capStartPos = pos;
      }
    }
  }

  if (inCaps) {
    switch (nWords) {
      case 0:
        buf[capStartPos] = wordToken;
        break;
      case 1:
        buf[capStartPos] = wordToken;
        buf.splice(secondCapStartPos, 0, wordToken);
        pos++;
        break;
      default:
        buf.splice(capEndPos, 0, endToken);
        pos++;
      }
  }

  return buf.slice(0, pos).join('');
}

function capcode_decode(data) {
    let destination = "";  
    let inCaps = false;
    let charUp = false;
    let wordUp = false;
    for (let r of data) {
        switch (r) {
            case characterToken:
            charUp = true;
            break;
            case wordToken:
            wordUp = true;
            break;
            case beginToken:
            inCaps = true;
            break;
            case endToken:
            inCaps = false;
            break;
            default:
                if (charUp) {
                    destination += r.toUpperCase();
                    charUp = false;
                  } else if (wordUp) {
                    if (isLetter(r)) {
                        destination += r.toUpperCase();
                    } else {
                        if (!(isNumber(r) || r == apostrophe || r == apostrophe2 || isModifier(r))) {
                            wordUp = false
                        }
                        destination += r;
                    }
                  } else if (inCaps) {
                    destination += r.toUpperCase();
                  } else {
                    destination += r;
                  }
      }
    }
    return destination;
  }

class CapcodeDecoder {
    constructor() {
      this.inCaps = false;
      this.charUp = false;
      this.wordUp = false;
    }
  
    decode(data) {
      let destination = "";
      for (let r of data) {
        switch (r) {
          case characterToken:
            this.charUp = true;
            break;
          case wordToken:
            this.wordUp = true;
            break;
          case beginToken:
            this.inCaps = true;
            break;
          case endToken:
            this.inCaps = false;
            break;
          default:
            if (this.charUp) {
              destination += r.toUpperCase();
              this.charUp = false;
            } else if (this.wordUp) {
              if (isLetter(r)) {
                destination += r.toUpperCase();
              } else {
                if (!(isNumber(r) || r == apostrophe || r == apostrophe2 || isModifier(r))) {
                  this.wordUp = false;
                }
                destination += r;
              }
            } else if (this.inCaps) {
              destination += r.toUpperCase();
            } else {
              destination += r;
            }
        }
      }
      return destination;
    }
  }