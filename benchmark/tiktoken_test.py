#
#  Usage: python3 tiktoken_test.py whatever.txt
#

import sys
import tiktoken
import timeit

def load_text_from_file(file_path):
    try:
        with open(file_path, 'r') as file:
            return file.read()
    except Exception as e:
        print(f"An error occurred while trying to read the file: {e}")
        return None

def encode_tokens(encoding, text_from_file):
    return encoding.encode(text_from_file, disallowed_special=())

def benchmark():
    if len(sys.argv) < 2:
        print("Please include the filename as a command line argument.")
        return

    filename = sys.argv[1]
    text_from_file = load_text_from_file(filename)

    if text_from_file is None:
        print("Failed to load the file.")
        return

    for encoding_name in ["p50k_base", "cl100k_base"]:
        encoding = tiktoken.get_encoding(encoding_name)

        # Create a Timer object with setup and statement
        timer = timeit.Timer(lambda: encode_tokens(encoding, text_from_file))

        # Perform the benchmark
        num_tokens = len(encoding.encode(text_from_file, disallowed_special=()))
        elapsed_time = timer.timeit(number=1) * 1_000_000  # Convert to microseconds

        print(filename)
        print(f'Number of tokens for tiktoken {encoding_name} : {num_tokens}')
        print(f'Time elapsed for {encoding_name}: {elapsed_time / 1000000:.3f} seconds')
        print()

if __name__ == '__main__':
    benchmark()
