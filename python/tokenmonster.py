import unicodedata
import os
import requests
import capcode

class TokenMonster:

    class DecoderClass:

        def __init__(self, parent):
            self.parent = parent
            self.remainder = b''
            self.capcodeDecoder = capcode.Decoder()

        def detokenize(self, tokens):
            if isinstance(tokens[0], int):
                return self.detokenize_string(tokens)
            elif isinstance(tokens[0], list):
                return [self.detokenize_string(token_list) for token_list in tokens]
            else:
                raise ValueError("Input to detokenize must be a list of Token IDs or a list of lists of Token IDs.")

        def detokenize_string(self, tokens):
            if self.parent.charset == 0: # binary
                return self.parent.detokenize_bytes(tokens)
            # Compute total bytes needed
            nwords = len(self.parent.id2word)
            total_bytes = len(self.remainder) + sum(len(self.parent.id2word[id]) if id < nwords else 0 for id in tokens)
            # Create a bytearray of the necessary size
            decoded = bytearray(total_bytes)
            # Copy bytes into bytearray
            decoded[:len(self.remainder)] = self.remainder
            offset = len(self.remainder)
            for id in tokens:
                if id < nwords:
                    bytes_val = self.parent.id2word[id]
                    decoded[offset:offset + len(bytes_val)] = bytes_val
                    offset += len(bytes_val)
            # Convert bytearray back to bytes
            decoded = bytes(decoded)
            if self.parent.charset == 1: # UTF-8
                invalidBytes = incomplete_utf8_bytes(decoded)
                decodedString = decoded[:len(decoded)-invalidBytes]
                decodedString = decoded.decode('utf-8')
            else:                        # UTF-16
                invalidBytes = incomplete_utf16_bytes(decoded)
                decodedString = decoded[:len(decoded)-invalidBytes]
                decodedString = decoded.decode('utf-16-le')
            self.remainder = decoded[len(decoded)-invalidBytes:]
            if self.parent.capcode:
                decodedString = self.capcodeDecoder.decode(decodedString)
            return decodedString

    def __init__(self):
        self.word2id = {}
        self.id2word = []
        self.id2sacrifice = []
        self.id2sacrifice_length = []
        self.id2begin = []
        self.id2end = []
        self.max_token_len = 0
        self.charset = 0
        self.capcode = False

    def decoder(self):
        return self.DecoderClass(self)

    @staticmethod
    def load(filepath, url=None):
        if os.path.exists(filepath):
            try:
                with open(filepath, 'rb') as f:
                    buffer = f.read()
            except IOError as e:
                raise Exception(f"Error reading local file: {str(e)}")
            return TokenMonster.load_from_memory(buffer)
        if url is None:
            raise FileNotFoundError(f"File not found: {filepath}")
        try:
            response = requests.get(url, allow_redirects=True)
            response.raise_for_status()
        except (requests.HTTPError, requests.ConnectionError) as e:
            raise Exception(f"Error while downloading the vocabulary: {str(e)}")
        try:
            with open(filepath, 'wb') as f:
                f.write(response.content)
        except IOError as e:
            raise Exception(f"Error saving vocabulary to local file: {str(e)}")
        return TokenMonster.load_from_memory(response.content)

    @staticmethod
    def load_from_memory(buffer):
        tm = TokenMonster()

        # Read capcode boolean value
        if buffer[0] == 0:
            tm.capcode = False
        elif buffer[0] == 1:
            tm.capcode = True
        else:
            raise Exception("Invalid TokenMonster vocabulary file.")

        # Read charset value
        tm.charset = buffer[1]
        if tm.charset > 2:
            raise Exception("Invalid TokenMonster vocabulary file.")

        # Read the first 8 bytes as an encoded integer
        n = (buffer[2] | (buffer[3] << 8) | (buffer[4] << 16))

        # Initialize the arrays with 0s
        tm.id2word = [b""] * n
        tm.id2sacrifice = [0] * n
        tm.id2sacrifice_length = [0] * n
        tm.id2begin = [False] * n
        tm.id2end = [False] * n

        # Iterate n times
        offset = 5
        max_token_len = 0
        for index in range(n):
            # Read 1 byte and convert it to an integer
            length = buffer[offset]
            offset += 1

            # Read key
            key = buffer[offset:offset + length]
            offset += length
            max_token_len = max(max_token_len, length)

            # Set the key in the dictionary to the corresponding index
            tm.word2id[key] = index
            tm.id2word[index] = key

            # Get the begin and end flags for this
            begin_end_flag = buffer[offset]
            offset += 1

            if begin_end_flag == 0:
                tm.id2begin[index] = False
                tm.id2end[index] = False
            elif begin_end_flag == 1:
                tm.id2begin[index] = True
                tm.id2end[index] = False
            elif begin_end_flag == 2:
                tm.id2begin[index] = False
                tm.id2end[index] = True
            elif begin_end_flag == 3:
                tm.id2begin[index] = True
                tm.id2end[index] = True
            else:
                raise Exception("Invalid TokenMonster vocabulary file.")

            sacrifice_index = (buffer[offset] | (buffer[offset + 1] << 8) | (buffer[offset + 2] << 16))
            offset += 3

            if sacrifice_index != 16777215:  # index 16777215 means no sacrifice
                tm.id2sacrifice[index] = sacrifice_index
                tm.id2sacrifice_length[index] = len(tm.id2word[sacrifice_index])

        tm.max_token_len = max_token_len

        # Check if there are remaining bytes in the buffer
        if offset < len(buffer):
            raise Exception("Invalid TokenMonster vocabulary file.")

        return tm
        
    def tokenize(self, texts):
        if isinstance(texts, str) or isinstance(texts, bytes):
            return self.tokenize_string(texts)
        elif isinstance(texts, list):
            return [self.tokenize_string(text) for text in texts]
        else:
            raise ValueError("Input to tokenize must be a string or a list of strings.")

    def tokenize_string(self, text):
        if self.charset == 1:
            if isinstance(text, bytes):
                text = text.decode('utf-8')
            if self.capcode:
                text = capcode.encode(text)
            text = unicodedata.normalize('NFD', text).encode('utf-8')
        elif self.charset == 2:
            if isinstance(text, bytes):
                text = text.decode('utf-16')
            if self.capcode:
                text = capcode.encode(text)
            text = unicodedata.normalize('NFD', text).encode('utf-16-le')
        return self.tokenize_bytes(text)

    def tokenize_bytes(self, text):
        tokens = []
        text_len = len(text)
        i = 0
        i2 = 0
        i3 = 0
        id = 0
        id2 = 0
        id3 = 0
        len1 = 0
        len2 = 0
        len3 = 0
        slen = 0
        branch1 = 0
        branch2 = 0
        found = False
        found2 = False
        found3 = False

        while i < text_len:
            found = False
            # Check for tokens starting from the maximum token length
            for len1 in range(min(text_len - i, self.max_token_len), 0, -1):
                id = self.word2id.get(text[i:i + len1], None)
                if id is not None:
                    found = True
                    while i < text_len:
                        slen = self.id2sacrifice_length[id]
                        i2 = i + len1
                        if slen > 0 and i2 < text_len:
                            found2 = False
                            found3 = False
                            # lookforward original
                            for len2 in range(min(text_len - i2, self.max_token_len), 0, -1):
                                id2 = self.word2id.get(text[i2:i2 + len2], None)
                                if id2 is not None:
                                    found2 = True
                                    break
                            # lookforward alternative
                            i3 = i + slen
                            for len3 in range(min(text_len - i3, self.max_token_len), 0, -1):
                                id3 = self.word2id.get(text[i3:i3 + len3], None)
                                if id3 is not None:
                                    found3 = True
                                    break

                            branch1 = len1 + len2
                            branch2 = slen + len3

                            # Decide
                            if branch1 > branch2 or (branch1 == branch2 and self.id2end[id] != self.id2begin[id2]):
                                tokens.append(id)
                                i += len1
                                if not found2:
                                    break
                                id = id2
                                len1 = len2
                            else:
                                tokens.append(self.id2sacrifice[id])
                                i += slen
                                if not found3:
                                    break
                                id = id3
                                len1 = len3
                        # there is no alternative
                        else:
                            tokens.append(id)
                            i += len1
                            break
                    # break out of the outer loop as well
                    break
            if not found:
                i += 1
        return tokens

