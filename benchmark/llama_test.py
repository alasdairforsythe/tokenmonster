#
#  Usage: python3 llama_test.py whatever.txt
#

import sys
import timeit
from transformers import LlamaTokenizer

def load_text_from_file(file_path):
    try:
        with open(file_path, 'r') as file:
            return file.read()
    except Exception as e:
        print(f"An error occurred while trying to read the file: {e}")
        return None

def benchmark():
    if len(sys.argv) < 2:
        print("Please include the filename as a command line argument.")
        return

    filename = sys.argv[1]
    text_from_file = load_text_from_file(filename)

    if text_from_file is None:
        print("Failed to load the file.")
        return

    tokenizer = LlamaTokenizer.from_pretrained('decapoda-research/llama-13b-hf', use_fast=True)

    # timeit library was returning zero, so just timing it this way
    start_time = timeit.default_timer()
    tokens = tokenizer.tokenize(text_from_file)
    elapsed_time = (timeit.default_timer() - start_time) * 1_000_000
    num_tokens = len(tokens)

    print(filename)
    print(f'Number of tokens for LLaMa (32000): {num_tokens}')
    print(f'Time elapsed (clock time): {elapsed_time:.3f} seconds')

if __name__ == '__main__':
    benchmark()
