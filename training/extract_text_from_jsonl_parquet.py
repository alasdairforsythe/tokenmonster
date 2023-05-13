import argparse
import csv
import json
import os
import pandas as pd
import sys


def write_text_to_output_file(text_field, out_file, file_size, max_size):
    if max_size and (file_size + len(text_field) + 1) > max_size:
        return False
    out_file.write(text_field + '\n')
    return True


def process_jsonl_file(input_file, output_file, max_size=None):
    try:
        with open(input_file, 'r', encoding='utf-8') as in_file:
            file_size = 0
            for line in in_file:
                json_obj = json.loads(line)
                if 'text' in json_obj:
                    text_field = json_obj["text"]
                    if not write_text_to_output_file(text_field, output_file, file_size, max_size):
                        break
                    file_size += len(text_field) + 1
                else:
                    print(f"Skipping line: {line.strip()}. No 'text' field found.")
    except FileNotFoundError:
        raise FileNotFoundError(f"Error: File {input_file} not found.")


def process_parquet_file(input_file, output_file, max_size=None):
    try:
        df = pd.read_parquet(input_file)
        relevant_cols = ["content", "text", "body"]
        file_size = 0
        for col in relevant_cols:
            if col in df.columns:
                col_data = df[col].dropna()
                for text_field in col_data:
                    if not write_text_to_output_file(text_field, output_file, file_size, max_size):
                        break
                    file_size += len(text_field) + 1
                    if max_size and file_size > max_size:
                        break
    except FileNotFoundError:
        raise FileNotFoundError(f"Error: File {input_file} not found.")


def process_all_files(output_file=None, max_size=None):
    input_files = [f for f in os.listdir('.') if os.path.isfile(f) and (f.endswith(".jsonl") or f.endswith(".parquet"))]
    
    if output_file:
        with open(output_file, 'w', encoding='utf-8') as out_file:
            for input_file in input_files:
                if input_file.endswith(".jsonl"):
                    process_jsonl_file(input_file, out_file, max_size=max_size)
                elif input_file.endswith(".parquet"):
                    process_parquet_file(input_file, out_file, max_size=max_size)
    else:
        for input_file in input_files:
            output_file = input_file.rsplit(".", 1)[0] + ".txt"
            if input_file.endswith(".jsonl"):
                process_jsonl_file(input_file, output_file, max_size=max_size)
            elif input_file.endswith(".parquet"):
                process_parquet_file(input_file, output_file, max_size=max_size)


def main():
    parser = argparse.ArgumentParser(description='Extract text from JSONL or parquet files.')
    parser.add_argument('input_file', type=str, nargs='?', help='The input file to extract text from.')
    parser.add_argument('output_file', type=str, nargs='?', help='The output file to write the extracted text to.')
    parser.add_argument('-max', type=int, help='The maximum output file size in bytes.')
    parser.add_argument('--all', action='store_true', help='Extract text from all JSONL and parquet files in the current directory.')
    args = parser.parse_args()
    try:
        if args.all:
            process_all_files(output_file=args.output_file, max_size=args.max)
        else:
            if not args.input_file or not args.output_file:
                parser.print_help()
                sys.exit(1)
            input_file = args.input_file
            output_file = args.output_file
            max_size = args.max
            if input_file.endswith(".jsonl"):
                process_jsonl_file(input_file, output_file, max_size=max_size)
            elif input_file.endswith(".parquet"):
                process_parquet_file(input_file, output_file, max_size=max_size)
            else:
                raise ValueError(f"Error: Unrecognized file format for {input_file}. Only .jsonl and .parquet files are supported.")
    except Exception as e:
        print(str(e))
        sys.exit(1)

if __name__ == '__main__':
    main()