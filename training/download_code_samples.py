#
#  Script for obtaining equally weighted samples of code in multiple programming languages
#  Data download from from huggingface.co/datasets/codeparrot/github-code
#  Run this script inside a directory and it will download segments of code in 30 different language.
#  It retrieves 2MB and 10MB of code for each language. Each language runs in a separate thread.
#  It outputs 2 files for each language (_2mb.txt & _10mb.txt) and afterward concatenates them all_2mb.txt & all_10mb.txt.
#
#  Languges (30):
#    Assembly, Batchfile, C, C#, C++, CMake, CSS, Dockerfile, FORTRAN, 
#    GO, Haskell, HTML, Java, JavaScript, Julia, Lua, Makefile, Markdown, 
#    PHP, Perl, PowerShell, Python, Ruby, Rust, SQL, Scala, Shell, 
#    TypeScript, TeX, Visual Basic
#

import sys
import subprocess
from datasets import load_dataset

def to_filename(s):
    s = s.replace('#', 'Sharp').replace('+', 'p')
    return ''.join(c if c.isalnum() else '_' for c in s)

def process_language(lang):
    # Load the dataset for the specific language
    ds = load_dataset("codeparrot/github-code", streaming=True, split="train", languages=[lang])

    code_size_2mb = 0
    code_size_10mb = 0
    ended = False
    seen_repos = set()

    with open(f"{to_filename(lang)}_2mb.txt", "w") as outfile1, open(f"{to_filename(lang)}_10mb.txt", "w") as outfile2:
        # Iterate over the data and extract code text for the specific language
        for data in ds:
            # If we have seen this repository before, skip it
            if data['repo_name'] in seen_repos:
                continue

            # Add this repo to our set of seen repos
            seen_repos.add(data['repo_name'])

            # Split the code into lines
            lines = data['code'].split('\n')
            
            # If there are more than 200 lines, take the middle 200, otherwise take all lines
            if len(lines) > 200:
                middle_index = len(lines) // 2
                start_index = middle_index - 100  # Start 100 lines before the middle
                end_index = middle_index + 100  # End 100 lines after the middle
                code = '\n'.join(lines[start_index:end_index])
            else:
                code = data['code']

            data_size = len(code.encode('utf-8'))

            # If we've collected less than or equal to 2MB of code for this language, write to the file
            if code_size_2mb <= 2 * 1024 * 1024:
                outfile1.write(code)
                code_size_2mb += data_size
                
            # If we've collected less than or equal to 10MB of code for this language, write to the file
            if code_size_10mb <= 10 * 1024 * 1024:
                outfile2.write(code)
                code_size_10mb += data_size
                
            # If we've collected more than 10MB for this language, break the loop
            if code_size_10mb > 10 * 1024 * 1024:
                print(f"{lang} 2MB Size: {code_size_2mb / 1024} KB, 10MB Size: {code_size_10mb / 1024} KB")
                ended = True
                break

        if not ended:
            print(f"{lang} 2MB Size: {code_size_2mb / 1024} KB, 10MB Size: {code_size_10mb / 1024} KB")

def main():
    languages = ["Assembly", "Batchfile", "C", "C#", "C++", "CMake", "CSS", "Dockerfile", "FORTRAN", 
                 "GO", "Haskell", "HTML", "Java", "JavaScript", "Julia", "Lua", "Makefile", "Markdown", 
                 "PHP", "Perl", "PowerShell", "Python", "Ruby", "Rust", "SQL", "Scala", "Shell", 
                 "TypeScript", "TeX", "Visual Basic"]

    if len(sys.argv) > 1:
        # The script was launched with an argument, so process the provided language
        lang = sys.argv[1]
        process_language(lang)
    else:
        # The script was launched without arguments, so spawn a new process for each language
        processes = []
        for lang in languages:
            p = subprocess.Popen([sys.executable, __file__, '"' + lang + '"'])
            processes.append(p)

        # Wait for all processes to finish
        for p in processes:
            p.wait()

        # All processes have finished, now combine all _2mb.txt files and _10mb.txt files
        with open("all_2mb.txt", "w") as outfile_all_2mb, open("all_10mb.txt", "w") as outfile_all_10mb:
            for lang in languages:
                filename_2mb = f"{to_filename(lang)}_2mb.txt"
                filename_10mb = f"{to_filename(lang)}_10mb.txt"
                
                with open(filename_2mb, "r") as infile_2mb:
                    outfile_all_2mb.write(infile_2mb.read())
                
                with open(filename_10mb, "r") as infile_10mb:
                    outfile_all_10mb.write(infile_10mb.read())

if __name__ == "__main__":
    main()