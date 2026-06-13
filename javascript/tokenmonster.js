class Uint8ArrayHashMap {
  constructor() {
    this.buckets = Array.from({length: 65536}, () => []);
    this.bloom20to40 = BigInt(0);
    this.bloom30to40 = BigInt(0);
    this.bloom10to20 = BigInt(0);
    this.bloom5to10 = BigInt(0);
  }

  _hash(key, length = key.length) {
    let hash = 0;
    for (let i = 0; i < length; i++) {
      hash = ((hash << 5) - hash + key[i]) | 0;
    }
    return hash & 0xFFFF;
  }

  _equals(a, b) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (a[i] !== b[i]) return false;
    }
    return true;
  }

  _addToBloom(bloomFilter, key, length) {
    const hash = this._hash(key, length);
    return bloomFilter | (BigInt(1) << BigInt(hash));
  }

  _checkBloom(bloomFilter, key, length) {
    const hash = this._hash(key, length);
    return (bloomFilter & (BigInt(1) << BigInt(hash))) === BigInt(0);
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
    if (key.length > 20) {
      this.bloom20to40 = this._addToBloom(this.bloom20to40, key, 20);
      if (key.length > 30)
      this.bloom30to40 = this._addToBloom(this.bloom30to40, key, 30);
    } else if (key.length > 10) {
      this.bloom10to20 = this._addToBloom(this.bloom10to20, key, 10);
    } else if (key.length > 5) {
      this.bloom5to10 = this._addToBloom(this.bloom5to10, key, 5);
    }
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

  findLargestSubarray(arr) {
    let len = arr.length;
    if (len > 20) {
      if (this._checkBloom(this.bloom20to40, arr, 20)) {
        len = 20;
      } else if (len > 30) {
        if (this._checkBloom(this.bloom30to40, arr, 30)) {
          len = 30;
        }
      }
    }
    if (len > 10 && len <= 20) {
      if (this._checkBloom(this.bloom10to20, arr, 10)) {
        len = 10;
      }
    }
    if (len > 5 && len <= 10) {
      if (this._checkBloom(this.bloom5to10, arr, 5)) {
        len = 5;
      }
    }
    while (len > 0) {
      const value = this.get(arr.subarray(0, len));
      if (value !== -1) {
        return [value, len, true];
      }
      len--;
    }
    return [-1, 0, false];
  }
}

function displayString1(key, capcode) {
  const decoder = new TextDecoder('utf-8');
  const keystr = decoder.decode(key);
  if (capcode == 2) {
    const replacedString = keystr.replace(/("|\n|\r|\t| |W|D|C)/g, (match, capture) => {
      if (capture === '"') {
        return '&quot;';
      } else if (capture === '\n') {
        return '\\n';
      } else if (capture === '\r') {
        return '\\r';
      } else if (capture === '\t') {
        return '\\t';
      } else if (capture === ' ') {
        return '&nbsp;';
      } else if (capture === 'W') {
        return '\u21EA'; // caps lock symbol
      } else if (capture === 'D') {
        return '\u2326'; // forward delete symbol
      } else if (capture === 'C') {
        return '\u21E7'; // shift symbol
      }
    });
    return replacedString;
  } else {
    const replacedString = keystr.replace(/("|\n|\r|\t| |[\x7F])/g, (match, capture) => {
      if (capture === '"') {
        return '&quot;';
      } else if (capture === '\n') {
        return '\\n';
      } else if (capture === '\r') {
        return '\\r';
      } else if (capture === '\t') {
        return '\\t';
      } else if (capture === ' ') {
        return '\u2007'; //'&nbsp;';
      } else if (capture === '\x7F') {
        return '\u2326';
      }
    });
    return replacedString;
  }
}

function displayString2(key, capcode) {
  const decoder = new TextDecoder('utf-8');
  const keystr = decoder.decode(key);
  if (capcode == 2) {
    const replacedString = keystr.replace(/(\r\n|\n|\r|W|D|C)/g, (match, capture) => {
      if (capture === '\r\n') {
        return '↵\r\n';
      } if (capture === '\n') {
        return '↵\n';
      } else if (capture === '\r') {
        return '↵\r';
      } else if (capture === 'W') {
        return '\u21EA'; // caps lock symbol
      } else if (capture === 'D') {
        return '\u2326'; // forward delete symbol
      } else if (capture === 'C') {
        return '\u21E7'; // shift symbol
      }
    });
    return replacedString;
  } else {
    const replacedString = keystr.replace(/(\r\n|\n|\r|[\x7F])/g, (match, capture) => {
      if (capture === '\r\n') {
        return '↵\r\n';
      } if (capture === '\n') {
        return '↵\n';
      } else if (capture === '\r') {
        return '↵\r';
      } else if (capture === ' ') {
        return '\u2007'; //'&nbsp;';
      } else if (capture === '\x7F') {
        return '\u2326';
      }
    });
    return replacedString;
  }
}

