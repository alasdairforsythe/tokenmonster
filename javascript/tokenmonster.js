class TokenMonster {
  constructor() {
    this.word2id = new Map();
    this.id2word = [];
    this.max_token_len = 0;
  }

  async load(url) {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error('Failed to fetch the file.');
    }

    const buffer = await response.arrayBuffer();
    const dataView = new DataView(buffer);

    // Read the first 8 bytes as an encoded integer
    const n = dataView.getUint8(0) | (dataView.getUint8(1) << 8) | (dataView.getUint8(2) << 16) | (dataView.getUint8(3) << 24) | (dataView.getUint8(4) << 32) | (dataView.getUint8(5) << 40) | (dataView.getUint8(6) << 48) | (dataView.getUint8(7) << 56);

    // Initialize an empty map
    this.word2id = new Map();

    // Iterate n times
    let offset = 8;
    let max_token_len = 0;
    for (let i = 0; i < n; i++) {
      // Read 1 byte and convert it to an integer
      const len = dataView.getUint8(offset);
      offset += 1;

      // Read len bytes as a string
      const str = new TextDecoder().decode(buffer.slice(offset, offset + len));
      offset += len;
      max_token_len = Math.max(max_token_len, len);

      // Set the key in the map to the corresponding index
      this.word2id.set(str, i);
      this.id2word[i] = str;
    }
    //this.id2word = Array.from(this.word2id.keys());
    this.max_token_len = max_token_len;

    // Check if there are remaining bytes in the buffer
    if (offset < buffer.byteLength) {
      throw new Error('Invalid file.');
    }
  }

  tokenize(text) {
    const tokens = [];
    const textLen = text.length;
    let i = 0;

    while (i < textLen) {
      let matchedToken = false;

      // Check for tokens starting from the maximum token length
      for (let len = this.max_token_len; len > 0; len--) {
        if (i + len <= textLen) {
          const substr = text.substr(i, len);
          if (this.word2id.has(substr)) {
            tokens.push(this.word2id.get(substr));
            i += len;
            matchedToken = true;
            break;
          }
        }
      }
      if (!matchedToken) {
        i++;
      }
    }

    return tokens;
  }

  detokenize(tokens) {
    let text = '';
    for (const id of tokens) {
      text += this.id2word[id];
    }
    return text;
  }
}
