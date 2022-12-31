import signal


def execute_command(args):
    cmd = args[0]
    match cmd.upper():
        case 'STATUS':
            pass
        case 'START':
            pass
        case 'STOP':
            pass
        case 'RESTART':
            pass
        case 'RELOAD':
            pass
        case 'HELP':
            print_commands()
        case 'EXIT' | 'QUIT':
            exit();
        case _:
            print(f'*** Unkown syntax {" ".join(args)}')

def print_commands():
    print('commands: start, stop, restart, reload, exit, quit')

def prompt():
    try:
        while True:
            line = input('taskmaster> ')
            args = line.split(' ')
            execute_command(args)
    except EOFError:
        print()
        pass