class TokenMonster {
  constructor() {
    this.word2index = new Uint8ArrayHashMap();
    this.index2id = []
    this.index2alternative = [];
    this.index2alternative_length = [];
    this.index2alternative2 = [];
    this.index2alternative2_length = [];
    this.index2flag = [];
    this.index2nWords = [];
    this.id2word = [];
    this.id2string = [];
    this.id2display = [];
    this.max_token_len = 0;
    this.charset = 0;
    this.capcode = 0;
    this.normalization = 0;
    this.useUnk = false;
    this.unk = 0;
    this.hasDeleteToken = false;
    this.deleteToken = 0;
  }

  applyNormalize(text) {
    const flag = this.normalization;
    if (flag == 1) {
      return text.normalize("NFD");
    } else if (flag == 0) {
      return text;
    }
    if ((flag & 128) != 0) {
      text = text.replace(/\r\n/g, '\n');
    }
    if ((flag & 16) != 0) {
      text = text.replace(/ {2,}/g, ' ');
    }
    if ((flag & 8) != 0) {
      text = text.replace(/[\u2018\u2019]/g, "'").replace(/[\u201C\u201D]/g, '"');
    }
    if ((flag & 32) != 0) {
      text = text.trim();
    }
    if ((flag & 64) != 0) {
      if (!text.startsWith(' ')) { text = ' ' + text; }
    }
    if ((flag & 4) != 0) {
      text = text.normalize("NFD").replace(/[\u0300-\u036f]/g, "");
    }
    if ((flag & 2) != 0) {
      text = text.toLowerCase();
    }
    if ((flag & 1) != 0) {
      text = text.normalize("NFD");
    }
    return text;
  }

