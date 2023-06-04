#
#  Usage: python3 llama_test.py whatever.txt
#

import sys
import timeit
from transformers import LlamaTokenizerFast

def load_text_from_file(file_path):
    try:
        with open(file_path, 'r') as file:
            return file.read()
    except Exception as e:
        print(f"An error occurred while trying to read the file: {e}")
        return None

def encode_tokens(tokenizer, text_from_file):
    return tokenizer.encode(text_from_file)

def benchmark():
    if len(sys.argv) < 2:
        print("Please include the filename as a command line argument.")
        return

    filename = sys.argv[1]
    text_from_file = load_text_from_file(filename)

    if text_from_file is None:
        print("Failed to load the file.")
        return

    tokenizer = LlamaTokenizerFast.from_pretrained("hf-internal-testing/llama-tokenizer")

    # Create a Timer object with setup and statement
    timer = timeit.Timer(lambda: encode_tokens(tokenizer, text_from_file))

    # Perform the benchmark
    num_tokens = len(tokenizer.encode(text_from_file))
    elapsed_time = timer.timeit(number=1)

    print(filename)
    print(f'Number of tokens for LLaMa (32000) : {num_tokens}')
    print(f'Time elapsed: {elapsed_time / 1000000:.3f} seconds')
    print()

if __name__ == '__main__':
    benchmark()
