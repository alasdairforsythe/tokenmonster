import numpy as np
import struct
import subprocess
import os
import urllib.request
import platform
import sys
from collections.abc import Iterable
if platform.system() == 'Windows':
    import getpass

def set_local_directory(path):
    """
    The default directory for TokenMonster is ~/_tokenmonster
    Use this function to set the default directory elsewhere, before loading any vocabularies.
    """
    Vocab._set_local_directory(path)

def disconnect():
    """
    Closes tokenmonsterserver subprocess.
    """
    Vocab._disconnect()

def load(path):
    """
    Loads a TokenMonster vocabulary from file, URL or by name.

    Parameters:
        path (string): A filepath, URL or pre-built vocabulary name.

    Returns:
        Vocab: An instance of tokenmonster.Vocab.

    Usage:
        vocab = tokenmonster.load("english-32000-balanced-v1")
        tokens = vocab.tokenize(str)
        decoded_string = vocab.decode(tokens)
    """
    return Vocab(path)

def load_multiprocess_safe(path):
    """
    Loads a TokenMonster vocabulary from file, URL or by name.
    It's safe for multiprocessing, but vocabulary modification is disabled and tokenization is slightly slower.

    Parameters:
        path (string): A filepath, URL or pre-built vocabulary name.

    Returns:
        Vocab: An instance of tokenmonster.Vocab.

    Usage:
        vocab = tokenmonster.load_multiprocess_safe("english-32000-balanced-v1")
    """
    return Vocab(path, True)

def new(yaml):
    """
    Creates a new vocabulary from a YAML string.
    A sample YAML file can be found here: https://github.com/alasdairforsythe/tokenmonster/yaml_guide
    You should save it in the vocab format with `vocab.save()` for future use.

    Parameters:
        yaml (string or bytes string): The YAML file.

    Returns:
        TokenMonster instance: An vocabulary instance of TokenMonster class.

    Usage:
        vocab = tokenmonster.new(yaml_string)
        vocab.save(filename)
    """
    if not isinstance(yaml, bytes):
        if isinstance(yaml, str):
            yaml = yaml.encode('utf-8')
        else:
            raise ValueError("TokenMonster: Input to `tokenmonster.new()` must be a YAML string.")
    Vocab._set_local_directory()
    job_type = 18
    vocab = Vocab.__new__(Vocab)
    vocab._multiprocess = False
    vocab.fname = None
    response = vocab._communicate(job_type, 0, len(yaml), yaml)
    vocab._capcode = response[0]
    vocab._charset = response[1]
    vocab._normalization = response[2]
    vocab._mode = response[3]
    vocab.vocab_size = _read_uint32(response[4:8])
    vocab.id = _read_uint32(response[8:12])
    unk = _read_uint32(response[12:16])
    vocab._highest_token_id_plus_one = _read_uint32(response[16:20])
    if unk == 16777215:
        vocab.unk = None
    else:
         vocab.unk = unk
    if vocab._highest_token_id_plus_one > 65536:
        vocab.encoding_length = 4
    else:
        vocab.encoding_length = 2
    vocab.dictionary = None
    vocab._token_to_id = None
    vocab._modified_id = 0
    vocab._decoders = []
    return vocab