  debug(index) {
    if (index === undefined) {
      return "";
    }
    const id = this.index2id[index];
    const decoded = new Uint8Array(this.id2word[id].length);
    decoded.set(this.id2word[id], 0);
    const decoder = new TextDecoder('utf-8');
    return decoder.decode(decoded.slice(0, decoded.length));
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

        function toArrayBuffer(buffer) {
          const arrayBuffer = new ArrayBuffer(buffer.length);
          const view = new Uint8Array(arrayBuffer);
          for (let i = 0; i < buffer.length; ++i) {
              view[i] = buffer[i];
          }
          return arrayBuffer;
        }

        buffer = toArrayBuffer(buffer);
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

    // Read capcode
    this.capcode = dataView.getUint8(0);

    // Read charset
    this.charset = dataView.getUint8(1);
    if (this.charset > 2) {
      throw new Error('Invalid TokenMonster vocabulary file.');
    }

    // Read normalization
    this.normalization = dataView.getUint8(2);
    let offset = 8
    
    // Read the UNK token
    this.unk = dataView.getUint8(offset) | (dataView.getUint8(offset+1) << 8) | (dataView.getUint8(offset+2) << 16);
    if (this.unk != 16777215) {
      this.useUnk = true;
    }
    offset += 3;

    // Read vocabsize
    this.vocab_size = dataView.getUint8(offset) | (dataView.getUint8(offset+1) << 8) | (dataView.getUint8(offset+2) << 16);
    offset += 3;
    offset += 3;

    // Read nInfo
    const n = dataView.getUint8(offset) | (dataView.getUint8(offset+1) << 8) | (dataView.getUint8(offset+2) << 16);
    offset += 3;

    this.deleteToken = dataView.getUint8(offset) | (dataView.getUint8(offset+1) << 8) | (dataView.getUint8(offset+2) << 16);
    offset += 3;
    if (this.deleteToken != 16777215) {
      this.hasDeleteToken = true;
    }

    this.max_token_len = dataView.getUint8(offset);
    offset++;

    let lengths = []
    for (let i = 0; i < n; i++) {
      // Read the token info
      const len = dataView.getUint8(offset++);
      const key = new Uint8Array(dataView.buffer, dataView.byteOffset + offset, len);
      offset += len;
      const flag = dataView.getUint8(offset++);
      const nWords = dataView.getUint8(offset++);
      const index1 = dataView.getUint8(offset) | (dataView.getUint8(offset + 1) << 8) | (dataView.getUint8(offset + 2) << 16);
      offset += 3;
      const index2 = dataView.getUint8(offset) | (dataView.getUint8(offset + 1) << 8) | (dataView.getUint8(offset + 2) << 16);
      offset += 3;
      const id = dataView.getUint8(offset) | (dataView.getUint8(offset + 1) << 8) | (dataView.getUint8(offset + 2) << 16);
      offset += 7;
      // Parse it
      this.index2id[i] = id;
      this.word2index.set(key, i);
      lengths[i] = key.length;
      if (this.id2word[id] === undefined) {
        this.id2word[id] = key;
        this.id2string[id] = displayString1(key, this.capcode);
        this.id2display[id] = displayString2(key, this.capcode);
      }
      this.index2flag[i] = flag;
      this.index2nWords[i] = nWords;
      if (index1 == 16777215) {
        this.index2alternative_length[i] = 0;
      } else {
        const sac = this.id2word[this.index2id[index1]];
        this.index2alternative_length[i] = lengths[index1];
      }
      this.index2alternative[i] = index1;
      if (index2 == 16777215) {
        this.index2alternative2_length[i] = 0;
      } else {
        this.index2alternative2_length[i] = lengths[index2];
      }
      this.index2alternative2[i] = index2;
    }

    // set the UNK token
    if (this.useUnk) {
      this.id2word[this.unk] = new Uint8Array([]);
      this.id2string[this.unk] = "<UNK>";
      this.id2display[this.unk] = "<UNK>";
    }

    // Setup beginByte
    this.beginByte = Array(256)
    for (var i = 0; i < 256; i++) {
      this.beginByte[i] = dataView.getUint8(offset++);
    }
  }

