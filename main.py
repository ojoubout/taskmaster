#!/usr/local/bin/python3.10

import sys

from taskmaster.commands import execute_command, prompt
from taskmaster.executor import start_initial_state
from taskmaster.parser import parse_config_file
from taskmaster.signal import handle_signals


def main():
    args = sys.argv

    conf = parse_config_file()
    handle_signals()
    # if len(args) > 1:
    #     execute_command(args[1:])
    # else:
    start_initial_state(conf)
    prompt()


if __name__ == "__main__":
    main()
