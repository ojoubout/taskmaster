#!/usr/local/bin/python3.10

import sys

from taskmaster.commands import execute_command, prompt
from taskmaster.signal import handle_signals

def main():
    args = sys.argv

    handle_signals()
    if len(args) > 1:
        execute_command(args[1:])
    else:
        prompt()

if __name__ == "__main__":
    main()