  tokenize(text) {
    if (text instanceof Uint8Array) {
      const decoder = new TextDecoder('utf-8');
      text = decoder.decode(text);
    }
    text = this.applyNormalize(text);
    if (this.capcode == 2) {
      text = capcode_encode(text);
    } else if (this.capcode == 1) {
      // apply deleteToken only
    }
    text = new TextEncoder().encode(text);
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
    let i4 = 0;
    let id = 0
    let id2 = 0;
    let id3 = 0;
    let id4 = 0;
    let len = 0;
    let len2 = 0;
    let len3 = 0;
    let len4 = 0;
    let slen = 0;
    let slen2 = 0;
    let branch2 = 0;
    let branch3 = 0;
    let found = false;
    let found2 = false;
    let found3 = false;
    let found4 = false;
    let score1 = 0;
    let score2 = 0;
    let score3 = 0;
    let alternativeid = 0;
    let alternative2id = 0;
    let forwardDelete = 0;
    let score1b = 0;
    let score2b = 0;
    let score3b = 0;
    let maxScore = 0;
    let branch1b = 0;
    let branch2b = 0;
    let branch3b = 0;
    let id2b = 0;
    let id3b = 0;
    let id4b = 0;
    let len2b = 0;
    let len3b = 0;
    let len4b = 0;
    let found2b = false;
    let found3b = false;
    let found4b = false;
    let beginByte = '.';
    let testlen = 0;
    let nWords = 0;
    let nWords2 = 0;
    let flag = 0;
    let flag2 = 0;
    const lilbuf = new Uint8Array(this.max_token_len + this.charset);
    lilbuf[0] = 32;

    outerLoop:
    while (i < textLen) {

      [id, len, found] = this.word2index.findLargestSubarray(text.subarray(i, i + Math.min(textLen - i, this.max_token_len)));

          if (found) {
            while (i < textLen) {

              maxScore = -1000000;
              score1 = -1000000;
              score2 = -1000000;
              score3 = -1000000;
              score1b = -1000000;
              score2b = -1000000;
              score3b = -1000000;
              
              i2 = i + len;
              const temp1 = this.index2flag[id] & 32;
              const temp2 = this.beginByte[text[i2]];
              if (i2 < textLen && (temp1 == 0 || temp2 != 12)) {
                // Look ahead from first option
                [id2, len2, found2] = this.word2index.findLargestSubarray(text.subarray(i2, i2 + Math.min(textLen - i2, this.max_token_len)));
                //console.log("[1] '" + this.debug(id) + "' + '" + this.debug(id2) + "' (" + (len + len2) + ")");

                if (found2) {
                  // Score first option
                  nWords = this.index2nWords[id] - forwardDelete;
                  nWords2 = this.index2nWords[id2];
                  flag = this.index2flag[id];
                  flag2 = this.index2flag[id2];
                  beginByte = this.beginByte[text[i2 + len2]];
                  score1 = (
                            len + len2 + (flag >> 7) + (flag2 >> 7)
                            + ((nWords > 0 ? nWords - 1 : 0) + (nWords2 > 0 ? nWords2 - 1 : 0))
                            + ((flag2 >> 2) & 1) + ((beginByte >> 2) & 1)
                            + ((nWords + nWords2 + (beginByte >> 3)) * 100)
                          ) - (
                            (((flag & 1) & (flag2 >> 1)) * 103)
                            + (((flag >> 3) & 1 & (flag2 >> 4)) * 100)
                            + ((flag2 & 1 & beginByte) * 3)
                          );
                  maxScore = score1;

                  // If this is the middle of a word, try it as a word
                  if (this.hasDeleteToken && (flag2 & 2) !== 0 && beginByte === 1 && nWords2 === 0) {
                    testlen = Math.min(textLen - i2, this.max_token_len - this.charset);
                    lilbuf.set(text.subarray(i2, i2 + testlen), this.charset);
                    [id2b, len2b, found2b] = this.word2index.findLargestSubarray(lilbuf.subarray(0, testlen + this.charset));
                    if (found2b && len2b > len2 + 1) {
                      len2b -= this.charset;
                      branch1b = len + len2b;
                      nWords2 = this.index2nWords[id2b] - 1;
                      flag2 = this.index2flag[id2b];
                      beginByte = this.beginByte[text[i2 + len2b]];
                      score1b = (
                                branch1b + (flag >> 7) + (flag2 >> 7)
                                + ((nWords > 0 ? nWords - 1 : 0) + (nWords2 > 0 ? nWords2 - 1 : 0))
                                + ((beginByte >> 2) & 1)
                                + ((nWords + nWords2 + (beginByte >> 3)) * 100)
                              ) - (
                                ((flag & 1) * 103)
                                + (((flag >> 3) & 1 & (flag2 >> 4)) * 100)
                                + (((flag2 & 1) & beginByte) * 3)
                                + 1
                              );
                      maxScore = Math.max(maxScore, score1b);
                    }
                  }
                }

                alternativeid = this.index2alternative[id];
                if (alternativeid != 16777215) {
                  // Get alternative 1
                  slen = this.index2alternative_length[id] - forwardDelete;
                  i3 = i + slen;
                  [id3, len3, found3] = this.word2index.findLargestSubarray(text.subarray(i3, i3 + Math.min(textLen - i3, this.max_token_len)));
                  //console.log("[2] " + this.debug(alternativeid) + " + " + this.debug(id3) + " (" + (slen + len3) + ")");

                  if (found3) {
                    // Score for alternative 1
                    branch2 = slen + len3;
                    nWords = this.index2nWords[alternativeid] - forwardDelete;
                    nWords2 = this.index2nWords[id3];
                    flag = this.index2flag[alternativeid];
                    flag2 = this.index2flag[id3];
                    beginByte = this.beginByte[text[i3 + len3]];
                    score2 = (
                               branch2 + (flag >> 7) + (flag2 >> 7)
                              + ((nWords > 0 ? nWords - 1 : 0) + (nWords2 > 0 ? nWords2 - 1 : 0))
                              + ((flag2 >> 2) & 1) + ((beginByte >> 2) & 1)
                              + ((nWords + nWords2 + (beginByte >> 3)) * 100)
                            ) - (
                              ((flag & 1 & (flag2 >> 1)) * 103)
                              + (((flag >> 3) & 1 & (flag2 >> 4)) * 100)
                              + ((flag2 & 1 & beginByte) * 3)
                            );
                    score2 -= (branch2 <= len) ? ((branch2 == len) ? 10000 : 100) : 0;
                    maxScore = Math.max(maxScore, score2);

                    // If this is the middle of a word, try it as a word
                   if (this.hasDeleteToken && (flag2 & 2) !== 0 && beginByte === 1 && nWords2 === 0) {
                      testlen = Math.min(textLen - i3, this.max_token_len - this.charset);
                      lilbuf.set(text.subarray(i3, i3 + testlen), this.charset);
                      [id3b, len3b, found3b] = this.word2index.findLargestSubarray(lilbuf.subarray(0, testlen + this.charset));
                      if (found3b && len3b > len3 + 1) {
                        len3b -= this.charset;
                        branch2b = slen + len3b;
                        nWords2 = this.index2nWords[id3b] - 1;
                        flag2 = this.index2flag[id3b];
                        beginByte = this.beginByte[text[i3 + len3b]];
                        score2b = (
                                  branch2b + (flag >> 7) + (flag2 >> 7)
                                  + ((nWords > 0 ? nWords - 1 : 0) + (nWords2 > 0 ? nWords2 - 1 : 0))
                                  + ((beginByte >> 2) & 1)
                                  + ((nWords + nWords2 + (beginByte >> 3)) * 100)
                                ) - (
                                  ((flag & 1) * 103)
                                  + (((flag >> 3) & 1 & (flag2 >> 4)) * 100)
                                  + ((flag2 & 1 & beginByte) * 3)
                                  + 1
                                );
                        score2b -= (branch2b <= len) ? ((branch2b == len) ? 10000 : 100) : 0;
                        maxScore = Math.max(maxScore, score2b);
                      }
                    }
                  }
                  
                  // Look for alternative 2
                  alternative2id = this.index2alternative2[id];
                  if (alternative2id != 16777215) {

                      slen2 = this.index2alternative2_length[id] - forwardDelete;
                      i4 = i + slen2;
                      [id4, len4, found4] = this.word2index.findLargestSubarray(text.subarray(i4, i4 + Math.min(textLen - i4, this.max_token_len)));
                      //console.log("[3] " + this.debug(alternative2id) + " + " + this.debug(id4) + " (" + (slen2 + len4) + ")");

                      if (found4) {
                        branch3 = slen2 + len4;
                        nWords = this.index2nWords[alternative2id] - forwardDelete;
                        nWords2 = this.index2nWords[id4];
                        flag = this.index2flag[alternative2id];
                        flag2 = this.index2flag[id4];
                        beginByte = this.beginByte[text[i4 + len4]];
                        score3 = (
                                  branch3 + (flag >> 7) + (flag2 >> 7)
                                  + ((nWords > 0 ? nWords - 1 : 0) + (nWords2 > 0 ? nWords2 - 1 : 0))
                                  + ((flag2 >> 2) & 1) + ((beginByte >> 2) & 1)
                                  + ((nWords + nWords2 + (beginByte >> 3)) * 100)
                                ) - (
                                  ((flag & 1 & (flag2 >> 1)) * 103)
                                  + (((flag >> 3) & 1 & (flag2 >> 4)) * 100)
                                  + ((flag2 & 1 & beginByte) * 3)
                                );
                        score3 -= (branch3 <= len) ? ((branch3 == len) ? 10000 : 100) : 0;
                        maxScore = Math.max(maxScore, score3);

                        if (this.hasDeleteToken && (flag2 & 2) !== 0 && beginByte === 1 && nWords2 === 0) {
                          testlen = Math.min(textLen - i4, this.max_token_len - this.charset);
                          lilbuf.set(text.subarray(i4, i4 + testlen), this.charset);
                          [id4b, len4b, found4b] = this.word2index.findLargestSubarray(lilbuf.subarray(0, testlen + this.charset));
                          if (found4b && len4b > len4 + 1) {
                            len4b -= this.charset;
                            branch3b = slen2 + len4b;
                            nWords2 = this.index2nWords[id4b] - 1;
                            flag2 = this.index2flag[id4b];
                            beginByte = this.beginByte[text[i4 + len4b]];
                            score3b = (
                                      branch3b + (flag >> 7) + (flag2 >> 7)
                                      + ((nWords > 0 ? nWords - 1 : 0) + (nWords2 > 0 ? nWords2 - 1 : 0))
                                      + ((beginByte >> 2) & 1)
                                      + ((nWords + nWords2 + (beginByte >> 3)) * 100)
                                    ) - (
                                      ((flag & 1) * 103)
                                      + (((flag >> 3) & 1 & (flag2 >> 4)) * 100)
                                      + ((flag2 & 1 & beginByte) * 3)
                                      + 1
                                    );
                            score3b -= (branch3b <= len) ? ((branch3b == len) ? 10000 : 100) : 0;
                            maxScore = Math.max(maxScore, score3b);
                          }
                        }
                    }
                  }
                }

                //console.log("Scores", score1, score1b, score2, score2b, score3, score3b);
                
                switch (maxScore) {
                    case -1000000:
                      tokens.push(this.index2id[id]);
                      i += len;
                      forwardDelete = 0;
                      //console.log("branch 0");
                      continue outerLoop;
                    case score1:
                      tokens.push(this.index2id[id]);
                      i += len;
                      id = id2;
                      len = len2;
                      forwardDelete = 0;
                      //console.log("branch 1");
                      break;
                    case score2:
                      tokens.push(this.index2id[alternativeid]);
                      i += slen;
                      id = id3;
                      len = len3;
                      forwardDelete = 0;
                      //console.log("branch 2");
                      break;
                    case score3:
                      // Go with branch 3
                      tokens.push(this.index2id[alternative2id]);
                      i += slen2;
                      id = id4;
                      len = len4;
                      forwardDelete = 0;
                      //console.log("branch 3");
                      break;
                    case score1b:
                      tokens.push(this.index2id[id]);
                      tokens.push(this.deleteToken);
                      i += len;
                      id = id2b;
                      len = len2b;
                      forwardDelete = 1;
                      //console.log("branch 1b");
                      break;
                    case score2b:
                      tokens.push(this.index2id[alternativeid]);
                      tokens.push(this.deleteToken);
                      i += slen;
                      id = id3b;
                      len = len3b;
                      forwardDelete = 1;
                      //console.log("branch 2b");
                      break;
                    case score3b:
                      tokens.push(this.index2id[alternative2id]);
                      tokens.push(this.deleteToken);
                      i += slen2;
                      id = id4b;
                      len = len4b;
                      forwardDelete = 1;
                      //console.log("branch 3b");
                      break;
                    default:
                      //console.log("BRANCH ERROR!");
                      return tokens;
                }
              } else {
                tokens.push(this.index2id[id]);
                i += len;
                forwardDelete = 0;
                //console.log("branch 1 of 1");
                continue outerLoop;
              }
            }
          } else {
            //!found
            i++;
            forwardDelete = 0;
            if (this.useUnk) {
              tokens.push(this.unk);
            }
            //console.log("branch N/A");
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
  if ((bytes[bytesLen - 1] & 0b10000000) === 0)
      return 0;
  // Find the start of the last character sequence
  let seqStart = bytesLen - 1;
  while (seqStart >= 0 && (bytes[seqStart] & 0b11000000) === 0b10000000)
      seqStart--;
  // If no sequence start found, all bytes are continuation bytes and thus are all incomplete
  if (seqStart === -1)
      return bytesLen;
  // Determine expected sequence length from leading byte
  let firstByte = bytes[seqStart];
  let seqLen;
  if ((firstByte & 0b10000000) === 0) {
      seqLen = 1;
  } else if ((firstByte & 0b11100000) === 0b11000000) {
      seqLen = 2;
  } else if ((firstByte & 0b11110000) === 0b11100000) {
      seqLen = 3;
  } else if ((firstByte & 0b11111000) === 0b11110000) {
      seqLen = 4;
  } else {
      // This is not a valid UTF-8 starting byte
      return bytesLen - seqStart;
  }
  // If sequence length is larger than the remaining bytes, it's incomplete
  if (bytesLen - seqStart < seqLen)
      return bytesLen - seqStart;
  // If the sequence start byte was not the start of a multi-byte sequence, then the array is incomplete.
  if (seqLen === 1 && (bytes[seqStart] & 0b11000000) !== 0)
      return bytesLen;
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
const deleteToken = 'D';
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
  let buf = new Array(Math.ceil(data.length + (data.length / 2) + 8));
  let pos = 0;
  let gobackPos = 0;
  let wordTokenPos = 0;
  let rlast = '.';
  let rlast2 = '.';
  let inWord = false;
  let multiLetter = false;

  for (let r of data) {

    if (inWord) {
      if (isUpper(r)) {
        if (!(isLetter(rlast) || rlast == apostrophe || rlast == apostrophe2 || isModifier(rlast))) {
          buf[pos++] = deleteToken;
          buf[pos++] = ' ';
        }
        multiLetter = true;
        buf[pos++] = r.toLowerCase();
      } else {
        if (isLower(r)) {
          inWord = false;
          buf[wordTokenPos] = characterToken;
          if (multiLetter) {
            for (let i2 = gobackPos; i2 < pos; i2++) {
              if (buf[i2] == deleteToken && buf[i2+1] == ' ') {
                if (isLower(buf[i2 + 2])) {
                  for (let j = pos+1; j > i2 + 2; j--) {
                    buf[j] = buf[j - 1];
                  }
                  buf[i2] = deleteToken;
                  buf[i2+1] = characterToken;
                  buf[i2+2] = ' ';
                  pos++;
                  i2++
                }
                i2 += 2;
              } else {
                if (isLower(buf[i2])) {
                  for (let j = pos+3; j > i2; j--) {
                    buf[j] = buf[j - 3];
                  }
                  buf[i2] = deleteToken;
                  buf[i2+1] = characterToken;
                  buf[i2+2] = ' ';
                  pos += 3;
                  i2 += 3;
                }
              }
            }
          }
          if (!(isLetter(rlast) || rlast == apostrophe || rlast == apostrophe2 || isModifier(rlast))) {
            buf[pos++] = deleteToken;
            buf[pos++] = ' ';
          }
        } else {
          if (isNumber(r)) {
            if (!isNumber(rlast)) {
              buf[pos++] = deleteToken;
              buf[pos++] = ' '
            }
          } else if (!(r == apostrophe || r == apostrophe2 || isModifier(r))) {
            inWord = false;
          }
        }
        buf[pos++] = r
      }
    } else {
      if (isLower(r)) {
        if (!(rlast == ' ' || isLetter(rlast) || (isLetter(rlast2) && (rlast == apostrophe || rlast == apostrophe2)) || isModifier(rlast))) {
          buf[pos++] = deleteToken;
          buf[pos++] = ' ';
        }
        buf[pos++] = r;
      } else if (isUpper(r)) {
        if (rlast == ' ') {
          wordTokenPos = pos - 1;
          buf[wordTokenPos] = wordToken;
          buf[pos++] = ' ';
        } else {
          buf[pos++] = deleteToken;
          wordTokenPos = pos;
          buf[pos++] = wordToken;
          buf[pos++] = ' '
        }
        buf[pos++] = r.toLowerCase();
        gobackPos = pos;
        multiLetter = false;
        inWord = true;
      } else if (isNumber(r)) {
        if (!(rlast == ' ' || isNumber(rlast))) {
          buf[pos++] = deleteToken;
          buf[pos++] = ' ';
        }
        buf[pos++] = r;
      } else {
        buf[pos++] = r;
      }
    }
    rlast2 = rlast;
    rlast = r;
  }

  return buf.slice(0, pos).join('');
}

class CapcodeDecoder {
    constructor() {
      this.inWord = false;
      this.inChar = false;
      this.delete = false;
      this.ignore = false;
    }
  
    decode(data) {
      let destination = "";
      for (let r of data) {
        switch (r) {
          case characterToken:
            this.inChar = true;
            this.inWord = false;
            continue;
          case wordToken:
            this.inWord = true;
            this.inChar = false;
            this.ignore = true;
            continue;
          case deleteToken:
            this.delete = true;
            continue;
          case ' ':
            if (this.delete) {
                this.delete = false;
            } else {
                destination += ' ';
                if (!this.ignore) {
                    this.inWord = false;
                }
            }
            break;
          default:
            if (this.delete) {
                this.delete = false;
            } else if (this.inChar) {
                this.inChar = false;
                destination += r.toUpperCase();
            } else if (this.inWord) {
                if (isLower(r) || isUpper(r)) {
                    destination += r.toUpperCase();
                } else {
                    destination += r;
                    if (!(isNumber(r) || r == apostrophe || r == apostrophe2 || isModifier(r))) {
                      this.inWord = false;
                    }
                }
            } else {
                destination += r;
            }
        }
        this.ignore = false;
      }

      return destination;
    }
}
