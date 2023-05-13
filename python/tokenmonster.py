import struct
from collections import defaultdict

class TokenMonster:
    def __init__(self):
        self.word2id = defaultdict(int)
        self.id2word = []
        self.max_token_len = 0

    def load(self, filename):
        with open(filename, 'rb') as file:
            encoded_integer = file.read(8)
            n = struct.unpack('>Q', encoded_integer)[0]

            self.max_token_len = 0
            for i in range(n):
                len_byte = file.read(1)
                len_ = struct.unpack('B', len_byte)[0]
                str_ = file.read(len_).decode()

                self.max_token_len = max(self.max_token_len, len_)
                self.word2id[str_] = i

            self.id2word = list(self.word2id.keys())

            if file.read():
                raise Exception("Unexpected data found after processing the file.")

    def tokenize(self, text):
        tokens = []
        i = 0
        text_len = len(text)

        while i < text_len:
            matched_token = False

            for len_ in range(self.max_token_len, 0, -1):
                if (i + len_) <= text_len:
                    substr = text[i: i+len_]
                    if substr in self.word2id:
                        tokens.append(self.word2id[substr])
                        i += len_
                        matched_token = True
                        break

            if not matched_token:
                i += 1

        return tokens

    def detokenize(self, tokens):
        return ''.join(self.id2word[id_] for id_ in tokens)