# detokenize_bytes does no normalization or capcode decoding, it just returns a bytes string of the tokens themselves
def detokenize_bytes(self, tokens):
    # Compute total bytes needed
    nwords = len(self.id2word)
    total_bytes = sum(len(self.parent.id2word[id]) if id < nwords else 0 for id in tokens)
    # Create a bytearray of the necessary size
    decoded = bytearray(total_bytes)
    # Copy bytes into bytearray
    offset = 0
    for id in tokens:
        if id < nwords:
            bytes_val = self.parent.id2word[id]
            decoded[offset:offset + len(bytes_val)] = bytes_val
            offset += len(bytes_val)
    # Convert bytearray back to bytes
    decoded = bytes(decoded)
    return decoded

def incomplete_utf8_bytes(bytes_str):
    bytes_len = len(bytes_str)
    if bytes_len == 0 or (bytes_str[-1] & 0b10000000) == 0:
        return 0
    # Find the start of the last character sequence
    seq_start = bytes_len - 1
    while seq_start >= 0 and (bytes_str[seq_start] & 0b11000000) == 0b10000000:
        seq_start -= 1
    # If no sequence start found, all bytes are continuation bytes and thus are all incomplete
    if seq_start == -1:
        return bytes_len
    # Determine expected sequence length from leading byte
    first_byte = bytes_str[seq_start]
    if (first_byte & 0b10000000) == 0:
        seq_len = 1
    elif (first_byte & 0b11100000) == 0b11000000:
        seq_len = 2
    elif (first_byte & 0b11110000) == 0b11100000:
        seq_len = 3
    elif (first_byte & 0b11111000) == 0b11110000:
        seq_len = 4
    else:
        # This is not a valid UTF-8 starting byte
        return bytes_len - seq_start
    # If sequence length is larger than the remaining bytes, it's incomplete
    if bytes_len - seq_start < seq_len:
        return seq_len - (bytes_len - seq_start)
    return 0

def incomplete_utf16_bytes(bytes):
    bytes_len = len(bytes)
    if bytes_len == 0:
        return 0
    # Check if bytes_len is divisible by 2
    if bytes_len % 2 != 0:
        last_three_bytes = int.from_bytes(bytes[bytes_len-3:bytes_len-1], byteorder='little') if bytes_len >= 3 else None
        return 3 if last_three_bytes is not None and 0xD800 <= last_three_bytes <= 0xDBFF else 1
    # Check if last 16-bit unit is a high surrogate
    last_two_bytes = int.from_bytes(bytes[bytes_len-2:bytes_len], byteorder='little')
    if 0xD800 <= last_two_bytes <= 0xDBFF and bytes_len < 4:
        return 2  # High surrogate without a following low surrogate
    return 0
