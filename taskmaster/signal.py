import signal

def handler(signum, frame):
    match signum:
        case signal.SIGINT:
            print("\b\b  ")
            exit(130)
        case signal.SIGQUIT:
            print("\b\bQuit")
            exit(131)
        case _:
            exit(1)

def handle_signals():
    signal.signal(signal.SIGINT, handler);
    signal.signal(signal.SIGQUIT, handler);