class Vocab:
    """
    Main class for TokenMonster.

    Usage:
        vocab = tokenmonster.Load("english-32000-balanced-v1")
        tokens = vocab.tokenize(str)
        decoded_string = vocab.decode(tokens)
    """

    class DecoderInstance:
        """
        A nested class for decoding streams of tokens in sequence.

        This class takes tokens and decodes them to generate human-readable strings.

        Usage:
            vocab = tokenmonster.Load("english-32000-balanced-v1")
            decoder = vocab.decoder()
            decoded_string = decoder.decode(tokens)
            decoded_string += decoder.decode(more_tokens)
        """

        def __init__(self, parent):
            self.parent = parent
            self.id = parent._communicate(5, parent.id, 0)
            self._modified_id = parent._modified_id
            parent._decoders.append(self.id)
        
        def decode(self, tokens):
            """
            A decoder object used for decoding token streams.

            This decoder object is used instead of the vocabulary decode method when you are
            decoding tokens in small segments, or one by one, that are part of a longer
            stream of encoded tokens. A new decoder object should be used for each
            stream, then deleted. If you are decoding all tokens in one call, instead of
            in multiple calls, then you can use the vocabulary decode method directly.

            Parameters:
                tokens (int or list of ints or numpy array): A token ID or list of token IDs.

            Returns:
                string: A human-readable string derived from the input tokens.

            Usage:
                vocab = tokenmonster.Load("english-32000-balanced-v1")
                decoder = vocab.Decoder()
                decoded_string = decoder.decode(tokens)
                decoded_string += decoder.decode(more_tokens)
            """
            if self.parent._modified_id != self._modified_id:
                raise RuntimeError("Access denied to tokenmonster.DecoderInstance. The decoder instance has either expired, or it was not created by this multiprocessing thread.")
            if is_iterable(tokens):
                if len(tokens) == 0:
                    return ''
                if is_iterable(tokens[0]):
                    raise ValueError("TokenMonster: You can't batch decode on a decoder object, use the vocab decoder for that.")
            else:
                if isinstance(tokens, int):
                    tokens = [tokens]
                elif isinstance(tokens, (np.uint16, np.uint32)):
                    tokens = np.array([tokens])
                else:
                    raise ValueError("TokenMonster: Decoder decode accepts int, list of ints or np.array.")
            payload = self.parent.serialize_tokens(tokens)
            job_type = self.parent.encoding_length + 5
            response = self.parent._communicate(job_type, self.id, len(payload), payload)
            return self.parent._bytes_to_string(response)
        
        def _unload(self):
            if hasattr(self, 'id'):
                if self.id is not None:
                    if self.parent._modified_id != -1:
                        self.parent._communicate(6, self.id, 0)
        
        def __del__(self):
            try:
                if not sys.is_finalizing():
                    self._unload()
            except AttributeError:
                pass

    def __init__(self, path, multiprocess_safe = False):
        Vocab._set_local_directory()
        if not any(char in path for char in "./\\"): # if its not a filename or URL
            if Vocab._file_exists(path + ".vocab"):
                path = os.path.join(Vocab._dir, path + ".vocab")
            else:
                clean = path.replace("+", "")
                if Vocab._file_exists(clean + ".vocab"):
                    path = os.path.join(Vocab._dir, clean + ".vocab")
                else:
                    if _is_prebuilt(clean):
                        path = clean + ".vocab"
                        Vocab._download(_TOKENMONSTER_URL + "vocabs/" + path, path)
                        if Vocab._file_exists(path):
                            path = os.path.join(Vocab._dir, path)
                        else:
                            raise RuntimeError("TokenMonster: Unable to download the prebuilt vocabulary, please check availability at huggingface.co/alasdairforsythe/tokenmonster")
        elif path.startswith("http://") or path.startswith("https://"): # it's a URL
            fname = os.path.basename(path)
            if Vocab._file_exists(fname):
                path = os.path.join(Vocab._dir, fname)
            else:
                Vocab._download(path, fname)
                if Vocab._file_exists(fname):
                    path = os.path.join(Vocab._dir, fname)
                else:
                    raise FileNotFoundError("TokenMonster: Unable to download " + path + " to " + Vocab._dir)
        elif os.path.isfile(path): # it's a local filepath relative to the working directory
            if not os.path.isabs(path):
                path = os.path.join(os.getcwd(), path)
        elif Vocab._file_exists(path): # it's a local filepath relative to the _tokenmonster dir
            path = os.path.join(Vocab._dir, path)
        elif Vocab._file_exists(path + ".vocab"): # it's a local filepath relative to the _tokenmonster dir without file extension
            path = os.path.join(Vocab._dir, path + ".vocab")
        else:
            raise FileNotFoundError("TokenMonster: Unable to locate " + path)
        # Now read the vocabulary header
        with open(path, 'rb') as file:
            vocab_header = file.read(17)
        self._capcode = vocab_header[0]
        self._charset = vocab_header[1]
        self._normalization = vocab_header[2]
        self._mode = vocab_header[3]
        unk = vocab_header[8] | (vocab_header[9] << 8) | (vocab_header[10] << 16)
        self.vocab_size = vocab_header[11] | (vocab_header[12] << 8) | (vocab_header[13] << 16)
        self._highest_token_id_plus_one = vocab_header[14] | (vocab_header[15] << 8) | (vocab_header[16] << 16)
        self.unk = None
        if unk != 16777215:
            self.unk = unk
        if self._highest_token_id_plus_one > 65536:
            self.encoding_length = 4
        else:
            self.encoding_length = 2
        self.fname = path
        self.dictionary = None
        self._token_to_id = None
        self._modified_id = 0
        self._decoders = []
        self._multiprocess = multiprocess_safe
        path_encoded = path.encode("utf-8")
        if len(path_encoded) > 255:
            raise RuntimeError("TokenMonster: Vocabulary filepath is too long, the absolute path must be less than 256 characters")
        payload = _write_uint8(len(path_encoded)) + path_encoded
        self.id = self._communicate(10, 0, len(payload), payload)

    def _unload(self):
        if hasattr(self, 'id'):
            if self.id is not None:
                if self._modified_id != -1:
                    for _, decoder_id in enumerate(self._decoders):
                        self._communicate(6, decoder_id, 0)
                    self._communicate(11, self.id, 0)

    def __del__(self):
        try:
            if not sys.is_finalizing():
                self._unload()
        except AttributeError:
            pass

    def __len__(self):
        return self.vocab_size

    def decoder(self):
        """
        Returns a new decoder instance used for decoding tokens into text.
        """
        return self.DecoderInstance(self)
    
    def capcode(self):
        """
        Returns the capcode level of the vocabulary.
        0 = disabled
        1 = only deleteToken
        2 = enabled
        """
        return self._capcode
    
    def charset(self):
        """
        Returns one of "UTF-8", "UTF-16", "None"
        """
        if self._charset == 1:
            return "UTF-8"
        elif self._charset == 2:
            return "UTF-16"
        return "None"
    
    def mode(self):
        """
        Returns the optimization mode of the vocabulary.
        """
        if self._mode == 0:
            return "unfiltered"
        elif self._mode == 1:
            return "clean"
        elif self._mode == 2:
            return "balanced"
        elif self._mode == 3:
            return "consistent"
        elif self._mode == 4:
            return "strict"
        elif self._mode == 5:
            return "n/a"
    
    def normalization(self):
        """
        Returns the normalization of the vocabulary, e.g. "NFD trim"
        """
        flag = self._normalization
        s = ''
        if flag == 0:
            return 'None'
        if flag & 1 != 0:
            s = 'NFD '
        if flag & 2 != 0:
            s += 'Lowercase '
        if flag & 4 != 0:
            s += 'Accents '
        if flag & 8 != 0:
            s += 'Quotemarks '
        if flag & 16 != 0:
            s += 'Collapse '
        if flag & 32 != 0:
            s += 'Trim '
        if flag & 64 != 0:
            s += 'LeadingSpace '
        if flag & 128 != 0:
            s += 'NewLines '
        return s.strip()
    
    def decode(self, tokens):
        """
        Decodes tokens into a string.

        Only use this "decode" method if you are decoding a complete "batch" or complete "conversation".
        For decoding an incomplete batch sequentially (as the tokens become available) instead
        use the decoder object.

        Parameters:
            tokens (int or list of int or numpy array): The tokens to decode into a string

        Returns:
            string: The composed string from the input tokens.

        Usage:
            decoded_string = vocab.decode(tokens)
        """
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        length = 4
        batch_size = 1
        payload = [b'']
        # Parse input
        if is_iterable(tokens):
            if len(tokens) == 0:
                return
        else:
            if isinstance(tokens, int):
                tokens = [tokens]
            elif isinstance(tokens, (np.uint16, np.uint32)):
                tokens = np.array([tokens])
            else:
                raise ValueError("TokenMonster: Input to decode must be an int, list of ints, list of lists, or numpy array.")
        if not is_iterable(tokens[0]):
            data = self.serialize_tokens(tokens)
            payload.append(_write_uint64(len(data)))
            payload.append(data)
            length += len(data) + 8
            single = True
        else:
            batch_size = len(tokens)
            single = False
            for _, item in enumerate(tokens):
                if not is_iterable(item):
                    data = self.serialize_tokens(item)
                    payload.append(_write_uint64(len(data)))
                    payload.append(data)
                    length += len(item) + 8
                else:
                    raise ValueError("TokenMonster: Input to decode must be an int, list of ints, list of lists, or numpy array.")
        # Send
        job_type = self.encoding_length
        payload[0] = _write_uint32(batch_size)
        response = self._communicate(job_type, self.id, length, payload)
        batches_reply = _read_uint32(response[0:4])
        if batches_reply != batch_size:
            raise RuntimeError("TokenMonster: batch size from response differs from request")
        decoded = [None] * batches_reply
        offset = 4
        for i in range(batch_size):
            batch_length = _read_uint64(response[offset:offset+8])
            offset += 8
            decoded[i] = self._bytes_to_string(response[offset:offset+batch_length])
            offset += batch_length
        if single:
            return decoded[0]
        else:
            return decoded
    
    def tokenize(self, text):
        """
        Tokenizes a string into tokens according to the vocabulary.

        You can pass a string or a list of strings. If you pass a list of strings they are tokenized
        in parallel using as many threads as the list size. Note that if you pass a string
        it is converted to a binary string, so if you have binary string in the first place, feel
        free to pass that instead.

        Parameters:
            string or list of strings: A string or bytes string, or list of strings or bytes strings.

        Returns:
            tokens (numpy array or list of numpy arrays): The tokens IDs

        Usage:
            tokens = vocab.tokenize(text)
        """
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        length = 4
        batch_size = 1
        payload = [b'']
        single = False
        if isinstance(text, str):
            if len(text) == 0:
                return
            data = self._string_to_bytes(text)
            length += len(data) + 8
            payload.append(_write_uint64(len(data)))
            payload.append(data)
            single = True
        elif isinstance(text, bytes):
            if len(text) == 0:
                return
            length += len(text) + 8
            payload.append(_write_uint64(len(text)))
            payload.append(text)
            single = True
        elif is_iterable(text):
            batch_size = len(text)
            for i, item in enumerate(text):
                if isinstance(item, str):
                    data = self._string_to_bytes(item)
                    payload.append(_write_uint64(len(data)))
                    payload.append(data)
                    length += len(data) + 8
                elif isinstance(item, bytes):
                    payload.append(_write_uint64(len(item)))
                    payload.append(item)
                    length += len(item) + 8
                else:
                    raise ValueError("TokenMonster: Input to tokenize must be a string or a list of strings.")
        else:
            raise ValueError("TokenMonster: Input to tokenize must be a string or a list of strings.")
        # Send
        job_type = 1
        payload[0] = _write_uint32(batch_size)
        response = self._communicate(job_type, self.id, length, payload)
        batches_reply = _read_uint32(response[0:4])
        if batches_reply != batch_size:
            raise RuntimeError("TokenMonster: batch size of response differs from request")
        offset = 4
        if single:
            batch_length = _read_uint64(response[offset:offset+8])
            offset += 8
            return self.deserialize_tokens(response[offset:offset+batch_length])
        tokens = [None] * batches_reply
        for i in range(batch_size):
            batch_length = _read_uint64(response[offset:offset+8])
            offset += 8
            tokens[i] = self.deserialize_tokens(response[offset:offset+batch_length])
            offset += batch_length
        return tokens
        
    def tokenize_count(self, text):
        """
        Same as tokenize, but it returns only the number of tokens.

        The number of tokens is the same as you would get from `tokenize`. If you want to count any characters
        for which there are no tokens or single byte tokens, you should `enable_unk_token()`. It's okay to
        enable `enable_unk_token()`, run `tokenize_count` and then `disable_unk_token()`.

        Parameters:
            string or list of strings: A string or bytes string, or list of strings or bytes strings.

        Returns:
            n_tokens (int or list of ints): The number of tokens for each input string

        Usage:
            tokens = vocab.tokenize_count(text)
        """
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        length = 4
        batch_size = 1
        payload = [b'']
        single = False
        if isinstance(text, str):
            if len(text) == 0:
                return
            data = self._string_to_bytes(text)
            length += len(data) + 8
            payload.append(_write_uint64(len(data)))
            payload.append(data)
            single = True
        elif isinstance(text, bytes):
            if len(text) == 0:
                return
            length += len(text) + 8
            payload.append(_write_uint64(len(text)))
            payload.append(text)
            single = True
        elif is_iterable(text):
            batch_size = len(text)
            for i, item in enumerate(text):
                if isinstance(item, str):
                    data = self._string_to_bytes(item)
                    payload.append(_write_uint64(len(data)))
                    payload.append(data)
                    length += len(data) + 8
                elif isinstance(item, bytes):
                    payload.append(_write_uint64(len(item)))
                    payload.append(item)
                    length += len(item) + 8
                else:
                    raise ValueError("TokenMonster: Input to tokenize must be a string or a list of strings.")
        else:
            raise ValueError("TokenMonster: Input to tokenize must be a string or a list of strings.")
        # Send
        job_type = 20
        payload[0] = _write_uint32(batch_size)
        response = self._communicate(job_type, self.id, length, payload)
        batches_reply = _read_uint32(response[0:4])
        if batches_reply != batch_size:
            raise RuntimeError("TokenMonster: batch size of response differs from request")
        offset = 4
        if single:
            return _read_uint64(response[offset:offset+8])
        tokens = [0] * batch_size
        for i in range(batch_size):
            tokens[i] = _read_uint64(response[offset:offset+8])
            offset += 8
        return tokens

    def get_dictionary(self):
        """
        Returns a dictionary of all tokens in the vocabulary.

        This returns a list of dictionaries with keys "id", "token", "token_decoded", "type" and "score".
        Note that you should not attempt to use this to decode tokenized sequences because the capcode
        encoded tokens can change the way the next tokens are decoded. Therefore you should always use
        one of the two "decode" methods.

        Parameters:
            string or list of strings: A string or bytes string, or list of strings or bytes strings.

        Returns:
            list of dictionaries with keys are as follows:
                id (int): the ID of the token
                token (string): the token including capcode encoding
                token_decoded (string): the same token decoded from it's capcode form
                type (int): the type of token (0 = regular, 1 = byte, 2 = special, 3 = UNK)
                score (float): token's representation in the dataset used to train the vocabulary

        Usage:
            tokens = vocab.get_dictionary()
        """
        if self.dictionary is not None:
            return self.dictionary
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        job_type = 15
        response = self._communicate(job_type, self.id, 0)
        size = _read_uint32(response[0:4])
        self.vocab_size = size # it should be already the same
        offset = 4
        self.dictionary = {}
        self._token_to_id = {}
        self.unk = None
        types = ["regular", "single", "special", "unk"]
        for _ in range(size):
            id = _read_uint32(response[offset: offset + 4])
            offset += 4
            len_token = response[offset]
            len_token_decoded = response[offset + 1]
            typ = response[offset + 2]
            score = _read_float32(response[offset + 3: offset + 7])
            offset += 7
            token = self._bytes_to_string(response[offset : offset + len_token])
            offset += len_token
            token_decoded = self._bytes_to_string(response[offset : offset + len_token_decoded])
            offset += len_token_decoded
            self.dictionary[id] = {'id': id, 'token': token, 'token_decoded': token_decoded, 'type': types[typ], 'score': score}
            self._token_to_id[token] = id
            self._token_to_id[token_decoded] = id
            if typ == 3:
                self.unk = id
        return self.dictionary
    
    def id_to_token(self, id):
        """
        Get the token string from a single token ID, in it's capcode-encoded form.

        Parameters:
            id: int

        Returns:
            string or None
        """
        if self.dictionary is None:
            self.get_dictionary()
        if id >= 0 and id < len(self.dictionary):
            return self.dictionary[id]['token']
        else:
            return None
    
    def id_to_token_decoded(self, id):
        """
        Get the token string from a single token ID, in it's capcode-decoded form.

        Parameters:
            id: int

        Returns:
            string or None
        """
        if self.dictionary is None:
            self.get_dictionary()
        if id >= 0 and id < len(self.dictionary):
            return self.dictionary[id]['token_decoded']
        else:
            return None
    
    def token_to_id(self, token):
        """
        Returns the ID of a single token.

        This works for both capcode-encoded "raw" tokens, and their decoded form.

        Parameters:
            token: string

        Returns:
            int or None
        """
        if self.dictionary is None:
            self.get_dictionary()
        return self._token_to_id.get(token, None)
    
    def unk_token_id(self):
        """
        Returns the ID of the UNK token, or 'None' type if there is no UNK token

        Parameters:
            token: string

        Returns:
            int or None
        """
        if self.unk == False:
            self.get_dictionary()
        return self.unk

    def modify(self, add_special_tokens = None, add_regular_tokens = None, delete_tokens = None, resize = None, change_unk = None, reset_token_ids = False):
        """
        Modifies the vocabulary. Doing so invalidates all decoder objects associated with the
        model before modification.

        Notes:
            - Special tokens are special in that they cannot be skipped. All regular tokens
              that contain specials tokens within them are deleted.
            - When resizing the vocabulary down, the worst performing tokens are deleted
              ensuring the vocabulary remains efficient. However, only regular tokens
              with a score > 0 are can be removed by resizing.
            - A vocabulary can also be resized up. If any tokens have been removed by deleting
              or resizing, they can be restored by resizing the vocabulary to be larger.
            - After modifying you will need to "save" the vocabulary to a file or it'll be
              lost when the script ends.
            - delete_tokens can be in either raw or decoded form.

        Parameters:
            add_special_tokens (string or list of strings): Special tokens to add to the vocabulary
            add_regular_tokens (string or list of strings): Regular tokens to add to the vocabulary
            delete_tokens (string or list of strings): Regular or Special tokens to delete
            resize (int): Resizes the vocabulary to this size
            change_unk (boolean): If set, it enables or disables the Unk token
            reset_token_ids (boolean): If true the IDs are all reset starting from zero.

        Returns:
            int: The new size of the vocabulary.

        Usage:
            # adds the special token <eos>
            vocab.modify("<eos>")
            # adds the special token <eos> and keep the vocabulary at the current size
            vocab.modify("<eos>", None, None, len(vocab))
        """
        # Parse and format the inputs
        if self._multiprocess:
            raise RuntimeError("TokenMonster: Vocabs loaded with load_multiprocess_safe cannot be modified. Please modify it first, save it, then load it with load_multiprocess_safe.")
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        add_special_tokens = self._format_list(add_special_tokens)
        add_regular_tokens = self._format_list(add_regular_tokens)
        delete_tokens = self._format_list(delete_tokens)
        if resize is None:
            resize = 0
        if change_unk == True:
            change_unk = 2
        elif change_unk == False:
            change_unk = 1
        else:
            change_unk = 0
        # Build request
        payload = _write_uint8(int(reset_token_ids)) + _write_uint8(change_unk) + _write_uint32(len(add_regular_tokens))
        for _, item in enumerate(add_regular_tokens):
            payload +=  _write_uint8(len(item)) + item
        payload += _write_uint32(len(delete_tokens))
        for _, item in enumerate(delete_tokens):
            payload += _write_uint8(len(item)) + item
        payload += _write_uint32(len(add_special_tokens))
        for _, item in enumerate(add_special_tokens):
            payload += _write_uint8(len(item)) + item
        payload += _write_uint32(resize)
        job_type = 14
        self.vocab_size, self._highest_token_id_plus_one = self._communicate(job_type, self.id, len(payload), payload)
        self._modified()
        return self.vocab_size
    
    def modify_from_yaml(self, yaml):
        """
        Modifies the vocabulary using a YAML file.
        A sample YAML file can be found here: https://github.com/alasdairforsythe/tokenmonster/yaml_guide

        Parameters:
            yaml (string or bytes string): The YAML file containing the modifications

        Returns:
            int: The new size of the vocabulary.

        Usage:
            # Example deletes 2 tokens, one with ID 127, and another token ' test'
            vocab.modify_from_yaml("delete:\n  - id: 127\n  - token ' test'")

        Returns:
            int: The new size of the vocabulary.
        """
        if self._multiprocess:
            raise RuntimeError("TokenMonster: Vocabs loaded with load_multiprocess_safe cannot be modified. Please modify it first, save it, then load it with load_multiprocess_safe.")
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        job_type = 17
        self.vocab_size, self._highest_token_id_plus_one = self._communicate(job_type, self.id, len(yaml), yaml)
        self._modified()
        return self.vocab_size

    def add_token(self, token):
        """
        Add one or more regular tokens.

        Returns:
            int: The new size of the vocabulary.
        """
        return self.modify(None, token, None, 0)

    def delete_token(self, token):
        """
        Delete one or more regular or special tokens.
        You can give the token in either its encoded or decoded form.

        Returns:
            int: The new size of the vocabulary.
        """
        return self.modify(None, None, token, 0)
    
    def delete_token_by_id(self, id):
        """
        Delete one or more regular or special token by specifying the token ID.

        Returns:
            int: The new size of the vocabulary.
        """
        if self._multiprocess:
            raise RuntimeError("TokenMonster: Vocabs loaded with load_multiprocess_safe cannot be modified. You can load it normally, modify it, save it, then load the modified version with load_multiprocess_safe.")
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        if isinstance(id, int):
            id = [id]
        elif isinstance(id, (np.uint16, np.uint32)):
            id = np.array([id])
        else:
            if not is_iterable(id):
                raise ValueError("TokenMonster: Input to delete_token_by_id must be int or list of ints.")
            if len(id) == 0:
                return self.vocab_size
            if not is_int(id[0]):
                raise ValueError("TokenMonster: Input to delete_token_by_id must be int or list of ints.")
        payload = _write_uint32(len(id)) + _pack_32bit_ints(id)
        job_type = 16
        self.vocab_size, self._highest_token_id_plus_one = self._communicate(job_type, self.id, len(payload), payload)
        self._modified()
        return self.vocab_size

    def add_special_token(self, token):
        """
        Add one or more special tokens.

        Returns:
            int: The new size of the vocabulary.
        """
        return self.modify(token, None, None, 0)
    
    def resize(self, size, reset_token_ids = False):
        """
        Changes the size of the vocabulary and optionally resets the token IDs.

        A vocabulary can be enlarged as well reduced in size. Only the worst performing
        tokens are removed when reducing.

        Resizing only removes regular tokens that are not single byte token and have
        score > 0. If there are not enough of these, the new size may not match
        the target size.

        Returns:
            int: The new size of the vocabulary.
        """
        return self.modify(None, None, None, size, None, reset_token_ids)
    
    def reset_token_ids(self):
        """
        Resets the token IDs to be sequential beginning from zero.

        If tokens have been deleted from the vocabulary there will be gaps in the token IDs.
        Resetting the token IDs removes these gaps but all tokens will have new IDs.
        """
        return self.modify(None, None, None, None, None, True)
    
    def enable_unk_token(self):
        """
        Enables the UNK token.
        If enabled, the UNK token appears whenever there is a character that is not in the vocabulary.
        Note that the UNK token will not be enabled if all possible characters have tokens.
        Use `vocab.unk_token_id()` to retrieve the ID for the UNK token.

        Returns:
            int: The new size of the vocabulary.
        """
        return self.modify(None, None, None, 0, True)
    
    def disable_unk_token(self):
        """
        Disables the UNK token.
        Without an UNK token, any character for which there is no token is ignored during tokenization

        Returns:
            int: The new size of the vocabulary.
        """
        return self.modify(None, None, None, 0, False)

    def save(self, fname):
        """
        Saves the current vocabulary to a file.

        Parameters:
            filename (string): The filename to save the vocabulary to.

        Returns:
            Nothing (raises error on failure)

        Usage:
            vocab.save("test.vocab")
        """
        if self._multiprocess:
            raise RuntimeError("TokenMonster: Vocabs loaded with load_multiprocess_safe cannot be saved.")
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        fname_encoded = fname.encode("utf-8")
        if len(fname_encoded) > 255:
            raise RuntimeError("TokenMonster: Vocabulary filepath is too long, it must be less than 256 characters")
        payload = _write_uint8(len(fname_encoded)) + fname_encoded
        self._communicate(12, 0, len(payload), payload)

    def export_yaml(self, order_by_score = False):
        if self._multiprocess:
            raise RuntimeError("TokenMonster: Vocabs loaded with load_multiprocess_safe cannot be exported.")
        """
        Exports the vocabulary as a YAML file, which is returned as a bytes string.

        Parameters:
            order_by_score (boolean): If true the tokens are order by score instead of alphabetically.

        Returns:
            bytes string: The vocabulary in YAML format.

        Usage:
            yaml = vocab.export_yaml()
            with open(file_path, 'wb') as file:
                file.write(yaml)
        """
        if self._modified_id == -1:
            raise RuntimeError("TokenMonster: Access denied to expired Vocab instance.")
        payload = _write_uint8(int(order_by_score))
        job_type = 19
        return self._communicate(job_type, self.id, 1, payload)

    def deserialize_tokens(self, binary_string):
        """
        Deserializes a binary string into a numpy array of tokens IDs.
        The encoding_length needs to be recorded separetely.
        """
        if self.encoding_length == 2:
            return _unpack_16bit_ints(binary_string)
        elif self.encoding_length == 4:
            return _unpack_32bit_ints(binary_string)
        #elif self.encoding_length == 3:
        #    return _unpack_24bit_ints(binary_string)
        else:
            raise RuntimeError("TokenMonster: Invalid encoding length")
        
    def serialize_tokens(self, integer_list):
        """
        Serializes tokens from a numpy array into a binary string.
        The encoding_length needs to be recorded separetely.
        """
        if self.encoding_length == 2:
            return _pack_16bit_ints(integer_list)
        elif self.encoding_length == 4:
            return _pack_32bit_ints(integer_list)
        #elif self.encoding_length == 3:
        #    return _pack_24bit_ints(integer_list)
        else:
            raise RuntimeError("TokenMonster: Invalid encoding length")
    
    def _bytes_to_string(self, input):
        if self._charset == 1:
            return input.decode('utf-8', errors='replace')
        elif self._charset == 2:
            return input.decode('utf-16-le', errors='replace')
        else:
            return input.decode('latin-1')
        
    def _string_to_bytes(self, input):
        if self._charset == 1:
            return input.encode('utf-8')
        elif self._charset == 2:
            return input.encode('utf-16-le')
        else:
            return input.encode('latin-1')

    def _format_list(self, data):
        if data is None:
            return []
        elif isinstance(data, str):
            if len(data) == 0:
                return []
            if self._charset == 2:
                return [data.encode("utf-16-le")]
            else:
                return [data.encode("utf-8")]
        elif isinstance(data, bytes):
            if len(data) == 0:
                return []
            else:
                return [data]
        elif is_iterable(data):
            if len(data) == 0:
                return data
            else:
                for i, item in enumerate(data):
                    if isinstance(item, str):
                        if self._charset == 2:
                            data[i] = item.encode("utf-16-le")
                        else:
                            data[i] = item.encode("utf-8")
                    elif not isinstance(item, bytes):
                        raise ValueError("TokenMonster: Invalid input for vocabulary modification. Input should be string or bytes string, or list thereof.")
            return data
        else:
            raise ValueError("TokenMonster: Invalid input for vocabulary modification. Input should be string or bytes string, or list thereof.")

    def _modified(self):
        self._modified_id += 1
        self.dictionary = None
        self._token_to_id = None
        self.unk = False
        if self._highest_token_id_plus_one > 65536:
            self.encoding_length = 4
        else:
            self.encoding_length = 2
        # Unload all the decoder objects
        for _, decoder_id in enumerate(self._decoders):
            self._communicate(6, decoder_id, 0)
        self._decoders = []

    @classmethod
    def _set_local_directory(cls, dir=None):
        if dir is None:
            if cls._dir is not None:
                return
            dir = os.path.join(os.path.expanduser("~"), "_tokenmonster")
        cls._os, cls._bin = _get_binary_filename()
        if not os.path.exists(dir):
            os.makedirs(dir)
            if not os.path.exists(dir):
                raise RuntimeError("Unable to create directory: {}".format(dir))
        cls._dir = dir

    @classmethod
    def _disconnect(cls):
        if cls._process is not None:
            cls._process.stdin.close()
            cls._process.stdout.close()
            cls._process.kill()
            cls._process = None
            for i in range(len(cls._vocabs)):
                cls._vocabs[i]._modified_id = -1
            cls._vocabs = []

    @classmethod
    def _download(cls, url, fname):
        urllib.request.urlretrieve(url, os.path.join(cls._dir, fname))

    @classmethod
    def _file_exists(cls, fname):
        return os.path.exists(os.path.join(cls._dir, fname))
    
    def _communicate(self, job_type, id, data_length, data = None):
        if self._multiprocess:
            if os.getpid() != Vocab._pid: # this is a folked child process
                Vocab._process = None # don't use the parent's subprocess
                Vocab._connect() # make a new connection to tokenmonsterserver
                self._modified_id = Vocab._pid # invalidate decoder instances from parent
                # Reload the vocabulary from file
                path_encoded = self.fname.encode("utf-8")
                payload = _write_uint8(len(path_encoded)) + path_encoded
                self.id = self._communicate(10, 0, len(payload), payload)
            else:
                Vocab._connect()
        else:
            Vocab._connect()
        Vocab._process.stdin.write(struct.pack('<BIQ', job_type, id, data_length)[0:12])
        if data is not None:
            if isinstance(data, bytes):
                Vocab._process.stdin.write(data)
            else:
                for item in data:
                    Vocab._process.stdin.write(item)
        Vocab._process.stdin.flush()
        response = Vocab._process.stdout.read(9)
        if len(response) == 0: # this happens when the app is shutting down
            return None
        status = response[0]
        if status == 0: # HEADER_IS_LENGTH
            length = _read_uint64(response[1:9])
            return Vocab._process.stdout.read(length)
        elif status == 1: # HEADER_IS_ID
            id = _read_uint32(response[1:5])
            return id
        elif status == 2: # HEADER_IS_EMPTY
            return None
        elif status == 3: # HEADER_IS_2VAL
            a = _read_uint32(response[1:5])
            b = _read_uint32(response[5:9])
            return a, b
        elif status == 10: # ERROR_ID_DOES_NOT_EXIST
            raise RuntimeError("tokenmonsterserver: This ID does not exist")
        elif status == 11: # ERROR_ID_IS_UNLOADED
            raise RuntimeError("tokenmonsterserver: This ID has already been unloaded")
        elif status == 12: # ERROR_FILE_CANNOT_OPEN
            raise RuntimeError("tokenmonsterserver: Cannot open or save vocabulary file, please check permissions")
        elif status == 13: # ERROR_NORMALIZATION_FAILED
            raise RuntimeError("tokenmonsterserver: An error occurred normalizing your text")
        elif status == 14: # ERROR_READ_FAILED
            raise RuntimeError("tokenmonsterserver: Read failed")
        elif status == 15: # ERROR_INVALID_JOB
            raise RuntimeError("tokenmonsterserver: Invalid job ID")
        elif status == 16: # ERROR_INVALID_JOB
            raise ValueError("TokenMonster: YAML is invalid")
        else:
            raise RuntimeError("tokenmonsterserver: Data corruption. If you are using multiprocessing, you must load the vocabulary with `load_multiprocess_safe`.")

    @classmethod
    def _start_process(cls):
        exe = os.path.join(cls._dir, cls._bin)
        pid = os.getpid()
        cls._pid = pid
        try:
            cls._process = subprocess.Popen([exe, str(pid)], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        except Exception:
            cls._process = None
            return False
        else:
            if cls._process is None:
                return False
            return True

    @classmethod
    def _install_tokenmonsterserver(cls):
        cls._download(_TOKENMONSTER_URL + "binaries/" + cls._os + "/" + cls._bin, cls._bin)
        if not cls._file_exists(cls._bin):
            raise FileNotFoundError("TokenMonster: Unable to download " + cls._bin + " to " + cls._dir + " from Hugging Face")
        # attempt to add execute permission for this user
        exe = os.path.join(cls._dir, cls._bin)
        if cls._os.startswith("windows"):
            try:
                username = getpass.getuser()
                subprocess.run(["icacls", exe, "/grant", f"{username}:(RX)"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except Exception:
                pass
        else:
            try:
                os.chmod(exe, 0o700)
            except Exception:
                pass
        if not cls._start_process():
            sep = '=' * 64
            raise RuntimeError("\n"+sep+"\n\n\tTo get started with TokenMonster please enable execute permissions for:\n\t"+exe+"\n\n"+sep+"\n")

    @classmethod
    def _tms_get_version(cls):
        cls._process.stdin.write(struct.pack('<BIQ', 0, 0, 0)[0:12])
        Vocab._process.stdin.flush()
        response = Vocab._process.stdout.read(9)
        return _read_uint32(response[1:5])

    @classmethod
    def _connect(cls):
        if cls._process is None:
            for i in range(len(cls._vocabs)):
                cls._vocabs[i]._modified_id = -1
            cls._vocabs = []
            if cls._file_exists(cls._bin):
                if not cls._start_process():
                    raise RuntimeError("TokenMonster: Unable to start tokenmonsterserver, please give execute permission to " + os.path.join(cls._dir, cls._bin))
            else:
                cls._install_tokenmonsterserver()
            # Now check verison number
            tms_version = cls._tms_get_version()
            if tms_version < _TMS_VERSION_ID:
                cls._disconnect()
                cls._install_tokenmonsterserver()
                tms_version = cls._tms_get_version()
                if tms_version < _TMS_VERSION_ID:
                    raise RuntimeError("TokenMonster: tokenmonsterserver version does not match Python library version")
            if tms_version > _TMS_VERSION_ID:
                cls._disconnect()
                raise RuntimeError("TokenMonster: Version mismatch. Please upgrade tokenmonster with `pip install --upgrade tokenmonster`")

    #@classmethod
    #def _reconnect(cls):
    #    cls._close()
    #    cls._connect()

    #@classmethod
    #def _discard(cls):
    #    cls._process.stdout.read()

    # class level variables
    _dir = None
    _os = None
    _bin = None
    _process = None
    _pid = 0
    _vocabs = []


### Helper Functions

def _is_prebuilt(name):
    if name == "gpt2" or name == "llama":
        return True
    parts = name.split("-")
    if len(parts) < 4 or len(parts) > 5:
        return False
    if not parts[0] in ["english", "code", "fiction", "englishcode"]:
        return False
    if not parts[1] in ["1024", "2048", "4096", "8000", "12000", "16000", "24000", "32000", "40000", "50256", "65536", "100256"]:
        return False
    if not parts[2] in ["unfiltered", "clean", "balanced", "consistent", "strict"]:
        return False
    if len(parts) == 4:
        if len(parts[3]) == 0:
            return False
        if parts[3][0] == 'v':
            return True
    else:
        if parts[3] != "nocapcode":
            return False
        if len(parts[4]) == 0:
            return False
        if parts[4][0] == 'v':
            return True
    return False

def _get_binary_filename():
    os_name = platform.system()
    arch_name = platform.machine()
    if os_name == "Windows":
        if arch_name == "x86_64":
            return "windows_x86_64", "tokenmonsterserver.exe"
        elif arch_name == "AMD64": # same as x86_64
            return "windows_x86_64", "tokenmonsterserver.exe"
        elif arch_name.startswith("arm"):
            return "windows_arm64", "tokenmonsterserver.exe"
        elif arch_name == "aarch64":
            return "windows_arm64", "tokenmonsterserver.exe"
        else:
            raise RuntimeError("Unsupported architecture for Windows: {}".format(arch_name))
    elif os_name == "Linux":
        if arch_name == "x86_64":
            return "linux_x86_64", "tokenmonsterserver"
        elif arch_name.startswith("arm"):
            return "linux_arm64", "tokenmonsterserver"
        elif arch_name == "aarch64":
            return "linux_arm64", "tokenmonsterserver"
        else:
            raise RuntimeError("Unsupported architecture for Linux: {}".format(arch_name))
    elif os_name == "Darwin":
        if arch_name == "x86_64":
            return "darwin_x86_64", "tokenmonsterserver"
        elif arch_name == "AMD64": # same as x86_64
            return "darwin_x86_64", "tokenmonsterserver"
        elif arch_name == "arm64":
            return "darwin_arm64", "tokenmonsterserver"
        else:
            raise RuntimeError("Unsupported architecture for macOS: {}".format(arch_name))
    else:
        raise RuntimeError("Unsupported operating system: {}".format(os_name))

def _unpack_16bit_ints(binary_string):
    n = len(binary_string) // 2
    return np.frombuffer(binary_string, dtype='<u2', count=n)

#def _unpack_24bit_ints(binary_string):
#    n = len(binary_string) // 3
#    return [int.from_bytes(binary_string[i:i+3], byteorder='little') for i in range(0, 3*n, 3)]

def _unpack_32bit_ints(binary_string):
    n = len(binary_string) // 4
    return np.frombuffer(binary_string, dtype='<u4', count=n)

def _pack_16bit_ints(integer_list):
    if isinstance(integer_list, np.ndarray):
        if integer_list.dtype.byteorder == '>':
            return integer_list.byteswap().tobytes()
        else:
            return integer_list.tobytes()
    else:
        return struct.pack('<' + 'H' * len(integer_list), *integer_list)

#def _pack_24bit_ints(integer_list):
#    return b''.join([int(i).to_bytes(3, byteorder='little') for i in integer_list])

def _pack_32bit_ints(integer_list):
    if isinstance(integer_list, np.ndarray):
        if integer_list.dtype.byteorder == '>':
            return integer_list.byteswap().tobytes()
        else:
            return integer_list.tobytes()
    else:
        return struct.pack('<' + 'I' * len(integer_list), *integer_list)

def _write_uint32(input):
    return struct.pack('<I', input)

def _write_uint64(input):
    return struct.pack('<Q', input)

def _write_uint8(input):
    return struct.pack('B', input)

def _read_uint32(input):
    return struct.unpack('<I', input)[0]

def _read_uint64(input):
    return struct.unpack('<Q', input)[0]

def _read_float32(input):
    return struct.unpack('<f', input)[0]

def is_iterable(obj):
    if isinstance(obj, (str, bytes)):
        return False
    return isinstance(obj, Iterable)

def is_int(obj):
    if isinstance(obj, (int, np.uint16, np.uint32)):
        return True
    return False

_TOKENMONSTER_URL = "https://huggingface.co/alasdairforsythe/tokenmonster/resolve/main/"
_TMS_VERSION_ID = 5
